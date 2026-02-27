package orchestrator

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jcwearn/agent-orchestrator/internal/coder"
	"github.com/jcwearn/agent-orchestrator/internal/store"
)

// --- mock executor ---

type mockExecutor struct {
	mu          sync.Mutex
	sshFunc     func(ctx context.Context, workspace, command string, stdout, stderr io.Writer) (*coder.SSHResult, error)
	startFunc   func(ctx context.Context, workspace string) error
	stopFunc    func(ctx context.Context, workspace string) error
	sshCalls    []sshCall
	startCalls  []string
	stopCalls   []string
}

type sshCall struct {
	Workspace string
	Command   string
}

func newMockExecutor() *mockExecutor {
	return &mockExecutor{
		sshFunc: func(ctx context.Context, workspace, command string, stdout, stderr io.Writer) (*coder.SSHResult, error) {
			fmt.Fprint(stdout, "mock plan output")
			return &coder.SSHResult{ExitCode: 0}, nil
		},
		startFunc: func(ctx context.Context, workspace string) error { return nil },
		stopFunc:  func(ctx context.Context, workspace string) error { return nil },
	}
}

func (m *mockExecutor) SSH(ctx context.Context, workspace, command string, stdout, stderr io.Writer) (*coder.SSHResult, error) {
	m.mu.Lock()
	m.sshCalls = append(m.sshCalls, sshCall{Workspace: workspace, Command: command})
	m.mu.Unlock()
	return m.sshFunc(ctx, workspace, command, stdout, stderr)
}

func (m *mockExecutor) StartWorkspace(ctx context.Context, workspace string) error {
	m.mu.Lock()
	m.startCalls = append(m.startCalls, workspace)
	m.mu.Unlock()
	return m.startFunc(ctx, workspace)
}

func (m *mockExecutor) StopWorkspace(ctx context.Context, workspace string) error {
	m.mu.Lock()
	m.stopCalls = append(m.stopCalls, workspace)
	m.mu.Unlock()
	return m.stopFunc(ctx, workspace)
}

func (m *mockExecutor) ListWorkspaces(ctx context.Context) ([]coder.WorkspaceInfo, error) {
	return nil, nil
}

// --- test helpers ---

func testStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	s := store.New(db, slog.Default())
	if err := s.RunMigrations(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func testOrchestrator(t *testing.T, exec *mockExecutor, pool *coder.Pool) (*Orchestrator, *store.Store) {
	t.Helper()
	s := testStore(t)
	if pool == nil {
		pool = coder.NewPool(coder.DefaultWorkspaces)
	}
	o := New(s, exec, pool, slog.Default(), Config{TickInterval: 50 * time.Millisecond})
	return o, s
}

func createTask(t *testing.T, s *store.Store, prompt string) *store.Task {
	t.Helper()
	task := &store.Task{
		Prompt:     prompt,
		RepoURL:    "https://github.com/test/repo.git",
		BaseBranch: "main",
		SourceType: "manual",
		SessionID:  "session-" + prompt,
	}
	if err := s.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	return task
}

func strPtr(s string) *string { return &s }

// --- tests ---

func TestTick_NoTasks(t *testing.T) {
	exec := newMockExecutor()
	o, _ := testOrchestrator(t, exec, nil)

	if err := o.tick(context.Background()); err != nil {
		t.Fatal("tick should succeed with empty queue:", err)
	}
	if len(exec.sshCalls) != 0 {
		t.Fatal("no SSH calls expected")
	}
}

func TestTick_PicksOldestTask(t *testing.T) {
	exec := newMockExecutor()
	o, s := testOrchestrator(t, exec, nil)
	ctx := context.Background()

	// Create tasks with slight delay so created_at ordering is deterministic.
	t1 := createTask(t, s, "first")
	time.Sleep(10 * time.Millisecond)
	createTask(t, s, "second")

	if err := o.tick(ctx); err != nil {
		t.Fatal(err)
	}

	// Wait for goroutine to update DB.
	time.Sleep(100 * time.Millisecond)

	task, err := s.GetTask(ctx, t1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != StatusAwaitingApproval {
		t.Fatalf("expected awaiting_approval, got %s", task.Status)
	}
}

func TestTick_NoFreeWorkspace(t *testing.T) {
	exec := newMockExecutor()
	pool := coder.NewPool([]string{"ws-1"})
	o, s := testOrchestrator(t, exec, pool)
	ctx := context.Background()

	// Occupy the only workspace.
	if _, err := pool.Acquire("other-task"); err != nil {
		t.Fatal(err)
	}

	createTask(t, s, "waiting")

	if err := o.tick(ctx); err != nil {
		t.Fatal(err)
	}

	// Task should remain queued.
	tasks, _ := s.ListTasks(ctx, StatusQueued)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 queued task, got %d", len(tasks))
	}
}

func TestRunTask_PlanSuccess(t *testing.T) {
	exec := newMockExecutor()
	exec.sshFunc = func(ctx context.Context, workspace, command string, stdout, stderr io.Writer) (*coder.SSHResult, error) {
		fmt.Fprint(stdout, "the generated plan")
		return &coder.SSHResult{ExitCode: 0}, nil
	}

	o, s := testOrchestrator(t, exec, nil)
	ctx := context.Background()
	task := createTask(t, s, "plan me")

	ws, _ := o.pool.Acquire(task.ID)
	o.runTask(ctx, task, ws)

	updated, err := s.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != StatusAwaitingApproval {
		t.Fatalf("expected awaiting_approval, got %s", updated.Status)
	}
	if updated.Plan == nil || *updated.Plan != "the generated plan" {
		t.Fatalf("expected plan to be captured, got %v", updated.Plan)
	}
	// Workspace should be released.
	if o.pool.FreeCount() != len(coder.DefaultWorkspaces) {
		t.Fatal("workspace not released")
	}
}

func TestRunTask_PlanFailure(t *testing.T) {
	exec := newMockExecutor()
	exec.sshFunc = func(ctx context.Context, workspace, command string, stdout, stderr io.Writer) (*coder.SSHResult, error) {
		return nil, errors.New("ssh connection failed")
	}

	o, s := testOrchestrator(t, exec, nil)
	ctx := context.Background()
	task := createTask(t, s, "will fail")

	ws, _ := o.pool.Acquire(task.ID)
	o.runTask(ctx, task, ws)

	updated, _ := s.GetTask(ctx, task.ID)
	if updated.Status != StatusFailed {
		t.Fatalf("expected failed, got %s", updated.Status)
	}
	if updated.ErrorMessage == nil {
		t.Fatal("expected error message")
	}
	if o.pool.FreeCount() != len(coder.DefaultWorkspaces) {
		t.Fatal("workspace not released")
	}
}

func TestRunTask_WorkspaceStartFailure(t *testing.T) {
	exec := newMockExecutor()
	exec.startFunc = func(ctx context.Context, workspace string) error {
		return errors.New("workspace start failed")
	}

	o, s := testOrchestrator(t, exec, nil)
	ctx := context.Background()
	task := createTask(t, s, "start fail")

	ws, _ := o.pool.Acquire(task.ID)
	o.runTask(ctx, task, ws)

	updated, _ := s.GetTask(ctx, task.ID)
	if updated.Status != StatusFailed {
		t.Fatalf("expected failed, got %s", updated.Status)
	}
	if o.pool.FreeCount() != len(coder.DefaultWorkspaces) {
		t.Fatal("workspace not released")
	}
}

func TestRunImplement_Success(t *testing.T) {
	exec := newMockExecutor()
	o, s := testOrchestrator(t, exec, nil)
	ctx := context.Background()

	task := createTask(t, s, "implement me")
	task.Status = StatusAwaitingApproval
	task.Plan = strPtr("the plan")
	task.PlanFeedback = strPtr("approved")
	s.UpdateTask(ctx, task.ID, task)

	ws, _ := o.pool.Acquire(task.ID)
	o.runImplement(ctx, task, ws)

	updated, _ := s.GetTask(ctx, task.ID)
	if updated.Status != StatusComplete {
		t.Fatalf("expected complete, got %s", updated.Status)
	}
	if updated.CompletedAt == nil {
		t.Fatal("expected completed_at to be set")
	}
	if o.pool.FreeCount() != len(coder.DefaultWorkspaces) {
		t.Fatal("workspace not released")
	}
}

func TestRunImplement_Failure(t *testing.T) {
	exec := newMockExecutor()
	exec.sshFunc = func(ctx context.Context, workspace, command string, stdout, stderr io.Writer) (*coder.SSHResult, error) {
		return nil, errors.New("implement failed")
	}

	o, s := testOrchestrator(t, exec, nil)
	ctx := context.Background()

	task := createTask(t, s, "implement fail")
	task.Status = StatusAwaitingApproval
	task.Plan = strPtr("the plan")
	task.PlanFeedback = strPtr("approved")
	s.UpdateTask(ctx, task.ID, task)

	ws, _ := o.pool.Acquire(task.ID)
	o.runImplement(ctx, task, ws)

	updated, _ := s.GetTask(ctx, task.ID)
	if updated.Status != StatusFailed {
		t.Fatalf("expected failed, got %s", updated.Status)
	}
}

func TestProcessApprovedTasks(t *testing.T) {
	exec := newMockExecutor()
	o, s := testOrchestrator(t, exec, nil)
	ctx := context.Background()

	// Create one approved and one not-yet-approved task.
	approved := createTask(t, s, "approved")
	approved.Status = StatusAwaitingApproval
	approved.Plan = strPtr("plan")
	approved.PlanFeedback = strPtr("approved")
	s.UpdateTask(ctx, approved.ID, approved)

	pending := createTask(t, s, "pending review")
	pending.Status = StatusAwaitingApproval
	pending.Plan = strPtr("plan")
	s.UpdateTask(ctx, pending.ID, pending)

	if err := o.processApprovedTasks(ctx); err != nil {
		t.Fatal(err)
	}

	// Wait for goroutine.
	time.Sleep(100 * time.Millisecond)

	a, _ := s.GetTask(ctx, approved.ID)
	if a.Status != StatusComplete {
		t.Fatalf("expected approved task complete, got %s", a.Status)
	}

	p, _ := s.GetTask(ctx, pending.ID)
	if p.Status != StatusAwaitingApproval {
		t.Fatalf("expected pending task unchanged, got %s", p.Status)
	}
}

func TestRecoverActiveTasks(t *testing.T) {
	exec := newMockExecutor()
	o, s := testOrchestrator(t, exec, nil)
	ctx := context.Background()

	// Create tasks in active states.
	planning := createTask(t, s, "planning")
	planning.Status = StatusPlanning
	s.UpdateTask(ctx, planning.ID, planning)

	implementing := createTask(t, s, "implementing")
	implementing.Status = StatusImplementing
	s.UpdateTask(ctx, implementing.ID, implementing)

	// Also a queued task that should NOT be affected.
	queued := createTask(t, s, "queued")

	if err := o.recoverActiveTasks(ctx); err != nil {
		t.Fatal(err)
	}

	p, _ := s.GetTask(ctx, planning.ID)
	if p.Status != StatusFailed {
		t.Fatalf("expected planning task failed, got %s", p.Status)
	}

	i, _ := s.GetTask(ctx, implementing.ID)
	if i.Status != StatusFailed {
		t.Fatalf("expected implementing task failed, got %s", i.Status)
	}

	q, _ := s.GetTask(ctx, queued.ID)
	if q.Status != StatusQueued {
		t.Fatalf("expected queued task unchanged, got %s", q.Status)
	}
}

func TestRun_ContextCancellation(t *testing.T) {
	exec := newMockExecutor()
	o, _ := testOrchestrator(t, exec, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- o.Run(ctx)
	}()

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

func TestLogWriter(t *testing.T) {
	exec := newMockExecutor()
	o, s := testOrchestrator(t, exec, nil)
	ctx := context.Background()

	task := createTask(t, s, "log test")

	w := o.newLogWriter(ctx, task.ID, "plan", "stdout")
	w.Write([]byte("line one\nline two\n"))
	w.Write([]byte("partial"))
	w.Flush()

	if w.String() != "line one\nline two\npartial" {
		t.Fatalf("unexpected accumulated output: %q", w.String())
	}

	logs, err := s.ListTaskLogs(ctx, task.ID, "plan")
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 3 {
		t.Fatalf("expected 3 log lines, got %d", len(logs))
	}
	if logs[0].Line != "line one" {
		t.Fatalf("expected first line %q, got %q", "line one", logs[0].Line)
	}
	if logs[1].Line != "line two" {
		t.Fatalf("expected second line %q, got %q", "line two", logs[1].Line)
	}
	if logs[2].Line != "partial" {
		t.Fatalf("expected third line %q, got %q", "partial", logs[2].Line)
	}
}

func TestFullLifecycle(t *testing.T) {
	exec := newMockExecutor()
	planOutput := "detailed plan"
	callCount := 0
	exec.sshFunc = func(ctx context.Context, workspace, command string, stdout, stderr io.Writer) (*coder.SSHResult, error) {
		callCount++
		if callCount == 1 {
			fmt.Fprint(stdout, planOutput)
		}
		return &coder.SSHResult{ExitCode: 0}, nil
	}

	o, s := testOrchestrator(t, exec, nil)
	ctx := context.Background()

	// 1. Create task (queued).
	task := createTask(t, s, "full lifecycle")

	// 2. Tick picks up the task and runs planning.
	if err := o.tick(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)

	updated, _ := s.GetTask(ctx, task.ID)
	if updated.Status != StatusAwaitingApproval {
		t.Fatalf("expected awaiting_approval, got %s", updated.Status)
	}
	if updated.Plan == nil || *updated.Plan != planOutput {
		t.Fatal("plan not captured")
	}

	// 3. Approve the task.
	updated.PlanFeedback = strPtr("approved")
	s.UpdateTask(ctx, updated.ID, updated)

	// 4. Tick picks up the approved task and runs implementation.
	if err := o.tick(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)

	final, _ := s.GetTask(ctx, task.ID)
	if final.Status != StatusComplete {
		t.Fatalf("expected complete, got %s", final.Status)
	}
}

func TestMultipleTasksQueueing(t *testing.T) {
	exec := newMockExecutor()
	// Make SSH slow enough that both goroutines are still running when we check.
	exec.sshFunc = func(ctx context.Context, workspace, command string, stdout, stderr io.Writer) (*coder.SSHResult, error) {
		time.Sleep(200 * time.Millisecond)
		fmt.Fprint(stdout, "plan")
		return &coder.SSHResult{ExitCode: 0}, nil
	}

	pool := coder.NewPool([]string{"ws-1", "ws-2"})
	o, s := testOrchestrator(t, exec, pool)
	ctx := context.Background()

	// Create 3 tasks.
	t1 := createTask(t, s, "task-1")
	time.Sleep(10 * time.Millisecond)
	t2 := createTask(t, s, "task-2")
	time.Sleep(10 * time.Millisecond)
	t3 := createTask(t, s, "task-3")

	// First tick picks up task-1.
	if err := o.tick(ctx); err != nil {
		t.Fatal(err)
	}
	// Second tick picks up task-2.
	if err := o.tick(ctx); err != nil {
		t.Fatal(err)
	}
	// Third tick: no free workspace.
	if err := o.tick(ctx); err != nil {
		t.Fatal(err)
	}

	// Task-3 should still be queued.
	check, _ := s.GetTask(ctx, t3.ID)
	if check.Status != StatusQueued {
		t.Fatalf("expected task-3 queued, got %s", check.Status)
	}

	// Wait for first two to complete.
	time.Sleep(500 * time.Millisecond)

	c1, _ := s.GetTask(ctx, t1.ID)
	c2, _ := s.GetTask(ctx, t2.ID)
	if c1.Status != StatusAwaitingApproval {
		t.Fatalf("expected task-1 awaiting_approval, got %s", c1.Status)
	}
	if c2.Status != StatusAwaitingApproval {
		t.Fatalf("expected task-2 awaiting_approval, got %s", c2.Status)
	}

	// Now task-3 can be picked up.
	if err := o.tick(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)

	c3, _ := s.GetTask(ctx, t3.ID)
	if c3.Status != StatusAwaitingApproval {
		t.Fatalf("expected task-3 awaiting_approval, got %s", c3.Status)
	}
}
