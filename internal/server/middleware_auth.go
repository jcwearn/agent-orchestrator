package server

import (
	"context"
	"net/http"
	"time"
)

type contextKey string

const userContextKey contextKey = "user"

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session_id")
		if err != nil || cookie.Value == "" {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		sess, err := s.store.GetSession(r.Context(), cookie.Value)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		if time.Now().UTC().After(sess.ExpiresAt) {
			_ = s.store.DeleteSession(r.Context(), sess.ID)
			writeError(w, http.StatusUnauthorized, "session expired")
			return
		}

		user, err := s.store.GetUserByID(r.Context(), sess.UserID)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		ctx := context.WithValue(r.Context(), userContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
