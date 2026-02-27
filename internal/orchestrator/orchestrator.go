package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jcwearn/agent-orchestrator/internal/coder"
	"github.com/jcwearn/agent-orchestrator/internal/store"
)

// Config holds orchestrator settings.
type Config struct {
	TickInterval time.Duration
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
		if !isApproved(t) {
			continue
		}

		workspace, err := o.pool.Acquire(t.ID)
		if err != nil {
			o.logger.Debug("no workspace for approved task", "task_id", t.ID)
			return nil // no free workspace; stop processing
		}

		go o.runImplement(ctx, t, workspace)
	}
	return nil
}

// runTask drives a task through the planning step. On success, the task moves
// to awaiting_approval and the workspace is released.
func (o *Orchestrator) runTask(ctx context.Context, task *store.Task, workspace string) {
	o.logger.Info("starting task", "task_id", task.ID, "workspace", workspace)

	task.Status = StatusPlanning
	step := "plan"
	task.CurrentStep = &step
	now := time.Now().UTC()
	task.StartedAt = &now
	if err := o.store.UpdateTask(ctx, task.ID, task); err != nil {
		o.logger.Error("update task to planning", "task_id", task.ID, "error", err)
		o.stopAndRelease(ctx, workspace)
		return
	}

	if err := o.startWorkspace(ctx, task, workspace); err != nil {
		o.failTask(ctx, task, workspace, err)
		return
	}

	if err := o.stepPlan(ctx, task, workspace); err != nil {
		o.failTask(ctx, task, workspace, err)
		return
	}

	task.Status = StatusAwaitingApproval
	task.WorkspaceID = nil
	if err := o.store.UpdateTask(ctx, task.ID, task); err != nil {
		o.logger.Error("update task to awaiting_approval", "task_id", task.ID, "error", err)
	}
	o.stopAndRelease(ctx, workspace)
	o.logger.Info("task plan complete, awaiting approval", "task_id", task.ID)
}

// runImplement drives a task through the implementation step. On success, the
// task moves to complete.
func (o *Orchestrator) runImplement(ctx context.Context, task *store.Task, workspace string) {
	o.logger.Info("starting implementation", "task_id", task.ID, "workspace", workspace)

	task.Status = StatusImplementing
	step := "implement"
	task.CurrentStep = &step
	if err := o.store.UpdateTask(ctx, task.ID, task); err != nil {
		o.logger.Error("update task to implementing", "task_id", task.ID, "error", err)
		o.stopAndRelease(ctx, workspace)
		return
	}

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
	o.stopAndRelease(ctx, workspace)
	o.logger.Info("task complete", "task_id", task.ID)
}
