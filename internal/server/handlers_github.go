package server

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	gogithub "github.com/google/go-github/v84/github"
	"github.com/google/uuid"
	"github.com/jcwearn/agent-orchestrator/internal/store"
)

const aiTaskLabel = "ai-task"

func (s *Server) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	if s.githubClient == nil {
		writeError(w, http.StatusServiceUnavailable, "GitHub integration not configured")
		return
	}

	payload, err := gogithub.ValidatePayload(r, s.webhookSecret)
	if err != nil {
		s.logger.Error("invalid webhook signature", "error", err)
		writeError(w, http.StatusUnauthorized, "invalid signature")
		return
	}

	event, err := gogithub.ParseWebHook(gogithub.WebHookType(r), payload)
	if err != nil {
		s.logger.Error("failed to parse webhook", "error", err)
		writeError(w, http.StatusBadRequest, "failed to parse webhook")
		return
	}

	switch e := event.(type) {
	case *gogithub.IssuesEvent:
		if err := s.handleIssuesEvent(r, e); err != nil {
			s.logger.Error("failed to handle issues event", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to process event")
			return
		}
	case *gogithub.PullRequestEvent:
		if err := s.handlePullRequestEvent(r, e); err != nil {
			s.logger.Error("failed to handle pull request event", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to process event")
			return
		}
	default:
		s.logger.Info("ignoring unhandled webhook event type", "type", gogithub.WebHookType(r))
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleIssuesEvent(r *http.Request, event *gogithub.IssuesEvent) error {
	switch event.GetAction() {
	case "labeled":
		if event.GetLabel().GetName() != aiTaskLabel {
			return nil
		}
	case "opened":
		if !hasLabel(event.GetIssue(), aiTaskLabel) {
			return nil
		}
	default:
		return nil
	}

	issue := event.GetIssue()
	repo := event.GetRepo()
	owner := repo.GetOwner().GetLogin()
	repoName := repo.GetName()
	issueNumber := issue.GetNumber()

	if len(s.allowedUsers) > 0 {
		author := issue.GetUser().GetLogin()
		if !slices.Contains(s.allowedUsers, author) {
			s.logger.Info("ignoring issue from unauthorized user",
				"user", author, "owner", owner, "repo", repoName, "issue", issueNumber)
			return nil
		}
	}

	s.logger.Info("processing ai-task label",
		"owner", owner, "repo", repoName, "issue", issueNumber)

	// Deduplicate: skip if a non-failed task already exists for this issue.
	existing, err := s.store.GetTaskByGithubIssue(r.Context(), owner, repoName, issueNumber)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("checking existing task: %w", err)
	}
	if existing != nil {
		s.logger.Info("task already exists for issue, skipping",
			"existing_task", existing.ID, "issue", issueNumber)
		return nil
	}

	prompt := issue.GetTitle()
	if body := issue.GetBody(); body != "" {
		prompt = prompt + "\n\n" + body
	}

	baseBranch := repo.GetDefaultBranch()
	if baseBranch == "" {
		baseBranch = "main"
	}

	issueTitle := issue.GetTitle()
	task := &store.Task{
		Title:       &issueTitle,
		Prompt:      prompt,
		RepoURL:     strings.TrimSuffix(repo.GetCloneURL(), ".git"),
		BaseBranch:  baseBranch,
		SourceType:  "github",
		GithubOwner: &owner,
		GithubRepo:  &repoName,
		GithubIssue: &issueNumber,
		SessionID:   uuid.New().String(),
	}

	if err := s.store.CreateTask(r.Context(), task); err != nil {
		if errors.Is(err, store.ErrDuplicateTask) {
			s.logger.Info("duplicate task prevented by constraint, skipping",
				"owner", owner, "repo", repoName, "issue", issueNumber)
			return nil
		}
		return fmt.Errorf("creating task: %w", err)
	}

	s.hub.Broadcast(Event{Type: "task.created", TaskID: task.ID, Data: task})

	// Post acknowledgement comment on the issue.
	comment := fmt.Sprintf(
		"🤖 Task `%s` created. Starting work on this issue...\n\n"+
			"I'll post updates here as I progress through planning, implementation, and PR creation.",
		task.ID,
	)
	_, _, err = s.githubClient.Issues.CreateComment(r.Context(), owner, repoName, issueNumber,
		&gogithub.IssueComment{Body: gogithub.Ptr(comment)})
	if err != nil {
		s.logger.Error("failed to post acknowledgement comment", "issue", issueNumber, "error", err)
		// Non-fatal: task was already created.
	}

	return nil
}

func (s *Server) handlePullRequestEvent(r *http.Request, event *gogithub.PullRequestEvent) error {
	if event.GetAction() != "closed" || !event.GetPullRequest().GetMerged() {
		return nil
	}

	repo := event.GetRepo()
	owner := repo.GetOwner().GetLogin()
	repoName := repo.GetName()
	prNumber := event.GetPullRequest().GetNumber()

	s.logger.Debug("received merged PR webhook",
		"owner", owner, "repo", repoName, "pr", prNumber)

	task, err := s.store.GetTaskByPRNumber(r.Context(), owner, repoName, prNumber)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.logger.Debug("no task found for merged PR",
				"owner", owner, "repo", repoName, "pr", prNumber)
			return nil // not our PR
		}
		return fmt.Errorf("looking up task by PR number: %w", err)
	}

	if task.GithubIssue == nil {
		s.logger.Debug("task has no associated GitHub issue",
			"task_id", task.ID, "owner", owner, "repo", repoName, "pr", prNumber)
		return nil
	}

	s.logger.Info("closing issue after PR merge",
		"owner", owner, "repo", repoName, "pr", prNumber, "issue", *task.GithubIssue)

	state := "closed"
	_, _, err = s.githubClient.Issues.Edit(r.Context(), owner, repoName, *task.GithubIssue,
		&gogithub.IssueRequest{State: &state})
	if err != nil {
		return fmt.Errorf("closing issue %d: %w", *task.GithubIssue, err)
	}

	return nil
}

func hasLabel(issue *gogithub.Issue, name string) bool {
	for _, l := range issue.Labels {
		if l.GetName() == name {
			return true
		}
	}
	return false
}
