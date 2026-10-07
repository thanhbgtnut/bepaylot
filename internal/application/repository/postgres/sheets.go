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

// SheetsRepo persists case sheets and the corrections made on them (§6.9.6).
type SheetsRepo struct{ pool *pgxpool.Pool }

const sheetCols = `s.id, s.case_id, s.template_id, s.template_version, COALESCE(t.name, ''), s.name, s.status, s.filled, s.total,
	s.rows, s.error, s.created_by, s.created_at, s.updated_at`

func scanSheet(row pgx.Row) (types.Sheet, error) {
	var s types.Sheet
	var rows []byte
	err := row.Scan(&s.ID, &s.CaseID, &s.TemplateID, &s.TemplateVersion, &s.TemplateName, &s.Name, &s.Status, &s.Filled, &s.Total,
		&rows, &s.Error, &s.CreatedBy, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrNotFound
	}
	if err != nil {
		return s, err
	}
	_ = json.Unmarshal(rows, &s.Rows)
	if s.Rows == nil {
		s.Rows = []types.SheetRow{}
	}
	return s, nil
}

// Create inserts a pending sheet.
func (r *SheetsRepo) Create(ctx context.Context, s types.Sheet) (types.Sheet, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `INSERT INTO case_sheets (case_id, template_id, template_version, name, total, created_by)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, s.CaseID, s.TemplateID, s.TemplateVersion, cleanText(s.Name), s.Total, s.CreatedBy).Scan(&id)
	if err != nil {
		return types.Sheet{}, fmt.Errorf("sheets.Create: %w", err)
	}
	return r.Get(ctx, id)
}

// Get returns a sheet.
func (r *SheetsRepo) Get(ctx context.Context, id uuid.UUID) (types.Sheet, error) {
	return scanSheet(r.pool.QueryRow(ctx, `SELECT `+sheetCols+` FROM case_sheets s
		LEFT JOIN prompt_templates t ON t.id = s.template_id WHERE s.id = $1`, id))
}

// ListByCase returns the sheets of a case, newest first.
func (r *SheetsRepo) ListByCase(ctx context.Context, caseID uuid.UUID) ([]types.Sheet, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+sheetCols+` FROM case_sheets s
		LEFT JOIN prompt_templates t ON t.id = s.template_id WHERE s.case_id = $1 ORDER BY s.created_at DESC`, caseID)
	if err != nil {
		return nil, fmt.Errorf("sheets.ListByCase: %w", err)
	}
	defer rows.Close()
	out := []types.Sheet{}
	for rows.Next() {
		s, err := scanSheet(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SetState records the progress of a sheet; rows are written when not nil.
func (r *SheetsRepo) SetState(ctx context.Context, id uuid.UUID, status string, filled int, rows []types.SheetRow, errMsg string) error {
	var raw any
	if rows != nil {
		b, err := cleanJSON(rows)
		if err != nil {
			return err
		}
		raw = b
	}
	_, err := r.pool.Exec(ctx, `UPDATE case_sheets SET status = $2, filled = $3, rows = COALESCE($4::jsonb, rows),
		error = $5, updated_at = now() WHERE id = $1`, id, status, filled, raw, cleanText(errMsg))
	return err
}

// DeleteByCase removes the sheets of a deleted case.
func (r *SheetsRepo) DeleteByCase(ctx context.Context, caseID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM case_sheets WHERE case_id = $1`, caseID)
	return err
}

// SheetEdit is one cell a user saves (§6.9.6): the field to write and, when
// the value differs from the AI's, the correction to record.
type SheetEdit struct {
	Row        int
	Field      FieldWrite
	Correction *types.Correction
}

// SaveEdits writes the user's fields, records corrections (a value set back
// to the AI's reverts the earlier ones of that key) and stores the new field
// ids in the sheet rows, in one transaction.
func (r *SheetsRepo) SaveEdits(ctx context.Context, sheetID uuid.UUID, edits []SheetEdit) (types.Sheet, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return types.Sheet{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	sh, err := scanSheet(tx.QueryRow(ctx, `SELECT `+sheetCols+` FROM case_sheets s
		LEFT JOIN prompt_templates t ON t.id = s.template_id WHERE s.id = $1 FOR UPDATE OF s`, sheetID))
	if err != nil {
		return types.Sheet{}, err
	}
	for _, e := range edits {
		f, err := saveField(ctx, tx, e.Field)
		if err != nil {
			return types.Sheet{}, err
		}
		if e.Row >= 0 && e.Row < len(sh.Rows) {
			id := f.ID
			sh.Rows[e.Row].FieldID = &id
		}
		key := e.Field.Key
		if _, err := tx.Exec(ctx, `UPDATE field_corrections SET reverted = true WHERE sheet_id = $1 AND key = $2 AND NOT reverted`, sheetID, key); err != nil {
			return types.Sheet{}, err
		}
		if c := e.Correction; c != nil {
			id := f.ID
			if _, err := tx.Exec(ctx, `INSERT INTO field_corrections (sheet_id, template_id, template_version, key, ai_field_id, user_field_id,
				ai_value_text, user_value_text, origin, user_id) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
				sheetID, sh.TemplateID, sh.TemplateVersion, key, c.AIFieldID, &id, cleanText(c.AIValueText), cleanText(c.UserValueText), c.Origin, c.UserID); err != nil {
				return types.Sheet{}, fmt.Errorf("sheets.SaveEdits correction: %w", err)
			}
		}
	}
	raw, err := cleanJSON(sh.Rows)
	if err != nil {
		return types.Sheet{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE case_sheets SET rows = $2, updated_at = now() WHERE id = $1`, sheetID, raw); err != nil {
		return types.Sheet{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return types.Sheet{}, err
	}
	return r.Get(ctx, sheetID)
}

// CorrectionStats counts, per field and template version, how many sheets had
// the field and how many times users corrected the AI value (§6.9.6).
func (r *SheetsRepo) CorrectionStats(ctx context.Context, templateID uuid.UUID) ([]types.CorrectionStat, error) {
	rows, err := r.pool.Query(ctx, `
		WITH used AS (
			SELECT s.template_version AS version, r->>'key' AS key, count(*) AS sheets
			FROM case_sheets s, jsonb_array_elements(s.rows) r
			WHERE s.template_id = $1 AND s.status = 'done' GROUP BY 1, 2
		), fixed AS (
			SELECT template_version AS version, key, count(DISTINCT sheet_id) AS edited,
			       (array_agg(ai_value_text || ' → ' || user_value_text ORDER BY created_at DESC))[1:3] AS examples
			FROM field_corrections WHERE template_id = $1 AND NOT reverted GROUP BY 1, 2
		)
		SELECT u.key, u.version, u.sheets, COALESCE(f.edited, 0), COALESCE(f.examples, '{}')
		FROM used u LEFT JOIN fixed f ON f.version = u.version AND f.key = u.key
		ORDER BY u.version DESC, COALESCE(f.edited, 0)::float / u.sheets DESC, u.key`, templateID)
	if err != nil {
		return nil, fmt.Errorf("sheets.CorrectionStats: %w", err)
	}
	defer rows.Close()
	out := []types.CorrectionStat{}
	for rows.Next() {
		var s types.CorrectionStat
		if err := rows.Scan(&s.Key, &s.Version, &s.Sheets, &s.Edited, &s.Examples); err != nil {
			return nil, err
		}
		if s.Sheets > 0 {
			s.Rate = float64(s.Edited) / float64(s.Sheets)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// UsageRepo records the cost of model calls (§8.5).
type UsageRepo struct{ pool *pgxpool.Pool }

// Insert stores one event.
func (r *UsageRepo) Insert(ctx context.Context, e types.UsageEvent) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO usage_events (user_id, case_id, kind, model, tokens_in, tokens_out, cost)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, e.UserID, e.CaseID, e.Kind, e.Model, e.TokensIn, e.TokensOut, e.Cost)
	return err
}

// Spent sums the cost since from, for one user or (nil) everyone.
func (r *UsageRepo) Spent(ctx context.Context, user *uuid.UUID, from time.Time) (float64, error) {
	var v float64
	err := r.pool.QueryRow(ctx, `SELECT COALESCE(sum(cost), 0)::float8 FROM usage_events
		WHERE created_at >= $2 AND ($1::uuid IS NULL OR user_id = $1)`, user, from).Scan(&v)
	return v, err
}

// ByKind sums the cost since from per kind, for one user or (nil) everyone.
func (r *UsageRepo) ByKind(ctx context.Context, user *uuid.UUID, from time.Time) (map[string]float64, error) {
	rows, err := r.pool.Query(ctx, `SELECT kind, COALESCE(sum(cost), 0)::float8 FROM usage_events
		WHERE created_at >= $2 AND ($1::uuid IS NULL OR user_id = $1) GROUP BY kind`, user, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{types.UsageChat: 0, types.UsageSheet: 0, types.UsageParse: 0}
	for rows.Next() {
		var k string
		var v float64
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// DocumentPayer returns who pays for processing a document: its uploader,
// and its case (§8.5).
func (r *UsageRepo) DocumentPayer(ctx context.Context, doc uuid.UUID) (user, caseID *uuid.UUID) {
	_ = r.pool.QueryRow(ctx, `SELECT created_by, case_id FROM documents WHERE id = $1`, doc).Scan(&user, &caseID)
	return user, caseID
}
