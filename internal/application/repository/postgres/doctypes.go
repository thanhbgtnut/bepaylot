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

// DocTypesRepo persists the catalogue of document types of each owner, the
// versions of their AI instructions, their sample documents and the test
// runs on them (§6.9.10). Knowledge bases keep only the codes they use; their
// doc types are resolved from the catalogue when read.
type DocTypesRepo struct{ pool *pgxpool.Pool }

const docTypeCols = `code, title, description, fields, prompt_version, updated_at`

func scanDocType(row pgx.Row) (types.CatalogDocType, error) {
	var t types.CatalogDocType
	var fields []byte
	err := row.Scan(&t.Name, &t.Title, &t.Description, &fields, &t.PromptVersion, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	if err != nil {
		return t, err
	}
	_ = json.Unmarshal(fields, &t.Fields)
	return t, nil
}

// List returns an owner's catalogue, by title.
func (r *DocTypesRepo) List(ctx context.Context, owner uuid.UUID) ([]types.CatalogDocType, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+docTypeCols+` FROM doc_types WHERE owner_id = $1 ORDER BY lower(title), code`, owner)
	if err != nil {
		return nil, fmt.Errorf("doc_types: %w", err)
	}
	defer rows.Close()
	out := []types.CatalogDocType{}
	for rows.Next() {
		t, err := scanDocType(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Get returns one type of an owner's catalogue.
func (r *DocTypesRepo) Get(ctx context.Context, owner uuid.UUID, code string) (types.CatalogDocType, error) {
	return scanDocType(r.pool.QueryRow(ctx, `SELECT `+docTypeCols+` FROM doc_types WHERE owner_id = $1 AND code = $2`, owner, code))
}

// dbExec and dbQuery are a pool or a transaction.
type dbExec interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}
type dbQuery interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Save creates a type of the catalogue or, when it exists, updates its title
// and description (kept when empty) and, with fields not nil, its fields and
// those of the instructions in use. A new type gets version 1 of its
// instructions.
func (r *DocTypesRepo) Save(ctx context.Context, owner uuid.UUID, t types.DocType, fields bool) error {
	return saveDocType(ctx, r.pool, owner, t, fields)
}

func saveDocType(ctx context.Context, db dbExec, owner uuid.UUID, t types.DocType, withFields bool) error {
	if t.Fields == nil {
		t.Fields = []types.SheetField{}
	}
	fields, err := cleanJSON(t.Fields)
	if err != nil {
		return err
	}
	title := t.Title
	if title == "" {
		title = t.Name
	}
	if _, err := db.Exec(ctx, `INSERT INTO doc_types (owner_id, code, title, description, fields) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (owner_id, code) DO UPDATE SET
			title = CASE WHEN $6 THEN COALESCE(NULLIF($7, ''), doc_types.title) ELSE doc_types.title END,
			description = CASE WHEN $6 AND $4 <> '' THEN $4 ELSE doc_types.description END,
			fields = CASE WHEN $8 THEN EXCLUDED.fields ELSE doc_types.fields END,
			updated_at = now()`,
		owner, t.Name, cleanText(title), cleanText(t.Description), fields, t.Title != "", cleanText(t.Title), withFields); err != nil {
		return fmt.Errorf("doc_types.Save: %w", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO doc_type_prompts (owner_id, code, version, instruction, fields, note)
		SELECT owner_id, code, 1, $3, fields, 'Bản đầu tiên' FROM doc_types WHERE owner_id = $1 AND code = $2
		ON CONFLICT DO NOTHING`, owner, t.Name, types.DefaultInstruction(title)); err != nil {
		return fmt.Errorf("doc_types.Save prompt: %w", err)
	}
	if withFields {
		if _, err := db.Exec(ctx, `UPDATE doc_type_prompts p SET fields = d.fields FROM doc_types d
			WHERE d.owner_id = $1 AND d.code = $2 AND p.owner_id = d.owner_id AND p.code = d.code AND p.version = d.prompt_version`,
			owner, t.Name); err != nil {
			return fmt.Errorf("doc_types.Save fields: %w", err)
		}
	}
	return nil
}

// Delete removes a type of the catalogue with its instructions and samples.
func (r *DocTypesRepo) Delete(ctx context.Context, owner uuid.UUID, code string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM doc_types WHERE owner_id = $1 AND code = $2`, owner, code)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// KBsUsing returns the owner's knowledge bases that use a type.
func (r *DocTypesRepo) KBsUsing(ctx context.Context, owner uuid.UUID, code string) ([]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx, `SELECT id FROM knowledge_bases WHERE owner_id = $1 AND deleted_at IS NULL
		AND jsonb_typeof(config->'doc_types') = 'array' AND config->'doc_types' @> jsonb_build_array(jsonb_build_object('name', $2::text))
		ORDER BY created_at`, owner, code)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ---- instructions

const promptCols = `code, version, instruction, fields, rules, shots, note, created_by, created_at`

func scanPrompt(row pgx.Row) (types.DocTypePrompt, error) {
	var p types.DocTypePrompt
	var fields, rules, shots []byte
	err := row.Scan(&p.Code, &p.Version, &p.Instruction, &fields, &rules, &shots, &p.Note, &p.CreatedBy, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	_ = json.Unmarshal(fields, &p.Fields)
	_ = json.Unmarshal(rules, &p.Rules)
	_ = json.Unmarshal(shots, &p.Shots)
	if p.Fields == nil {
		p.Fields = []types.SheetField{}
	}
	if p.Rules == nil {
		p.Rules = []string{}
	}
	if p.Shots == nil {
		p.Shots = []types.PromptShot{}
	}
	return p, nil
}

// Prompts returns the versions of a type's instructions, newest first.
func (r *DocTypesRepo) Prompts(ctx context.Context, owner uuid.UUID, code string) ([]types.DocTypePrompt, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+promptCols+` FROM doc_type_prompts WHERE owner_id = $1 AND code = $2 ORDER BY version DESC`, owner, code)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []types.DocTypePrompt{}
	for rows.Next() {
		p, err := scanPrompt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Prompt returns one version; version 0 is the one in use.
func (r *DocTypesRepo) Prompt(ctx context.Context, owner uuid.UUID, code string, version int) (types.DocTypePrompt, error) {
	return scanPrompt(r.pool.QueryRow(ctx, `SELECT `+promptCols+` FROM doc_type_prompts p WHERE p.owner_id = $1 AND p.code = $2
		AND p.version = CASE WHEN $3 > 0 THEN $3 ELSE (SELECT prompt_version FROM doc_types WHERE owner_id = $1 AND code = $2) END`, owner, code, version))
}

// AddPrompt stores a new version (the largest number plus one, so a deleted
// number is never reused) and returns it.
func (r *DocTypesRepo) AddPrompt(ctx context.Context, owner uuid.UUID, p types.DocTypePrompt) (types.DocTypePrompt, error) {
	if p.Fields == nil {
		p.Fields = []types.SheetField{}
	}
	if p.Rules == nil {
		p.Rules = []string{}
	}
	if p.Shots == nil {
		p.Shots = []types.PromptShot{}
	}
	fields, _ := cleanJSON(p.Fields)
	rules, _ := cleanJSON(p.Rules)
	shots, _ := cleanJSON(p.Shots)
	return scanPrompt(r.pool.QueryRow(ctx, `INSERT INTO doc_type_prompts (owner_id, code, version, instruction, fields, rules, shots, note, created_by)
		SELECT $1, $2, COALESCE(MAX(version), 0) + 1, $3, $4, $5, $6, $7, $8 FROM doc_type_prompts WHERE owner_id = $1 AND code = $2
		RETURNING `+promptCols, owner, p.Code, cleanText(p.Instruction), fields, rules, shots, cleanText(p.Note), p.CreatedBy))
}

// SetCurrent makes a version the one in use; the type's fields become its fields.
func (r *DocTypesRepo) SetCurrent(ctx context.Context, owner uuid.UUID, code string, version int) error {
	tag, err := r.pool.Exec(ctx, `UPDATE doc_types d SET prompt_version = p.version, fields = p.fields, updated_at = now()
		FROM doc_type_prompts p WHERE d.owner_id = $1 AND d.code = $2 AND p.owner_id = d.owner_id AND p.code = d.code AND p.version = $3`,
		owner, code, version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeletePrompt removes a version that is not in use.
func (r *DocTypesRepo) DeletePrompt(ctx context.Context, owner uuid.UUID, code string, version int) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM doc_type_prompts p WHERE p.owner_id = $1 AND p.code = $2 AND p.version = $3
		AND p.version <> (SELECT prompt_version FROM doc_types WHERE owner_id = $1 AND code = $2)`, owner, code, version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	_, err = r.pool.Exec(ctx, `DELETE FROM doc_type_runs r USING doc_type_samples s
		WHERE r.sample_id = s.id AND s.owner_id = $1 AND s.code = $2 AND r.version = $3`, owner, code, version)
	return err
}

// ---- samples

const sampleCols = `id, code, file_name, storage_key, mime_type, status, error, pages, truth, reviewed_at, created_at`

func scanSample(row pgx.Row) (types.DocTypeSample, error) {
	var s types.DocTypeSample
	var pages, truth []byte
	err := row.Scan(&s.ID, &s.Code, &s.FileName, &s.StorageKey, &s.MimeType, &s.Status, &s.Error, &pages, &truth, &s.ReviewedAt, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrNotFound
	}
	if err != nil {
		return s, err
	}
	_ = json.Unmarshal(pages, &s.Pages)
	if len(truth) > 0 {
		_ = json.Unmarshal(truth, &s.Truth)
	}
	if s.Pages == nil {
		s.Pages = []types.SamplePage{}
	}
	return s, nil
}

// CreateSample records an uploaded sample, being processed.
func (r *DocTypesRepo) CreateSample(ctx context.Context, owner uuid.UUID, s types.DocTypeSample, by uuid.UUID) (types.DocTypeSample, error) {
	return scanSample(r.pool.QueryRow(ctx, `INSERT INTO doc_type_samples (owner_id, code, file_name, storage_key, mime_type, created_by)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+sampleCols, owner, s.Code, cleanText(s.FileName), s.StorageKey, s.MimeType, by))
}

// Samples returns the samples of a type, oldest first.
func (r *DocTypesRepo) Samples(ctx context.Context, owner uuid.UUID, code string) ([]types.DocTypeSample, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+sampleCols+` FROM doc_type_samples WHERE owner_id = $1 AND code = $2 ORDER BY created_at`, owner, code)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []types.DocTypeSample{}
	for rows.Next() {
		s, err := scanSample(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Sample returns one sample of an owner.
func (r *DocTypesRepo) Sample(ctx context.Context, owner, id uuid.UUID) (types.DocTypeSample, error) {
	return scanSample(r.pool.QueryRow(ctx, `SELECT `+sampleCols+` FROM doc_type_samples WHERE owner_id = $1 AND id = $2`, owner, id))
}

// SampleByID returns one sample (workers use it).
func (r *DocTypesRepo) SampleByID(ctx context.Context, id uuid.UUID) (types.DocTypeSample, uuid.UUID, error) {
	var owner uuid.UUID
	if err := r.pool.QueryRow(ctx, `SELECT owner_id FROM doc_type_samples WHERE id = $1`, id).Scan(&owner); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return types.DocTypeSample{}, owner, ErrNotFound
		}
		return types.DocTypeSample{}, owner, err
	}
	s, err := r.Sample(ctx, owner, id)
	return s, owner, err
}

// SetSampleRead stores what docpipe read from a sample, or why it failed.
func (r *DocTypesRepo) SetSampleRead(ctx context.Context, id uuid.UUID, status string, pages []types.SamplePage, errMsg string) error {
	if pages == nil {
		pages = []types.SamplePage{}
	}
	raw, err := cleanJSON(pages)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `UPDATE doc_type_samples SET status = $2, pages = $3, error = $4 WHERE id = $1`, id, status, raw, cleanText(errMsg))
	return err
}

// SetTruth stores the values a user reviewed for a sample.
func (r *DocTypesRepo) SetTruth(ctx context.Context, owner, id uuid.UUID, truth map[string]string, by uuid.UUID) (types.DocTypeSample, error) {
	raw, err := cleanJSON(truth)
	if err != nil {
		return types.DocTypeSample{}, err
	}
	return scanSample(r.pool.QueryRow(ctx, `UPDATE doc_type_samples SET truth = $3, reviewed_by = $4, reviewed_at = now()
		WHERE owner_id = $1 AND id = $2 RETURNING `+sampleCols, owner, id, raw, by))
}

// DeleteSample removes a sample and its runs.
func (r *DocTypesRepo) DeleteSample(ctx context.Context, owner, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM doc_type_samples WHERE owner_id = $1 AND id = $2`, owner, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- runs

// SaveRun stores what one version read from a sample (replacing an earlier run).
func (r *DocTypesRepo) SaveRun(ctx context.Context, run types.SampleRun) error {
	raw, err := cleanJSON(run.Output)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO doc_type_runs (sample_id, version, output) VALUES ($1, $2, $3)
		ON CONFLICT (sample_id, version) DO UPDATE SET output = EXCLUDED.output, ran_at = now()`, run.SampleID, run.Version, raw)
	return err
}

// Runs returns the runs of every sample of a type.
func (r *DocTypesRepo) Runs(ctx context.Context, owner uuid.UUID, code string) ([]types.SampleRun, error) {
	rows, err := r.pool.Query(ctx, `SELECT r.sample_id, r.version, r.output, r.ran_at FROM doc_type_runs r
		JOIN doc_type_samples s ON s.id = r.sample_id WHERE s.owner_id = $1 AND s.code = $2`, owner, code)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []types.SampleRun{}
	for rows.Next() {
		var run types.SampleRun
		var raw []byte
		if err := rows.Scan(&run.SampleID, &run.Version, &raw, &run.RanAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &run.Output)
		out = append(out, run)
	}
	return out, rows.Err()
}

// ---- knowledge bases

// resolveDocTypes fills the doc types of knowledge bases from their owners'
// catalogues, keeping each KB's order; a code missing from the catalogue
// stays as stored.
func resolveDocTypes(ctx context.Context, db dbQuery, kbs []*types.KnowledgeBase) error {
	owners := map[uuid.UUID]bool{}
	for _, kb := range kbs {
		if len(kb.Config.DocTypes) > 0 {
			owners[kb.OwnerID] = true
		}
	}
	if len(owners) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(owners))
	for id := range owners {
		ids = append(ids, id)
	}
	rows, err := db.Query(ctx, `SELECT owner_id, code, title, description, fields FROM doc_types WHERE owner_id = ANY($1)`, ids)
	if err != nil {
		return fmt.Errorf("doc_types resolve: %w", err)
	}
	defer rows.Close()
	type key struct {
		owner uuid.UUID
		code  string
	}
	cat := map[key]types.DocType{}
	for rows.Next() {
		var owner uuid.UUID
		var t types.DocType
		var fields []byte
		if err := rows.Scan(&owner, &t.Name, &t.Title, &t.Description, &fields); err != nil {
			return err
		}
		_ = json.Unmarshal(fields, &t.Fields)
		cat[key{owner, t.Name}] = t
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, kb := range kbs {
		for i, t := range kb.Config.DocTypes {
			if c, ok := cat[key{kb.OwnerID, t.Name}]; ok {
				kb.Config.DocTypes[i] = c
			}
		}
	}
	return nil
}

// storeDocTypes puts the doc types a KB declares into its owner's catalogue
// (a code the catalogue lacks is added; a type given with a title or fields
// updates them, which is how clients before 0.33 edit fields) and returns
// the config to store: the codes only.
func storeDocTypes(ctx context.Context, db dbExec, owner uuid.UUID, cfg types.KBConfig) (types.KBConfig, error) {
	out := cfg
	out.DocTypes = make([]types.DocType, 0, len(cfg.DocTypes))
	for _, t := range cfg.DocTypes {
		if err := saveDocType(ctx, db, owner, t, t.Fields != nil); err != nil {
			return cfg, err
		}
		out.DocTypes = append(out.DocTypes, types.DocType{ClassLabel: types.ClassLabel{Name: t.Name}})
	}
	return out, nil
}

// SetKBs makes kbs (of the owner) the knowledge bases that use a type: the
// code is appended to the doc types of those that lack it and removed from
// the others.
func (r *DocTypesRepo) SetKBs(ctx context.Context, owner uuid.UUID, code string, kbs []uuid.UUID) error {
	if kbs == nil {
		kbs = []uuid.UUID{}
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	has := `COALESCE(jsonb_typeof(config->'doc_types') = 'array' AND config->'doc_types' @> jsonb_build_array(jsonb_build_object('name', $2::text)), false)`
	if _, err := tx.Exec(ctx, `UPDATE knowledge_bases SET config = jsonb_set(config, '{doc_types}',
			COALESCE((SELECT jsonb_agg(t) FROM jsonb_array_elements(config->'doc_types') t WHERE t->>'name' <> $2), '[]')), updated_at = now()
		WHERE owner_id = $1 AND NOT (id = ANY($3)) AND `+has, owner, code, kbs); err != nil {
		return fmt.Errorf("doc_types.SetKBs remove: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE knowledge_bases SET config = jsonb_set(COALESCE(config, '{}'), '{doc_types}',
			CASE WHEN jsonb_typeof(config->'doc_types') = 'array' THEN config->'doc_types' ELSE '[]' END || jsonb_build_array(jsonb_build_object('name', $2::text))), updated_at = now()
		WHERE owner_id = $1 AND id = ANY($3) AND deleted_at IS NULL AND NOT (`+has+`)`, owner, code, kbs); err != nil {
		return fmt.Errorf("doc_types.SetKBs add: %w", err)
	}
	return tx.Commit(ctx)
}

// Usage returns, per code, the owner's knowledge bases that use it.
func (r *DocTypesRepo) Usage(ctx context.Context, owner uuid.UUID) (map[string][]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx, `SELECT t->>'name', k.id FROM knowledge_bases k,
		jsonb_array_elements(CASE WHEN jsonb_typeof(k.config->'doc_types') = 'array' THEN k.config->'doc_types' ELSE '[]' END) t
		WHERE k.owner_id = $1 AND k.deleted_at IS NULL ORDER BY k.created_at`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]uuid.UUID{}
	for rows.Next() {
		var code string
		var id uuid.UUID
		if err := rows.Scan(&code, &id); err != nil {
			return nil, err
		}
		out[code] = append(out[code], id)
	}
	return out, rows.Err()
}

// ScoreRow is a sample with its reviewed values and what the instructions in
// use read from it (nil when not run or not reviewed).
type ScoreRow struct {
	Code   string
	Truth  map[string]string
	Output map[string]types.RunValue
}

// ScoreRows returns one row per sample of the owner, for the list's counts
// and accuracy.
func (r *DocTypesRepo) ScoreRows(ctx context.Context, owner uuid.UUID) ([]ScoreRow, error) {
	rows, err := r.pool.Query(ctx, `SELECT s.code, s.truth, r.output FROM doc_type_samples s
		JOIN doc_types d ON d.owner_id = s.owner_id AND d.code = s.code
		LEFT JOIN doc_type_runs r ON r.sample_id = s.id AND r.version = d.prompt_version
		WHERE s.owner_id = $1`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScoreRow{}
	for rows.Next() {
		var row ScoreRow
		var truth, output []byte
		if err := rows.Scan(&row.Code, &truth, &output); err != nil {
			return nil, err
		}
		if len(truth) > 0 {
			_ = json.Unmarshal(truth, &row.Truth)
		}
		if len(output) > 0 {
			_ = json.Unmarshal(output, &row.Output)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// ---- jobs

const jobCols = `id, code, kind, status, version, sample_ids, result, error, created_by, created_at, finished_at`

func scanJob(row pgx.Row) (types.DocTypeJob, error) {
	var j types.DocTypeJob
	var result []byte
	err := row.Scan(&j.ID, &j.Code, &j.Kind, &j.Status, &j.Version, &j.SampleIDs, &result, &j.Error, &j.CreatedBy, &j.CreatedAt, &j.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, ErrNotFound
	}
	if err != nil {
		return j, err
	}
	if len(result) > 0 {
		j.Result = &types.OptimizeProposal{}
		_ = json.Unmarshal(result, j.Result)
	}
	return j, nil
}

// CreateJob records a run or an optimization, running.
func (r *DocTypesRepo) CreateJob(ctx context.Context, owner uuid.UUID, j types.DocTypeJob) (types.DocTypeJob, error) {
	if j.SampleIDs == nil {
		j.SampleIDs = []uuid.UUID{}
	}
	return scanJob(r.pool.QueryRow(ctx, `INSERT INTO doc_type_jobs (owner_id, code, kind, version, sample_ids, created_by)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+jobCols, owner, j.Code, j.Kind, j.Version, j.SampleIDs, j.CreatedBy))
}

// Job returns one job of an owner.
func (r *DocTypesRepo) Job(ctx context.Context, owner, id uuid.UUID) (types.DocTypeJob, error) {
	return scanJob(r.pool.QueryRow(ctx, `SELECT `+jobCols+` FROM doc_type_jobs WHERE owner_id = $1 AND id = $2`, owner, id))
}

// JobByID returns one job and its owner (workers use it).
func (r *DocTypesRepo) JobByID(ctx context.Context, id uuid.UUID) (types.DocTypeJob, uuid.UUID, error) {
	var owner uuid.UUID
	if err := r.pool.QueryRow(ctx, `SELECT owner_id FROM doc_type_jobs WHERE id = $1`, id).Scan(&owner); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return types.DocTypeJob{}, owner, ErrNotFound
		}
		return types.DocTypeJob{}, owner, err
	}
	j, err := r.Job(ctx, owner, id)
	return j, owner, err
}

// Jobs returns the running jobs of a type and the latest finished one of
// each kind, newest first.
func (r *DocTypesRepo) Jobs(ctx context.Context, owner uuid.UUID, code string) ([]types.DocTypeJob, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+jobCols+` FROM doc_type_jobs WHERE owner_id = $1 AND code = $2
		AND (status = 'running' OR id IN (SELECT DISTINCT ON (kind) id FROM doc_type_jobs WHERE owner_id = $1 AND code = $2 AND status <> 'running' ORDER BY kind, created_at DESC))
		ORDER BY created_at DESC`, owner, code)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []types.DocTypeJob{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// FinishJob ends a job with its result or error.
func (r *DocTypesRepo) FinishJob(ctx context.Context, id uuid.UUID, result *types.OptimizeProposal, errMsg string) error {
	status := types.JobDone
	if errMsg != "" {
		status = types.JobFailed
	}
	var raw []byte
	if result != nil {
		var err error
		if raw, err = cleanJSON(result); err != nil {
			return err
		}
	}
	_, err := r.pool.Exec(ctx, `UPDATE doc_type_jobs SET status = $2, result = $3, error = $4, finished_at = now() WHERE id = $1`,
		id, status, raw, cleanText(errMsg))
	return err
}

// DropProposal forgets the proposal of an optimization (the user dropped it
// or applied it).
func (r *DocTypesRepo) DropProposal(ctx context.Context, owner, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM doc_type_jobs WHERE owner_id = $1 AND id = $2 AND kind = 'optimize'`, owner, id)
	return err
}

// ClearDescription empties the description of a type.
func (r *DocTypesRepo) ClearDescription(ctx context.Context, owner uuid.UUID, code string) error {
	_, err := r.pool.Exec(ctx, `UPDATE doc_types SET description = '', updated_at = now() WHERE owner_id = $1 AND code = $2`, owner, code)
	return err
}

// SetFirstPrompt replaces version 1 of a type that has no other version (a
// copy starts from the version in use of its source).
func (r *DocTypesRepo) SetFirstPrompt(ctx context.Context, owner uuid.UUID, p types.DocTypePrompt) error {
	fields, err := cleanJSON(p.Fields)
	if err != nil {
		return err
	}
	rules, err := cleanJSON(nonNil(p.Rules))
	if err != nil {
		return err
	}
	shots, err := cleanJSON(nonNil(p.Shots))
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `UPDATE doc_type_prompts SET instruction = $3, fields = $4, rules = $5, shots = $6, note = $7, created_by = $8
		WHERE owner_id = $1 AND code = $2 AND version = 1`, owner, p.Code, cleanText(p.Instruction), fields, rules, shots, cleanText(p.Note), p.CreatedBy)
	return err
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
