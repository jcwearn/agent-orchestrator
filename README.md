# Agent Orchestrator

A service that orchestrates AI coding agents (Claude Code) running on [Coder](https://coder.com) workspaces. It accepts tasks via a REST API or GitHub issues and runs a **plan → approve → implement** lifecycle, producing pull requests as output.

## Overview

Agent Orchestrator manages a pool of Coder workspaces, each with Claude Code installed. When a task is submitted — either through the API or by labeling a GitHub issue with `ai-task` — the orchestrator assigns it to a free workspace, generates an implementation plan, waits for human approval, and then executes the plan. The result is a pull request on the target repository.

A built-in React dashboard provides real-time task monitoring, log streaming, and plan approval directly from the browser.

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    Agent Orchestrator                    │
│                                                         │
│  ┌─────────────┐  ┌──────────────┐  ┌───────────────┐  │
│  │ HTTP/WS     │  │ Orchestrator │  │ Coder         │  │
│  │ Server      │──│ (tick loop,  │──│ Integration   │  │
│  │ (REST API,  │  │  state       │  │ (workspace    │  │
│  │  WebSocket, │  │  machine)    │  │  pool, SSH    │  │
│  │  SPA)       │  │              │  │  executor)    │  │
│  └─────────────┘  └──────────────┘  └───────────────┘  │
│         │                │                   │          │
│  ┌──────┴──────┐  ┌──────┴──────┐  ┌────────┴───────┐  │
│  │ React       │  │ SQLite      │  │ GitHub App     │  │
│  │ Dashboard   │  │ Store       │  │ Integration    │  │
│  │ (embedded)  │  │ (tasks,     │  │ (webhooks,     │  │
│  │             │  │  logs,      │  │  comments,     │  │
│  │             │  │  migrations)│  │  approvals)    │  │
│  └─────────────┘  └─────────────┘  └────────────────┘  │
└─────────────────────────────────────────────────────────┘
                           │
              ┌────────────┼────────────┐
              ▼            ▼            ▼
         ┌─────────┐ ┌─────────┐ ┌─────────┐
         │agent-1  │ │agent-2  │ │  ...     │
         │(Coder   │ │(Coder   │ │(Coder   │
         │workspace│ │workspace│ │workspace│
         │+ Claude)│ │+ Claude)│ │+ Claude)│
         └─────────┘ └─────────┘ └─────────┘
```

**Components:**

- **Orchestrator** — Runs a tick loop (every 5s) that drives the task state machine. Manages workspace acquisition/release, invokes Claude Code for planning and implementation, and coordinates approval.
- **HTTP/WebSocket Server** — Chi-based REST API for task management, SSE endpoint for log streaming, WebSocket for real-time event push, and serves the embedded React SPA.
- **Coder Integration** — Manages a pool of Coder workspaces (default: `agent-1` through `agent-4`). Executes Claude Code commands over SSH using the Coder CLI.
- **GitHub App Integration** — Receives webhook events for issue labeling, posts plans as issue comments, polls for approval via thumbs-up reactions, and collects feedback from follow-up comments.
- **SQLite Store** — Persists tasks and logs with schema migrations applied at startup.
- **React Dashboard** — Single-page app for viewing tasks, streaming logs, reviewing plans, and approving/providing feedback.

## Task Lifecycle

Tasks follow this state machine:

```
queued → planning → awaiting_approval → implementing → complete
                         │       ▲                       │
                         │       │                       │
                         ▼       │                       ▼
                      (feedback/revise)               failed
```

1. **Queued** — Task is created and waiting for a free workspace.
2. **Planning** — A workspace is acquired and Claude Code generates an implementation plan.
3. **Awaiting Approval** — The plan is presented for human review (via the dashboard or as a GitHub issue comment). The reviewer can approve or provide feedback.
   - If feedback is given, the task returns to **Planning** for a revised plan.
4. **Implementing** — After approval, Claude Code executes the plan and creates a pull request.
5. **Complete** — The PR has been created. The PR URL is recorded on the task.
6. **Failed** — An error occurred during planning or implementation.

## Prerequisites

- **Go 1.25+**
- **Node.js 22+** (for building the web frontend)
- **Coder CLI** — Installed and authenticated (`CODER_URL` and `CODER_SESSION_TOKEN` configured)
- **Claude Code CLI** — Installed on each Coder workspace
- **GitHub App** (optional) — Required only for the GitHub issue integration

## Getting Started

```bash
# Clone the repository
git clone https://github.com/jcwearn/agent-orchestrator.git
cd agent-orchestrator

# Build the frontend and server
make web-build
make build

# Configure environment (see Configuration section below)
export PORT=8080
export DATABASE_PATH=./data/orchestrator.db

# Run
./bin/agent-orchestrator
```

The server starts on port 8080 (default). Open `http://localhost:8080` to access the dashboard.

## Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | HTTP server listen port | `8080` |
| `DATABASE_PATH` | Path to SQLite database file | `./data/orchestrator.db` |
| `LOG_LEVEL` | Log level (`debug`, `info`, `warn`, `error`) | `info` |
| `CODER_URL` | URL of your Coder deployment | *(required by Coder CLI)* |
| `CODER_SESSION_TOKEN` | Coder API session token | *(required by Coder CLI)* |
| `GITHUB_APP_ID` | GitHub App ID | *(optional)* |
| `GITHUB_APP_INSTALLATION_ID` | GitHub App installation ID | *(optional)* |
| `GITHUB_APP_PRIVATE_KEY` | GitHub App private key (PEM contents) | *(optional)* |
| `GITHUB_WEBHOOK_SECRET` | Secret for validating GitHub webhook payloads | *(optional)* |

GitHub integration activates only when `GITHUB_APP_ID`, `GITHUB_APP_INSTALLATION_ID`, and `GITHUB_APP_PRIVATE_KEY` are all set.

## API Reference

### Tasks

#### `POST /api/v1/tasks`

Create a new task.

**Request body:**
```json
{
  "repo": "owner/repo",
  "description": "Add input validation to the signup form"
}
```

#### `GET /api/v1/tasks`

List all tasks. Supports optional `?status=` query parameter to filter by status.

#### `GET /api/v1/tasks/{id}`

Get a single task by ID.

#### `DELETE /api/v1/tasks/{id}`

Delete a task.

#### `POST /api/v1/tasks/{id}/approve`

Approve a task's plan, moving it to the implementing phase.

#### `POST /api/v1/tasks/{id}/feedback`

Submit feedback on a task's plan, sending it back for revision.

**Request body:**
```json
{
  "feedback": "Please also add server-side validation"
}
```

#### `GET /api/v1/tasks/{id}/logs`

Stream task logs via Server-Sent Events (SSE).

### Agents

#### `GET /api/v1/agents`

List all agent workspaces and their current status (free or assigned to a task).

### GitHub

#### `POST /api/v1/webhooks/github`

GitHub webhook endpoint. Receives issue events and triggers task creation when the `ai-task` label is applied.

### Real-time

#### `GET /api/v1/ws`

WebSocket endpoint for real-time task update events.

### Health

#### `GET /healthz`

Health check endpoint. Returns `{"status": "ok"}`.

## GitHub Integration

Agent Orchestrator can be driven by GitHub issues through a GitHub App.

### Setup

1. Create a GitHub App with the following permissions:
   - **Issues**: Read & Write (to read issues and post comments)
   - **Pull Requests**: Read (to link PRs)
   - **Reactions**: Read (to detect approval)
2. Subscribe to the **Issues** webhook event.
3. Install the App on your target repositories.
4. Configure the environment variables: `GITHUB_APP_ID`, `GITHUB_APP_INSTALLATION_ID`, `GITHUB_APP_PRIVATE_KEY`, and `GITHUB_WEBHOOK_SECRET`.
5. Point the webhook URL to `https://<your-host>/api/v1/webhooks/github`.

### Workflow

1. Add the `ai-task` label to a GitHub issue. The issue title and body become the task description.
2. The orchestrator creates a task, assigns a workspace, and generates a plan.
3. The plan is posted as a comment on the issue.
4. **To approve**: Add a thumbs-up (+1) reaction to the plan comment.
5. **To request changes**: Post a follow-up comment on the issue with feedback. The plan will be revised and re-posted.
6. After approval, implementation runs and a completion comment is posted with the PR link.

## Development

```bash
# Build the Go binary
make build

# Run Go tests
make test

# Run the linter (requires golangci-lint)
make lint

# Build the web frontend
make web-build

# Build the Docker image
make docker-build

# Run the frontend dev server (with hot reload)
cd web && npm run dev
```

## Docker

The multi-stage Dockerfile builds both the frontend and backend into a single image based on `debian:bookworm-slim`, with the Coder CLI pre-installed.

```bash
# Build
make docker-build

# Run
docker run -p 8080:8080 \
  -e DATABASE_PATH=/data/orchestrator.db \
  -e CODER_URL=https://your-coder.example.com \
  -e CODER_SESSION_TOKEN=your-token \
  -v orchestrator-data:/data \
  agent-orchestrator
```

The release workflow publishes images to `ghcr.io/jcwearn/agent-orchestrator` with semver tags.

## Project Structure

```
agent-orchestrator/
├── cmd/
│   └── main.go                  # Entrypoint, wiring, configuration
├── internal/
│   ├── coder/
│   │   ├── executor.go          # SSH command execution via Coder CLI
│   │   └── workspace.go         # Workspace pool management
│   ├── github/
│   │   ├── client.go            # GitHub App authentication
│   │   └── notifier.go          # Issue comments, approval polling
│   ├── orchestrator/
│   │   ├── orchestrator.go      # Core tick loop and task state machine
│   │   ├── queue.go             # Task queue and status constants
│   │   ├── steps.go             # Planning and implementation step logic
│   │   └── log_writer.go        # Log capture for task execution
│   ├── server/
│   │   ├── server.go            # HTTP server, routing, middleware
│   │   ├── handlers_tasks.go    # Task CRUD and approval endpoints
│   │   ├── handlers_agents.go   # Agent status endpoint
│   │   ├── handlers_github.go   # GitHub webhook handler
│   │   ├── handlers_logs.go     # SSE log streaming
│   │   ├── hub.go               # WebSocket event hub
│   │   ├── websocket.go         # WebSocket connection handling
│   │   └── ui.go                # SPA serving and embedding
│   └── store/
│       ├── store.go             # SQLite connection and migrations
│       ├── tasks.go             # Task persistence queries
│       ├── models.go            # Database model types
│       └── migrations/          # SQL schema migrations
├── web/                         # React SPA (Vite + Tailwind CSS)
│   ├── embed.go                 # Go embed directive for built assets
│   └── src/
│       ├── pages/               # Dashboard, TaskList, TaskDetail, NewTask
│       ├── components/          # PlanView, LogViewer, ApprovalForm, etc.
│       ├── hooks/               # useTasks, useAgents, useLogStream, useWebSocket
│       ├── api/                 # API client
│       └── types/               # TypeScript type definitions
├── Dockerfile                   # Multi-stage build (Node → Go → Debian)
├── Makefile                     # Build, test, lint, docker targets
└── .github/workflows/           # CI/CD (lint, test, build, release)
```
