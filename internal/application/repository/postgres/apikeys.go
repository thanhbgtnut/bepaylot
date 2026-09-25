package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thanhenti/bepaylot/internal/types"
)

// APIKeysRepo persists and verifies API keys.
type APIKeysRepo struct{ pool *pgxpool.Pool }

// keyPlaintextPrefix is prepended to every generated key so it is recognisable
// in logs and config, mirroring Anthropic's "sk-ant-" convention.
const keyPlaintextPrefix = "sk-bepaylot-"

// Issue generates a new plaintext key, stores its hash, and returns the
// plaintext exactly once.
func (r *APIKeysRepo) Issue(ctx context.Context, userID uuid.UUID, name string) (plaintext string, key types.APIKey, err error) {
	buf := make([]byte, 24)
	if _, err = rand.Read(buf); err != nil {
		return "", types.APIKey{}, err
	}
	plaintext = keyPlaintextPrefix + hex.EncodeToString(buf)
	sum := sha256.Sum256([]byte(plaintext))
	prefix := plaintext[:min(len(plaintext), 16)]

	err = r.pool.QueryRow(ctx, `
		INSERT INTO api_keys (user_id, name, key_prefix, key_hash)
		VALUES ($1, $2, $3, $4)
		RETURNING id, user_id, name, key_prefix, key_hash, created_at`,
		userID, name, prefix, sum[:],
	).Scan(&key.ID, &key.UserID, &key.Name, &key.Prefix, &key.Hash, &key.CreatedAt)
	if err != nil {
		return "", types.APIKey{}, fmt.Errorf("apikeys.Issue: %w", err)
	}
	return plaintext, key, nil
}

// Verify resolves a plaintext key to its owning user, in constant time with
// respect to the stored hash. It also bumps last_used_at.
func (r *APIKeysRepo) Verify(ctx context.Context, plaintext string) (types.User, error) {
	plaintext = strings.TrimSpace(plaintext)
	if plaintext == "" {
		return types.User{}, ErrNotFound
	}
	sum := sha256.Sum256([]byte(plaintext))
	prefix := plaintext[:min(len(plaintext), 16)]

	rows, err := r.pool.Query(ctx, `
		SELECT k.id, k.key_hash, u.id, u.email, u.name, u.created_at
		FROM api_keys k JOIN users u ON u.id = k.user_id
		WHERE k.key_prefix = $1 AND k.revoked_at IS NULL`, prefix)
	if err != nil {
		return types.User{}, fmt.Errorf("apikeys.Verify: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var keyID uuid.UUID
		var hash []byte
		var u types.User
		if err := rows.Scan(&keyID, &hash, &u.ID, &u.Email, &u.Name, &u.CreatedAt); err != nil {
			return types.User{}, err
		}
		if subtle.ConstantTimeCompare(hash, sum[:]) == 1 {
			rows.Close()
			_, _ = r.pool.Exec(ctx, `UPDATE api_keys SET last_used_at = now() WHERE id = $1`, keyID)
			return u, nil
		}
	}
	return types.User{}, ErrNotFound
}

// Get returns a key row by id (without the plaintext, which is never stored).
func (r *APIKeysRepo) Get(ctx context.Context, id uuid.UUID) (types.APIKey, error) {
	var k types.APIKey
	var lastUsed, revoked *time.Time
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, name, key_prefix, key_hash, last_used_at, revoked_at, created_at
		FROM api_keys WHERE id = $1`, id).
		Scan(&k.ID, &k.UserID, &k.Name, &k.Prefix, &k.Hash, &lastUsed, &revoked, &k.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return types.APIKey{}, ErrNotFound
	}
	if err != nil {
		return types.APIKey{}, fmt.Errorf("apikeys.Get: %w", err)
	}
	k.LastUsedAt, k.RevokedAt = lastUsed, revoked
	return k, nil
}
