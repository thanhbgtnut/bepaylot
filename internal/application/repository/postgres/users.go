package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thanhenti/bepaylot/internal/types"
)

// ErrNotFound is returned by repositories when a row does not exist.
var ErrNotFound = errors.New("not found")

// ErrEmailTaken is returned by UsersRepo.Register when the email is in use.
var ErrEmailTaken = errors.New("email already registered")

// UsersRepo persists types.User.
type UsersRepo struct{ pool *pgxpool.Pool }

const userCols = `id, email, name, auth_provider, password_hash <> '', is_active, role, monthly_limit::float8, last_login_at, created_at`

func scanUser(row pgx.Row) (types.User, error) {
	var u types.User
	err := row.Scan(userDest(&u)...)
	return u, err
}

// userDest lists the scan targets of userCols.
func userDest(u *types.User) []any {
	return []any{&u.ID, &u.Email, &u.Name, &u.AuthProvider, &u.HasPassword, &u.IsActive, &u.Role, &u.MonthlyLimit, &u.LastLoginAt, &u.CreatedAt}
}

// Create inserts a user, or returns the existing one on email conflict. It
// sets no password; cmd/seed and the dev auth bypass use it.
func (r *UsersRepo) Create(ctx context.Context, email, name string) (types.User, error) {
	u, err := scanUser(r.pool.QueryRow(ctx, `
		INSERT INTO users (email, name) VALUES ($1, $2)
		ON CONFLICT (email) DO UPDATE SET name = COALESCE(NULLIF(EXCLUDED.name, ''), users.name)
		RETURNING `+userCols,
		email, name,
	))
	if err != nil {
		return types.User{}, fmt.Errorf("users.Create: %w", err)
	}
	return u, nil
}

// Register inserts a local user with a password hash. An existing email
// returns ErrEmailTaken.
func (r *UsersRepo) Register(ctx context.Context, email, name, passwordHash string) (types.User, error) {
	u, err := scanUser(r.pool.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash) VALUES ($1, $2, $3)
		RETURNING `+userCols,
		email, name, passwordHash,
	))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return types.User{}, ErrEmailTaken
	}
	if err != nil {
		return types.User{}, fmt.Errorf("users.Register: %w", err)
	}
	return u, nil
}

// Get returns a user by id.
func (r *UsersRepo) Get(ctx context.Context, id uuid.UUID) (types.User, error) {
	u, err := scanUser(r.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return types.User{}, ErrNotFound
	}
	if err != nil {
		return types.User{}, fmt.Errorf("users.Get: %w", err)
	}
	return u, nil
}

// Credentials returns the user with this email and its password hash (empty
// when the account has no local password).
func (r *UsersRepo) Credentials(ctx context.Context, email string) (types.User, string, error) {
	var u types.User
	var hash string
	err := r.pool.QueryRow(ctx, `SELECT `+userCols+`, password_hash FROM users WHERE email = $1`, email).
		Scan(append(userDest(&u), &hash)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return types.User{}, "", ErrNotFound
	}
	if err != nil {
		return types.User{}, "", fmt.Errorf("users.Credentials: %w", err)
	}
	return u, hash, nil
}

// PasswordHash returns the stored password hash of a user.
func (r *UsersRepo) PasswordHash(ctx context.Context, id uuid.UUID) (string, error) {
	var hash string
	err := r.pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, id).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return hash, err
}

// SetPassword replaces a user's password hash.
func (r *UsersRepo) SetPassword(ctx context.Context, id uuid.UUID, passwordHash string) error {
	_, err := r.pool.Exec(ctx, `UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1`, id, passwordHash)
	return err
}

// TouchLogin records a successful sign-in.
func (r *UsersRepo) TouchLogin(ctx context.Context, id uuid.UUID) {
	_, _ = r.pool.Exec(ctx, `UPDATE users SET last_login_at = now() WHERE id = $1`, id)
}

// UpsertOIDC resolves an OIDC identity to a user: first by (provider,
// subject), then by email (linking the identity to the existing account),
// else a new passwordless user is created. created reports the last case.
func (r *UsersRepo) UpsertOIDC(ctx context.Context, provider, subject, email, name string) (u types.User, created bool, err error) {
	u, err = scanUser(r.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE auth_provider = $1 AND oidc_subject = $2`, provider, subject))
	if err == nil {
		return u, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return types.User{}, false, fmt.Errorf("users.UpsertOIDC: %w", err)
	}
	// xmax = 0 only for a freshly inserted row.
	var inserted bool
	err = r.pool.QueryRow(ctx, `
		INSERT INTO users (email, name, auth_provider, oidc_subject) VALUES ($1, $2, $3, $4)
		ON CONFLICT (email) DO UPDATE SET
			oidc_subject  = COALESCE(users.oidc_subject, EXCLUDED.oidc_subject),
			auth_provider = CASE WHEN users.oidc_subject IS NULL AND users.password_hash = '' THEN EXCLUDED.auth_provider ELSE users.auth_provider END,
			name          = COALESCE(NULLIF(users.name, ''), EXCLUDED.name),
			updated_at    = now()
		RETURNING `+userCols+`, xmax = 0`,
		email, name, provider, subject,
	).Scan(append(userDest(&u), &inserted)...)
	if err != nil {
		return types.User{}, false, fmt.Errorf("users.UpsertOIDC: %w", err)
	}
	return u, inserted, nil
}

// ListWithUsage lists every user with the cost of their model calls since
// from and the number of live API keys (admin, §7.8).
func (r *UsersRepo) ListWithUsage(ctx context.Context, from time.Time) ([]types.UserUsage, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+userCols+`,
		       COALESCE((SELECT sum(e.cost)::float8 FROM usage_events e WHERE e.user_id = users.id AND e.created_at >= $1), 0),
		       (SELECT count(*) FROM api_keys k WHERE k.user_id = users.id AND k.revoked_at IS NULL)
		FROM users ORDER BY created_at`, from)
	if err != nil {
		return nil, fmt.Errorf("users.ListWithUsage: %w", err)
	}
	defer rows.Close()
	var out []types.UserUsage
	for rows.Next() {
		var u types.UserUsage
		if err := rows.Scan(append(userDest(&u.User), &u.Spent, &u.Keys)...); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UserUpdate is an admin change to a user; nil fields are left as they are.
type UserUpdate struct {
	Role         *string
	MonthlyLimit *float64 // < 0 clears the limit
	IsActive     *bool
}

// Update applies an admin change.
func (r *UsersRepo) Update(ctx context.Context, id uuid.UUID, up UserUpdate) (types.User, error) {
	var limit any
	clear := false
	if up.MonthlyLimit != nil {
		if *up.MonthlyLimit < 0 {
			clear = true
		} else {
			limit = *up.MonthlyLimit
		}
	}
	u, err := scanUser(r.pool.QueryRow(ctx, `
		UPDATE users SET
			role          = COALESCE($2, role),
			monthly_limit = CASE WHEN $4 THEN NULL WHEN $3::numeric IS NOT NULL THEN $3::numeric ELSE monthly_limit END,
			is_active     = COALESCE($5, is_active),
			updated_at    = now()
		WHERE id = $1
		RETURNING `+userCols, id, up.Role, limit, clear, up.IsActive))
	if errors.Is(err, pgx.ErrNoRows) {
		return types.User{}, ErrNotFound
	}
	if err != nil {
		return types.User{}, fmt.Errorf("users.Update: %w", err)
	}
	return u, nil
}

// SetDefaultLimit gives a new account the configured monthly limit (§8.5).
func (r *UsersRepo) SetDefaultLimit(ctx context.Context, id uuid.UUID, limit float64) {
	if limit <= 0 {
		return
	}
	_, _ = r.pool.Exec(ctx, `UPDATE users SET monthly_limit = $2 WHERE id = $1 AND monthly_limit IS NULL`, id, limit)
}
