package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thanhenti/bepaylot/internal/types"
)

// CallbacksRepo persists document completion callbacks and their attempts.
type CallbacksRepo struct{ pool *pgxpool.Pool }

const cbCols = `id, document_id, gen, run, event, url, state, attempts, max_attempts, last_status_code, last_error,
	last_attempt_at, next_attempt_at, delivered_at, payload, created_at, updated_at`

func scanCallback(row pgx.Row) (types.DocumentCallback, error) {
	var c types.DocumentCallback
	err := row.Scan(&c.ID, &c.DocumentID, &c.Gen, &c.Run, &c.Event, &c.URL, &c.State, &c.Attempts, &c.MaxAttempts, &c.LastStatusCode,
		&c.LastError, &c.LastAttemptAt, &c.NextAttemptAt, &c.DeliveredAt, &metaScanner{&c.Payload}, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// Create inserts a pending delivery due now. It reports false (and returns
// the existing row) when this document generation and run already has a
// delivery to the same URL, so a status set twice never sends twice.
func (r *CallbacksRepo) Create(ctx context.Context, c types.DocumentCallback) (types.DocumentCallback, bool, error) {
	payload, err := cleanJSON(nonNilMap(c.Payload))
	if err != nil {
		return c, false, err
	}
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	out, err := scanCallback(r.pool.QueryRow(ctx, `
		INSERT INTO document_callbacks (id, document_id, gen, run, event, url, state, max_attempts, next_attempt_at, payload)
		VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7, now(), $8)
		ON CONFLICT (document_id, gen, run, url) DO NOTHING
		RETURNING `+cbCols, c.ID, c.DocumentID, c.Gen, c.Run, c.Event, c.URL, c.MaxAttempts, payload))
	if errors.Is(err, ErrNotFound) {
		existing, gerr := scanCallback(r.pool.QueryRow(ctx, `SELECT `+cbCols+` FROM document_callbacks
			WHERE document_id = $1 AND gen = $2 AND run = $3 AND url = $4`, c.DocumentID, c.Gen, c.Run, c.URL))
		return existing, false, gerr
	}
	return out, err == nil, err
}

// Get returns one delivery.
func (r *CallbacksRepo) Get(ctx context.Context, id uuid.UUID) (types.DocumentCallback, error) {
	return scanCallback(r.pool.QueryRow(ctx, `SELECT `+cbCols+` FROM document_callbacks WHERE id = $1`, id))
}

// ListByDocument returns a document's deliveries, newest first, with their
// attempt history.
func (r *CallbacksRepo) ListByDocument(ctx context.Context, doc uuid.UUID) ([]types.DocumentCallback, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+cbCols+` FROM document_callbacks WHERE document_id = $1 ORDER BY created_at DESC, gen DESC`, doc)
	if err != nil {
		return nil, err
	}
	var out []types.DocumentCallback
	idx := map[uuid.UUID]int{}
	for rows.Next() {
		c, err := scanCallback(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		idx[c.ID] = len(out)
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(out) == 0 {
		return out, err
	}
	ids := make([]uuid.UUID, 0, len(out))
	for _, c := range out {
		ids = append(ids, c.ID)
	}
	arows, err := r.pool.Query(ctx, `SELECT callback_id, attempt, status_code, error, response, duration_ms, created_at
		FROM document_callback_attempts WHERE callback_id = ANY($1) ORDER BY callback_id, attempt`, ids)
	if err != nil {
		return nil, err
	}
	defer arows.Close()
	for arows.Next() {
		var id uuid.UUID
		var a types.CallbackAttempt
		if err := arows.Scan(&id, &a.Attempt, &a.StatusCode, &a.Error, &a.Response, &a.DurationMs, &a.CreatedAt); err != nil {
			return nil, err
		}
		out[idx[id]].History = append(out[idx[id]].History, a)
	}
	return out, arows.Err()
}

// AttemptOutcome is the result of one HTTP attempt to record.
type AttemptOutcome struct {
	Attempt    int
	StatusCode int // 0 = no response
	Error      string
	Response   string
	Duration   time.Duration
	// State after this attempt; NextAttemptAt is set only while pending.
	State         string
	NextAttemptAt *time.Time
}

// RecordAttempt logs an attempt and moves the delivery to its new state. It
// is a no-op (false) when the attempt was already recorded, so a redelivered
// task cannot count twice.
func (r *CallbacksRepo) RecordAttempt(ctx context.Context, id uuid.UUID, o AttemptOutcome) (bool, error) {
	var status *int
	if o.StatusCode > 0 {
		status = &o.StatusCode
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `INSERT INTO document_callback_attempts (callback_id, attempt, status_code, error, response, duration_ms)
		VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT DO NOTHING`,
		id, o.Attempt, status, cleanText(o.Error), cleanText(o.Response), int(o.Duration.Milliseconds()))
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE document_callbacks SET
			attempts = GREATEST(attempts, $2), state = $3, last_status_code = $4, last_error = $5,
			last_attempt_at = now(), next_attempt_at = $6,
			delivered_at = CASE WHEN $3 = 'succeeded' THEN now() ELSE delivered_at END,
			updated_at = now()
		WHERE id = $1`, id, o.Attempt, o.State, status, cleanText(o.Error), o.NextAttemptAt); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// Rearm makes a delivery pending again for extra more attempts, due now
// (manual retry). It returns the updated row.
func (r *CallbacksRepo) Rearm(ctx context.Context, id uuid.UUID, extra int) (types.DocumentCallback, error) {
	return scanCallback(r.pool.QueryRow(ctx, `UPDATE document_callbacks SET state = 'pending',
			max_attempts = attempts + $2, next_attempt_at = now(), updated_at = now()
		WHERE id = $1 RETURNING `+cbCols, id, extra))
}

// Overdue returns pending deliveries whose next attempt is older than before
// (their task was lost, e.g. an inline queue restart).
func (r *CallbacksRepo) Overdue(ctx context.Context, before time.Time, limit int) ([]types.DocumentCallback, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+cbCols+` FROM document_callbacks
		WHERE state = 'pending' AND next_attempt_at < $1 ORDER BY next_attempt_at LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.DocumentCallback
	for rows.Next() {
		c, err := scanCallback(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MissingForTerminal returns documents that reached a terminal status with a
// callback URL but have no delivery for their current generation (the
// process stopped between the status update and scheduling).
func (r *CallbacksRepo) MissingForTerminal(ctx context.Context, before time.Time, limit int) ([]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx, `SELECT d.id FROM documents d
		WHERE d.callback_url <> '' AND d.status IN ('completed', 'partial', 'failed', 'cancelled') AND d.updated_at < $1
		  AND NOT EXISTS (SELECT 1 FROM document_callbacks c WHERE c.document_id = d.id AND c.gen = d.gen
		      AND c.run = d.callback_run AND c.url = d.callback_url)
		ORDER BY d.updated_at LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
