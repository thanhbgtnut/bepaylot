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

const sheetCols = `s.id, s.case_id, s.name, s.status, s.filled, s.total,
	s.rows, s.error, s.created_by, s.created_at, s.updated_at, s.tables, s.columns`

func scanSheet(row pgx.Row) (types.Sheet, error) {
	var s types.Sheet
	var rows, cols []byte
	err := row.Scan(&s.ID, &s.CaseID, &s.Name, &s.Status, &s.Filled, &s.Total,
		&rows, &s.Error, &s.CreatedBy, &s.CreatedAt, &s.UpdatedAt, &s.Tables, &cols)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrNotFound
	}
	if err != nil {
		return s, err
	}
	s.Rows = decodeRows(rows)
	if len(s.Tables) == 0 {
		s.Tables = []string{""} // a 0.16 sheet: one sub-table without label
	}
	// The sub-tables of the sheet, in order; a sheet made from a template
	// carries every table of the template and keeps the chosen ones.
	var all []types.SheetTable
	_ = json.Unmarshal(cols, &all)
	want := map[string]bool{}
	for _, l := range s.Tables {
		want[l] = true
	}
	s.Columns = []types.SheetTable{}
	for _, t := range all {
		if want[t.Label] {
			s.Columns = append(s.Columns, t)
		}
	}
	return s, nil
}

// decodeRows reads case_sheets.rows; a 0.16 sheet (one row per field) becomes
// one row of a sub-table without label whose cells are those fields.
func decodeRows(raw []byte) []types.SheetRow {
	var probe []map[string]json.RawMessage
	_ = json.Unmarshal(raw, &probe)
	if len(probe) > 0 {
		if _, legacy := probe[0]["cells"]; !legacy {
			var old []struct {
				Key         string     `json:"key"`
				FieldID     *uuid.UUID `json:"field_id"`
				AIFieldID   *uuid.UUID `json:"ai_field_id"`
				AIValueText string     `json:"ai_value_text"`
				Note        string     `json:"note"`
			}
			_ = json.Unmarshal(raw, &old)
			row := types.SheetRow{Cells: map[string]types.SheetCell{}}
			for _, o := range old {
				row.Cells[o.Key] = types.SheetCell{FieldID: o.FieldID, AIFieldID: o.AIFieldID, AIValueText: o.AIValueText, Note: o.Note}
			}
			return []types.SheetRow{row}
		}
	}
	var out []types.SheetRow
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = []types.SheetRow{}
	}
	return out
}

// Create inserts a pending sheet.
func (r *SheetsRepo) Create(ctx context.Context, s types.Sheet) (types.Sheet, error) {
	var id uuid.UUID
	if s.Tables == nil {
		s.Tables = []string{}
	}
	cols, err := cleanJSON(s.Columns)
	if err != nil {
		return types.Sheet{}, err
	}
	err = r.pool.QueryRow(ctx, `INSERT INTO case_sheets (case_id, name, total, created_by, tables, columns)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, s.CaseID, cleanText(s.Name), s.Total, s.CreatedBy, s.Tables, cols).Scan(&id)
	if err != nil {
		return types.Sheet{}, fmt.Errorf("sheets.Create: %w", err)
	}
	return r.Get(ctx, id)
}

// Get returns a sheet.
func (r *SheetsRepo) Get(ctx context.Context, id uuid.UUID) (types.Sheet, error) {
	return scanSheet(r.pool.QueryRow(ctx, `SELECT `+sheetCols+` FROM case_sheets s WHERE s.id = $1`, id))
}

// ListByCase returns the sheets of a case, newest first.
func (r *SheetsRepo) ListByCase(ctx context.Context, caseID uuid.UUID) ([]types.Sheet, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+sheetCols+` FROM case_sheets s WHERE s.case_id = $1 ORDER BY s.created_at DESC`, caseID)
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

// SetState records the progress of a sheet; rows are written when not nil,
// total when > 0.
func (r *SheetsRepo) SetState(ctx context.Context, id uuid.UUID, status string, filled int, rows []types.SheetRow, errMsg string) error {
	return r.SetProgress(ctx, id, status, filled, 0, rows, errMsg)
}

// SetProgress is SetState that also sets the number of cells (total > 0).
func (r *SheetsRepo) SetProgress(ctx context.Context, id uuid.UUID, status string, filled, total int, rows []types.SheetRow, errMsg string) error {
	var raw any
	if rows != nil {
		b, err := cleanJSON(rows)
		if err != nil {
			return err
		}
		raw = b
	}
	_, err := r.pool.Exec(ctx, `UPDATE case_sheets SET status = $2, filled = $3, rows = COALESCE($4::jsonb, rows),
		error = $5, total = CASE WHEN $6 > 0 THEN $6 ELSE total END, updated_at = now() WHERE id = $1`,
		id, status, filled, raw, cleanText(errMsg), total)
	return err
}

// Delete removes one sheet; its corrections go with it (cascade). The
// extracted fields belong to the case and stay.
func (r *SheetsRepo) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM case_sheets WHERE id = $1`, id)
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
	Row        int    // index in Sheet.Rows
	Key        string // column key of the cell (Field.Key carries the sub-table's label on a file row)
	Field      FieldWrite
	Correction *types.Correction
}

// SaveEdits writes the user's fields, records corrections (a value set back
// to the AI's reverts the earlier ones of that cell) and stores the new field
// ids in the sheet cells, in one transaction.
func (r *SheetsRepo) SaveEdits(ctx context.Context, sheetID uuid.UUID, edits []SheetEdit) (types.Sheet, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return types.Sheet{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	sh, err := scanSheet(tx.QueryRow(ctx, `SELECT `+sheetCols+` FROM case_sheets s WHERE s.id = $1 FOR UPDATE OF s`, sheetID))
	if err != nil {
		return types.Sheet{}, err
	}
	for _, e := range edits {
		f, err := saveField(ctx, tx, e.Field)
		if err != nil {
			return types.Sheet{}, err
		}
		key, label := e.Key, ""
		if e.Row >= 0 && e.Row < len(sh.Rows) {
			row := &sh.Rows[e.Row]
			label = row.Table
			if row.Cells == nil {
				row.Cells = map[string]types.SheetCell{}
			}
			cell := row.Cells[key]
			id := f.ID
			cell.FieldID = &id
			row.Cells[key] = cell
		}
		if _, err := tx.Exec(ctx, `UPDATE field_corrections SET reverted = true WHERE sheet_id = $1 AND label = $2 AND key = $3
			AND segment_id IS NOT DISTINCT FROM $4::uuid AND NOT reverted`, sheetID, label, key, e.Field.SegmentID); err != nil {
			return types.Sheet{}, err
		}
		if c := e.Correction; c != nil {
			id := f.ID
			if _, err := tx.Exec(ctx, `INSERT INTO field_corrections (sheet_id, key, ai_field_id, user_field_id,
				ai_value_text, user_value_text, origin, user_id, label, segment_id) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
				sheetID, key, c.AIFieldID, &id, cleanText(c.AIValueText), cleanText(c.UserValueText), c.Origin, c.UserID,
				label, e.Field.SegmentID); err != nil {
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
