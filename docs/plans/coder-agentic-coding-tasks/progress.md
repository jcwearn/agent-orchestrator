# Progress: Pivot to Standalone Coder Workspace Orchestrator

## Current Status: In Progress

| Phase | Status | Updated | Notes |
|-------|--------|---------|-------|
| 1. Foundation (Go Module + SQLite Store) | Complete | 2026-02-27 | Merged PR #3 |
| 2. Coder Workspace Executor | In Review | 2026-02-27 | PR open |
| 3. Task Orchestrator | In Review | 2026-02-27 | PR open |
| 4. HTTP API | In Review | 2026-02-27 | PR open |
| 5. GitHub Integration | Not Started | — | — |
| 6. Embedded Web UI (Vite + React) | Not Started | — | — |
| 7. Build, CI/CD + Docker Publishing | Not Started | — | — |

## Handoff Notes
- **2026-02-27**: Phase 4 implemented. `internal/server/` package adds: `Server` struct with chi router, task CRUD handlers (create/list/get/delete), approval + feedback endpoints, SSE log streaming (500ms poll with `ListTaskLogsSince`), agents endpoint (merges pool slots with Coder workspace status), WebSocket hub (register/unregister/broadcast), and `handleWebSocket` upgrade handler. Added `OnEvent` callback to orchestrator `Config` for bridging status transitions to the WebSocket hub. Added `ListTaskLogsSince` to store. Wired into `cmd/main.go` with hub creation and OnEvent callback. New dependency: `github.com/gorilla/websocket`. 26 server tests + 1 store test pass. Pre-existing flaky `TestMultipleTasksQueueing` exists on main (race condition in goroutine timing).
- **2026-02-27**: Phase 3 implemented. `internal/orchestrator/` package adds: `Orchestrator` (tick loop with 5s interval), queue helpers (FIFO via ListTasks DESC), 2-step lifecycle (plan → awaiting_approval → implement → complete), `logWriter` (line-splitting log persistence), crash recovery (`recoverActiveTasks`), workspace release during approval wait. Wired into `cmd/main.go` with signal-notified context. 14 tests pass (mock executor, in-memory SQLite). No new dependencies.
- **2026-02-27**: Phase 2 implemented. `internal/coder/` package adds: `Executor` (CLI wrapper for `coder ssh/start/stop/list`), `WorkspaceExecutor` interface for Phase 3, `Pool` (slot-based workspace assignment with `Acquire`/`Release`), sentinel errors, workspace status parsing. 17 tests pass (9 executor via `os/exec` test helper pattern, 7 pool including concurrency, 1 helper). No new dependencies added.
- **2026-02-26**: Phase 1 implemented and PR #3 opened. Go module initialized with chi/v5, modernc.org/sqlite, google/uuid. Store package has models, embedded migrations, full CRUD (7 methods), and 10 passing tests. `cmd/main.go` serves `/healthz` with graceful shutdown. Ready for Phase 2 (Coder Workspace Executor) after merge.
- **2026-02-25**: Plan revised to add three details: (1) workspace lifecycle management with `coder list/start/stop` and `--yes` flags in Phases 2+3, (2) `session_id` column in Phase 1 schema with `--session-id`/`--resume` usage in Phase 3 steps, (3) Coder-inspired UI design direction in Phase 6 (Tailwind + shadcn/ui, dark mode, table layout for agents).
