package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TasksRepo persists dead letters, pending ops and processing spans (§4.5).
type TasksRepo struct{ pool *pgxpool.Pool }

// DeadLetter is one archived task that exhausted its retries.
type DeadLetter struct {
	ID        int64           `json:"id"`
	TaskType  string          `json:"task_type"`
	Queue     string          `json:"queue"`
	Scope     string          `json:"scope"`
	ScopeID   string          `json:"scope_id"`
	RelatedID string          `json:"related_id"`
	Payload   json.RawMessage `json:"payload"`
	LastError string          `json:"last_error"`
	FailCount int             `json:"fail_count"`
	FailedAt  time.Time       `json:"failed_at"`
}

// InsertDeadLetter archives a failed task.
func (r *TasksRepo) InsertDeadLetter(ctx context.Context, d DeadLetter) error {
	payload := d.Payload
	if len(payload) == 0 || !json.Valid(payload) {
		payload = json.RawMessage("{}")
	}
	_, err := r.pool.Exec(ctx, `INSERT INTO task_dead_letters (task_type, queue, scope, scope_id, related_id, payload, last_error, fail_count)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, d.TaskType, d.Queue, d.Scope, d.ScopeID, d.RelatedID, []byte(payload), cleanText(d.LastError), d.FailCount)
	return err
}

// ListDeadLetters filters by task type and scope id.
func (r *TasksRepo) ListDeadLetters(ctx context.Context, taskType, scopeID string, limit int) ([]DeadLetter, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `SELECT id, task_type, queue, scope, scope_id, related_id, payload, last_error, fail_count, failed_at
		FROM task_dead_letters WHERE ($1 = '' OR task_type = $1) AND ($2 = '' OR scope_id = $2) ORDER BY failed_at DESC LIMIT $3`, taskType, scopeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeadLetter
	for rows.Next() {
		var d DeadLetter
		if err := rows.Scan(&d.ID, &d.TaskType, &d.Queue, &d.Scope, &d.ScopeID, &d.RelatedID, &d.Payload, &d.LastError, &d.FailCount, &d.FailedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// TakeDeadLetter deletes and returns a dead letter (for retry).
func (r *TasksRepo) TakeDeadLetter(ctx context.Context, id int64) (DeadLetter, error) {
	var d DeadLetter
	err := r.pool.QueryRow(ctx, `DELETE FROM task_dead_letters WHERE id = $1
		RETURNING id, task_type, queue, scope, scope_id, related_id, payload, last_error, fail_count, failed_at`, id).
		Scan(&d.ID, &d.TaskType, &d.Queue, &d.Scope, &d.ScopeID, &d.RelatedID, &d.Payload, &d.LastError, &d.FailCount, &d.FailedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

// PendingOp is a durable queued operation (debounced wiki work etc.).
type PendingOp struct {
	ID        int64
	TaskType  string
	Scope     string
	ScopeID   string
	Op        string
	DedupKey  string
	Payload   json.RawMessage
	FailCount int
}

// EnqueueOp stores a pending op.
func (r *TasksRepo) EnqueueOp(ctx context.Context, op PendingOp) error {
	payload := op.Payload
	if len(payload) == 0 {
		payload = json.RawMessage("{}")
	}
	_, err := r.pool.Exec(ctx, `INSERT INTO task_pending_ops (task_type, scope, scope_id, op, dedup_key, payload) VALUES ($1, $2, $3, $4, $5, $6)`,
		op.TaskType, op.Scope, op.ScopeID, op.Op, op.DedupKey, []byte(payload))
	return err
}

// PeekOps returns up to limit ops of a queue identity, oldest first.
func (r *TasksRepo) PeekOps(ctx context.Context, taskType, scope, scopeID string, limit int) ([]PendingOp, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, task_type, scope, scope_id, op, dedup_key, payload, fail_count FROM task_pending_ops
		WHERE task_type = $1 AND scope = $2 AND scope_id = $3 ORDER BY id LIMIT $4`, taskType, scope, scopeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingOp
	for rows.Next() {
		var o PendingOp
		if err := rows.Scan(&o.ID, &o.TaskType, &o.Scope, &o.ScopeID, &o.Op, &o.DedupKey, &o.Payload, &o.FailCount); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// DeleteOps removes consumed ops.
func (r *TasksRepo) DeleteOps(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := r.pool.Exec(ctx, `DELETE FROM task_pending_ops WHERE id = ANY($1)`, ids)
	return err
}

// IncrOpFail bumps fail counters.
func (r *TasksRepo) IncrOpFail(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := r.pool.Exec(ctx, `UPDATE task_pending_ops SET fail_count = fail_count + 1 WHERE id = ANY($1)`, ids)
	return err
}

// OpScopes lists scopes that have pending ops of taskType (startup recovery).
func (r *TasksRepo) OpScopes(ctx context.Context, taskType string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT DISTINCT scope_id FROM task_pending_ops WHERE task_type = $1`, taskType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Span is one processing span.
type Span struct {
	ID         int64      `json:"id"`
	DocumentID uuid.UUID  `json:"document_id"`
	Gen        int        `json:"gen"`
	Stage      string     `json:"stage"`
	Ref        string     `json:"ref,omitempty"`
	Status     string     `json:"status"`
	Error      string     `json:"error,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// StartSpan opens a span and returns its id.
func (r *TasksRepo) StartSpan(ctx context.Context, doc uuid.UUID, gen int, stage, ref string) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `INSERT INTO processing_spans (document_id, gen, stage, ref, status) VALUES ($1, $2, $3, $4, 'running') RETURNING id`,
		doc, gen, stage, ref).Scan(&id)
	return id, err
}

// FinishSpan closes a span.
func (r *TasksRepo) FinishSpan(ctx context.Context, id int64, errMsg string) {
	status := "done"
	if errMsg != "" {
		status = "failed"
	}
	_, _ = r.pool.Exec(ctx, `UPDATE processing_spans SET status = $2, error = $3, finished_at = now() WHERE id = $1`, id, status, cleanText(errMsg))
}

// Spans lists spans of a document generation.
func (r *TasksRepo) Spans(ctx context.Context, doc uuid.UUID, gen int, limit int) ([]Span, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, document_id, gen, stage, ref, status, error, started_at, finished_at FROM processing_spans
		WHERE document_id = $1 AND gen = $2 ORDER BY started_at LIMIT $3`, doc, gen, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Span
	for rows.Next() {
		var s Span
		if err := rows.Scan(&s.ID, &s.DocumentID, &s.Gen, &s.Stage, &s.Ref, &s.Status, &s.Error, &s.StartedAt, &s.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// LastActivity returns when a document last opened a span.
func (r *TasksRepo) LastActivity(ctx context.Context, doc uuid.UUID) (time.Time, error) {
	var t *time.Time
	err := r.pool.QueryRow(ctx, `SELECT max(coalesce(finished_at, started_at)) FROM processing_spans WHERE document_id = $1`, doc).Scan(&t)
	if err != nil || t == nil {
		return time.Time{}, err
	}
	return *t, nil
}
