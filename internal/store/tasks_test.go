package store

import (
	"context"
	"errors"
	"testing"
)

func TestCreateTask(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	task := &Task{
		Prompt:     "implement feature X",
		RepoURL:    "https://github.com/test/repo",
		SourceType: "manual",
		SessionID:  "session-123",
	}

	if err := s.CreateTask(ctx, task); err != nil {
		t.Fatal("create task:", err)
	}

	if task.ID == "" {
		t.Fatal("expected ID to be set")
	}
	if task.Status != "queued" {
		t.Fatalf("expected status 'queued', got %q", task.Status)
	}
	if task.BaseBranch != "main" {
		t.Fatalf("expected base_branch 'main', got %q", task.BaseBranch)
	}
	if task.CreatedAt.IsZero() {
		t.Fatal("expected CreatedAt to be set")
	}
}

func TestGetTask(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	task := &Task{
		Prompt:     "implement feature X",
		RepoURL:    "https://github.com/test/repo",
		SourceType: "manual",
		SessionID:  "session-123",
	}
	if err := s.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal("get task:", err)
	}
	if got.Prompt != task.Prompt {
		t.Fatalf("expected prompt %q, got %q", task.Prompt, got.Prompt)
	}
	if got.RepoURL != task.RepoURL {
		t.Fatalf("expected repo_url %q, got %q", task.RepoURL, got.RepoURL)
	}
	if got.SessionID != task.SessionID {
		t.Fatalf("expected session_id %q, got %q", task.SessionID, got.SessionID)
	}

	// Not found
	_, err = s.GetTask(ctx, "nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestListTasks(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	t1 := &Task{Prompt: "task 1", RepoURL: "https://github.com/test/repo", SourceType: "manual", SessionID: "s1"}
	t2 := &Task{Prompt: "task 2", RepoURL: "https://github.com/test/repo", SourceType: "manual", SessionID: "s2", Status: "planning"}
	if err := s.CreateTask(ctx, t1); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateTask(ctx, t2); err != nil {
		t.Fatal(err)
	}

	// List all
	tasks, err := s.ListTasks(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}

	// Filter by status
	tasks, err = s.ListTasks(ctx, "queued")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 queued task, got %d", len(tasks))
	}
	if tasks[0].Prompt != "task 1" {
		t.Fatalf("expected 'task 1', got %q", tasks[0].Prompt)
	}
}

func TestUpdateTask(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	task := &Task{Prompt: "original", RepoURL: "https://github.com/test/repo", SourceType: "manual", SessionID: "s1"}
	if err := s.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}

	task.Status = "planning"
	plan := "the plan"
	task.Plan = &plan
	if err := s.UpdateTask(ctx, task.ID, task); err != nil {
		t.Fatal("update task:", err)
	}

	got, err := s.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "planning" {
		t.Fatalf("expected status 'planning', got %q", got.Status)
	}
	if got.Plan == nil || *got.Plan != "the plan" {
		t.Fatal("expected plan to be set")
	}

	// Not found
	err = s.UpdateTask(ctx, "nonexistent", task)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestDeleteTask(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	task := &Task{Prompt: "to delete", RepoURL: "https://github.com/test/repo", SourceType: "manual", SessionID: "s1"}
	if err := s.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteTask(ctx, task.ID); err != nil {
		t.Fatal("delete task:", err)
	}

	_, err := s.GetTask(ctx, task.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}

	// Not found
	err = s.DeleteTask(ctx, "nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestCreateTaskLog(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	task := &Task{Prompt: "task", RepoURL: "https://github.com/test/repo", SourceType: "manual", SessionID: "s1"}
	if err := s.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}

	log := &TaskLog{TaskID: task.ID, Step: "plan", Stream: "stdout", Line: "hello world"}
	if err := s.CreateTaskLog(ctx, log); err != nil {
		t.Fatal("create task log:", err)
	}

	if log.ID == 0 {
		t.Fatal("expected ID to be set")
	}
	if log.CreatedAt.IsZero() {
		t.Fatal("expected CreatedAt to be set")
	}
}

func TestListTaskLogs(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	task := &Task{Prompt: "task", RepoURL: "https://github.com/test/repo", SourceType: "manual", SessionID: "s1"}
	if err := s.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}

	logs := []TaskLog{
		{TaskID: task.ID, Step: "plan", Stream: "stdout", Line: "line 1"},
		{TaskID: task.ID, Step: "plan", Stream: "stderr", Line: "line 2"},
		{TaskID: task.ID, Step: "implement", Stream: "stdout", Line: "line 3"},
	}
	for i := range logs {
		if err := s.CreateTaskLog(ctx, &logs[i]); err != nil {
			t.Fatal(err)
		}
	}

	// All logs for task
	all, err := s.ListTaskLogs(ctx, task.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 logs, got %d", len(all))
	}

	// Filter by step
	planLogs, err := s.ListTaskLogs(ctx, task.ID, "plan")
	if err != nil {
		t.Fatal(err)
	}
	if len(planLogs) != 2 {
		t.Fatalf("expected 2 plan logs, got %d", len(planLogs))
	}

	// Verify ordering (ASC by created_at)
	if planLogs[0].Line != "line 1" {
		t.Fatalf("expected first log 'line 1', got %q", planLogs[0].Line)
	}
}

func TestListTaskLogsSince(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	task := &Task{Prompt: "task", RepoURL: "https://github.com/test/repo", SourceType: "manual", SessionID: "s1"}
	if err := s.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}

	logs := []TaskLog{
		{TaskID: task.ID, Step: "plan", Stream: "stdout", Line: "line 1"},
		{TaskID: task.ID, Step: "plan", Stream: "stdout", Line: "line 2"},
		{TaskID: task.ID, Step: "plan", Stream: "stdout", Line: "line 3"},
	}
	for i := range logs {
		if err := s.CreateTaskLog(ctx, &logs[i]); err != nil {
			t.Fatal(err)
		}
	}

	// Get logs after the first one
	since, err := s.ListTaskLogsSince(ctx, task.ID, logs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(since) != 2 {
		t.Fatalf("expected 2 logs, got %d", len(since))
	}
	if since[0].Line != "line 2" {
		t.Fatalf("expected 'line 2', got %q", since[0].Line)
	}
	if since[1].Line != "line 3" {
		t.Fatalf("expected 'line 3', got %q", since[1].Line)
	}

	// Get all logs (afterID=0)
	all, err := s.ListTaskLogsSince(ctx, task.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 logs, got %d", len(all))
	}

	// Get none (afterID > last)
	none, err := s.ListTaskLogsSince(ctx, task.ID, logs[2].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("expected 0 logs, got %d", len(none))
	}
}

func TestDeleteTaskWithLogs(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	task := &Task{Prompt: "task", RepoURL: "https://github.com/test/repo", SourceType: "manual", SessionID: "s1"}
	if err := s.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}

	log := &TaskLog{TaskID: task.ID, Step: "plan", Stream: "stdout", Line: "output"}
	if err := s.CreateTaskLog(ctx, log); err != nil {
		t.Fatal(err)
	}

	// FK constraint should block deletion
	err := s.DeleteTask(ctx, task.ID)
	if err == nil {
		t.Fatal("expected FK constraint error when deleting task with logs")
	}
}
