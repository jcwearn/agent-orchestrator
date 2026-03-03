package orchestrator

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
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
	startFunc   func(ctx context.Context, workspace string, params map[string]string) error
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
			if !strings.Contains(command, "test -d") {
				_, _ = fmt.Fprint(stdout, "mock plan output")
			}
			return &coder.SSHResult{ExitCode: 0}, nil
		},
		startFunc: func(ctx context.Context, workspace string, params map[string]string) error { return nil },
		stopFunc:  func(ctx context.Context, workspace string) error { return nil },
	}
}

func (m *mockExecutor) SSH(ctx context.Context, workspace, command string, stdout, stderr io.Writer) (*coder.SSHResult, error) {
	m.mu.Lock()
	m.sshCalls = append(m.sshCalls, sshCall{Workspace: workspace, Command: command})
	m.mu.Unlock()
	return m.sshFunc(ctx, workspace, command, stdout, stderr)
}

func (m *mockExecutor) StartWorkspace(ctx context.Context, workspace string, params map[string]string) error {
	m.mu.Lock()
	m.startCalls = append(m.startCalls, workspace)
	m.mu.Unlock()
	return m.startFunc(ctx, workspace, params)
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
	t.Cleanup(func() { _ = db.Close() })

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
	o := New(s, exec, pool, slog.Default(), Config{TickInterval: 50 * time.Millisecond, VerifyRetryDelay: 10 * time.Millisecond})
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

// waitForStatus polls the store until the task reaches the expected status or
// the timeout expires. This replaces flaky time.Sleep calls in tests that launch
// goroutines via tick().
func waitForStatus(t *testing.T, s *store.Store, taskID, expected string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, err := s.GetTask(context.Background(), taskID)
		if err != nil {
			t.Fatalf("waitForStatus: get task: %v", err)
		}
		if task.Status == expected {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	task, _ := s.GetTask(context.Background(), taskID)
	t.Fatalf("timed out waiting for task %s to reach %q, current status: %q", taskID, expected, task.Status)
}

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

	waitForStatus(t, s, t1.ID, StatusAwaitingApproval, 5*time.Second)
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
		if !strings.Contains(command, "test -d") {
			_, _ = fmt.Fprint(stdout, "the generated plan")
		}
		return &coder.SSHResult{ExitCode: 0}, nil
	}

	o, s := testOrchestrator(t, exec, nil)
	ctx := context.Background()
	task := createTask(t, s, "plan me")

	ws, _ := o.pool.Acquire(task.ID)
	task.Status = StatusPlanning
	_ = s.UpdateTask(ctx, task.ID, task)
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
	// First SSH call should be the repo dir verification.
	exec.mu.Lock()
	defer exec.mu.Unlock()
	if len(exec.sshCalls) < 2 {
		t.Fatalf("expected at least 2 SSH calls (verify + plan), got %d", len(exec.sshCalls))
	}
	if !strings.Contains(exec.sshCalls[0].Command, "test -d") {
		t.Fatalf("expected first SSH call to be repo dir verify, got: %s", exec.sshCalls[0].Command)
	}
	// Plan command should NOT contain --permission-mode plan.
	if strings.Contains(exec.sshCalls[1].Command, "--permission-mode plan") {
		t.Fatalf("plan command should not contain --permission-mode plan, got: %s", exec.sshCalls[1].Command)
	}
	if !strings.Contains(exec.sshCalls[1].Command, "> /dev/null 2>&1") {
		t.Fatalf("expected git checkout redirected to /dev/null, got: %s", exec.sshCalls[1].Command)
	}
	if !strings.Contains(exec.sshCalls[1].Command, "TERM=dumb claude") {
		t.Fatalf("expected TERM=dumb before claude command, got: %s", exec.sshCalls[1].Command)
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
	task.Status = StatusPlanning
	_ = s.UpdateTask(ctx, task.ID, task)
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
	exec.startFunc = func(ctx context.Context, workspace string, params map[string]string) error {
		return errors.New("workspace start failed")
	}

	o, s := testOrchestrator(t, exec, nil)
	ctx := context.Background()
	task := createTask(t, s, "start fail")

	ws, _ := o.pool.Acquire(task.ID)
	task.Status = StatusPlanning
	_ = s.UpdateTask(ctx, task.ID, task)
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
	task.Status = StatusImplementing
	task.Plan = strPtr("the plan")
	task.PlanFeedback = strPtr("approved")
	_ = s.UpdateTask(ctx, task.ID, task)

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
	// First SSH call is repo dir verify, second is the implement command.
	exec.mu.Lock()
	defer exec.mu.Unlock()
	if len(exec.sshCalls) < 2 {
		t.Fatalf("expected at least 2 SSH calls (verify + implement), got %d", len(exec.sshCalls))
	}
	if !strings.Contains(exec.sshCalls[0].Command, "test -d") {
		t.Fatalf("expected first SSH call to be repo dir verify, got: %s", exec.sshCalls[0].Command)
	}
	if strings.Contains(exec.sshCalls[1].Command, "--permission-mode plan") {
		t.Fatalf("implement command should not contain --permission-mode plan, got: %s", exec.sshCalls[1].Command)
	}
}

func TestRunImplement_Failure(t *testing.T) {
	exec := newMockExecutor()
	exec.sshFunc = func(ctx context.Context, workspace, command string, stdout, stderr io.Writer) (*coder.SSHResult, error) {
		// Verify call succeeds, implement call fails.
		if strings.Contains(command, "test -d") {
			return &coder.SSHResult{ExitCode: 0}, nil
		}
		_, _ = fmt.Fprint(stderr, "Error: authentication failed\nfatal: could not push")
		return nil, errors.New("implement failed")
	}

	o, s := testOrchestrator(t, exec, nil)
	ctx := context.Background()

	task := createTask(t, s, "implement fail")
	task.Status = StatusImplementing
	task.Plan = strPtr("the plan")
	task.PlanFeedback = strPtr("approved")
	_ = s.UpdateTask(ctx, task.ID, task)

	ws, _ := o.pool.Acquire(task.ID)
	o.runImplement(ctx, task, ws)

	updated, _ := s.GetTask(ctx, task.ID)
	if updated.Status != StatusFailed {
		t.Fatalf("expected failed, got %s", updated.Status)
	}
	// Error message should contain stderr content.
	if updated.ErrorMessage == nil {
		t.Fatal("expected error message")
	}
	if !strings.Contains(*updated.ErrorMessage, "authentication failed") {
		t.Fatalf("expected stderr content in error message, got: %s", *updated.ErrorMessage)
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
	_ = s.UpdateTask(ctx, approved.ID, approved)

	pending := createTask(t, s, "pending review")
	pending.Status = StatusAwaitingApproval
	pending.Plan = strPtr("plan")
	_ = s.UpdateTask(ctx, pending.ID, pending)

	if err := o.processApprovedTasks(ctx); err != nil {
		t.Fatal(err)
	}

	waitForStatus(t, s, approved.ID, StatusComplete, 5*time.Second)

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
	_ = s.UpdateTask(ctx, planning.ID, planning)

	implementing := createTask(t, s, "implementing")
	implementing.Status = StatusImplementing
	_ = s.UpdateTask(ctx, implementing.ID, implementing)

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
	_, _ = w.Write([]byte("line one\nline two\n"))
	_, _ = w.Write([]byte("partial"))
	_ = w.Flush()

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
	exec.sshFunc = func(ctx context.Context, workspace, command string, stdout, stderr io.Writer) (*coder.SSHResult, error) {
		// Write plan output only for the Claude plan command (not verify or implement).
		if !strings.Contains(command, "test -d") && strings.Contains(command, "--session-id") {
			_, _ = fmt.Fprint(stdout, planOutput)
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
	waitForStatus(t, s, task.ID, StatusAwaitingApproval, 5*time.Second)

	updated, _ := s.GetTask(ctx, task.ID)
	if updated.Plan == nil || *updated.Plan != planOutput {
		t.Fatal("plan not captured")
	}

	// 3. Approve the task.
	updated.PlanFeedback = strPtr("approved")
	_ = s.UpdateTask(ctx, updated.ID, updated)

	// 4. Tick picks up the approved task and runs implementation.
	if err := o.tick(ctx); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, s, task.ID, StatusComplete, 5*time.Second)
}

func TestMultipleTasksQueueing(t *testing.T) {
	exec := newMockExecutor()
	// Make SSH slow enough that both goroutines are still running when we check.
	exec.sshFunc = func(ctx context.Context, workspace, command string, stdout, stderr io.Writer) (*coder.SSHResult, error) {
		if !strings.Contains(command, "test -d") {
			time.Sleep(200 * time.Millisecond)
			_, _ = fmt.Fprint(stdout, "plan")
		}
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

	// Wait for first two to finish planning.
	waitForStatus(t, s, t1.ID, StatusAwaitingApproval, 5*time.Second)
	waitForStatus(t, s, t2.ID, StatusAwaitingApproval, 5*time.Second)

	// Now task-3 can be picked up.
	if err := o.tick(ctx); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, s, t3.ID, StatusAwaitingApproval, 5*time.Second)
}

// --- mock notifier ---

type mockNotifier struct {
	mu               sync.Mutex
	planReadyCalls   []string
	checkCalls       []string
	completeCalls    []string
	failedCalls      []string
	planReadyResult  int64
	checkApproved    bool
	checkFeedback    string
}

func newMockNotifier() *mockNotifier {
	return &mockNotifier{planReadyResult: 42}
}

func (m *mockNotifier) NotifyPlanReady(ctx context.Context, owner, repo string, issue int, plan string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.planReadyCalls = append(m.planReadyCalls, fmt.Sprintf("%s/%s#%d", owner, repo, issue))
	return m.planReadyResult, nil
}

func (m *mockNotifier) CheckApproval(ctx context.Context, owner, repo string, issue int, commentID int64) (bool, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkCalls = append(m.checkCalls, fmt.Sprintf("%s/%s#%d@%d", owner, repo, issue, commentID))
	return m.checkApproved, m.checkFeedback, nil
}

func (m *mockNotifier) NotifyComplete(ctx context.Context, owner, repo string, issue int, prURL string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.completeCalls = append(m.completeCalls, fmt.Sprintf("%s/%s#%d", owner, repo, issue))
	return nil
}

func (m *mockNotifier) NotifyFailed(ctx context.Context, owner, repo string, issue int, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failedCalls = append(m.failedCalls, fmt.Sprintf("%s/%s#%d: %s", owner, repo, issue, reason))
	return nil
}

func intPtr(i int) *int { return &i }

func createGitHubTask(t *testing.T, s *store.Store, prompt string) *store.Task {
	t.Helper()
	task := &store.Task{
		Prompt:      prompt,
		RepoURL:     "https://github.com/test/repo.git",
		BaseBranch:  "main",
		SourceType:  "github",
		GithubOwner: strPtr("test"),
		GithubRepo:  strPtr("repo"),
		GithubIssue: intPtr(1),
		SessionID:   "session-" + prompt,
	}
	if err := s.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	return task
}

func TestRunTask_GitHubNotifyPlanReady(t *testing.T) {
	exec := newMockExecutor()
	exec.sshFunc = func(ctx context.Context, workspace, command string, stdout, stderr io.Writer) (*coder.SSHResult, error) {
		if !strings.Contains(command, "test -d") {
			_, _ = fmt.Fprint(stdout, "the plan")
		}
		return &coder.SSHResult{ExitCode: 0}, nil
	}

	notifier := newMockNotifier()
	o, s := testOrchestrator(t, exec, nil)
	o.config.Notifier = notifier
	ctx := context.Background()

	task := createGitHubTask(t, s, "github plan")
	ws, _ := o.pool.Acquire(task.ID)
	task.Status = StatusPlanning
	_ = s.UpdateTask(ctx, task.ID, task)
	o.runTask(ctx, task, ws)

	updated, _ := s.GetTask(ctx, task.ID)
	if updated.Status != StatusAwaitingApproval {
		t.Fatalf("expected awaiting_approval, got %s", updated.Status)
	}
	if updated.PlanCommentID == nil || *updated.PlanCommentID != 42 {
		t.Fatalf("expected plan_comment_id 42, got %v", updated.PlanCommentID)
	}

	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	if len(notifier.planReadyCalls) != 1 {
		t.Fatalf("expected 1 plan ready call, got %d", len(notifier.planReadyCalls))
	}
	if notifier.planReadyCalls[0] != "test/repo#1" {
		t.Fatalf("unexpected call: %s", notifier.planReadyCalls[0])
	}
}

func TestRunTask_NonGitHubSkipsNotifier(t *testing.T) {
	exec := newMockExecutor()
	exec.sshFunc = func(ctx context.Context, workspace, command string, stdout, stderr io.Writer) (*coder.SSHResult, error) {
		if !strings.Contains(command, "test -d") {
			_, _ = fmt.Fprint(stdout, "the plan")
		}
		return &coder.SSHResult{ExitCode: 0}, nil
	}

	notifier := newMockNotifier()
	o, s := testOrchestrator(t, exec, nil)
	o.config.Notifier = notifier
	ctx := context.Background()

	// Non-GitHub task (source_type = "manual").
	task := createTask(t, s, "manual plan")
	ws, _ := o.pool.Acquire(task.ID)
	task.Status = StatusPlanning
	_ = s.UpdateTask(ctx, task.ID, task)
	o.runTask(ctx, task, ws)

	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	if len(notifier.planReadyCalls) != 0 {
		t.Fatalf("expected 0 plan ready calls for non-github task, got %d", len(notifier.planReadyCalls))
	}
}

func TestFailTask_GitHubNotifyFailed(t *testing.T) {
	exec := newMockExecutor()
	exec.sshFunc = func(ctx context.Context, workspace, command string, stdout, stderr io.Writer) (*coder.SSHResult, error) {
		return nil, errors.New("ssh broken")
	}

	notifier := newMockNotifier()
	o, s := testOrchestrator(t, exec, nil)
	o.config.Notifier = notifier
	ctx := context.Background()

	task := createGitHubTask(t, s, "github fail")
	ws, _ := o.pool.Acquire(task.ID)
	task.Status = StatusPlanning
	_ = s.UpdateTask(ctx, task.ID, task)
	o.runTask(ctx, task, ws)

	updated, _ := s.GetTask(ctx, task.ID)
	if updated.Status != StatusFailed {
		t.Fatalf("expected failed, got %s", updated.Status)
	}

	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	if len(notifier.failedCalls) != 1 {
		t.Fatalf("expected 1 failed call, got %d", len(notifier.failedCalls))
	}
}

func TestProcessApprovedTasks_GitHubCheckApproval(t *testing.T) {
	exec := newMockExecutor()
	notifier := newMockNotifier()
	notifier.checkApproved = true

	o, s := testOrchestrator(t, exec, nil)
	o.config.Notifier = notifier
	ctx := context.Background()

	// Create GitHub task in awaiting_approval with a plan comment ID.
	task := createGitHubTask(t, s, "github approve")
	task.Status = StatusAwaitingApproval
	task.Plan = strPtr("the plan")
	commentID := 42
	task.PlanCommentID = &commentID
	_ = s.UpdateTask(ctx, task.ID, task)

	if err := o.processApprovedTasks(ctx); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, s, task.ID, StatusComplete, 5*time.Second)

	updated, _ := s.GetTask(ctx, task.ID)
	if updated.PlanFeedback == nil || *updated.PlanFeedback != "approved" {
		t.Fatal("expected plan_feedback 'approved'")
	}

	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	if len(notifier.checkCalls) != 1 {
		t.Fatalf("expected 1 check call, got %d", len(notifier.checkCalls))
	}
}

func TestProcessApprovedTasks_GitHubFeedback(t *testing.T) {
	exec := newMockExecutor()
	notifier := newMockNotifier()
	notifier.checkFeedback = "please add tests"

	o, s := testOrchestrator(t, exec, nil)
	o.config.Notifier = notifier
	ctx := context.Background()

	task := createGitHubTask(t, s, "github feedback")
	task.Status = StatusAwaitingApproval
	task.Plan = strPtr("the plan")
	commentID := 42
	task.PlanCommentID = &commentID
	_ = s.UpdateTask(ctx, task.ID, task)

	if err := o.processApprovedTasks(ctx); err != nil {
		t.Fatal(err)
	}

	updated, _ := s.GetTask(ctx, task.ID)
	// Task should remain awaiting_approval with feedback set.
	if updated.Status != StatusAwaitingApproval {
		t.Fatalf("expected awaiting_approval, got %s", updated.Status)
	}
	if updated.PlanFeedback == nil || *updated.PlanFeedback != "please add tests" {
		t.Fatalf("expected feedback, got %v", updated.PlanFeedback)
	}
	if updated.PlanRevision != 1 {
		t.Fatalf("expected plan_revision 1, got %d", updated.PlanRevision)
	}
}

func TestRunImplement_GitHubNotifyComplete(t *testing.T) {
	exec := newMockExecutor()
	notifier := newMockNotifier()

	o, s := testOrchestrator(t, exec, nil)
	o.config.Notifier = notifier
	ctx := context.Background()

	task := createGitHubTask(t, s, "github complete")
	task.Status = StatusImplementing
	task.Plan = strPtr("the plan")
	task.PlanFeedback = strPtr("approved")
	_ = s.UpdateTask(ctx, task.ID, task)

	ws, _ := o.pool.Acquire(task.ID)
	o.runImplement(ctx, task, ws)

	updated, _ := s.GetTask(ctx, task.ID)
	if updated.Status != StatusComplete {
		t.Fatalf("expected complete, got %s", updated.Status)
	}

	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	if len(notifier.completeCalls) != 1 {
		t.Fatalf("expected 1 complete call, got %d", len(notifier.completeCalls))
	}
}

func TestBuildPlanPrompt_IncludesRepoName(t *testing.T) {
	task := &store.Task{
		Prompt:  "Add a README",
		RepoURL: "https://github.com/jcwearn/agent-orchestrator",
	}
	prompt := buildPlanPrompt(task)

	if !strings.Contains(prompt, "agent-orchestrator") {
		t.Fatalf("expected plan prompt to contain repo name, got: %s", prompt)
	}
	if !strings.Contains(prompt, "Add a README") {
		t.Fatalf("expected plan prompt to contain task prompt, got: %s", prompt)
	}
}

func TestRepoName(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://github.com/user/repo.git", "repo"},
		{"https://github.com/user/repo", "repo"},
		{"https://github.com/jcwearn/agent-orchestrator", "agent-orchestrator"},
	}
	for _, tt := range tests {
		got := repoName(tt.url)
		if got != tt.want {
			t.Errorf("repoName(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestStripANSI(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"no escapes", "hello world", "hello world"},
		{"CSI color", "\x1b[31mred\x1b[0m", "red"},
		{"CSI cursor", "\x1b[?1004l\x1b[?2004l\x1b[?25h", ""},
		{"OSC sequence", "\x1b]9;4;0;\x07done", "done"},
		{"mixed", "\x1b[32mok\x1b[0m plain \x1b]0;title\x07 end", "ok plain  end"},
		{"multiline with escapes", "\x1b[1mline1\x1b[0m\nline2\n\x1b[33mline3\x1b[0m", "line1\nline2\nline3"},
		{"CSI private param", "\x1b[<u", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripANSI(tt.input)
			if got != tt.want {
				t.Errorf("stripANSI(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStepPlan_EmptyOutput(t *testing.T) {
	exec := newMockExecutor()
	exec.sshFunc = func(ctx context.Context, workspace, command string, stdout, stderr io.Writer) (*coder.SSHResult, error) {
		if strings.Contains(command, "test -d") {
			return &coder.SSHResult{ExitCode: 0}, nil
		}
		// Simulate PTY junk: SSH succeeds but stdout is empty/whitespace.
		_, _ = fmt.Fprint(stdout, "   \n\n  ")
		_, _ = fmt.Fprint(stderr, "some debug output")
		return &coder.SSHResult{ExitCode: 0}, nil
	}

	o, s := testOrchestrator(t, exec, nil)
	ctx := context.Background()
	task := createTask(t, s, "empty plan")

	ws, _ := o.pool.Acquire(task.ID)
	task.Status = StatusPlanning
	_ = s.UpdateTask(ctx, task.ID, task)
	o.runTask(ctx, task, ws)

	updated, _ := s.GetTask(ctx, task.ID)
	if updated.Status != StatusFailed {
		t.Fatalf("expected failed for empty plan, got %s", updated.Status)
	}
	if updated.ErrorMessage == nil || !strings.Contains(*updated.ErrorMessage, "empty output") {
		t.Fatalf("expected error about empty output, got: %v", updated.ErrorMessage)
	}
}

func TestStepPlan_VerifyRepoDirFailure(t *testing.T) {
	exec := newMockExecutor()
	exec.sshFunc = func(ctx context.Context, workspace, command string, stdout, stderr io.Writer) (*coder.SSHResult, error) {
		if strings.Contains(command, "test -d") {
			return &coder.SSHResult{ExitCode: 1}, fmt.Errorf("command exited with code 1: exit status 1")
		}
		_, _ = fmt.Fprint(stdout, "should not reach here")
		return &coder.SSHResult{ExitCode: 0}, nil
	}

	o, s := testOrchestrator(t, exec, nil)
	ctx := context.Background()
	task := createTask(t, s, "missing repo")

	ws, _ := o.pool.Acquire(task.ID)
	task.Status = StatusPlanning
	_ = s.UpdateTask(ctx, task.ID, task)
	o.runTask(ctx, task, ws)

	updated, _ := s.GetTask(ctx, task.ID)
	if updated.Status != StatusFailed {
		t.Fatalf("expected failed, got %s", updated.Status)
	}
	if updated.ErrorMessage == nil || !strings.Contains(*updated.ErrorMessage, "repo directory") {
		t.Fatalf("expected error about repo directory, got: %v", updated.ErrorMessage)
	}
	if !strings.Contains(*updated.ErrorMessage, "not found after 5 attempts") {
		t.Fatalf("expected retry exhaustion message, got: %v", updated.ErrorMessage)
	}
	// 5 verify retries + 1 diagnostic call, no Claude call.
	exec.mu.Lock()
	defer exec.mu.Unlock()
	if len(exec.sshCalls) != 6 {
		t.Fatalf("expected 6 SSH calls (5 verify retries + 1 diagnostic), got %d", len(exec.sshCalls))
	}
}

func TestStepPlan_VerifyRepoDirRetryThenSuccess(t *testing.T) {
	exec := newMockExecutor()
	var verifyAttempts int
	exec.sshFunc = func(ctx context.Context, workspace, command string, stdout, stderr io.Writer) (*coder.SSHResult, error) {
		if strings.Contains(command, "test -d") {
			verifyAttempts++
			if verifyAttempts < 3 {
				return &coder.SSHResult{ExitCode: 1}, fmt.Errorf("command exited with code 1: exit status 1")
			}
			return &coder.SSHResult{ExitCode: 0}, nil
		}
		_, _ = fmt.Fprint(stdout, "the plan")
		return &coder.SSHResult{ExitCode: 0}, nil
	}

	o, s := testOrchestrator(t, exec, nil)
	ctx := context.Background()
	task := createTask(t, s, "retry repo")

	ws, _ := o.pool.Acquire(task.ID)
	task.Status = StatusPlanning
	_ = s.UpdateTask(ctx, task.ID, task)
	o.runTask(ctx, task, ws)

	updated, _ := s.GetTask(ctx, task.ID)
	if updated.Status != StatusAwaitingApproval {
		t.Fatalf("expected awaiting_approval, got %s", updated.Status)
	}
	// 3 verify attempts + 1 plan call.
	exec.mu.Lock()
	defer exec.mu.Unlock()
	if len(exec.sshCalls) != 4 {
		t.Fatalf("expected 4 SSH calls (3 verify attempts + 1 plan), got %d", len(exec.sshCalls))
	}
}

func TestStepPlan_VerifyRepoDirSuccess(t *testing.T) {
	exec := newMockExecutor()
	exec.sshFunc = func(ctx context.Context, workspace, command string, stdout, stderr io.Writer) (*coder.SSHResult, error) {
		if !strings.Contains(command, "test -d") {
			_, _ = fmt.Fprint(stdout, "the plan")
		}
		return &coder.SSHResult{ExitCode: 0}, nil
	}

	o, s := testOrchestrator(t, exec, nil)
	ctx := context.Background()
	task := createTask(t, s, "valid repo")

	ws, _ := o.pool.Acquire(task.ID)
	task.Status = StatusPlanning
	_ = s.UpdateTask(ctx, task.ID, task)
	o.runTask(ctx, task, ws)

	updated, _ := s.GetTask(ctx, task.ID)
	if updated.Status != StatusAwaitingApproval {
		t.Fatalf("expected awaiting_approval, got %s", updated.Status)
	}
	if updated.Plan == nil || *updated.Plan != "the plan" {
		t.Fatalf("expected plan to be captured, got %v", updated.Plan)
	}
	// Both verify and Claude SSH calls should have been made.
	exec.mu.Lock()
	defer exec.mu.Unlock()
	if len(exec.sshCalls) != 2 {
		t.Fatalf("expected 2 SSH calls (verify + plan), got %d", len(exec.sshCalls))
	}
	if !strings.Contains(exec.sshCalls[0].Command, "test -d") {
		t.Fatalf("expected first call to be verify, got: %s", exec.sshCalls[0].Command)
	}
}

func TestLogWriter_Tail(t *testing.T) {
	exec := newMockExecutor()
	o, s := testOrchestrator(t, exec, nil)
	ctx := context.Background()
	task := createTask(t, s, "tail test")

	w := o.newLogWriter(ctx, task.ID, "plan", "stderr")
	_, _ = w.Write([]byte("line1\nline2\nline3\nline4\nline5"))

	got := w.Tail(3)
	if got != "line3\nline4\nline5" {
		t.Fatalf("Tail(3) = %q, want %q", got, "line3\nline4\nline5")
	}

	all := w.Tail(100)
	if all != "line1\nline2\nline3\nline4\nline5" {
		t.Fatalf("Tail(100) = %q, want all lines", all)
	}
}
