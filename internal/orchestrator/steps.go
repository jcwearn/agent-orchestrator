package orchestrator

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/jcwearn/agent-orchestrator/internal/store"
)

// stepPlan invokes Claude CLI to produce a plan. The repo is already cloned
// by the workspace template via the repo_url parameter.
func (o *Orchestrator) stepPlan(ctx context.Context, task *store.Task, workspace string) error {
	stdout := o.newLogWriter(ctx, task.ID, "plan", "stdout")
	stderr := o.newLogWriter(ctx, task.ID, "plan", "stderr")

	repoDir := "/home/coder/" + repoName(task.RepoURL)
	cmd := fmt.Sprintf(
		"cd %s && git checkout %s > /dev/null 2>&1 && claude --session-id %s --permission-mode plan -p %s --print",
		shellQuote(repoDir),
		shellQuote(task.BaseBranch),
		shellQuote(task.SessionID),
		shellQuote(buildPlanPrompt(task)),
	)

	_, err := o.executor.SSH(ctx, workspace, cmd, stdout, stderr)
	_ = stdout.Flush()
	_ = stderr.Flush()

	if err != nil {
		return fmt.Errorf("plan step: %w\n\nstderr tail:\n%s", err, stderr.Tail(20))
	}

	plan := stdout.String()
	task.Plan = &plan
	return nil
}

// stepImplement resumes the Claude session to implement the approved plan.
// The repo is already present from the workspace template.
func (o *Orchestrator) stepImplement(ctx context.Context, task *store.Task, workspace string) error {
	stdout := o.newLogWriter(ctx, task.ID, "implement", "stdout")
	stderr := o.newLogWriter(ctx, task.ID, "implement", "stderr")

	repoDir := "/home/coder/" + repoName(task.RepoURL)
	cmd := fmt.Sprintf(
		"cd %s && git checkout %s > /dev/null 2>&1 && claude --resume %s -p %s --print",
		shellQuote(repoDir),
		shellQuote(task.BaseBranch),
		shellQuote(task.SessionID),
		shellQuote(buildImplementPrompt(task)),
	)

	_, err := o.executor.SSH(ctx, workspace, cmd, stdout, stderr)
	_ = stdout.Flush()
	_ = stderr.Flush()

	if err != nil {
		return fmt.Errorf("implement step: %w\n\nstderr tail:\n%s", err, stderr.Tail(20))
	}
	return nil
}

// startWorkspace starts the assigned workspace, passing the repo URL as a
// template parameter so the workspace clones it on boot.
func (o *Orchestrator) startWorkspace(ctx context.Context, task *store.Task, workspace string) error {
	params := map[string]string{"git_repo": task.RepoURL}
	if err := o.executor.StartWorkspace(ctx, workspace, params); err != nil {
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
	o.publishEvent(task.ID, "task.updated")

	// For GitHub tasks, post failure comment.
	if o.isGitHubTask(task) {
		if err := o.config.Notifier.NotifyFailed(ctx, *task.GithubOwner, *task.GithubRepo, *task.GithubIssue, errMsg); err != nil {
			o.logger.Error("notify failed", "task_id", task.ID, "error", err)
		}
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
		"You are a coding agent operating in plan-only mode. Your goal is to explore the codebase "+
			"and produce an implementation plan for the task below.\n\n"+
			"BEFORE writing the plan, use your tools to thoroughly explore the repository:\n"+
			"- Use Glob to find relevant files and understand the project structure\n"+
			"- Use Grep to search for related code, patterns, and existing conventions\n"+
			"- Use Read to examine key files, interfaces, and functions you will need to modify or extend\n"+
			"- Use Bash for read-only commands (e.g., git log, ls) to gather additional context\n\n"+
			"After exploring, output ONLY the implementation plan in markdown. The plan must include:\n\n"+
			"1. **Objective** -- One-sentence restatement of what will be built or changed\n"+
			"2. **Key Findings** -- What you discovered during exploration that informs the approach "+
			"(existing patterns to follow, utilities to reuse, constraints found)\n"+
			"3. **Phases** -- Ordered implementation phases, each with:\n"+
			"   - Description of the work\n"+
			"   - Specific files to create or modify (full paths)\n"+
			"   - Key functions, interfaces, or types involved\n"+
			"   - Concrete steps to take\n"+
			"4. **Verification** -- How to test and validate the changes\n\n"+
			"Reference specific files and code you found during exploration. "+
			"Do not implement anything -- only plan.\n\n"+
			"Task: %s",
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

// repoName extracts the repository name from a URL.
// e.g. "https://github.com/user/repo.git" → "repo"
func repoName(repoURL string) string {
	base := path.Base(repoURL)
	return strings.TrimSuffix(base, ".git")
}

// shellQuote wraps a string in single quotes, escaping embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
