package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jcwearn/agent-orchestrator/internal/store"
)

// stepPlan runs the planning step: clone the repo and invoke Claude CLI to produce a plan.
func (o *Orchestrator) stepPlan(ctx context.Context, task *store.Task, workspace string) error {
	stdout := o.newLogWriter(ctx, task.ID, "plan", "stdout")
	stderr := o.newLogWriter(ctx, task.ID, "plan", "stderr")

	cmd := fmt.Sprintf(
		"cd /home/coder && git clone %s repo && cd repo && git checkout %s && claude --session-id %s -p %s --print",
		shellQuote(task.RepoURL),
		shellQuote(task.BaseBranch),
		shellQuote(task.SessionID),
		shellQuote(buildPlanPrompt(task)),
	)

	_, err := o.executor.SSH(ctx, workspace, cmd, stdout, stderr)
	stdout.Flush()
	stderr.Flush()

	if err != nil {
		return fmt.Errorf("plan step: %w", err)
	}

	plan := stdout.String()
	task.Plan = &plan
	return nil
}

// stepImplement runs the implementation step: resume the Claude session to implement the approved plan.
func (o *Orchestrator) stepImplement(ctx context.Context, task *store.Task, workspace string) error {
	stdout := o.newLogWriter(ctx, task.ID, "implement", "stdout")
	stderr := o.newLogWriter(ctx, task.ID, "implement", "stderr")

	cmd := fmt.Sprintf(
		"cd /home/coder && git clone %s repo && cd repo && git checkout %s && claude --resume %s -p %s --print",
		shellQuote(task.RepoURL),
		shellQuote(task.BaseBranch),
		shellQuote(task.SessionID),
		shellQuote(buildImplementPrompt(task)),
	)

	_, err := o.executor.SSH(ctx, workspace, cmd, stdout, stderr)
	stdout.Flush()
	stderr.Flush()

	if err != nil {
		return fmt.Errorf("implement step: %w", err)
	}
	return nil
}

// startWorkspace starts the assigned workspace.
func (o *Orchestrator) startWorkspace(ctx context.Context, task *store.Task, workspace string) error {
	if err := o.executor.StartWorkspace(ctx, workspace); err != nil {
		return fmt.Errorf("start workspace %s: %w", workspace, err)
	}
	ws := workspace
	task.WorkspaceID = &ws
	return nil
}

// stopAndRelease stops the workspace and releases it back to the pool.
func (o *Orchestrator) stopAndRelease(ctx context.Context, workspace string) {
	if err := o.executor.StopWorkspace(ctx, workspace); err != nil {
		o.logger.Error("stop workspace", "workspace", workspace, "error", err)
	}
	if err := o.pool.Release(workspace); err != nil {
		o.logger.Error("release workspace", "workspace", workspace, "error", err)
	}
}

// releaseWorkspace releases the workspace without stopping it. Used when we
// want to free the slot (e.g., during approval wait) but may reuse the workspace later.
func (o *Orchestrator) releaseWorkspace(ctx context.Context, task *store.Task, workspace string) {
	if err := o.pool.Release(workspace); err != nil {
		o.logger.Error("release workspace", "workspace", workspace, "error", err)
	}
	task.WorkspaceID = nil
}

// failTask marks the task as failed, records the error, and releases the workspace.
func (o *Orchestrator) failTask(ctx context.Context, task *store.Task, workspace string, taskErr error) {
	task.Status = StatusFailed
	errMsg := taskErr.Error()
	task.ErrorMessage = &errMsg
	now := time.Now().UTC()
	task.CompletedAt = &now

	if err := o.store.UpdateTask(ctx, task.ID, task); err != nil {
		o.logger.Error("update failed task", "task_id", task.ID, "error", err)
	}
	o.stopAndRelease(ctx, workspace)
}

// recoverActiveTasks marks any planning/implementing tasks as failed on startup.
// This handles crash recovery — these tasks were interrupted and cannot resume.
func (o *Orchestrator) recoverActiveTasks(ctx context.Context) error {
	tasks, err := o.activeTasks(ctx)
	if err != nil {
		return fmt.Errorf("recover active tasks: %w", err)
	}

	for i := range tasks {
		t := &tasks[i]
		o.logger.Warn("recovering stale task", "task_id", t.ID, "status", t.Status)
		t.Status = StatusFailed
		errMsg := "recovered: task was active when orchestrator restarted"
		t.ErrorMessage = &errMsg
		now := time.Now().UTC()
		t.CompletedAt = &now
		if err := o.store.UpdateTask(ctx, t.ID, t); err != nil {
			return fmt.Errorf("fail recovered task %s: %w", t.ID, err)
		}
	}
	return nil
}

func buildPlanPrompt(task *store.Task) string {
	return fmt.Sprintf(
		"You are an AI coding agent. Read the following task and produce a detailed implementation plan. "+
			"Do NOT implement anything yet — only plan.\n\nTask: %s",
		task.Prompt,
	)
}

func buildImplementPrompt(task *store.Task) string {
	return fmt.Sprintf(
		"The plan has been approved. Implement it now. "+
			"Create a new branch, make all changes, commit, and push.\n\nApproved plan:\n%s",
		stringVal(task.Plan),
	)
}

func stringVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// shellQuote wraps a string in single quotes, escaping embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
