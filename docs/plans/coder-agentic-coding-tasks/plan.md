# Plan: Pivot to Standalone Coder Workspace Orchestrator

## Context

Replace the existing Kubernetes-based agent-operator with a standalone Go service that orchestrates agentic coding tasks across 4 preset Coder workspaces (agent-1 through agent-4). The current operator creates ephemeral K8s pods for each step (plan, implement, test, PR). The new service simplifies this to 2 steps (plan + implement), uses `coder ssh` to execute Claude CLI in persistent workspaces, adds a web UI for monitoring and approval, and uses SQLite for state.

**Key motivation**: Coder workspaces provide persistent, authenticated environments with Claude CLI pre-installed, eliminating the need for ephemeral pod management and K8s operator complexity.

**Prior art**: The architecture and workflow patterns are informed by the existing [agent-operator](https://github.com/jcwearn/agent-operator) repo (Kubebuilder operator, chi HTTP server, WebSocket hub, GitHub App integration).

## Phases

### Phase 1: Foundation -- Go Module + SQLite Store

- Initialize Go module with chi router and SQLite (`modernc.org/sqlite`, CGO-free)
- Create `cmd/main.go` -- standalone binary with `slog`, config from env vars
- Create `internal/store/` package: database wrapper, embedded SQL migrations, domain models, CRUD
- Health endpoint at `/healthz`
- Domain models: `Task`, `TaskLog`

**Files**:
- `cmd/main.go`
- `internal/store/store.go`
- `internal/store/migrations.go`
- `internal/store/models.go`
- `internal/store/tasks.go`

**SQLite schema**:
```sql
CREATE TABLE tasks (
    id              TEXT PRIMARY KEY,
    status          TEXT NOT NULL DEFAULT 'queued',
    prompt          TEXT NOT NULL,
    plan            TEXT,
    plan_feedback   TEXT,
    repo_url        TEXT NOT NULL,
    base_branch     TEXT NOT NULL DEFAULT 'main',
    source_type     TEXT NOT NULL,
    github_owner    TEXT,
    github_repo     TEXT,
    github_issue    INTEGER,
    workspace_id    TEXT,
    current_step    TEXT,
    plan_comment_id INTEGER,
    plan_revision   INTEGER NOT NULL DEFAULT 0,
    pr_url          TEXT,
    pr_number       INTEGER,
    run_tests       BOOLEAN NOT NULL DEFAULT 0,
    decisions       TEXT,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at      DATETIME,
    completed_at    DATETIME,
    error_message   TEXT
);

CREATE TABLE task_logs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id    TEXT NOT NULL REFERENCES tasks(id),
    step       TEXT NOT NULL,
    stream     TEXT NOT NULL,
    line       TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_tasks_status ON tasks(status);
CREATE INDEX idx_tasks_workspace ON tasks(workspace_id);
CREATE INDEX idx_task_logs_task ON task_logs(task_id, step);
```

**Acceptance criteria**: `go build ./cmd/...` succeeds, binary starts and serves `/healthz`, store CRUD tests pass.

---

### Phase 2: Coder Workspace Executor

- Create `internal/coder/executor.go` -- runs commands via `coder ssh agent-N -- bash -c "command"`
- Create `internal/coder/workspace.go` -- workspace pool (agent-1 through agent-4), start/stop via `coder start`
- Shell out to `coder` CLI (no SDK import needed)
- Start workspaces with `git_repo` parameter: `coder start agent-N --parameter git_repo=<url>`
- Stream stdout/stderr to `io.Writer` for real-time log capture

**Files**:
- `internal/coder/executor.go`
- `internal/coder/workspace.go`

**Acceptance criteria**: Executor can start a workspace, run a command, capture output. Unit tests with mocked exec pass.

---

### Phase 3: Task Orchestrator

- Create `internal/orchestrator/orchestrator.go` -- background goroutine with tick loop (polls every 5s)
- Create `internal/orchestrator/queue.go` -- FIFO queue via SQLite status + created_at ordering
- Create `internal/orchestrator/steps.go` -- plan and implement step execution
- Two-step workflow:
  1. **Plan**: SSH into workspace, clone repo, run `claude -p "plan prompt" --print --dangerously-skip-permissions`. Capture output, store as plan.
  2. **Implement**: After approval, run `claude -p "implement prompt with plan context" --print --dangerously-skip-permissions`. Claude handles branching, coding, testing, committing, pushing, PR creation.
- Workspace pool: 4 slots, FIFO assignment, release on completion/failure
- Broadcasts WebSocket events at each status transition
- Task statuses: `queued -> planning -> awaiting_approval -> implementing -> complete/failed`

**Files**:
- `internal/orchestrator/orchestrator.go`
- `internal/orchestrator/queue.go`
- `internal/orchestrator/steps.go`

**Acceptance criteria**: Tasks flow through the full lifecycle, workspaces are allocated/released correctly, multiple tasks queue when all workspaces are busy.

---

### Phase 4: HTTP API

- Create `internal/server/server.go` -- chi router with middleware, inject `*store.Store` and `*orchestrator.Orchestrator`
- Create `internal/server/handlers_tasks.go` -- task CRUD against SQLite
- Create `internal/server/handlers_logs.go` -- log streaming via SSE
- Create `internal/server/websocket.go` -- WebSocket hub for real-time events (reuse pattern from agent-operator)

**API routes**:
```
GET    /healthz
POST   /api/v1/tasks
GET    /api/v1/tasks
GET    /api/v1/tasks/{id}
DELETE /api/v1/tasks/{id}
POST   /api/v1/tasks/{id}/approve
POST   /api/v1/tasks/{id}/feedback
GET    /api/v1/tasks/{id}/logs
GET    /api/v1/agents
POST   /api/v1/webhooks/github
GET    /api/v1/ws
GET    /* (embedded web UI, Phase 6)
```

**Files**:
- `internal/server/server.go`
- `internal/server/handlers_tasks.go`
- `internal/server/handlers_logs.go`
- `internal/server/websocket.go`

**Acceptance criteria**: All API endpoints work against SQLite, plan approval triggers orchestrator, WebSocket broadcasts work.

---

### Phase 5: GitHub Integration

- Port `internal/github/client.go` from agent-operator -- GitHub App authentication (no K8s deps)
- Port `internal/github/notifier.go` -- plan posting, approval checking (replace controller-runtime logger with `slog`)
- Create `internal/server/handlers_github.go` -- webhook handler that creates tasks in SQLite
- Bridge orchestrator with GitHub approval polling (reaction checking on plan comments)

**Files**:
- `internal/github/client.go` -- ported from agent-operator
- `internal/github/notifier.go` -- ported and adapted
- `internal/server/handlers_github.go`

**Acceptance criteria**: GitHub issue with `ai-task` label creates a task, plan posts as comment, thumbs-up triggers implementation, PR status tracked.

---

### Phase 6: Embedded Web UI (Vite + React)

- Scaffold `web/` directory with Vite + React + TypeScript
- Dashboard: 4 agent cards showing workspace status (idle/running/awaiting approval)
- Task list: table with status, repo, prompt, created time, actions
- Task detail: full prompt, rendered plan (markdown), approval/feedback form, log stream, PR link
- WebSocket hook for real-time updates
- Embed compiled frontend in Go binary via `embed.FS`
- SPA routing fallback to `index.html`

**Files**:
- `web/` -- Vite project
- `web/src/App.tsx`
- `web/src/pages/Dashboard.tsx`
- `web/src/pages/TaskList.tsx`
- `web/src/pages/TaskDetail.tsx`
- `web/src/hooks/useWebSocket.ts`
- `internal/server/ui.go` -- embed.FS handler

**Acceptance criteria**: Dashboard shows real-time agent status, task list/detail works, plan approval from UI triggers implementation, logs stream in real-time.

---

### Phase 7: Build + Deploy

- Dockerfile: multi-stage build (Go + Node.js for frontend, slim runtime with `coder` CLI)
- Makefile: `build`, `test`, `web-build`, `docker-build` targets
- GitHub Actions CI: lint, test, build image
- `.gitignore`, `README.md`

**Acceptance criteria**: `go build`, `go test`, and Docker build all succeed. Service runs in container and connects to Coder workspaces.

## Verification

1. **Unit tests**: `go test ./...` -- store CRUD, executor mocks, orchestrator state machine
2. **Manual E2E**: Start the service, create a task via API, watch it flow through plan -> approval -> implement -> PR
3. **Web UI**: Open browser, verify dashboard shows agents, create task, approve plan, see logs stream
4. **GitHub flow**: Label an issue with `ai-task`, verify plan comment, approve via reaction, verify PR created
5. **Queue behavior**: Create 5 tasks, verify first 4 start immediately and 5th queues until a workspace frees up
