package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thanhenti/bepaylot/internal/types"
)

// ErrNotFound is returned by repositories when a row does not exist.
var ErrNotFound = errors.New("not found")

// UsersRepo persists types.User.
type UsersRepo struct{ pool *pgxpool.Pool }

// Create inserts a user, or returns the existing one on email conflict.
func (r *UsersRepo) Create(ctx context.Context, email, name string) (types.User, error) {
	var u types.User
	err := r.pool.QueryRow(ctx, `
		INSERT INTO users (email, name) VALUES ($1, $2)
		ON CONFLICT (email) DO UPDATE SET name = COALESCE(NULLIF(EXCLUDED.name, ''), users.name)
		RETURNING id, email, name, created_at`,
		email, name,
	).Scan(&u.ID, &u.Email, &u.Name, &u.CreatedAt)
	if err != nil {
		return types.User{}, fmt.Errorf("users.Create: %w", err)
	}
	return u, nil
}

// Get returns a user by id.
func (r *UsersRepo) Get(ctx context.Context, id uuid.UUID) (types.User, error) {
	var u types.User
	err := r.pool.QueryRow(ctx, `SELECT id, email, name, created_at FROM users WHERE id = $1`, id).
		Scan(&u.ID, &u.Email, &u.Name, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return types.User{}, ErrNotFound
	}
	if err != nil {
		return types.User{}, fmt.Errorf("users.Get: %w", err)
	}
	return u, nil
}
