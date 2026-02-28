package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jcwearn/agent-orchestrator/internal/coder"
	"github.com/jcwearn/agent-orchestrator/internal/orchestrator"
	"github.com/jcwearn/agent-orchestrator/internal/server"
	"github.com/jcwearn/agent-orchestrator/internal/store"
)

func main() {
	port := envOr("PORT", "8080")
	dbPath := envOr("DATABASE_PATH", "./data/orchestrator.db")
	logLevel := envOr("LOG_LEVEL", "info")

	var level slog.Level
	if err := level.UnmarshalText([]byte(logLevel)); err != nil {
		level = slog.LevelInfo
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		logger.Error("create data directory", "error", err)
		os.Exit(1)
	}

	dsn := dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	db.SetMaxOpenConns(1)

	s := store.New(db, logger)
	defer s.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := s.RunMigrations(ctx); err != nil {
		logger.Error("run migrations", "error", err)
		os.Exit(1)
	}

	exec := coder.NewExecutor(logger, nil)
	pool := coder.NewPool(coder.DefaultWorkspaces)

	hub := server.NewHub()

	orchConfig := orchestrator.DefaultConfig()
	orchConfig.OnEvent = func(taskID, eventType string) {
		task, err := s.GetTask(ctx, taskID)
		if err != nil {
			logger.Error("fetch task for event", "task_id", taskID, "error", err)
			return
		}
		hub.Broadcast(server.Event{Type: eventType, TaskID: taskID, Data: task})
	}

	orch := orchestrator.New(s, exec, pool, logger, orchConfig)
	go func() {
		if err := orch.Run(ctx); err != nil && err != context.Canceled {
			logger.Error("orchestrator error", "error", err)
		}
	}()

	srv := server.New(s, pool, exec, hub, logger)

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Ping(r.Context()); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"status": "error", "error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	r.Mount("/", srv.Routes())

	httpSrv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	go func() {
		logger.Info("server starting", "port", port)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown error", "error", err)
		os.Exit(1)
	}

	logger.Info("server stopped")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
