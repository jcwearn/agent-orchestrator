package orchestrator

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/jcwearn/agent-orchestrator/internal/store"
)

// verifyRepoDir checks that the expected repo directory exists in the workspace.
// This catches cases where the Coder parameter didn't apply (stale workspace,
// parameter mismatch, clone failure).
//
// Retries up to 5 times with 5s delays to handle NFS attribute caching and
// workspace startup timing races (wait_for_rollout=false + start_blocks_login).
func (o *Orchestrator) verifyRepoDir(ctx context.Context, workspace, repoDir string) error {
	const maxAttempts = 5
	retryDelay := o.config.VerifyRetryDelay
	if retryDelay == 0 {
		retryDelay = 5 * time.Second
	}

	cmd := fmt.Sprintf("test -d %s/.git", shellQuote(repoDir))

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var stdout, stderr bytes.Buffer
		_, err := o.executor.SSH(ctx, workspace, cmd, &stdout, &stderr)
		if err == nil {
			if attempt > 1 {
				o.logger.Info("verifyRepoDir succeeded after retry",
					"workspace", workspace, "repo_dir", repoDir, "attempt", attempt)
			}
			return nil
		}
		lastErr = err
		o.logger.Warn("verifyRepoDir attempt failed",
			"workspace", workspace, "repo_dir", repoDir,
			"attempt", attempt, "max_attempts", maxAttempts, "error", err)

		if attempt < maxAttempts {
			select {
			case <-ctx.Done():
				return fmt.Errorf("repo directory %s verification cancelled: %w", repoDir, ctx.Err())
			case <-time.After(retryDelay):
			}
		}
	}

	// Collect diagnostics on final failure.
	var diagOut, diagErr bytes.Buffer
	diagCmd := fmt.Sprintf("ls -la %s/ 2>&1 || echo 'parent dir not found'; ls -la %s/.git 2>&1 || echo '.git not found'",
		shellQuote(path.Dir(repoDir+"/x")), shellQuote(repoDir))
	_, _ = o.executor.SSH(ctx, workspace, diagCmd, &diagOut, &diagErr)

	return fmt.Errorf("repo directory %s not found after %d attempts: %w\n\ndiagnostics:\n%s",
		repoDir, maxAttempts, lastErr, diagOut.String())
}

// stepPlan invokes Claude CLI to produce a plan. The repo is already cloned
// by the workspace template via the repo_url parameter.
func (o *Orchestrator) stepPlan(ctx context.Context, task *store.Task, workspace string) error {
	stdout := o.newLogWriter(ctx, task.ID, "plan", "stdout")
	stderr := o.newLogWriter(ctx, task.ID, "plan", "stderr")

	repoDir := "/home/coder/" + repoName(task.RepoURL)
	if err := o.verifyRepoDir(ctx, workspace, repoDir); err != nil {
		return err
	}
	cmd := fmt.Sprintf(
		"cd %s && git checkout %s > /dev/null 2>&1 && TERM=dumb claude --session-id %s -p %s --print",
		shellQuote(repoDir),
		shellQuote(task.BaseBranch),
		shellQuote(task.SessionID),
		shellQuote(buildPlanPrompt(task)),
	)

	_, err := o.executor.SSH(ctx, workspace, cmd, stdout, stderr)
	_ = stdout.Flush()
	_ = stderr.Flush()

	o.logger.Info("plan step SSH completed",
		"task_id", task.ID,
		"stdout_len", len(stdout.String()),
		"stderr_len", len(stderr.String()))

	if err != nil {
		return fmt.Errorf("plan step: %w\n\nstderr tail:\n%s", err, stderr.Tail(20))
	}

	plan := stdout.String()
	if strings.TrimSpace(plan) == "" {
		return fmt.Errorf("plan step produced empty output\n\nstderr tail:\n%s\n\nstdout tail:\n%s", stderr.Tail(20), stdout.Tail(20))
	}
	task.Plan = &plan
	return nil
}

// stepImplement resumes the Claude session to implement the approved plan.
// The repo is already present from the workspace template.
func (o *Orchestrator) stepImplement(ctx context.Context, task *store.Task, workspace string) error {
	stdout := o.newLogWriter(ctx, task.ID, "implement", "stdout")
	stderr := o.newLogWriter(ctx, task.ID, "implement", "stderr")

	repoDir := "/home/coder/" + repoName(task.RepoURL)
	if err := o.verifyRepoDir(ctx, workspace, repoDir); err != nil {
		return err
	}

	cmd := fmt.Sprintf(
		"cd %s && git checkout %s > /dev/null 2>&1 && TERM=dumb claude --resume %s -p %s --print",
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
// template parameter so the workspace clones it on boot. After the build
// completes, it waits for the agent to reach "ready" before returning.
func (o *Orchestrator) startWorkspace(ctx context.Context, task *store.Task, workspace string) error {
	params := map[string]string{
		"git_repo":     task.RepoURL,
		"cpu":          "4",
		"memory":       "8",
		"dotfiles_uri": "https://github.com/jcwearn/dotfiles-coder",
	}
	if err := o.executor.StartWorkspace(ctx, workspace, params); err != nil {
		return fmt.Errorf("start workspace %s: %w", workspace, err)
	}
	if err := o.waitForAgentReady(ctx, workspace); err != nil {
		return fmt.Errorf("wait for workspace %s agent: %w", workspace, err)
	}
	ws := workspace
	task.WorkspaceID = &ws
	return nil
}

// waitForAgentReady polls ListWorkspaces until the named workspace's agent
// reports lifecycle_state "ready". It returns an error immediately on
// "start_error" or "start_timeout", and returns a timeout error if the agent
// doesn't become ready within AgentReadyTimeout.
func (o *Orchestrator) waitForAgentReady(ctx context.Context, workspace string) error {
	timeout := o.config.AgentReadyTimeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	poll := o.config.AgentReadyPollInterval
	if poll == 0 {
		poll = 5 * time.Second
	}

	deadline := time.After(timeout)
	for {
		workspaces, err := o.executor.ListWorkspaces(ctx)
		if err != nil {
			return fmt.Errorf("list workspaces: %w", err)
		}

		for _, ws := range workspaces {
			if ws.Name != workspace {
				continue
			}
			switch ws.AgentLifecycle {
			case "ready":
				o.logger.Info("agent ready", "workspace", workspace)
				return nil
			case "start_error":
				return fmt.Errorf("agent startup failed (lifecycle_state: start_error)")
			case "start_timeout":
				return fmt.Errorf("agent startup timed out (lifecycle_state: start_timeout)")
			default:
				o.logger.Debug("waiting for agent ready",
					"workspace", workspace, "lifecycle_state", ws.AgentLifecycle)
			}
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled waiting for agent ready: %w", ctx.Err())
		case <-deadline:
			return fmt.Errorf("timed out waiting for agent ready after %s", timeout)
		case <-time.After(poll):
		}
	}
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
		"You are a coding agent operating in plan-only mode. You are working in the %s repository. "+
			"Your goal is to explore the codebase and produce an implementation plan for the task below.\n\n"+
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
		repoName(task.RepoURL),
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
