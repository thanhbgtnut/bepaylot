package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thanhenti/bepaylot/internal/types"
)

// SessionsRepo persists types.Session.
type SessionsRepo struct{ pool *pgxpool.Pool }

// ErrCaseBound is returned when a session is already bound to another case.
var ErrCaseBound = errors.New("session is bound to another case")

const sessCols = `id, user_id, title, provider, model, system_override, summary, metadata, case_id, template_id, created_at, updated_at`

func scanSession(row pgx.Row) (types.Session, error) {
	var s types.Session
	err := row.Scan(&s.ID, &s.UserID, &s.Title, &s.Provider, &s.Model, &s.SystemOverride, &s.Summary,
		&metaScanner{&s.Metadata}, &s.CaseID, &s.TemplateID, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}

// CreateParams are the writable fields when creating a session.
type CreateParams struct {
	// ID pins the new session to a specific id — used when a caller (e.g. an
	// AG-UI client) already minted a thread id of its own and must see that
	// same id come back as the session id, so a later request carrying it
	// resolves to this session instead of spawning another one. Zero value
	// (uuid.Nil) lets Postgres generate one as usual.
	ID             uuid.UUID
	UserID         uuid.UUID
	Title          string
	Provider       string
	Model          string
	SystemOverride string
	Metadata       map[string]any
	// CaseID binds the new session to a case (§8.1).
	CaseID *uuid.UUID
	// TemplateID is the chat prompt template of the session (§8.4).
	TemplateID *uuid.UUID
}

// Create inserts a new session.
func (r *SessionsRepo) Create(ctx context.Context, p CreateParams) (types.Session, error) {
	meta := p.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	metaJSON, _ := cleanJSON(meta)

	id := p.ID
	if id == uuid.Nil {
		id = uuid.New()
	}

	s, err := scanSession(r.pool.QueryRow(ctx, `
		INSERT INTO sessions (id, user_id, title, provider, model, system_override, metadata, case_id, template_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+sessCols,
		id, p.UserID, cleanText(p.Title), cleanText(p.Provider), cleanText(p.Model), cleanText(p.SystemOverride), metaJSON, p.CaseID, p.TemplateID))
	if err != nil {
		return types.Session{}, fmt.Errorf("sessions.Create: %w", err)
	}
	return s, nil
}

// Get returns a non-deleted session by id.
func (r *SessionsRepo) Get(ctx context.Context, id uuid.UUID) (types.Session, error) {
	s, err := scanSession(r.pool.QueryRow(ctx, `SELECT `+sessCols+` FROM sessions WHERE id = $1 AND deleted_at IS NULL`, id))
	if errors.Is(err, ErrNotFound) {
		return types.Session{}, ErrNotFound
	}
	if err != nil {
		return types.Session{}, fmt.Errorf("sessions.Get: %w", err)
	}
	return s, nil
}

// BindCase sets the session's case once: it succeeds when the session has no
// case or already has this one, and returns ErrCaseBound for another case.
func (r *SessionsRepo) BindCase(ctx context.Context, id, caseID uuid.UUID) (types.Session, error) {
	s, err := scanSession(r.pool.QueryRow(ctx, `UPDATE sessions SET case_id = $2, updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL AND (case_id IS NULL OR case_id = $2) RETURNING `+sessCols, id, caseID))
	if errors.Is(err, ErrNotFound) {
		if cur, gerr := r.Get(ctx, id); gerr == nil && cur.CaseID != nil && *cur.CaseID != caseID {
			return cur, ErrCaseBound
		}
	}
	return s, err
}

// ListByCase returns a user's sessions bound to one case, newest first.
func (r *SessionsRepo) ListByCase(ctx context.Context, userID, caseID uuid.UUID, limit int) ([]types.Session, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.pool.Query(ctx, `SELECT `+sessCols+` FROM sessions
		WHERE user_id = $1 AND case_id = $2 AND deleted_at IS NULL AND NOT metadata ? 'sheet_id' ORDER BY updated_at DESC LIMIT $3`, userID, caseID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListByUser returns sessions for a user, newest first, using updated_at as a
// keyset cursor. A zero `before` starts from the most recent.
func (r *SessionsRepo) ListByUser(ctx context.Context, userID uuid.UUID, before time.Time, limit int) ([]types.Session, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if before.IsZero() {
		before = time.Now().Add(24 * time.Hour)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT `+sessCols+`
		FROM sessions
		WHERE user_id = $1 AND deleted_at IS NULL AND updated_at < $2 AND NOT metadata ? 'sheet_id'
		ORDER BY updated_at DESC
		LIMIT $3`, userID, before, limit)
	if err != nil {
		return nil, fmt.Errorf("sessions.ListByUser: %w", err)
	}
	defer rows.Close()

	var out []types.Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// UpdateParams carries optional session mutations; nil fields are left as-is.
type UpdateParams struct {
	Title    *string
	Summary  *string
	Metadata map[string]any
}

// Update applies a partial update and bumps updated_at.
func (r *SessionsRepo) Update(ctx context.Context, id uuid.UUID, p UpdateParams) (types.Session, error) {
	var metaJSON []byte
	if p.Metadata != nil {
		metaJSON, _ = cleanJSON(p.Metadata)
	}
	title, summary := cleanPtr(p.Title), cleanPtr(p.Summary)
	s, err := scanSession(r.pool.QueryRow(ctx, `
		UPDATE sessions SET
			title      = COALESCE($2, title),
			summary    = COALESCE($3, summary),
			metadata   = COALESCE($4, metadata),
			updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING `+sessCols,
		id, title, summary, metaJSON))
	if errors.Is(err, ErrNotFound) {
		return types.Session{}, ErrNotFound
	}
	if err != nil {
		return types.Session{}, fmt.Errorf("sessions.Update: %w", err)
	}
	return s, nil
}

// SetTemplate sets (or clears, with nil) the chat template of a session.
func (r *SessionsRepo) SetTemplate(ctx context.Context, id uuid.UUID, templateID *uuid.UUID) (types.Session, error) {
	return scanSession(r.pool.QueryRow(ctx, `UPDATE sessions SET template_id = $2, updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL RETURNING `+sessCols, id, templateID))
}

// Touch bumps updated_at, used after a turn completes.
func (r *SessionsRepo) Touch(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE sessions SET updated_at = now() WHERE id = $1`, id)
	return err
}

// SoftDelete marks a session deleted.
func (r *SessionsRepo) SoftDelete(ctx context.Context, id uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `UPDATE sessions SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("sessions.SoftDelete: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// metaScanner unmarshals a jsonb column into a map.
type metaScanner struct{ dst *map[string]any }

func (m *metaScanner) Scan(src any) error {
	*m.dst = map[string]any{}
	switch v := src.(type) {
	case nil:
		return nil
	case []byte:
		if len(v) == 0 {
			return nil
		}
		return json.Unmarshal(v, m.dst)
	case string:
		if v == "" {
			return nil
		}
		return json.Unmarshal([]byte(v), m.dst)
	default:
		return fmt.Errorf("metaScanner: unsupported type %T", src)
	}
}
