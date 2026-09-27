package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thanhenti/bepaylot/internal/types"
)

// CasesRepo persists cases (§6.2).
type CasesRepo struct{ pool *pgxpool.Pool }

const caseCols = `c.id, c.kb_id, c.code, c.case_type, c.title, c.status, c.metadata, c.created_by,
	c.wiki_schema, c.wiki_status, c.wiki_version, c.wiki_built_at, c.wiki_docs_covered, c.created_at, c.updated_at, c.deleted_at`

func scanCase(row pgx.Row) (types.Case, error) {
	var c types.Case
	err := row.Scan(&c.ID, &c.KBID, &c.Code, &c.CaseType, &c.Title, &c.Status, &metaScanner{&c.Metadata}, &c.CreatedBy,
		&c.WikiSchema, &c.WikiStatus, &c.WikiVersion, &c.WikiBuiltAt, &c.WikiDocsCovered, &c.CreatedAt, &c.UpdatedAt, &c.DeletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// Insert creates a case, or returns the live case with the same code in the
// KB (created=false). Two concurrent uploads with one code get one case.
func (r *CasesRepo) Insert(ctx context.Context, c types.Case) (types.Case, bool, error) {
	meta, err := cleanJSON(nonNilMap(c.Metadata))
	if err != nil {
		return c, false, err
	}
	out, err := scanCase(r.pool.QueryRow(ctx, `
		INSERT INTO cases AS c (kb_id, code, case_type, title, metadata, created_by, wiki_schema, wiki_status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (kb_id, code) WHERE deleted_at IS NULL DO NOTHING
		RETURNING `+caseCols,
		c.KBID, cleanText(c.Code), c.CaseType, cleanText(c.Title), meta, c.CreatedBy, c.WikiSchema, c.WikiStatus))
	if errors.Is(err, ErrNotFound) {
		existing, gerr := r.ByCode(ctx, c.KBID, c.Code)
		return existing, false, gerr
	}
	return out, err == nil, err
}

// Get returns a live case (no owner check).
func (r *CasesRepo) Get(ctx context.Context, id uuid.UUID) (types.Case, error) {
	return scanCase(r.pool.QueryRow(ctx, `SELECT `+caseCols+` FROM cases c WHERE c.id = $1 AND c.deleted_at IS NULL`, id))
}

// GetAny returns a case even when soft-deleted (case:delete).
func (r *CasesRepo) GetAny(ctx context.Context, id uuid.UUID) (types.Case, error) {
	return scanCase(r.pool.QueryRow(ctx, `SELECT `+caseCols+` FROM cases c WHERE c.id = $1`, id))
}

// GetOwned returns a live case of a live KB the owner owns.
func (r *CasesRepo) GetOwned(ctx context.Context, id, owner uuid.UUID) (types.Case, error) {
	return scanCase(r.pool.QueryRow(ctx, `SELECT `+caseCols+` FROM cases c JOIN knowledge_bases kb ON kb.id = c.kb_id
		WHERE c.id = $1 AND kb.owner_id = $2 AND c.deleted_at IS NULL AND kb.deleted_at IS NULL`, id, owner))
}

// ByCode returns the live case with this (already normalized) code.
func (r *CasesRepo) ByCode(ctx context.Context, kb uuid.UUID, code string) (types.Case, error) {
	return scanCase(r.pool.QueryRow(ctx, `SELECT `+caseCols+` FROM cases c WHERE c.kb_id = $1 AND c.code = $2 AND c.deleted_at IS NULL`, kb, code))
}

// CaseFilter selects cases for listing.
type CaseFilter struct {
	OwnerID  uuid.UUID
	KBID     uuid.UUID
	IDs      []uuid.UUID
	Query    string // part of the code or title, accent-insensitive
	CaseType string
	Status   string
	Metadata types.MetadataFilter
	Schema   *types.MetadataSchema
	Limit    int
	Before   *time.Time
}

// List returns cases matching the filter, newest first, with per-status
// document counts.
func (r *CasesRepo) List(ctx context.Context, f CaseFilter) ([]types.Case, error) {
	var a sqlArgs
	conds := []string{"c.deleted_at IS NULL", "kb.deleted_at IS NULL"}
	if f.OwnerID != uuid.Nil {
		conds = append(conds, "kb.owner_id = "+a.add(f.OwnerID))
	}
	if f.KBID != uuid.Nil {
		conds = append(conds, "c.kb_id = "+a.add(f.KBID))
	}
	if len(f.IDs) > 0 {
		conds = append(conds, "c.id = ANY("+a.add(f.IDs)+")")
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		p := a.add(q)
		conds = append(conds, fmt.Sprintf("(c.code ILIKE '%%' || %s || '%%' OR unaccent_vi(c.title) LIKE '%%' || unaccent_vi(%s) || '%%')", p, p))
	}
	if f.CaseType != "" {
		conds = append(conds, "c.case_type = "+a.add(f.CaseType))
	}
	if f.Status != "" {
		conds = append(conds, "c.status = "+a.add(f.Status))
	}
	if f.Before != nil {
		conds = append(conds, "c.created_at < "+a.add(*f.Before))
	}
	mf, err := metaFilterSQL("c.metadata", f.Metadata, f.Schema, &a)
	if err != nil {
		return nil, err
	}
	if mf != "" {
		conds = append(conds, mf)
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `SELECT `+caseCols+` FROM cases c JOIN knowledge_bases kb ON kb.id = c.kb_id
		WHERE `+strings.Join(conds, " AND ")+fmt.Sprintf(` ORDER BY c.created_at DESC LIMIT %d`, limit), a.vals...)
	if err != nil {
		return nil, err
	}
	var out []types.Case
	for rows.Next() {
		c, err := scanCase(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, r.attachCounts(ctx, out)
}

func (r *CasesRepo) attachCounts(ctx context.Context, cs []types.Case) error {
	if len(cs) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(cs))
	idx := map[uuid.UUID]int{}
	for i, c := range cs {
		ids[i], idx[c.ID] = c.ID, i
		cs[i].Documents = map[string]int{}
	}
	rows, err := r.pool.Query(ctx, `SELECT case_id, status, count(*) FROM documents
		WHERE case_id = ANY($1) AND deleted_at IS NULL GROUP BY 1, 2`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var st string
		var n int
		if err := rows.Scan(&id, &st, &n); err != nil {
			return err
		}
		cs[idx[id]].Documents[st] = n
	}
	return rows.Err()
}

// WithCounts fills the per-status document counts of one case.
func (r *CasesRepo) WithCounts(ctx context.Context, c types.Case) (types.Case, error) {
	one := []types.Case{c}
	err := r.attachCounts(ctx, one)
	return one[0], err
}

// CasePatch holds optional updates; code, case type and KB never change.
type CasePatch struct {
	Title    *string
	Status   *string
	Metadata map[string]any
}

// Update applies a patch to a live case.
func (r *CasesRepo) Update(ctx context.Context, id uuid.UUID, p CasePatch) (types.Case, error) {
	sets := []string{"updated_at = now()"}
	var a sqlArgs
	a.add(id)
	if p.Title != nil {
		sets = append(sets, "title = "+a.add(cleanText(*p.Title)))
	}
	if p.Status != nil {
		sets = append(sets, "status = "+a.add(*p.Status))
	}
	if p.Metadata != nil {
		b, err := cleanJSON(p.Metadata)
		if err != nil {
			return types.Case{}, err
		}
		sets = append(sets, "metadata = "+a.add(b))
	}
	return scanCase(r.pool.QueryRow(ctx, `UPDATE cases AS c SET `+strings.Join(sets, ", ")+`
		WHERE c.id = $1 AND c.deleted_at IS NULL RETURNING `+caseCols, a.vals...))
}

// SoftDelete marks a case deleted.
func (r *CasesRepo) SoftDelete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `UPDATE cases SET deleted_at = now(), updated_at = now() WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SoftDeleteByKB soft-deletes every live case of a KB and returns their ids.
func (r *CasesRepo) SoftDeleteByKB(ctx context.Context, kb uuid.UUID) ([]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx, `UPDATE cases SET deleted_at = now(), updated_at = now() WHERE kb_id = $1 AND deleted_at IS NULL RETURNING id`, kb)
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

// Deleted returns soft-deleted cases that still own documents or wiki rows,
// so housekeeping can re-drive case:delete.
func (r *CasesRepo) Deleted(ctx context.Context, limit int) ([]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx, `SELECT c.id FROM cases c WHERE c.deleted_at IS NOT NULL
		AND (EXISTS (SELECT 1 FROM documents d WHERE d.case_id = c.id)
		  OR EXISTS (SELECT 1 FROM wiki_pages w WHERE w.case_id = c.id)
		  OR EXISTS (SELECT 1 FROM wiki_log l WHERE l.case_id = c.id))
		LIMIT $1`, limit)
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

// SetWikiStatus records the wiki state of a case; covered < 0 keeps it.
func (r *CasesRepo) SetWikiStatus(ctx context.Context, id uuid.UUID, status string, covered int) error {
	_, err := r.pool.Exec(ctx, `UPDATE cases SET wiki_status = $2,
		wiki_docs_covered = CASE WHEN $3 >= 0 THEN $3 ELSE wiki_docs_covered END, updated_at = now() WHERE id = $1`, id, status, covered)
	return err
}

// WithWiki lists live cases whose wiki is enabled and not empty, for the
// periodic lint.
func (r *CasesRepo) WithWiki(ctx context.Context, limit int) ([]types.Case, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+caseCols+` FROM cases c WHERE c.deleted_at IS NULL AND c.wiki_version > 0
		ORDER BY c.updated_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.Case
	for rows.Next() {
		c, err := scanCase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
