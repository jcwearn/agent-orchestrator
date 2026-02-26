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
