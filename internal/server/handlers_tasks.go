package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	gogithub "github.com/google/go-github/v83/github"
	"github.com/google/uuid"
	"github.com/jcwearn/agent-orchestrator/internal/store"
)

type CreateTaskRequest struct {
	Prompt      string `json:"prompt"`
	RepoURL     string `json:"repo_url"`
	BaseBranch  string `json:"base_branch"`
	CreateIssue bool   `json:"create_issue"`
}

type ApproveRequest struct {
	RunTests  bool   `json:"run_tests"`
	Decisions string `json:"decisions"`
}

type FeedbackRequest struct {
	Feedback  string `json:"feedback"`
	Decisions string `json:"decisions"`
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req CreateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Prompt == "" {
		writeError(w, http.StatusBadRequest, "prompt is required")
		return
	}
	if req.RepoURL == "" {
		writeError(w, http.StatusBadRequest, "repo_url is required")
		return
	}
	if req.BaseBranch == "" {
		req.BaseBranch = "main"
	}

	task := &store.Task{
		Prompt:     req.Prompt,
		RepoURL:    req.RepoURL,
		BaseBranch: req.BaseBranch,
		SourceType: "api",
		SessionID:  uuid.New().String(),
	}

	if req.CreateIssue {
		if s.githubClient == nil {
			writeError(w, http.StatusBadRequest, "GitHub integration not configured")
			return
		}

		owner, repo, err := parseGitHubRepo(req.RepoURL)
		if err != nil || owner == "" {
			writeError(w, http.StatusBadRequest, "repo_url must be a valid GitHub repository URL")
			return
		}

		title, body := splitPromptForIssue(req.Prompt)
		issue, _, err := s.githubClient.Issues.Create(r.Context(), owner, repo, &gogithub.IssueRequest{
			Title:  &title,
			Body:   &body,
			Labels: &[]string{aiTaskLabel},
		})
		if err != nil {
			s.logger.Error("create github issue", "error", err)
			writeError(w, http.StatusBadGateway, fmt.Sprintf("failed to create GitHub issue: %v", err))
			return
		}

		issueNumber := issue.GetNumber()
		task.GithubOwner = &owner
		task.GithubRepo = &repo
		task.GithubIssue = &issueNumber
	}

	if err := s.store.CreateTask(r.Context(), task); err != nil {
		s.logger.Error("create task", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	s.hub.Broadcast(Event{Type: "task.created", TaskID: task.ID, Data: task})
	writeJSON(w, http.StatusCreated, task)
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")

	tasks, err := s.store.ListTasks(r.Context(), status)
	if err != nil {
		s.logger.Error("list tasks", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if tasks == nil {
		tasks = []store.Task{}
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	task, err := s.store.GetTask(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	if err != nil {
		s.logger.Error("get task", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	writeJSON(w, http.StatusOK, task)
}

func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	err := s.store.DeleteTask(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	if err != nil {
		s.logger.Error("delete task", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	s.hub.Broadcast(Event{Type: "task.deleted", TaskID: id})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleApproveTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	task, err := s.store.GetTask(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	if err != nil {
		s.logger.Error("get task for approve", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if task.Status != "awaiting_approval" {
		writeError(w, http.StatusConflict, "task is not awaiting approval")
		return
	}

	// Parse optional body (tolerate empty body for backward compat).
	var req ApproveRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	task.PlanFeedback = ptr("approved")
	task.RunTests = req.RunTests
	if req.Decisions != "" {
		task.Decisions = &req.Decisions
	}
	if err := s.store.UpdateTask(r.Context(), task.ID, task); err != nil {
		s.logger.Error("approve task", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	s.hub.Broadcast(Event{Type: "task.updated", TaskID: task.ID, Data: task})
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) handleFeedbackTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var req FeedbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Feedback == "" {
		writeError(w, http.StatusBadRequest, "feedback is required")
		return
	}

	task, err := s.store.GetTask(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	if err != nil {
		s.logger.Error("get task for feedback", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if task.Status != "awaiting_approval" {
		writeError(w, http.StatusConflict, "task is not awaiting approval")
		return
	}

	task.PlanFeedback = &req.Feedback
	task.PlanRevision++
	if req.Decisions != "" {
		task.Decisions = &req.Decisions
	}
	if err := s.store.UpdateTask(r.Context(), task.ID, task); err != nil {
		s.logger.Error("feedback task", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	s.hub.Broadcast(Event{Type: "task.updated", TaskID: task.ID, Data: task})
	writeJSON(w, http.StatusOK, task)
}

// parseGitHubRepo extracts owner and repo from a GitHub URL.
// Returns empty strings (no error) for non-GitHub hosts.
func parseGitHubRepo(repoURL string) (owner, repo string, err error) {
	u, err := url.Parse(repoURL)
	if err != nil {
		return "", "", err
	}
	if u.Host != "github.com" {
		return "", "", nil
	}
	path := strings.TrimPrefix(u.Path, "/")
	path = strings.TrimSuffix(path, ".git")
	parts := strings.SplitN(path, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid GitHub repo path: %s", u.Path)
	}
	return parts[0], parts[1], nil
}

// splitPromptForIssue splits a prompt into title (first line, max 256 chars)
// and body (remainder).
func splitPromptForIssue(prompt string) (title, body string) {
	title, body, _ = strings.Cut(prompt, "\n")
	if len(title) > 256 {
		title = title[:256]
	}
	body = strings.TrimSpace(body)
	return title, body
}
