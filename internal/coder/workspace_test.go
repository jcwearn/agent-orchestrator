package coder

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func testPool(t *testing.T) *Pool {
	t.Helper()
	return NewPool(DefaultWorkspaces)
}

func TestPool_AcquireRelease(t *testing.T) {
	p := testPool(t)

	ws, err := p.Acquire("task-1")
	if err != nil {
		t.Fatal("acquire:", err)
	}
	if ws == "" {
		t.Fatal("expected non-empty workspace name")
	}

	if err := p.Release(ws); err != nil {
		t.Fatal("release:", err)
	}

	// Re-acquire should succeed after release
	ws2, err := p.Acquire("task-2")
	if err != nil {
		t.Fatal("re-acquire:", err)
	}
	if ws2 == "" {
		t.Fatal("expected non-empty workspace name after re-acquire")
	}
}

func TestPool_AcquireAll(t *testing.T) {
	p := testPool(t)

	for i := range 4 {
		_, err := p.Acquire(fmt.Sprintf("task-%d", i))
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
	}

	_, err := p.Acquire("task-overflow")
	if !errors.Is(err, ErrNoFreeWorkspace) {
		t.Fatalf("expected ErrNoFreeWorkspace, got %v", err)
	}
}

func TestPool_ReleaseUnknown(t *testing.T) {
	p := testPool(t)

	err := p.Release("nonexistent")
	if !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("expected ErrWorkspaceNotFound, got %v", err)
	}
}

func TestPool_ReleaseAlreadyFree(t *testing.T) {
	p := testPool(t)

	err := p.Release(DefaultWorkspaces[0])
	if !errors.Is(err, ErrWorkspaceNotBusy) {
		t.Fatalf("expected ErrWorkspaceNotBusy, got %v", err)
	}
}

func TestPool_Status(t *testing.T) {
	p := testPool(t)

	ws, _ := p.Acquire("task-1")

	status := p.Status()
	if len(status) != 4 {
		t.Fatalf("expected 4 slots, got %d", len(status))
	}

	found := false
	for _, s := range status {
		if s.Name == ws {
			if s.TaskID != "task-1" {
				t.Fatalf("expected task-1 for %s, got %q", ws, s.TaskID)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("acquired workspace %q not found in status", ws)
	}
}

func TestPool_FreeCount(t *testing.T) {
	p := testPool(t)

	if got := p.FreeCount(); got != 4 {
		t.Fatalf("expected 4 free, got %d", got)
	}

	ws, _ := p.Acquire("task-1")
	if got := p.FreeCount(); got != 3 {
		t.Fatalf("expected 3 free, got %d", got)
	}

	_ = p.Release(ws)
	if got := p.FreeCount(); got != 4 {
		t.Fatalf("expected 4 free after release, got %d", got)
	}
}

func TestPool_ConcurrentAccess(t *testing.T) {
	p := testPool(t)

	var wg sync.WaitGroup
	acquired := make(chan string, 4)
	errs := make(chan error, 8)

	// 8 goroutines competing for 4 slots
	for i := range 8 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ws, err := p.Acquire(fmt.Sprintf("task-%d", id))
			if err != nil {
				errs <- err
				return
			}
			acquired <- ws
		}(i)
	}

	wg.Wait()
	close(acquired)
	close(errs)

	successCount := 0
	for range acquired {
		successCount++
	}

	errCount := 0
	for err := range errs {
		if !errors.Is(err, ErrNoFreeWorkspace) {
			t.Fatalf("unexpected error: %v", err)
		}
		errCount++
	}

	if successCount != 4 {
		t.Fatalf("expected 4 successful acquires, got %d", successCount)
	}
	if errCount != 4 {
		t.Fatalf("expected 4 ErrNoFreeWorkspace errors, got %d", errCount)
	}
}
