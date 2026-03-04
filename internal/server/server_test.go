package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jcwearn/agent-orchestrator/internal/coder"
	"github.com/jcwearn/agent-orchestrator/internal/store"
)

// --- test helpers ---

type mockExecutor struct {
	workspaces []coder.WorkspaceInfo
	err        error
}

func (m *mockExecutor) SSH(ctx context.Context, workspace, command string, stdout, stderr io.Writer) (*coder.SSHResult, error) {
	return &coder.SSHResult{ExitCode: 0}, nil
}

func (m *mockExecutor) StartWorkspace(ctx context.Context, workspace string, params map[string]string) error {
	return nil
}

func (m *mockExecutor) StopWorkspace(ctx context.Context, workspace string) error {
	return nil
}

func (m *mockExecutor) ListWorkspaces(ctx context.Context) ([]coder.WorkspaceInfo, error) {
	return m.workspaces, m.err
}

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

func testServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	s := testStore(t)
	pool := coder.NewPool([]string{"agent-1", "agent-2"})
	exec := &mockExecutor{}
	hub := NewHub()
	srv := New(s, pool, exec, hub, slog.Default())
	return srv, s
}

func testServerWithExecutor(t *testing.T, exec coder.WorkspaceExecutor) (*Server, *store.Store) {
	t.Helper()
	s := testStore(t)
	pool := coder.NewPool([]string{"agent-1", "agent-2"})
	hub := NewHub()
	srv := New(s, pool, exec, hub, slog.Default())
	return srv, s
}

func createTask(t *testing.T, s *store.Store, prompt string) *store.Task {
	t.Helper()
	task := &store.Task{
		Prompt:     prompt,
		RepoURL:    "https://github.com/test/repo",
		SourceType: "api",
		SessionID:  "session-123",
	}
	if err := s.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	return task
}

// --- task CRUD tests ---

func TestCreateTask_Success(t *testing.T) {
	srv, _ := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	body := `{"prompt": "implement feature X", "repo_url": "https://github.com/test/repo"}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var task store.Task
	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		t.Fatal(err)
	}
	if task.ID == "" {
		t.Fatal("expected task ID")
	}
	if task.Status != "queued" {
		t.Fatalf("expected status 'queued', got %q", task.Status)
	}
	if task.BaseBranch != "main" {
		t.Fatalf("expected base_branch 'main', got %q", task.BaseBranch)
	}
	if task.SourceType != "api" {
		t.Fatalf("expected source_type 'api', got %q", task.SourceType)
	}
	if task.SessionID == "" {
		t.Fatal("expected session_id to be set")
	}
}

func TestCreateTask_CustomBaseBranch(t *testing.T) {
	srv, _ := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	body := `{"prompt": "fix bug", "repo_url": "https://github.com/test/repo", "base_branch": "develop"}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var task store.Task
	_ = json.NewDecoder(resp.Body).Decode(&task)
	if task.BaseBranch != "develop" {
		t.Fatalf("expected base_branch 'develop', got %q", task.BaseBranch)
	}
}

func TestCreateTask_MissingPrompt(t *testing.T) {
	srv, _ := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	body := `{"repo_url": "https://github.com/test/repo"}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestCreateTask_MissingRepoURL(t *testing.T) {
	srv, _ := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	body := `{"prompt": "do something"}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestListTasks_Empty(t *testing.T) {
	srv, _ := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/tasks")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var tasks []store.Task
	_ = json.NewDecoder(resp.Body).Decode(&tasks)
	if len(tasks) != 0 {
		t.Fatalf("expected empty list, got %d", len(tasks))
	}
}

func TestListTasks_All(t *testing.T) {
	srv, s := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	createTask(t, s, "task-1")
	createTask(t, s, "task-2")

	resp, err := http.Get(ts.URL + "/api/v1/tasks")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var tasks []store.Task
	_ = json.NewDecoder(resp.Body).Decode(&tasks)
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
}

func TestListTasks_Filtered(t *testing.T) {
	srv, s := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	createTask(t, s, "task-1")
	task2 := createTask(t, s, "task-2")
	task2.Status = "planning"
	_ = s.UpdateTask(context.Background(), task2.ID, task2)

	resp, err := http.Get(ts.URL + "/api/v1/tasks?status=queued")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var tasks []store.Task
	_ = json.NewDecoder(resp.Body).Decode(&tasks)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 queued task, got %d", len(tasks))
	}
}

func TestGetTask_Success(t *testing.T) {
	srv, s := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	task := createTask(t, s, "get me")

	resp, err := http.Get(ts.URL + "/api/v1/tasks/" + task.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var got store.Task
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got.Prompt != "get me" {
		t.Fatalf("expected prompt 'get me', got %q", got.Prompt)
	}
}

func TestGetTask_NotFound(t *testing.T) {
	srv, _ := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/tasks/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestDeleteTask_Success(t *testing.T) {
	srv, s := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	task := createTask(t, s, "delete me")

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/tasks/"+task.ID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
}

func TestDeleteTask_NotFound(t *testing.T) {
	srv, _ := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/tasks/nonexistent", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

// --- approve tests ---

func TestApproveTask_Success(t *testing.T) {
	srv, s := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	task := createTask(t, s, "approve me")
	task.Status = "awaiting_approval"
	plan := "the plan"
	task.Plan = &plan
	_ = s.UpdateTask(context.Background(), task.ID, task)

	resp, err := http.Post(ts.URL+"/api/v1/tasks/"+task.ID+"/approve", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var updated store.Task
	_ = json.NewDecoder(resp.Body).Decode(&updated)
	if updated.PlanFeedback == nil || *updated.PlanFeedback != "approved" {
		t.Fatal("expected plan_feedback to be 'approved'")
	}
}

func TestApproveTask_WrongStatus(t *testing.T) {
	srv, s := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	task := createTask(t, s, "not ready")

	resp, err := http.Post(ts.URL+"/api/v1/tasks/"+task.ID+"/approve", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d", resp.StatusCode)
	}
}

func TestApproveTask_NotFound(t *testing.T) {
	srv, _ := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v1/tasks/nonexistent/approve", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestApproveTask_WithRunTestsAndDecisions(t *testing.T) {
	srv, s := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	task := createTask(t, s, "approve with options")
	task.Status = "awaiting_approval"
	plan := "### Decision: DB\n- [ ] PostgreSQL -- mature\n- [ ] SQLite -- simple"
	task.Plan = &plan
	_ = s.UpdateTask(context.Background(), task.ID, task)

	body := `{"run_tests": true, "decisions": "- [x] PostgreSQL -- mature"}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks/"+task.ID+"/approve", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var updated store.Task
	_ = json.NewDecoder(resp.Body).Decode(&updated)
	if !updated.RunTests {
		t.Fatal("expected run_tests to be true")
	}
	if updated.Decisions == nil || *updated.Decisions != "- [x] PostgreSQL -- mature" {
		t.Fatalf("expected decisions to be set, got %v", updated.Decisions)
	}
	if updated.PlanFeedback == nil || *updated.PlanFeedback != "approved" {
		t.Fatal("expected plan_feedback to be 'approved'")
	}
}

func TestApproveTask_EmptyBody(t *testing.T) {
	srv, s := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	task := createTask(t, s, "approve empty body")
	task.Status = "awaiting_approval"
	_ = s.UpdateTask(context.Background(), task.ID, task)

	resp, err := http.Post(ts.URL+"/api/v1/tasks/"+task.ID+"/approve", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var updated store.Task
	_ = json.NewDecoder(resp.Body).Decode(&updated)
	if updated.RunTests {
		t.Fatal("expected run_tests to be false")
	}
	if updated.Decisions != nil {
		t.Fatalf("expected decisions to be nil, got %v", updated.Decisions)
	}
}

// --- feedback tests ---

func TestFeedbackTask_Success(t *testing.T) {
	srv, s := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	task := createTask(t, s, "feedback me")
	task.Status = "awaiting_approval"
	plan := "the plan"
	task.Plan = &plan
	_ = s.UpdateTask(context.Background(), task.ID, task)

	body := `{"feedback": "please add error handling"}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks/"+task.ID+"/feedback", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var updated store.Task
	_ = json.NewDecoder(resp.Body).Decode(&updated)
	if updated.PlanFeedback == nil || *updated.PlanFeedback != "please add error handling" {
		t.Fatal("expected plan_feedback to be set")
	}
	if updated.PlanRevision != 1 {
		t.Fatalf("expected plan_revision 1, got %d", updated.PlanRevision)
	}
}

func TestFeedbackTask_EmptyFeedback(t *testing.T) {
	srv, s := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	task := createTask(t, s, "feedback me")
	task.Status = "awaiting_approval"
	_ = s.UpdateTask(context.Background(), task.ID, task)

	body := `{"feedback": ""}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks/"+task.ID+"/feedback", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestFeedbackTask_WithDecisions(t *testing.T) {
	srv, s := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	task := createTask(t, s, "feedback with decisions")
	task.Status = "awaiting_approval"
	plan := "the plan"
	task.Plan = &plan
	_ = s.UpdateTask(context.Background(), task.ID, task)

	body := `{"feedback": "use PostgreSQL", "decisions": "- [x] PostgreSQL -- mature"}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks/"+task.ID+"/feedback", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var updated store.Task
	_ = json.NewDecoder(resp.Body).Decode(&updated)
	if updated.PlanFeedback == nil || *updated.PlanFeedback != "use PostgreSQL" {
		t.Fatal("expected plan_feedback to be set")
	}
	if updated.Decisions == nil || *updated.Decisions != "- [x] PostgreSQL -- mature" {
		t.Fatalf("expected decisions to be set, got %v", updated.Decisions)
	}
	if updated.PlanRevision != 1 {
		t.Fatalf("expected plan_revision 1, got %d", updated.PlanRevision)
	}
}

func TestFeedbackTask_WrongStatus(t *testing.T) {
	srv, s := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	task := createTask(t, s, "not ready")

	body := `{"feedback": "some feedback"}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks/"+task.ID+"/feedback", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d", resp.StatusCode)
	}
}

// --- SSE logs test ---

func TestStreamLogs(t *testing.T) {
	srv, s := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	task := createTask(t, s, "log task")
	task.Status = "complete"
	_ = s.UpdateTask(context.Background(), task.ID, task)

	// Add some logs.
	for _, line := range []string{"line 1", "line 2"} {
		_ = s.CreateTaskLog(context.Background(), &store.TaskLog{
			TaskID: task.ID, Step: "plan", Stream: "stdout", Line: line,
		})
	}

	resp, err := http.Get(ts.URL + "/api/v1/tasks/" + task.ID + "/logs")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("expected text/event-stream, got %q", resp.Header.Get("Content-Type"))
	}

	body, _ := io.ReadAll(resp.Body)
	content := string(body)

	if !strings.Contains(content, "data: ") {
		t.Fatal("expected data: lines in SSE stream")
	}
	if !strings.Contains(content, "event: done") {
		t.Fatal("expected done event in SSE stream")
	}
	if !strings.Contains(content, "line 1") {
		t.Fatal("expected 'line 1' in SSE stream")
	}
	if !strings.Contains(content, "line 2") {
		t.Fatal("expected 'line 2' in SSE stream")
	}
}

func TestStreamLogs_NotFound(t *testing.T) {
	srv, _ := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/tasks/nonexistent/logs")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestStreamLogs_ActiveTask(t *testing.T) {
	srv, s := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	task := createTask(t, s, "active task")
	task.Status = "planning"
	_ = s.UpdateTask(context.Background(), task.ID, task)

	// Add a log.
	_ = s.CreateTaskLog(context.Background(), &store.TaskLog{
		TaskID: task.ID, Step: "plan", Stream: "stdout", Line: "line 1",
	})

	// Start SSE connection with timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/tasks/"+task.ID+"/logs", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read partial output - should get some data lines.
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	content := string(buf[:n])

	if !strings.Contains(content, "data: ") {
		t.Fatal("expected data: lines in SSE stream")
	}

	// Mark task as complete so the SSE endpoint closes.
	task.Status = "complete"
	_ = s.UpdateTask(context.Background(), task.ID, task)

	// Read remaining output.
	remaining, _ := io.ReadAll(resp.Body)
	content += string(remaining)

	if !strings.Contains(content, "event: done") {
		t.Fatal("expected done event after task completion")
	}
}

func testServerWithPool(t *testing.T, exec coder.WorkspaceExecutor, agents []string) (*Server, *store.Store) {
	t.Helper()
	s := testStore(t)
	pool := coder.NewPool(agents)
	hub := NewHub()
	srv := New(s, pool, exec, hub, slog.Default())
	return srv, s
}

// --- agents test ---

func TestListAgents(t *testing.T) {
	exec := &mockExecutor{
		workspaces: []coder.WorkspaceInfo{
			{Name: "agent-1", Status: coder.WorkspaceStatusRunning},
			{Name: "agent-2", Status: coder.WorkspaceStatusStopped},
		},
	}
	srv, _ := testServerWithExecutor(t, exec)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/agents")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var agents []AgentInfo
	_ = json.NewDecoder(resp.Body).Decode(&agents)
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}

	// Find agent-1 and verify workspace status is merged.
	statusMap := make(map[string]string)
	for _, a := range agents {
		statusMap[a.Name] = a.WorkspaceStatus
	}
	if statusMap["agent-1"] != "running" {
		t.Fatalf("expected agent-1 status 'running', got %q", statusMap["agent-1"])
	}
	if statusMap["agent-2"] != "stopped" {
		t.Fatalf("expected agent-2 status 'stopped', got %q", statusMap["agent-2"])
	}
}

func TestListAgents_ExecutorError(t *testing.T) {
	exec := &mockExecutor{
		err: io.ErrUnexpectedEOF,
	}
	srv, _ := testServerWithExecutor(t, exec)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/agents")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (graceful degradation), got %d", resp.StatusCode)
	}

	var agents []AgentInfo
	_ = json.NewDecoder(resp.Body).Decode(&agents)
	// Should still return pool slots, just without workspace status.
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}
}

func TestListAgents_SortOrder(t *testing.T) {
	exec := &mockExecutor{
		workspaces: []coder.WorkspaceInfo{
			{Name: "agent-1", Status: coder.WorkspaceStatusStopped},
			{Name: "agent-2", Status: coder.WorkspaceStatusRunning},
			{Name: "agent-3", Status: coder.WorkspaceStatusStopped},
			{Name: "agent-4", Status: coder.WorkspaceStatusRunning},
		},
	}
	srv, _ := testServerWithPool(t, exec, []string{"agent-1", "agent-2", "agent-3", "agent-4"})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/agents")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var agents []AgentInfo
	_ = json.NewDecoder(resp.Body).Decode(&agents)
	if len(agents) != 4 {
		t.Fatalf("expected 4 agents, got %d", len(agents))
	}

	// Expect: running first (alphabetical), then stopped (alphabetical).
	expected := []string{"agent-2", "agent-4", "agent-1", "agent-3"}
	for i, name := range expected {
		if agents[i].Name != name {
			t.Fatalf("agents[%d]: expected %q, got %q", i, name, agents[i].Name)
		}
	}
}

// --- hub tests ---

func TestHub_RegisterBroadcastUnregister(t *testing.T) {
	hub := NewHub()

	// Start a test websocket server.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		hub.Register(conn)
	}))
	defer ts.Close()

	// Connect a client.
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	// Give the server a moment to register.
	time.Sleep(50 * time.Millisecond)

	// Broadcast an event.
	hub.Broadcast(Event{Type: "task.created", TaskID: "test-123"})

	// Read the message from client.
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}

	var event Event
	if err := json.Unmarshal(msg, &event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "task.created" {
		t.Fatalf("expected type 'task.created', got %q", event.Type)
	}
	if event.TaskID != "test-123" {
		t.Fatalf("expected task_id 'test-123', got %q", event.TaskID)
	}

	// Unregister and verify broadcast doesn't fail.
	hub.Unregister(conn)
	hub.Broadcast(Event{Type: "task.updated", TaskID: "test-456"})
}

func TestHub_BroadcastNoConnections(t *testing.T) {
	hub := NewHub()
	// Should not panic.
	hub.Broadcast(Event{Type: "task.created", TaskID: "test"})
}

// --- websocket handler test ---

func TestWebSocket_Handler(t *testing.T) {
	srv, _ := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	// Give the server a moment to register the connection.
	time.Sleep(50 * time.Millisecond)

	// Broadcast via hub.
	srv.hub.Broadcast(Event{Type: "task.updated", TaskID: "ws-test"})

	// Read message.
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}

	var event Event
	_ = json.Unmarshal(msg, &event)
	if event.Type != "task.updated" {
		t.Fatalf("expected 'task.updated', got %q", event.Type)
	}
}

// --- JSON response tests ---

func TestCreateTask_InvalidJSON(t *testing.T) {
	srv, _ := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewReader([]byte("not json")))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}
