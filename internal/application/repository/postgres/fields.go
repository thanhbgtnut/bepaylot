package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thanhenti/bepaylot/internal/types"
)

// FieldsRepo persists Extracted Fields and their evidence (§6.9.2, §6.9.3).
type FieldsRepo struct{ pool *pgxpool.Pool }

// FieldWrite is one field to store. Evidence is already resolved and checked
// against the case by the caller.
type FieldWrite struct {
	CaseID       uuid.UUID
	DocumentID   uuid.UUID
	Key          string
	Ord          int
	Value        any
	ValueType    string
	ValueText    string
	ValueMatched bool
	Confidence   float64
	Status       string // proposed | confirmed
	Source       string // agent | user
	SessionID    *uuid.UUID
	CreatedBy    *uuid.UUID
	Note         string
	Evidence     []types.Evidence
}

const fieldCols = `f.id, f.case_id, f.document_id, f.key, f.ord, f.value, f.value_type, f.value_text, f.value_matched,
	f.confidence, f.status, f.source, f.supersedes, f.note, f.created_at`

func scanField(row pgx.Row) (types.Field, error) {
	var f types.Field
	var raw []byte
	var conf float32
	err := row.Scan(&f.ID, &f.CaseID, &f.DocumentID, &f.Key, &f.Ord, &raw, &f.ValueType, &f.ValueText, &f.ValueMatched,
		&conf, &f.Status, &f.Source, &f.Supersedes, &f.Note, &f.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return f, ErrNotFound
	}
	if err != nil {
		return f, err
	}
	f.Confidence = float64(conf)
	_ = json.Unmarshal(raw, &f.Value)
	return f, nil
}

// Save writes a field (§6.9.3). An agent field supersedes the proposed field
// of the same (document, key, ord); a user field is confirmed and supersedes
// both the proposed and the confirmed one. Rows of the slot are locked so two
// writers cannot both win.
func (r *FieldsRepo) Save(ctx context.Context, w FieldWrite) (types.Field, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return types.Field{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	f, err := saveField(ctx, tx, w)
	if err != nil {
		return types.Field{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return types.Field{}, err
	}
	return f, nil
}

func saveField(ctx context.Context, tx pgx.Tx, w FieldWrite) (types.Field, error) {
	if _, err := tx.Exec(ctx, `SELECT id FROM extracted_fields WHERE document_id = $1 AND key = $2 AND ord = $3 FOR UPDATE`,
		w.DocumentID, w.Key, w.Ord); err != nil {
		return types.Field{}, fmt.Errorf("fields.Save lock: %w", err)
	}
	replaced := []string{types.FieldProposed}
	if w.Status == types.FieldConfirmed {
		replaced = append(replaced, types.FieldConfirmed)
	}
	var prev *uuid.UUID
	// The field the new one replaces: the confirmed one first, else the proposal.
	_ = tx.QueryRow(ctx, `SELECT id FROM extracted_fields WHERE document_id = $1 AND key = $2 AND ord = $3 AND status = ANY($4)
		ORDER BY (status = 'confirmed') DESC, created_at DESC LIMIT 1`, w.DocumentID, w.Key, w.Ord, replaced).Scan(&prev)
	if _, err := tx.Exec(ctx, `UPDATE extracted_fields SET status = 'superseded', updated_at = now()
		WHERE document_id = $1 AND key = $2 AND ord = $3 AND status = ANY($4)`, w.DocumentID, w.Key, w.Ord, replaced); err != nil {
		return types.Field{}, fmt.Errorf("fields.Save supersede: %w", err)
	}
	val, err := cleanJSON(w.Value)
	if err != nil {
		return types.Field{}, err
	}
	var reviewer any
	if w.Status == types.FieldConfirmed {
		reviewer = w.CreatedBy
	}
	f, err := scanField(tx.QueryRow(ctx, `
		INSERT INTO extracted_fields AS f (case_id, document_id, gen, key, ord, value, value_type, value_text, value_matched,
			confidence, status, source, supersedes, session_id, created_by, reviewed_by, reviewed_at, note)
		VALUES ($1, $2, (SELECT gen FROM documents WHERE id = $2), $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
			$15, CASE WHEN $15::uuid IS NULL THEN NULL ELSE now() END, $16)
		RETURNING `+fieldCols,
		w.CaseID, w.DocumentID, w.Key, w.Ord, val, w.ValueType, cleanText(w.ValueText), w.ValueMatched, w.Confidence,
		w.Status, w.Source, prev, w.SessionID, w.CreatedBy, reviewer, cleanText(w.Note)))
	if err != nil {
		return types.Field{}, fmt.Errorf("fields.Save insert: %w", err)
	}
	for i, e := range w.Evidence {
		var bbox any
		if len(e.BBox) == 4 {
			bbox = e.BBox
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO evidence_spans (owner_type, owner_id, n, document_id, gen, page_no, anchor, line_from, line_to, bbox, quote, citation_id)
			VALUES ('field', $1, $2, $3, (SELECT gen FROM documents WHERE id = $3), $4, $5, $6, $7, $8, $9, $10)`,
			f.ID, i+1, e.DocumentID, e.PageNo, anchorOf(e), nullInt(e.LineFrom), nullInt(e.LineTo), bbox, cleanText(e.Quote), e.CitationID); err != nil {
			return types.Field{}, fmt.Errorf("fields.Save evidence: %w", err)
		}
	}
	f.Evidence = w.Evidence
	return f, nil
}

func anchorOf(e types.Evidence) string {
	if e.LineTo > 0 || e.LineFrom > 0 {
		return "lines"
	}
	return "page"
}

func nullInt(n int) any {
	if n <= 0 {
		return nil
	}
	return n
}

// Get returns one field with its evidence.
func (r *FieldsRepo) Get(ctx context.Context, id uuid.UUID) (types.Field, error) {
	f, err := scanField(r.pool.QueryRow(ctx, `SELECT `+fieldCols+` FROM extracted_fields f WHERE f.id = $1`, id))
	if err != nil {
		return f, err
	}
	ev, err := r.evidence(ctx, []uuid.UUID{f.ID})
	if err != nil {
		return f, err
	}
	f.Evidence = ev[f.ID]
	return f, nil
}

// GetMany returns fields by id, with evidence.
func (r *FieldsRepo) GetMany(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]types.Field, error) {
	out := map[uuid.UUID]types.Field{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT `+fieldCols+` FROM extracted_fields f WHERE f.id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		f, err := scanField(rows)
		if err != nil {
			return nil, err
		}
		out[f.ID] = f
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ev, err := r.evidence(ctx, ids)
	if err != nil {
		return nil, err
	}
	for id, f := range out {
		f.Evidence = ev[id]
		out[id] = f
	}
	return out, nil
}

// ListByCase returns the confirmed and proposed fields of a case (optionally
// one document or key), newest first.
func (r *FieldsRepo) ListByCase(ctx context.Context, caseID uuid.UUID, doc *uuid.UUID, key string) ([]types.Field, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+fieldCols+` FROM extracted_fields f
		JOIN documents d ON d.id = f.document_id AND d.case_id = $1 AND d.deleted_at IS NULL
		WHERE f.case_id = $1 AND f.status IN ('proposed','confirmed')
		  AND ($2::uuid IS NULL OR f.document_id = $2) AND ($3 = '' OR f.key = $3)
		ORDER BY f.key, f.ord, (f.status = 'confirmed') DESC, f.created_at DESC`, caseID, doc, key)
	if err != nil {
		return nil, fmt.Errorf("fields.ListByCase: %w", err)
	}
	defer rows.Close()
	var out []types.Field
	var ids []uuid.UUID
	for rows.Next() {
		f, err := scanField(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
		ids = append(ids, f.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ev, err := r.evidence(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Evidence = ev[out[i].ID]
	}
	return out, nil
}

// evidence loads the evidence of fields, in order.
func (r *FieldsRepo) evidence(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]types.Evidence, error) {
	out := map[uuid.UUID][]types.Evidence{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT e.owner_id, e.document_id, d.file_name, e.page_no, COALESCE(e.line_from, 0), COALESCE(e.line_to, 0),
		       COALESCE(e.bbox, '{}')::float8[], e.quote, e.citation_id, e.status
		FROM evidence_spans e JOIN documents d ON d.id = e.document_id
		WHERE e.owner_type = 'field' AND e.owner_id = ANY($1) ORDER BY e.owner_id, e.n`, ids)
	if err != nil {
		return nil, fmt.Errorf("fields.evidence: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var owner uuid.UUID
		var e types.Evidence
		if err := rows.Scan(&owner, &e.DocumentID, &e.FileName, &e.PageNo, &e.LineFrom, &e.LineTo, &e.BBox, &e.Quote, &e.CitationID, &e.Status); err != nil {
			return nil, err
		}
		out[owner] = append(out[owner], e)
	}
	return out, rows.Err()
}
