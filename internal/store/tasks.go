package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (s *Store) GetTaskByPRNumber(ctx context.Context, owner, repo string, prNumber int) (*Task, error) {
	row := s.db.QueryRowContext(ctx, `SELECT
		id, status, prompt, plan, plan_feedback, repo_url, base_branch,
		source_type, github_owner, github_repo, github_issue, session_id,
		workspace_id, current_step, plan_comment_id, plan_revision,
		pr_url, pr_number, run_tests, decisions,
		created_at, started_at, completed_at, error_message
	FROM tasks
	WHERE github_owner = ? AND github_repo = ? AND pr_number = ?
	LIMIT 1`, owner, repo, prNumber)

	t, err := scanTask(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get task by pr number: %w", err)
	}
	return t, nil
}

func (s *Store) GetTaskByGithubIssue(ctx context.Context, owner, repo string, issue int) (*Task, error) {
	row := s.db.QueryRowContext(ctx, `SELECT
		id, status, prompt, plan, plan_feedback, repo_url, base_branch,
		source_type, github_owner, github_repo, github_issue, session_id,
		workspace_id, current_step, plan_comment_id, plan_revision,
		pr_url, pr_number, run_tests, decisions,
		created_at, started_at, completed_at, error_message
	FROM tasks
	WHERE github_owner = ? AND github_repo = ? AND github_issue = ? AND status != 'failed'
	LIMIT 1`, owner, repo, issue)

	t, err := scanTask(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get task by github issue: %w", err)
	}
	return t, nil
}

func (s *Store) CreateTask(ctx context.Context, t *Task) error {
	t.ID = uuid.New().String()
	t.CreatedAt = time.Now().UTC()
	if t.Status == "" {
		t.Status = "queued"
	}
	if t.BaseBranch == "" {
		t.BaseBranch = "main"
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO tasks (
			id, status, prompt, plan, plan_feedback, repo_url, base_branch,
			source_type, github_owner, github_repo, github_issue, session_id,
			workspace_id, current_step, plan_comment_id, plan_revision,
			pr_url, pr_number, run_tests, decisions,
			created_at, started_at, completed_at, error_message
		) VALUES (
			?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?, ?
		)`,
		t.ID, t.Status, t.Prompt, t.Plan, t.PlanFeedback, t.RepoURL, t.BaseBranch,
		t.SourceType, t.GithubOwner, t.GithubRepo, t.GithubIssue, t.SessionID,
		t.WorkspaceID, t.CurrentStep, t.PlanCommentID, t.PlanRevision,
		t.PRUrl, t.PRNumber, t.RunTests, t.Decisions,
		t.CreatedAt, t.StartedAt, t.CompletedAt, t.ErrorMessage,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrDuplicateTask
		}
		return fmt.Errorf("insert task: %w", err)
	}
	return nil
}

func (s *Store) GetTask(ctx context.Context, id string) (*Task, error) {
	row := s.db.QueryRowContext(ctx, `SELECT
		id, status, prompt, plan, plan_feedback, repo_url, base_branch,
		source_type, github_owner, github_repo, github_issue, session_id,
		workspace_id, current_step, plan_comment_id, plan_revision,
		pr_url, pr_number, run_tests, decisions,
		created_at, started_at, completed_at, error_message
	FROM tasks WHERE id = ?`, id)

	t, err := scanTask(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}
	return t, nil
}

func (s *Store) ListTasks(ctx context.Context, status string) ([]Task, error) {
	var rows *sql.Rows
	var err error

	if status == "" {
		rows, err = s.db.QueryContext(ctx, `SELECT
			id, status, prompt, plan, plan_feedback, repo_url, base_branch,
			source_type, github_owner, github_repo, github_issue, session_id,
			workspace_id, current_step, plan_comment_id, plan_revision,
			pr_url, pr_number, run_tests, decisions,
			created_at, started_at, completed_at, error_message
		FROM tasks ORDER BY created_at DESC`)
	} else {
		rows, err = s.db.QueryContext(ctx, `SELECT
			id, status, prompt, plan, plan_feedback, repo_url, base_branch,
			source_type, github_owner, github_repo, github_issue, session_id,
			workspace_id, current_step, plan_comment_id, plan_revision,
			pr_url, pr_number, run_tests, decisions,
			created_at, started_at, completed_at, error_message
		FROM tasks WHERE status = ? ORDER BY created_at DESC`, status)
	}
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var tasks []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, *t)
	}
	return tasks, rows.Err()
}

func (s *Store) UpdateTask(ctx context.Context, id string, t *Task) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE tasks SET
			status = ?, prompt = ?, plan = ?, plan_feedback = ?,
			repo_url = ?, base_branch = ?, source_type = ?,
			github_owner = ?, github_repo = ?, github_issue = ?,
			session_id = ?, workspace_id = ?, current_step = ?,
			plan_comment_id = ?, plan_revision = ?,
			pr_url = ?, pr_number = ?, run_tests = ?, decisions = ?,
			started_at = ?, completed_at = ?, error_message = ?
		WHERE id = ?`,
		t.Status, t.Prompt, t.Plan, t.PlanFeedback,
		t.RepoURL, t.BaseBranch, t.SourceType,
		t.GithubOwner, t.GithubRepo, t.GithubIssue,
		t.SessionID, t.WorkspaceID, t.CurrentStep,
		t.PlanCommentID, t.PlanRevision,
		t.PRUrl, t.PRNumber, t.RunTests, t.Decisions,
		t.StartedAt, t.CompletedAt, t.ErrorMessage,
		id,
	)
	if err != nil {
		return fmt.Errorf("update task: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteTask(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM tasks WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateTaskLog(ctx context.Context, tl *TaskLog) error {
	tl.CreatedAt = time.Now().UTC()

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO task_logs (task_id, step, stream, line, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		tl.TaskID, tl.Step, tl.Stream, tl.Line, tl.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert task log: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("last insert id: %w", err)
	}
	tl.ID = int(id)
	return nil
}

func (s *Store) ListTaskLogsSince(ctx context.Context, taskID string, afterID int) ([]TaskLog, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		id, task_id, step, stream, line, created_at
	FROM task_logs WHERE task_id = ? AND id > ? ORDER BY id ASC`, taskID, afterID)
	if err != nil {
		return nil, fmt.Errorf("list task logs since: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var logs []TaskLog
	for rows.Next() {
		tl, err := scanTaskLog(rows)
		if err != nil {
			return nil, fmt.Errorf("scan task log: %w", err)
		}
		logs = append(logs, *tl)
	}
	return logs, rows.Err()
}

func (s *Store) ListTaskLogs(ctx context.Context, taskID string, step string) ([]TaskLog, error) {
	var rows *sql.Rows
	var err error

	if step == "" {
		rows, err = s.db.QueryContext(ctx, `SELECT
			id, task_id, step, stream, line, created_at
		FROM task_logs WHERE task_id = ? ORDER BY created_at ASC`, taskID)
	} else {
		rows, err = s.db.QueryContext(ctx, `SELECT
			id, task_id, step, stream, line, created_at
		FROM task_logs WHERE task_id = ? AND step = ? ORDER BY created_at ASC`, taskID, step)
	}
	if err != nil {
		return nil, fmt.Errorf("list task logs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var logs []TaskLog
	for rows.Next() {
		tl, err := scanTaskLog(rows)
		if err != nil {
			return nil, fmt.Errorf("scan task log: %w", err)
		}
		logs = append(logs, *tl)
	}
	return logs, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanTask(s scanner) (*Task, error) {
	var t Task
	err := s.Scan(
		&t.ID, &t.Status, &t.Prompt, &t.Plan, &t.PlanFeedback,
		&t.RepoURL, &t.BaseBranch, &t.SourceType,
		&t.GithubOwner, &t.GithubRepo, &t.GithubIssue,
		&t.SessionID, &t.WorkspaceID, &t.CurrentStep,
		&t.PlanCommentID, &t.PlanRevision,
		&t.PRUrl, &t.PRNumber, &t.RunTests, &t.Decisions,
		&t.CreatedAt, &t.StartedAt, &t.CompletedAt, &t.ErrorMessage,
	)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func scanTaskLog(s scanner) (*TaskLog, error) {
	var tl TaskLog
	err := s.Scan(
		&tl.ID, &tl.TaskID, &tl.Step, &tl.Stream, &tl.Line, &tl.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &tl, nil
}
