package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jcwearn/agent-orchestrator/internal/coder"
	ghclient "github.com/jcwearn/agent-orchestrator/internal/github"
	"github.com/jcwearn/agent-orchestrator/internal/store"
)

type Server struct {
	store         *store.Store
	pool          *coder.Pool
	executor      coder.WorkspaceExecutor
	hub           *Hub
	logger        *slog.Logger
	githubClient  *ghclient.Client // nil if GitHub not configured
	webhookSecret []byte           // nil if GitHub not configured
}

func New(store *store.Store, pool *coder.Pool, executor coder.WorkspaceExecutor, hub *Hub, logger *slog.Logger, opts ...Option) *Server {
	s := &Server{
		store:    store,
		pool:     pool,
		executor: executor,
		hub:      hub,
		logger:   logger,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Option configures optional Server fields.
type Option func(*Server)

// WithGitHub configures the GitHub App client and webhook secret.
func WithGitHub(client *ghclient.Client, webhookSecret []byte) Option {
	return func(s *Server) {
		s.githubClient = client
		s.webhookSecret = webhookSecret
	}
}

func (s *Server) Routes() chi.Router {
	r := chi.NewRouter()

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/tasks", s.handleCreateTask)
		r.Get("/tasks", s.handleListTasks)
		r.Get("/tasks/{id}", s.handleGetTask)
		r.Delete("/tasks/{id}", s.handleDeleteTask)
		r.Post("/tasks/{id}/approve", s.handleApproveTask)
		r.Post("/tasks/{id}/feedback", s.handleFeedbackTask)
		r.Get("/tasks/{id}/logs", s.handleStreamLogs)

		r.Get("/agents", s.handleListAgents)

		r.Post("/webhooks/github", s.handleGitHubWebhook)

		r.Get("/ws", s.handleWebSocket)
	})

	return r
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func ptr(s string) *string {
	return &s
}
