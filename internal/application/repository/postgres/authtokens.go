package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AuthTokensRepo records issued JWTs (by hash) so they can be revoked, as
// WeKnora's auth_tokens table does: a signed, unexpired JWT is accepted only
// while its row exists and is not revoked.
type AuthTokensRepo struct{ pool *pgxpool.Pool }

func tokenHash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// Record stores an issued token of kind "access" or "refresh".
func (r *AuthTokensRepo) Record(ctx context.Context, userID uuid.UUID, token, kind string, expiresAt time.Time) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO auth_tokens (user_id, token_hash, token_type, expires_at) VALUES ($1, $2, $3, $4)`,
		userID, tokenHash(token), kind, expiresAt)
	if err != nil {
		return fmt.Errorf("authtokens.Record: %w", err)
	}
	return nil
}

// Active returns the owner of token when it is recorded with this kind, not
// revoked and not expired; otherwise ErrNotFound.
func (r *AuthTokensRepo) Active(ctx context.Context, token, kind string) (uuid.UUID, error) {
	var userID uuid.UUID
	err := r.pool.QueryRow(ctx, `
		SELECT user_id FROM auth_tokens
		WHERE token_hash = $1 AND token_type = $2 AND revoked_at IS NULL AND expires_at > now()`,
		tokenHash(token), kind).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return userID, err
}

// Revoke revokes one token. It reports whether a live token was revoked, so a
// refresh token can be rotated exactly once.
func (r *AuthTokensRepo) Revoke(ctx context.Context, token string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE auth_tokens SET revoked_at = now() WHERE token_hash = $1 AND revoked_at IS NULL`, tokenHash(token))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// RevokeUser revokes every live token of a user (logout, password change).
func (r *AuthTokensRepo) RevokeUser(ctx context.Context, userID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE auth_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	return err
}

// PurgeExpired deletes rows that expired more than a day ago.
func (r *AuthTokensRepo) PurgeExpired(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM auth_tokens WHERE expires_at < now() - interval '1 day'`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// Secret returns the app secret called name, storing candidate first when
// none exists yet. Concurrent first calls agree on one value.
func (r *AuthTokensRepo) Secret(ctx context.Context, name, candidate string) (string, error) {
	if _, err := r.pool.Exec(ctx, `INSERT INTO app_secrets (name, value) VALUES ($1, $2) ON CONFLICT (name) DO NOTHING`, name, candidate); err != nil {
		return "", fmt.Errorf("app secret %s: %w", name, err)
	}
	var v string
	if err := r.pool.QueryRow(ctx, `SELECT value FROM app_secrets WHERE name = $1`, name).Scan(&v); err != nil {
		return "", fmt.Errorf("app secret %s: %w", name, err)
	}
	return v, nil
}
