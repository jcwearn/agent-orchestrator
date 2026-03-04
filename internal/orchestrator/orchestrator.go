package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jcwearn/agent-orchestrator/internal/coder"
	"github.com/jcwearn/agent-orchestrator/internal/store"
)

// ApprovalResult holds the outcome of checking a plan comment for approval.
type ApprovalResult struct {
	Approved  bool
	RunTests  bool
	Decisions string
	Feedback  string
}

// Notifier is called at lifecycle transitions for GitHub-sourced tasks.
type Notifier interface {
	NotifyPlanReady(ctx context.Context, owner, repo string, issue int, plan string) (commentID int64, err error)
	CheckApproval(ctx context.Context, owner, repo string, issue int, commentID int64) (ApprovalResult, error)
	NotifyImplementationStarted(ctx context.Context, owner, repo string, issue int) error
	NotifyComplete(ctx context.Context, owner, repo string, issue int, prURL string) error
	NotifyFailed(ctx context.Context, owner, repo string, issue int, reason string) error
	LinkPRToIssue(ctx context.Context, owner, repo string, prNumber, issue int) error
}

// Config holds orchestrator settings.
type Config struct {
	TickInterval           time.Duration
	VerifyRetryDelay       time.Duration // delay between verifyRepoDir retries (default 5s)
	AgentReadyTimeout      time.Duration // max wait for agent lifecycle "ready" (default 2m)
	AgentReadyPollInterval time.Duration // poll interval for agent readiness (default 5s)
	PlanRetries            int           // retries on empty plan output (default 1; total attempts = 1 + PlanRetries)
	ApprovalTimeout        time.Duration // max age before awaiting_approval tasks are auto-cancelled (default 24h)
	OnEvent                func(taskID, eventType string)
	Notifier               Notifier
}

// DefaultConfig returns sensible defaults: 5-second tick interval.
func DefaultConfig() Config {
	return Config{
		TickInterval:           5 * time.Second,
		VerifyRetryDelay:       5 * time.Second,
		AgentReadyTimeout:      2 * time.Minute,
		AgentReadyPollInterval: 5 * time.Second,
		PlanRetries:            1,
		ApprovalTimeout:        24 * time.Hour,
	}
}

// Orchestrator polls for queued tasks, assigns workspaces, and drives them
// through the plan → approval → implement lifecycle.
type Orchestrator struct {
	store              *store.Store
	executor           coder.WorkspaceExecutor
	pool               *coder.Pool
	logger             *slog.Logger
	config             Config
	rateLimitReset     time.Time
	approvalCheckIndex int
}

// New creates an Orchestrator.
func New(store *store.Store, executor coder.WorkspaceExecutor, pool *coder.Pool, logger *slog.Logger, config Config) *Orchestrator {
	return &Orchestrator{
		store:    store,
		executor: executor,
		pool:     pool,
		logger:   logger,
		config:   config,
	}
}

// Run blocks until ctx is cancelled. It recovers stale tasks on startup, then
// enters the tick loop.
func (o *Orchestrator) Run(ctx context.Context) error {
	if err := o.recoverActiveTasks(ctx); err != nil {
		return fmt.Errorf("orchestrator startup: %w", err)
	}
	o.logger.Info("orchestrator started", "tick_interval", o.config.TickInterval)

	ticker := time.NewTicker(o.config.TickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			o.logger.Info("orchestrator stopping")
			return ctx.Err()
		case <-ticker.C:
			if err := o.tick(ctx); err != nil {
				o.logger.Error("tick error", "error", err)
			}
		}
	}
}

// tick processes one iteration: first handle approved tasks, then pick up
// a new queued task if a workspace is free.
func (o *Orchestrator) tick(ctx context.Context) error {
	if err := o.processApprovedTasks(ctx); err != nil {
		return fmt.Errorf("process approved tasks: %w", err)
	}

	if o.pool.FreeCount() == 0 {
		return nil
	}

	task, err := o.nextTask(ctx)
	if err != nil {
		return fmt.Errorf("next task: %w", err)
	}
	if task == nil {
		return nil
	}

	workspace, err := o.pool.Acquire(task.ID)
	if err != nil {
		return nil // no free workspace, try next tick
	}

	// Mark as planning synchronously before launching the goroutine so the
	// next tick does not pick up the same task while it is still "queued".
	task.Status = StatusPlanning
	task.WorkspaceID = &workspace
	if err := o.store.UpdateTask(ctx, task.ID, task); err != nil {
		if relErr := o.pool.Release(workspace); relErr != nil {
			o.logger.Error("release workspace after update failure", "workspace", workspace, "error", relErr)
		}
		return fmt.Errorf("mark task planning: %w", err)
	}
	o.publishEvent(task.ID, "task.updated")

	go o.runTask(ctx, task, workspace)
	return nil
}

// processApprovedTasks finds awaiting_approval tasks that have been approved
// and starts the implementation step for each.
func (o *Orchestrator) processApprovedTasks(ctx context.Context) error {
	// Rate limit backoff: skip all API calls until the reset time.
	if !o.rateLimitReset.IsZero() && time.Now().Before(o.rateLimitReset) {
		return nil
	}

	tasks, err := o.store.ListTasks(ctx, StatusAwaitingApproval)
	if err != nil {
		return err
	}

	approvalTimeout := o.config.ApprovalTimeout
	if approvalTimeout == 0 {
		approvalTimeout = 24 * time.Hour
	}

	// First pass: expire stale tasks, collect already-approved and unapproved GitHub tasks.
	var approved []*store.Task
	var needsCheck []*store.Task
	for i := range tasks {
		t := &tasks[i]

		// Expire stale tasks that have been awaiting approval too long.
		if time.Since(t.CreatedAt) > approvalTimeout {
			o.logger.Info("expiring stale task", "task_id", t.ID, "age", time.Since(t.CreatedAt))
			t.Status = StatusCancelled
			now := time.Now().UTC()
			t.CompletedAt = &now
			msg := "Cancelled: approval timeout exceeded"
			t.ErrorMessage = &msg
			if err := o.store.UpdateTask(ctx, t.ID, t); err != nil {
				o.logger.Error("expire stale task", "task_id", t.ID, "error", err)
			}
			o.publishEvent(t.ID, "task.updated")
			if o.isGitHubTask(t) {
				if err := o.config.Notifier.NotifyFailed(ctx, *t.GithubOwner, *t.GithubRepo, *t.GithubIssue, "Task cancelled: approval timeout exceeded"); err != nil {
					o.logger.Error("notify stale task expired", "task_id", t.ID, "error", err)
				}
			}
			continue
		}

		if isApproved(t) {
			approved = append(approved, t)
		} else if o.isGitHubTask(t) && t.PlanCommentID != nil {
			needsCheck = append(needsCheck, t)
		}
	}

	// Round-robin: check only one unapproved GitHub task per tick.
	if len(needsCheck) > 0 {
		idx := o.approvalCheckIndex % len(needsCheck)
		o.approvalCheckIndex++
		t := needsCheck[idx]

		result, err := o.config.Notifier.CheckApproval(ctx, *t.GithubOwner, *t.GithubRepo, *t.GithubIssue, int64(*t.PlanCommentID))
		if err != nil {
			if resetAt, ok := isRateLimitError(err); ok {
				o.logger.Warn("rate limited by GitHub, backing off", "reset_at", resetAt)
				o.rateLimitReset = resetAt
				return nil
			}
			o.logger.Error("check approval", "task_id", t.ID, "error", err)
		} else if result.Approved {
			t.PlanFeedback = &approvedValue
			t.RunTests = result.RunTests
			if result.Decisions != "" {
				t.Decisions = &result.Decisions
			}
			if err := o.store.UpdateTask(ctx, t.ID, t); err != nil {
				o.logger.Error("update task after github approval", "task_id", t.ID, "error", err)
			} else {
				o.publishEvent(t.ID, "task.updated")
				approved = append(approved, t)
			}
		} else if result.Feedback != "" {
			t.PlanFeedback = &result.Feedback
			t.PlanRevision++
			if result.Decisions != "" {
				t.Decisions = &result.Decisions
			}
			if err := o.store.UpdateTask(ctx, t.ID, t); err != nil {
				o.logger.Error("update task with github feedback", "task_id", t.ID, "error", err)
			}
			o.publishEvent(t.ID, "task.updated")
		}
	}

	// Launch implementation for approved tasks.
	for _, t := range approved {
		workspace, err := o.pool.Acquire(t.ID)
		if err != nil {
			o.logger.Debug("no workspace for approved task", "task_id", t.ID)
			return nil // no free workspace; stop processing
		}

		// Mark as implementing synchronously before launching the goroutine
		// so the next tick does not pick up the same approved task again.
		t.Status = StatusImplementing
		t.WorkspaceID = &workspace
		if err := o.store.UpdateTask(ctx, t.ID, t); err != nil {
			if relErr := o.pool.Release(workspace); relErr != nil {
				o.logger.Error("release workspace after update failure", "workspace", workspace, "error", relErr)
			}
			o.logger.Error("mark task implementing", "task_id", t.ID, "error", err)
			continue
		}
		o.publishEvent(t.ID, "task.updated")

		// For GitHub tasks, post implementation-started comment (non-fatal).
		if o.isGitHubTask(t) {
			if err := o.config.Notifier.NotifyImplementationStarted(ctx, *t.GithubOwner, *t.GithubRepo, *t.GithubIssue); err != nil {
				o.logger.Error("notify implementation started", "task_id", t.ID, "error", err)
			}
		}

		go o.runImplement(ctx, t, workspace)
	}
	return nil
}

func (o *Orchestrator) publishEvent(taskID, eventType string) {
	if o.config.OnEvent != nil {
		o.config.OnEvent(taskID, eventType)
	}
}

// isGitHubTask returns true if the task has GitHub metadata and a notifier is configured.
func (o *Orchestrator) isGitHubTask(task *store.Task) bool {
	return o.config.Notifier != nil &&
		task.GithubOwner != nil && task.GithubRepo != nil && task.GithubIssue != nil
}

// runTask drives a task through the planning step. On success, the task moves
// to awaiting_approval and the workspace is released.
func (o *Orchestrator) runTask(ctx context.Context, task *store.Task, workspace string) {
	o.logger.Info("starting task", "task_id", task.ID, "workspace", workspace)

	// Status is already set to planning by tick(). Set remaining fields.
	step := "plan"
	task.CurrentStep = &step
	now := time.Now().UTC()
	task.StartedAt = &now
	if err := o.store.UpdateTask(ctx, task.ID, task); err != nil {
		o.logger.Error("update task to planning", "task_id", task.ID, "error", err)
		o.stopAndRelease(ctx, workspace)
		return
	}
	o.publishEvent(task.ID, "task.updated")

	if err := o.startWorkspace(ctx, task, workspace); err != nil {
		o.failTask(ctx, task, workspace, err)
		return
	}

	if err := o.stepPlan(ctx, task, workspace); err != nil {
		o.failTask(ctx, task, workspace, err)
		return
	}

	// For GitHub tasks, post plan as issue comment.
	if o.isGitHubTask(task) && task.Plan != nil {
		commentID, err := o.config.Notifier.NotifyPlanReady(ctx, *task.GithubOwner, *task.GithubRepo, *task.GithubIssue, *task.Plan)
		if err != nil {
			o.logger.Error("notify plan ready", "task_id", task.ID, "error", err)
		} else {
			cid := int(commentID)
			task.PlanCommentID = &cid
		}
	}

	// Check if task was cancelled while planning.
	latest, err := o.store.GetTask(ctx, task.ID)
	if err == nil && latest.Status == StatusCancelled {
		o.logger.Info("task cancelled during planning", "task_id", task.ID)
		o.stopAndRelease(ctx, workspace)
		return
	}

	task.Status = StatusAwaitingApproval
	task.WorkspaceID = nil
	if err := o.store.UpdateTask(ctx, task.ID, task); err != nil {
		o.logger.Error("update task to awaiting_approval", "task_id", task.ID, "error", err)
	}
	o.publishEvent(task.ID, "task.updated")
	o.stopAndRelease(ctx, workspace)
	o.logger.Info("task plan complete, awaiting approval", "task_id", task.ID)
}

// runImplement drives a task through the implementation step. On success, the
// task moves to complete.
func (o *Orchestrator) runImplement(ctx context.Context, task *store.Task, workspace string) {
	o.logger.Info("starting implementation", "task_id", task.ID, "workspace", workspace)

	// Status is already set to implementing by processApprovedTasks(). Set remaining fields.
	step := "implement"
	task.CurrentStep = &step
	if err := o.store.UpdateTask(ctx, task.ID, task); err != nil {
		o.logger.Error("update task to implementing", "task_id", task.ID, "error", err)
		o.stopAndRelease(ctx, workspace)
		return
	}
	o.publishEvent(task.ID, "task.updated")

	if err := o.startWorkspace(ctx, task, workspace); err != nil {
		o.failTask(ctx, task, workspace, err)
		return
	}

	if err := o.stepImplement(ctx, task, workspace); err != nil {
		o.failTask(ctx, task, workspace, err)
		return
	}

	// For GitHub tasks, ensure the PR body links to the issue for auto-close on merge.
	if o.isGitHubTask(task) && task.PRNumber != nil {
		if err := o.config.Notifier.LinkPRToIssue(ctx, *task.GithubOwner, *task.GithubRepo, *task.PRNumber, *task.GithubIssue); err != nil {
			o.logger.Error("link PR to issue", "task_id", task.ID, "error", err)
		}
	}

	task.Status = StatusComplete
	now := time.Now().UTC()
	task.CompletedAt = &now
	if err := o.store.UpdateTask(ctx, task.ID, task); err != nil {
		o.logger.Error("update task to complete", "task_id", task.ID, "error", err)
	}
	o.publishEvent(task.ID, "task.updated")

	// For GitHub tasks, post completion comment.
	if o.isGitHubTask(task) {
		prURL := ""
		if task.PRUrl != nil {
			prURL = *task.PRUrl
		}
		if err := o.config.Notifier.NotifyComplete(ctx, *task.GithubOwner, *task.GithubRepo, *task.GithubIssue, prURL); err != nil {
			o.logger.Error("notify complete", "task_id", task.ID, "error", err)
		}
	}

	o.stopAndRelease(ctx, workspace)
	o.logger.Info("task complete", "task_id", task.ID)
}
