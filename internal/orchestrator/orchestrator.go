package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jcwearn/agent-orchestrator/internal/coder"
	"github.com/jcwearn/agent-orchestrator/internal/store"
)

// Notifier is called at lifecycle transitions for GitHub-sourced tasks.
// The github.Notifier satisfies this interface structurally.
type Notifier interface {
	NotifyPlanReady(ctx context.Context, owner, repo string, issue int, plan string) (commentID int64, err error)
	CheckApproval(ctx context.Context, owner, repo string, issue int, commentID int64) (approved bool, feedback string, err error)
	NotifyComplete(ctx context.Context, owner, repo string, issue int, prURL string) error
	NotifyFailed(ctx context.Context, owner, repo string, issue int, reason string) error
}

// Config holds orchestrator settings.
type Config struct {
	TickInterval time.Duration
	OnEvent      func(taskID, eventType string)
	Notifier     Notifier
}

// DefaultConfig returns sensible defaults: 5-second tick interval.
func DefaultConfig() Config {
	return Config{TickInterval: 5 * time.Second}
}

// Orchestrator polls for queued tasks, assigns workspaces, and drives them
// through the plan → approval → implement lifecycle.
type Orchestrator struct {
	store    *store.Store
	executor coder.WorkspaceExecutor
	pool     *coder.Pool
	logger   *slog.Logger
	config   Config
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
		o.pool.Release(workspace)
		return fmt.Errorf("mark task planning: %w", err)
	}
	o.publishEvent(task.ID, "task.updated")

	go o.runTask(ctx, task, workspace)
	return nil
}

// processApprovedTasks finds awaiting_approval tasks that have been approved
// and starts the implementation step for each.
func (o *Orchestrator) processApprovedTasks(ctx context.Context) error {
	tasks, err := o.store.ListTasks(ctx, StatusAwaitingApproval)
	if err != nil {
		return err
	}

	for i := range tasks {
		t := &tasks[i]

		// For unapproved GitHub tasks with a plan comment, poll GitHub for approval.
		if !isApproved(t) && o.isGitHubTask(t) && t.PlanCommentID != nil {
			approved, feedback, err := o.config.Notifier.CheckApproval(ctx, *t.GithubOwner, *t.GithubRepo, *t.GithubIssue, int64(*t.PlanCommentID))
			if err != nil {
				o.logger.Error("check approval", "task_id", t.ID, "error", err)
				continue
			}
			if approved {
				t.PlanFeedback = &approvedValue
				if err := o.store.UpdateTask(ctx, t.ID, t); err != nil {
					o.logger.Error("update task after github approval", "task_id", t.ID, "error", err)
					continue
				}
				o.publishEvent(t.ID, "task.updated")
			} else if feedback != "" {
				t.PlanFeedback = &feedback
				t.PlanRevision++
				if err := o.store.UpdateTask(ctx, t.ID, t); err != nil {
					o.logger.Error("update task with github feedback", "task_id", t.ID, "error", err)
				}
				o.publishEvent(t.ID, "task.updated")
				continue
			} else {
				continue
			}
		}

		if !isApproved(t) {
			continue
		}

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
			o.pool.Release(workspace)
			o.logger.Error("mark task implementing", "task_id", t.ID, "error", err)
			continue
		}
		o.publishEvent(t.ID, "task.updated")

		go o.runImplement(ctx, t, workspace)
	}
	return nil
}

func (o *Orchestrator) publishEvent(taskID, eventType string) {
	if o.config.OnEvent != nil {
		o.config.OnEvent(taskID, eventType)
	}
}

// isGitHubTask returns true if the task originated from GitHub and a notifier is configured.
func (o *Orchestrator) isGitHubTask(task *store.Task) bool {
	return o.config.Notifier != nil && task.SourceType == "github" &&
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
