package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ModelCacheRepo keeps the answers of slow model calls on a page image by
// the hash of the request (§5.9, §6.9.4).
type ModelCacheRepo struct{ pool *pgxpool.Pool }

// Get returns the stored answer of key; ok is false when there is none.
func (r *ModelCacheRepo) Get(ctx context.Context, key string) (value []byte, ok bool, err error) {
	err = r.pool.QueryRow(ctx, `SELECT value FROM model_cache WHERE key = $1`, key).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	return value, err == nil, err
}

// Put stores the answer of key (JSON); an answer already stored is kept.
func (r *ModelCacheRepo) Put(ctx context.Context, key, kind string, value []byte) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO model_cache (key, kind, value) VALUES ($1, $2, $3) ON CONFLICT (key) DO NOTHING`, key, kind, value)
	return err
}
