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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jcwearn/agent-orchestrator/internal/coder"
	ghclient "github.com/jcwearn/agent-orchestrator/internal/github"
	"github.com/jcwearn/agent-orchestrator/internal/orchestrator"
	"github.com/jcwearn/agent-orchestrator/internal/server"
	"github.com/jcwearn/agent-orchestrator/internal/store"
	"github.com/jcwearn/agent-orchestrator/web"
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
	defer func() { _ = s.Close() }()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := s.RunMigrations(ctx); err != nil {
		logger.Error("run migrations", "error", err)
		os.Exit(1)
	}

	exec := coder.NewExecutor(logger, nil)
	pool := coder.NewPool(coder.DefaultWorkspaces)

	hub := server.NewHub()

	// GitHub App integration (optional — disabled if env vars are unset).
	var ghClient *ghclient.Client
	var serverOpts []server.Option
	appID := os.Getenv("GITHUB_APP_ID")
	installationID := os.Getenv("GITHUB_APP_INSTALLATION_ID")
	privateKey := os.Getenv("GITHUB_APP_PRIVATE_KEY")
	webhookSecret := os.Getenv("GITHUB_WEBHOOK_SECRET")

	if users := os.Getenv("GITHUB_ALLOWED_USERS"); users != "" {
		serverOpts = append(serverOpts, server.WithAllowedUsers(strings.Split(users, ",")))
	}

	// Auto-create GitHub issues for new tasks (default: true).
	createIssues := envOr("CREATE_GITHUB_ISSUES", "true")
	serverOpts = append(serverOpts, server.WithAutoCreateIssues(createIssues == "true"))

	if appID != "" && installationID != "" && privateKey != "" {
		parsedAppID, err := strconv.ParseInt(appID, 10, 64)
		if err != nil {
			logger.Error("parse GITHUB_APP_ID", "error", err)
			os.Exit(1)
		}
		parsedInstallationID, err := strconv.ParseInt(installationID, 10, 64)
		if err != nil {
			logger.Error("parse GITHUB_APP_INSTALLATION_ID", "error", err)
			os.Exit(1)
		}
		ghClient, err = ghclient.NewClient(parsedAppID, parsedInstallationID, []byte(privateKey))
		if err != nil {
			logger.Error("create github client", "error", err)
			os.Exit(1)
		}
		serverOpts = append(serverOpts, server.WithGitHub(ghClient, []byte(webhookSecret)))
		logger.Info("github integration enabled", "app_id", parsedAppID, "installation_id", parsedInstallationID)
	}

	orchConfig := orchestrator.DefaultConfig()
	orchConfig.OnEvent = func(taskID, eventType string) {
		task, err := s.GetTask(ctx, taskID)
		if err != nil {
			logger.Error("fetch task for event", "task_id", taskID, "error", err)
			return
		}
		hub.Broadcast(server.Event{Type: eventType, TaskID: taskID, Data: task})
	}

	// Wire notifier adapter if GitHub is configured.
	if ghClient != nil {
		notifier := ghclient.NewNotifier(ghClient, logger)
		orchConfig.Notifier = &notifierAdapter{notifier: notifier}
	}

	// Periodically clean up expired sessions.
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.DeleteExpiredSessions(ctx); err != nil {
					logger.Error("delete expired sessions", "error", err)
				}
			}
		}
	}()

	orch := orchestrator.New(s, exec, pool, logger, orchConfig)
	go func() {
		if err := orch.Run(ctx); err != nil && err != context.Canceled {
			logger.Error("orchestrator error", "error", err)
		}
	}()

	srv := server.New(s, pool, exec, hub, logger, serverOpts...)

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Ping(r.Context()); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "error", "error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	r.Mount("/", srv.Routes())
	r.NotFound(server.SPAHandler(web.DistFS).ServeHTTP)

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

// notifierAdapter bridges github.Notifier (which returns ApprovalResult) to the
// orchestrator.Notifier interface (which uses flat return values).
type notifierAdapter struct {
	notifier *ghclient.Notifier
}

func (a *notifierAdapter) NotifyPlanReady(ctx context.Context, owner, repo string, issue int, plan string) (int64, error) {
	return a.notifier.NotifyPlanReady(ctx, owner, repo, issue, plan)
}

func (a *notifierAdapter) CheckApproval(ctx context.Context, owner, repo string, issue int, commentID int64) (orchestrator.ApprovalResult, error) {
	result, err := a.notifier.CheckApproval(ctx, owner, repo, issue, commentID)
	if err != nil {
		return orchestrator.ApprovalResult{}, err
	}
	return orchestrator.ApprovalResult{
		Approved:  result.Approved,
		RunTests:  result.RunTests,
		Decisions: result.Decisions,
		Feedback:  result.Feedback,
	}, nil
}

func (a *notifierAdapter) NotifyImplementationStarted(ctx context.Context, owner, repo string, issue int) error {
	return a.notifier.NotifyImplementationStarted(ctx, owner, repo, issue)
}

func (a *notifierAdapter) NotifyComplete(ctx context.Context, owner, repo string, issue int, prURL string) error {
	return a.notifier.NotifyComplete(ctx, owner, repo, issue, prURL)
}

func (a *notifierAdapter) NotifyFailed(ctx context.Context, owner, repo string, issue int, reason string) error {
	return a.notifier.NotifyFailed(ctx, owner, repo, issue, reason)
}

func (a *notifierAdapter) LinkPRToIssue(ctx context.Context, owner, repo string, prNumber, issue int) error {
	return a.notifier.LinkPRToIssue(ctx, owner, repo, prNumber, issue)
}
