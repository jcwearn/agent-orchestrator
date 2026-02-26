# Progress: Pivot to Standalone Coder Workspace Orchestrator

## Current Status: Not Started

| Phase | Status | Updated | Notes |
|-------|--------|---------|-------|
| 1. Foundation (Go Module + SQLite Store) | Not Started | — | — |
| 2. Coder Workspace Executor | Not Started | — | — |
| 3. Task Orchestrator | Not Started | — | — |
| 4. HTTP API | Not Started | — | — |
| 5. GitHub Integration | Not Started | — | — |
| 6. Embedded Web UI (Vite + React) | Not Started | — | — |
| 7. Build, CI/CD + Docker Publishing | Not Started | — | — |

## Handoff Notes
- **2026-02-25**: Plan revised to add three details: (1) workspace lifecycle management with `coder list/start/stop` and `--yes` flags in Phases 2+3, (2) `session_id` column in Phase 1 schema with `--session-id`/`--resume` usage in Phase 3 steps, (3) Coder-inspired UI design direction in Phase 6 (Tailwind + shadcn/ui, dark mode, table layout for agents).
