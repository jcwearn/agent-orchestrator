# Plan: Outbound GitHub Issue Creation for UI Tasks

## Context

Currently, the two task creation flows are independent and one-directional: GitHub issues (labeled `ai-task`) create tasks via webhook, but tasks created via the UI have no GitHub presence. This means UI-created tasks miss out on GitHub-based plan visibility, reaction-based approval, and completion tracking on issues.

This plan adds an opt-in "Create GitHub Issue" toggle to the task creation form. When enabled, the backend creates a GitHub issue for the task and populates the GitHub metadata fields (`github_owner`, `github_repo`, `github_issue`). The orchestrator then treats these tasks identically to webhook-created tasks for notifications — posting plans as comments, polling for reaction-based approval, and posting completion comments.

## Phases

### Phase 1: Backend Changes

**1a. Config endpoint** — Expose whether GitHub is configured so the frontend can conditionally show the toggle.

- Add `GET /api/v1/config` route in `server.go`
- New handler file `handlers_config.go` with a simple response: `{ "github_configured": bool }`
- Files: `internal/server/server.go`, `internal/server/handlers_config.go` (new)

**1b. GitHub URL parser** — Extract `owner` and `repo` from a repo URL.

- Add `parseGitHubRepo(repoURL string) (owner, repo string, err error)` in `handlers_tasks.go`
- Parses `https://github.com/owner/repo` and `https://github.com/owner/repo.git`
- Returns `("", "", nil)` for non-GitHub hosts (no error, just not applicable)
- Add `splitPromptForIssue(prompt string) (title, body string)` — first line as title (max 256 chars), remainder as body
- Files: `internal/server/handlers_tasks.go`

**1c. Create issue in task handler** — When `create_issue: true`, call GitHub API before persisting the task.

- Add `CreateIssue bool` field to `CreateTaskRequest`
- In `handleCreateTask`: if `create_issue` is true and `githubClient` is configured, parse owner/repo, create issue via `s.githubClient.Issues.Create()`, populate `GithubOwner`/`GithubRepo`/`GithubIssue` on the task
- Issue created with `ai-task` label for consistency
- If GitHub API fails → return 502, task is not created
- Files: `internal/server/handlers_tasks.go`

**1d. Webhook dedup** — Prevent the webhook from creating a duplicate task when it fires for the newly created issue.

- Add `GetTaskByGitHubIssue(ctx, owner, repo, issueNumber) (*Task, error)` to the store
- Add partial unique index `idx_tasks_github_issue` on `(github_owner, github_repo, github_issue)` in a new migration `002_github_issue_index.sql`
- In `handleIssuesEvent`: check for existing task before creating; skip if found
- Files: `internal/store/tasks.go`, `internal/store/migrations/002_github_issue_index.sql` (new), `internal/server/handlers_github.go`

**1e. Orchestrator `isGitHubTask` update** — Key off metadata presence instead of `source_type`.

- Change `isGitHubTask()` from `task.SourceType == "github" && ...` to just check `task.GithubOwner != nil && task.GithubRepo != nil && task.GithubIssue != nil && o.config.Notifier != nil`
- This enables GitHub notifications for API-created tasks that have an associated issue
- Both UI approval (`POST /tasks/{id}/approve`) and GitHub reaction approval continue to work — whichever happens first triggers implementation
- Files: `internal/orchestrator/orchestrator.go`

### Phase 2: Frontend Changes

**2a. Types and API client** — Add new types and fetch function.

- Add `ConfigResponse { github_configured: boolean }` to `types/api.ts`
- Add `create_issue?: boolean` to `CreateTaskRequest`
- Add `getConfig()` to `client.ts`
- Files: `web/src/types/api.ts`, `web/src/api/client.ts`

**2b. NewTask form toggle** — Add checkbox, conditionally shown.

- Fetch config on mount via `useEffect` → `getConfig()`
- Show checkbox "Create GitHub issue" only when `githubConfigured` is true
- Pass `create_issue` in `createTask()` call
- Use native HTML checkbox (no new shadcn component needed for a single use)
- Files: `web/src/pages/NewTask.tsx`

### Phase 3: Tests

- `handlers_tasks.go`: Test create with `create_issue: true` (mock GitHub server), non-GitHub URL returns 400, GitHub not configured returns 400
- `handlers_github_test.go`: Test webhook dedup — pre-create task with GitHub metadata, send webhook, assert no duplicate
- `handlers_config_test.go` (new): Test config endpoint with/without GitHub configured
- `store/tasks_test.go`: Test `GetTaskByGitHubIssue` (found, not found)
- `orchestrator_test.go`: Test `isGitHubTask` returns true for API task with GitHub metadata

Files to modify/create:
- `internal/server/server_test.go`
- `internal/server/handlers_github_test.go`
- `internal/server/handlers_config_test.go` (new)
- `internal/store/tasks_test.go`
- `internal/orchestrator/orchestrator_test.go`
