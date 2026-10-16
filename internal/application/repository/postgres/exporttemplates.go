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

// ExportTemplatesRepo persists the Excel forms of a KB (§6.9.8).
type ExportTemplatesRepo struct{ pool *pgxpool.Pool }

const exportTplCols = `id, kb_id, name, file_name, object_key, layout, mapping, created_by, created_at, updated_at`

func scanExportTemplate(row pgx.Row) (types.ExportTemplate, error) {
	var t types.ExportTemplate
	var layout, mapping []byte
	err := row.Scan(&t.ID, &t.KBID, &t.Name, &t.FileName, &t.ObjectKey, &layout, &mapping, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	if err != nil {
		return t, err
	}
	_ = json.Unmarshal(layout, &t.Layout)
	_ = json.Unmarshal(mapping, &t.Mapping)
	return t, nil
}

// Create inserts a form.
func (r *ExportTemplatesRepo) Create(ctx context.Context, t types.ExportTemplate) (types.ExportTemplate, error) {
	layout, err := cleanJSON(t.Layout)
	if err != nil {
		return t, err
	}
	mapping, err := cleanJSON(t.Mapping)
	if err != nil {
		return t, err
	}
	id := t.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	return scanExportTemplate(r.pool.QueryRow(ctx, `INSERT INTO export_templates (id, kb_id, name, file_name, object_key, layout, mapping, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING `+exportTplCols,
		id, t.KBID, cleanText(t.Name), cleanText(t.FileName), t.ObjectKey, layout, mapping, t.CreatedBy))
}

// Get returns a form.
func (r *ExportTemplatesRepo) Get(ctx context.Context, id uuid.UUID) (types.ExportTemplate, error) {
	return scanExportTemplate(r.pool.QueryRow(ctx, `SELECT `+exportTplCols+` FROM export_templates WHERE id = $1`, id))
}

// ListByKB returns the forms of a KB, oldest first (the order of the Studio buttons).
func (r *ExportTemplatesRepo) ListByKB(ctx context.Context, kbID uuid.UUID) ([]types.ExportTemplate, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+exportTplCols+` FROM export_templates WHERE kb_id = $1 ORDER BY created_at`, kbID)
	if err != nil {
		return nil, fmt.Errorf("export_templates.ListByKB: %w", err)
	}
	defer rows.Close()
	out := []types.ExportTemplate{}
	for rows.Next() {
		t, err := scanExportTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Update sets the name and mapping of a form.
func (r *ExportTemplatesRepo) Update(ctx context.Context, id uuid.UUID, name string, m types.ExportMapping) (types.ExportTemplate, error) {
	mapping, err := cleanJSON(m)
	if err != nil {
		return types.ExportTemplate{}, err
	}
	return scanExportTemplate(r.pool.QueryRow(ctx, `UPDATE export_templates SET name = $2, mapping = $3, updated_at = now()
		WHERE id = $1 RETURNING `+exportTplCols, id, cleanText(name), mapping))
}

// Delete removes a form; sheets built for it keep their rows.
func (r *ExportTemplatesRepo) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM export_templates WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
