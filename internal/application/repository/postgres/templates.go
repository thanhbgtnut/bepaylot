package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thanhenti/bepaylot/internal/types"
)

// TemplatesRepo persists prompt templates and their immutable versions (§8.4).
type TemplatesRepo struct{ pool *pgxpool.Pool }

// ErrSlugTaken is returned when a template slug is in use.
var ErrSlugTaken = errors.New("template slug already exists")

// tplCols reads a template with one of its versions (v); the version is the
// published one unless the query joins another.
const tplCols = `t.id, t.kind, t.slug, t.name, t.description, COALESCE(t.case_type, ''), t.status,
	COALESCE(t.current_version, 0), (SELECT max(version) FROM prompt_template_versions WHERE template_id = t.id),
	COALESCE(v.version, 0), COALESCE(v.body, ''), COALESCE(v.fields, '[]'), COALESCE(v.tables, '[]'), t.created_by, COALESCE(u.name, u.email, ''), t.created_at, t.updated_at`

func scanTemplate(row pgx.Row) (types.PromptTemplate, error) {
	var t types.PromptTemplate
	var fields, tables []byte
	err := row.Scan(&t.ID, &t.Kind, &t.Slug, &t.Name, &t.Description, &t.CaseType, &t.Status, &t.CurrentVersion, &t.LatestVersion,
		&t.Version, &t.Body, &fields, &tables, &t.CreatedBy, &t.CreatedByName, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	if err != nil {
		return t, err
	}
	_ = json.Unmarshal(fields, &t.Fields)
	_ = json.Unmarshal(tables, &t.Tables)
	if len(t.Tables) == 0 && len(t.Fields) > 0 { // a 0.16 template: one sub-table, one row per file
		t.Tables = []types.SheetTable{{Title: "Tổng hợp", Fields: t.Fields}}
	}
	return t, nil
}

// List returns the templates of a kind ("" = all) usable for caseType ("" =
// any); drafts only when withDrafts. Each carries its published version, or
// its latest one when it has none yet.
func (r *TemplatesRepo) List(ctx context.Context, kind, caseType string, withDrafts bool) ([]types.PromptTemplate, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+tplCols+` FROM prompt_templates t
		LEFT JOIN prompt_template_versions v ON v.template_id = t.id
		  AND v.version = COALESCE(t.current_version, (SELECT max(version) FROM prompt_template_versions WHERE template_id = t.id))
		LEFT JOIN users u ON u.id = t.created_by
		WHERE ($1 = '' OR t.kind = $1) AND ($2 = '' OR t.case_type IS NULL OR t.case_type = $2)
		  AND (t.status = 'published' OR ($3 AND t.status = 'draft'))
		ORDER BY t.kind, t.name`, kind, caseType, withDrafts)
	if err != nil {
		return nil, fmt.Errorf("templates.List: %w", err)
	}
	defer rows.Close()
	out := []types.PromptTemplate{}
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Get returns a template at a version (0 = published, else latest).
func (r *TemplatesRepo) Get(ctx context.Context, id uuid.UUID, version int) (types.PromptTemplate, error) {
	return scanTemplate(r.pool.QueryRow(ctx, `SELECT `+tplCols+` FROM prompt_templates t
		LEFT JOIN prompt_template_versions v ON v.template_id = t.id AND v.version = CASE WHEN $2 > 0 THEN $2
		  ELSE COALESCE(t.current_version, (SELECT max(version) FROM prompt_template_versions WHERE template_id = t.id)) END
		LEFT JOIN users u ON u.id = t.created_by
		WHERE t.id = $1`, id, version))
}

// TemplateDraft creates a template or a new version of one.
type TemplateDraft struct {
	Kind, Slug, Name, Description, CaseType string
	Body                                    string
	Fields                                  []types.SheetField
	Tables                                  []types.SheetTable
	By                                      uuid.UUID
}

// Create inserts a draft template with version 1.
func (r *TemplatesRepo) Create(ctx context.Context, d TemplateDraft) (types.PromptTemplate, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return types.PromptTemplate{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var id uuid.UUID
	var caseType any
	if d.CaseType != "" {
		caseType = d.CaseType
	}
	err = tx.QueryRow(ctx, `INSERT INTO prompt_templates (kind, slug, name, description, case_type, created_by)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, d.Kind, d.Slug, cleanText(d.Name), cleanText(d.Description), caseType, d.By).Scan(&id)
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return types.PromptTemplate{}, ErrSlugTaken
	}
	if err != nil {
		return types.PromptTemplate{}, fmt.Errorf("templates.Create: %w", err)
	}
	if err := insertVersion(ctx, tx, id, 1, d); err != nil {
		return types.PromptTemplate{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return types.PromptTemplate{}, err
	}
	return r.Get(ctx, id, 1)
}

func insertVersion(ctx context.Context, tx pgx.Tx, id uuid.UUID, version int, d TemplateDraft) error {
	if len(d.Tables) == 0 && len(d.Fields) > 0 {
		d.Tables = []types.SheetTable{{Title: "Tổng hợp", Fields: d.Fields}}
	}
	if d.Tables == nil {
		d.Tables = []types.SheetTable{}
	}
	tables, err := cleanJSON(d.Tables)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO prompt_template_versions (template_id, version, body, tables, created_by) VALUES ($1, $2, $3, $4, $5)`,
		id, version, cleanText(d.Body), tables, d.By)
	return err
}

// AddVersion stores a new (draft) version; the published one is unchanged.
// Name and description, when given, update the template itself.
func (r *TemplatesRepo) AddVersion(ctx context.Context, id uuid.UUID, d TemplateDraft) (types.PromptTemplate, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return types.PromptTemplate{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var next int
	err = tx.QueryRow(ctx, `SELECT COALESCE(max(v.version), 0) + 1 FROM prompt_templates t
		LEFT JOIN prompt_template_versions v ON v.template_id = t.id WHERE t.id = $1 GROUP BY t.id FOR UPDATE OF t`, id).Scan(&next)
	if errors.Is(err, pgx.ErrNoRows) {
		return types.PromptTemplate{}, ErrNotFound
	}
	if err != nil {
		return types.PromptTemplate{}, fmt.Errorf("templates.AddVersion: %w", err)
	}
	if err := insertVersion(ctx, tx, id, next, d); err != nil {
		return types.PromptTemplate{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE prompt_templates SET name = COALESCE(NULLIF($2, ''), name),
		description = COALESCE(NULLIF($3, ''), description), updated_at = now() WHERE id = $1`, id, cleanText(d.Name), cleanText(d.Description)); err != nil {
		return types.PromptTemplate{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return types.PromptTemplate{}, err
	}
	return r.Get(ctx, id, next)
}

// Publish makes a version the published one.
func (r *TemplatesRepo) Publish(ctx context.Context, id uuid.UUID, version int) (types.PromptTemplate, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE prompt_templates SET current_version = $2, status = 'published', updated_at = now()
		WHERE id = $1 AND EXISTS (SELECT 1 FROM prompt_template_versions WHERE template_id = $1 AND version = $2)`, id, version)
	if err != nil {
		return types.PromptTemplate{}, fmt.Errorf("templates.Publish: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return types.PromptTemplate{}, ErrNotFound
	}
	return r.Get(ctx, id, 0)
}

// PublishedBody returns the body of the published version of a template, or
// "" when it has none (§8.4).
func (r *TemplatesRepo) PublishedBody(ctx context.Context, id uuid.UUID) string {
	var body string
	_ = r.pool.QueryRow(ctx, `SELECT v.body FROM prompt_templates t
		JOIN prompt_template_versions v ON v.template_id = t.id AND v.version = t.current_version
		WHERE t.id = $1 AND t.status = 'published'`, id).Scan(&body)
	return body
}
