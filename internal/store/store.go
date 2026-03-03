package store

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")
var ErrDuplicateTask = errors.New("duplicate task")

type Store struct {
	db     *sql.DB
	logger *slog.Logger
}

func New(db *sql.DB, logger *slog.Logger) *Store {
	return &Store{db: db, logger: logger}
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}
