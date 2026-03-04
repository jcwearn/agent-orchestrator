package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

func (s *Store) CreateUser(ctx context.Context, u *User) error {
	u.ID = uuid.New().String()
	u.CreatedAt = time.Now().UTC()
	if u.Role == "" {
		u.Role = "admin"
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users (id, username, password, role, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		u.ID, u.Username, u.Password, u.Role, u.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	var u User
	err := s.db.QueryRowContext(ctx, `
		SELECT id, username, password, role, created_at
		FROM users WHERE username = ?`, username).
		Scan(&u.ID, &u.Username, &u.Password, &u.Role, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user by username: %w", err)
	}
	return &u, nil
}

func (s *Store) GetUserByID(ctx context.Context, id string) (*User, error) {
	var u User
	err := s.db.QueryRowContext(ctx, `
		SELECT id, username, password, role, created_at
		FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Username, &u.Password, &u.Role, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user by id: %w", err)
	}
	return &u, nil
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return count, nil
}

func (s *Store) UpdateUserPassword(ctx context.Context, id string, hashedPassword string) error {
	result, err := s.db.ExecContext(ctx, "UPDATE users SET password = ? WHERE id = ?", hashedPassword, id)
	if err != nil {
		return fmt.Errorf("update user password: %w", err)
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
