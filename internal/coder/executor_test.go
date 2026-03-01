package coder

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"testing"
)

// fakeCommand returns a CommandCreator that re-execs the test binary as a
// helper process. Extra env vars (e.g. FAKE_STDOUT=hello) control the helper's
// behavior. This is the standard Go os/exec test pattern.
func fakeCommand(t *testing.T, env ...string) CommandCreator {
	t.Helper()
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cs := []string{"-test.run=TestHelperProcess", "--", name}
		cs = append(cs, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], cs...)
		cmd.Env = append(os.Environ(), "GO_TEST_HELPER=1")
		cmd.Env = append(cmd.Env, env...)
		return cmd
	}
}

// TestHelperProcess is invoked by fakeCommand. It is not a real test.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_TEST_HELPER") != "1" {
		return
	}

	if s := os.Getenv("FAKE_STDOUT"); s != "" {
		_, _ = fmt.Fprint(os.Stdout, s)
	}
	if s := os.Getenv("FAKE_STDERR"); s != "" {
		_, _ = fmt.Fprint(os.Stderr, s)
	}

	code := 0
	if s := os.Getenv("FAKE_EXIT_CODE"); s != "" {
		var err error
		code, err = strconv.Atoi(s)
		if err != nil {
			code = 1
		}
	}
	os.Exit(code)
}

func testExecutor(t *testing.T, env ...string) *Executor {
	t.Helper()
	return NewExecutor(slog.Default(), fakeCommand(t, env...))
}

func TestSSH_Success(t *testing.T) {
	e := testExecutor(t, "FAKE_STDOUT=hello world")

	var stdout, stderr bytes.Buffer
	result, err := e.SSH(context.Background(), "ws-1", "echo hello", &stdout, &stderr)
	if err != nil {
		t.Fatal("unexpected error:", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}
	if stdout.String() != "hello world" {
		t.Fatalf("expected stdout %q, got %q", "hello world", stdout.String())
	}
}

func TestSSH_StderrCapture(t *testing.T) {
	e := testExecutor(t, "FAKE_STDOUT=out", "FAKE_STDERR=err")

	var stdout, stderr bytes.Buffer
	result, err := e.SSH(context.Background(), "ws-1", "cmd", &stdout, &stderr)
	if err != nil {
		t.Fatal("unexpected error:", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}
	if stdout.String() != "out" {
		t.Fatalf("expected stdout %q, got %q", "out", stdout.String())
	}
	if stderr.String() != "err" {
		t.Fatalf("expected stderr %q, got %q", "err", stderr.String())
	}
}

func TestSSH_NonZeroExit(t *testing.T) {
	e := testExecutor(t, "FAKE_EXIT_CODE=42", "FAKE_STDERR=fail")

	var stdout, stderr bytes.Buffer
	result, err := e.SSH(context.Background(), "ws-1", "bad-cmd", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}
	if result == nil {
		t.Fatal("expected non-nil SSHResult for non-zero exit")
	}
	if result.ExitCode != 42 {
		t.Fatalf("expected exit code 42, got %d", result.ExitCode)
	}
	if stderr.String() != "fail" {
		t.Fatalf("expected stderr %q, got %q", "fail", stderr.String())
	}
}

func TestStartWorkspace(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		e := testExecutor(t)
		if err := e.StartWorkspace(context.Background(), "ws-1", nil); err != nil {
			t.Fatal("unexpected error:", err)
		}
	})

	t.Run("failure", func(t *testing.T) {
		e := testExecutor(t, "FAKE_EXIT_CODE=1", "FAKE_STDERR=workspace not found")
		err := e.StartWorkspace(context.Background(), "bad-ws", nil)
		if err == nil {
			t.Fatal("expected error")
		}
		if got := err.Error(); got == "" {
			t.Fatal("expected non-empty error message")
		}
	})
}

func TestStopWorkspace(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		e := testExecutor(t)
		if err := e.StopWorkspace(context.Background(), "ws-1"); err != nil {
			t.Fatal("unexpected error:", err)
		}
	})

	t.Run("failure", func(t *testing.T) {
		e := testExecutor(t, "FAKE_EXIT_CODE=1", "FAKE_STDERR=workspace not found")
		err := e.StopWorkspace(context.Background(), "bad-ws")
		if err == nil {
			t.Fatal("expected error")
		}
		if got := err.Error(); got == "" {
			t.Fatal("expected non-empty error message")
		}
	})
}

func TestListWorkspaces(t *testing.T) {
	json := `[{"name":"ws-1","latest_build":{"status":"running"}},{"name":"ws-2","latest_build":{"status":"stopped"}}]`
	e := testExecutor(t, "FAKE_STDOUT="+json)

	infos, err := e.ListWorkspaces(context.Background())
	if err != nil {
		t.Fatal("unexpected error:", err)
	}
	if len(infos) != 2 {
		t.Fatalf("expected 2 workspaces, got %d", len(infos))
	}
	if infos[0].Name != "ws-1" || infos[0].Status != WorkspaceStatusRunning {
		t.Fatalf("unexpected first workspace: %+v", infos[0])
	}
	if infos[1].Name != "ws-2" || infos[1].Status != WorkspaceStatusStopped {
		t.Fatalf("unexpected second workspace: %+v", infos[1])
	}
}

func TestListWorkspaces_Empty(t *testing.T) {
	e := testExecutor(t, "FAKE_STDOUT=[]")

	infos, err := e.ListWorkspaces(context.Background())
	if err != nil {
		t.Fatal("unexpected error:", err)
	}
	if len(infos) != 0 {
		t.Fatalf("expected 0 workspaces, got %d", len(infos))
	}
}

func TestListWorkspaces_UnknownStatus(t *testing.T) {
	json := `[{"name":"ws-1","latest_build":{"status":"something_new"}}]`
	e := testExecutor(t, "FAKE_STDOUT="+json)

	infos, err := e.ListWorkspaces(context.Background())
	if err != nil {
		t.Fatal("unexpected error:", err)
	}
	if infos[0].Status != WorkspaceStatusUnknown {
		t.Fatalf("expected WorkspaceStatusUnknown, got %q", infos[0].Status)
	}
}

func TestListWorkspaces_InvalidJSON(t *testing.T) {
	e := testExecutor(t, "FAKE_STDOUT=not json")

	_, err := e.ListWorkspaces(context.Background())
	if err == nil {
		t.Fatal("expected parse error")
	}
}
