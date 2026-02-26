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
    session_id      TEXT NOT NULL,
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
- Create `internal/coder/workspace.go` -- workspace pool (agent-1 through agent-4), full lifecycle management
- Shell out to `coder` CLI (no SDK import needed)
- **Status check**: `coder list --output json` to query workspace status before assignment. Parse JSON to determine if a workspace is `running`, `stopped`, `failed`, `starting`, or `stopping`.
- **Start**: `coder start agent-N --parameter git_repo=<url> --yes` (bypass interactive prompts)
- **Stop**: `coder stop agent-N --yes` at end of task lifecycle (completion or failure)
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
- **Workspace allocation flow**: Before assigning a workspace, check status via `coder list --output json` to find a `stopped` (available) workspace. Start it with the task's repo URL. After task completes or fails, stop the workspace with `coder stop agent-N --yes`.
- Two-step workflow using session ID for context continuity:
  1. **Plan**: SSH into workspace, clone repo, run `claude --session-id <task.session_id> -p "plan prompt" --print --dangerously-skip-permissions`. Capture output, store as plan.
  2. **Implement**: After approval, run `claude --resume <task.session_id> -p "implement prompt" --print --dangerously-skip-permissions`. Resuming the session preserves the full plan conversation context. Claude handles branching, coding, testing, committing, pushing, PR creation.
- Session ID continuity enables future PR feedback to resume the same Claude session with `--resume`, preserving full conversation context across the task lifecycle.
- Workspace pool: 4 slots, FIFO assignment, release on completion/failure
- Broadcasts WebSocket events at each status transition
- Status flow: `queued -> check workspace status -> start workspace -> planning -> awaiting_approval -> implementing -> stop workspace -> complete/failed`

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

- Scaffold `web/` directory with Vite + React + TypeScript + **Tailwind CSS** + **shadcn/ui** (Radix primitives)
- Embed compiled frontend in Go binary via `embed.FS`
- SPA routing fallback to `index.html`

**Design direction** (Coder-inspired):
- **Dark mode first**: Near-black background (`zinc-950` / `#09090b`), dark gray surfaces (`zinc-900`, `zinc-800`)
- **Accent color**: Sky blue (`sky-500` / `#0ea5e9`) for primary actions and links
- **Status colors**: Green (running), Amber (awaiting approval), Red (failed), Zinc-gray (idle/stopped)
- **Typography**: Geist (body), IBM Plex Mono (logs/code)
- **Layout**: Sticky 72px top nav with logo + nav links, full-width content area, max-width ~1380px

**Views**:
- **Agent dashboard**: Table layout (not cards) with columns: Agent Name, Status, Current Task, Last Active — matching Coder's workspace list pattern with 72px row height
- **Task list**: Table with status, repo, prompt, created time, actions
- **Task detail**: Markdown-rendered plan, approval/feedback form, real-time log stream in monospace, PR link
- **WebSocket hook** (`useWebSocket.ts`) for real-time status updates across all views

**Files**:
- `web/` -- Vite project
- `web/src/App.tsx`
- `web/src/pages/Dashboard.tsx`
- `web/src/pages/TaskList.tsx`
- `web/src/pages/TaskDetail.tsx`
- `web/src/hooks/useWebSocket.ts`
- `internal/server/ui.go` -- embed.FS handler

**Acceptance criteria**: Dashboard shows real-time agent status in table layout, task list/detail works, plan approval from UI triggers implementation, logs stream in real-time, dark theme matches design spec.

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
