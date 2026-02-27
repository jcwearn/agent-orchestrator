package store

import (
	"context"
	"database/sql"
	"log/slog"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	s := New(db, slog.Default())
	if err := s.RunMigrations(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRunMigrations(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	// Verify tasks table exists
	var name string
	err := s.db.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name='tasks'").Scan(&name)
	if err != nil {
		t.Fatal("tasks table not found:", err)
	}

	// Verify task_logs table exists
	err = s.db.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name='task_logs'").Scan(&name)
	if err != nil {
		t.Fatal("task_logs table not found:", err)
	}

	// Verify idempotent re-run
	if err := s.RunMigrations(ctx); err != nil {
		t.Fatal("re-run migrations:", err)
	}
}

func TestPing(t *testing.T) {
	s := testStore(t)

	if err := s.Ping(context.Background()); err != nil {
		t.Fatal("ping should succeed:", err)
	}

	s.db.Close()
	if err := s.Ping(context.Background()); err == nil {
		t.Fatal("ping should fail on closed db")
	}
}
