package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/jcwearn/agent-orchestrator/internal/store"
	"golang.org/x/crypto/bcrypt"
)

const sessionDuration = 7 * 24 * time.Hour

type setupRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type authStatusResponse struct {
	SetupRequired bool       `json:"setup_required"`
	Authenticated bool       `json:"authenticated"`
	User          *store.User `json:"user"`
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	count, err := s.store.CountUsers(r.Context())
	if err != nil {
		s.logger.Error("count users", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if count == 0 {
		writeJSON(w, http.StatusOK, authStatusResponse{SetupRequired: true})
		return
	}

	cookie, err := r.Cookie("session_id")
	if err != nil || cookie.Value == "" {
		writeJSON(w, http.StatusOK, authStatusResponse{})
		return
	}

	sess, err := s.store.GetSession(r.Context(), cookie.Value)
	if err != nil || time.Now().UTC().After(sess.ExpiresAt) {
		writeJSON(w, http.StatusOK, authStatusResponse{})
		return
	}

	user, err := s.store.GetUserByID(r.Context(), sess.UserID)
	if err != nil {
		writeJSON(w, http.StatusOK, authStatusResponse{})
		return
	}

	writeJSON(w, http.StatusOK, authStatusResponse{
		Authenticated: true,
		User:          user,
	})
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	count, err := s.store.CountUsers(r.Context())
	if err != nil {
		s.logger.Error("count users", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if count > 0 {
		writeError(w, http.StatusConflict, "setup already completed")
		return
	}

	var req setupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Username == "" {
		writeError(w, http.StatusBadRequest, "username is required")
		return
	}
	if req.Password == "" {
		writeError(w, http.StatusBadRequest, "password is required")
		return
	}
	if len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		s.logger.Error("hash password", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	user := &store.User{
		Username: req.Username,
		Password: string(hashed),
		Role:     "admin",
	}
	if err := s.store.CreateUser(r.Context(), user); err != nil {
		s.logger.Error("create user", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	s.createSessionAndRespond(w, r, user)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password are required")
		return
	}

	user, err := s.store.GetUserByUsername(r.Context(), req.Username)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	s.createSessionAndRespond(w, r, user)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session_id")
	if err == nil && cookie.Value != "" {
		_ = s.store.DeleteSession(r.Context(), cookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session_id",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createSessionAndRespond(w http.ResponseWriter, r *http.Request, user *store.User) {
	sess := &store.Session{
		UserID:    user.ID,
		ExpiresAt: time.Now().UTC().Add(sessionDuration),
	}
	if err := s.store.CreateSession(r.Context(), sess); err != nil {
		s.logger.Error("create session", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"

	http.SetCookie(w, &http.Cookie{
		Name:     "session_id",
		Value:    sess.ID,
		Path:     "/",
		MaxAge:   int(sessionDuration.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})

	writeJSON(w, http.StatusOK, authStatusResponse{
		Authenticated: true,
		User:          user,
	})
}
