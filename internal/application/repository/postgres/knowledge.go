package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thanhenti/bepaylot/internal/types"
)

// ErrDuplicate is returned when a document with the same content already
// exists in the knowledge base.
var ErrDuplicate = errors.New("duplicate")

// KBRepo persists knowledge bases.
type KBRepo struct{ pool *pgxpool.Pool }

const kbCols = `id, owner_id, name, description, config, metadata_schema, graph_schema_id, is_temporary, created_at, updated_at`

func scanKB(row pgx.Row) (types.KnowledgeBase, error) {
	var kb types.KnowledgeBase
	var cfg, schema []byte
	err := row.Scan(&kb.ID, &kb.OwnerID, &kb.Name, &kb.Description, &cfg, &schema, &kb.GraphSchemaID, &kb.IsTemporary, &kb.CreatedAt, &kb.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return kb, ErrNotFound
	}
	if err != nil {
		return kb, err
	}
	_ = json.Unmarshal(cfg, &kb.Config)
	if len(schema) > 0 && string(schema) != "null" {
		var s types.MetadataSchema
		if json.Unmarshal(schema, &s) == nil {
			kb.MetadataSchema = &s
		}
	}
	return kb, nil
}

// Create inserts a knowledge base.
func (r *KBRepo) Create(ctx context.Context, kb types.KnowledgeBase) (types.KnowledgeBase, error) {
	cfg, _ := json.Marshal(kb.Config)
	var schema any
	if kb.MetadataSchema != nil {
		b, _ := json.Marshal(kb.MetadataSchema)
		schema = b
	}
	return scanKB(r.pool.QueryRow(ctx, `
		INSERT INTO knowledge_bases (owner_id, name, description, config, metadata_schema, is_temporary)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+kbCols,
		kb.OwnerID, cleanText(kb.Name), cleanText(kb.Description), cfg, schema, kb.IsTemporary))
}

// Get returns a live knowledge base by id (no owner check; workers use it).
func (r *KBRepo) Get(ctx context.Context, id uuid.UUID) (types.KnowledgeBase, error) {
	return scanKB(r.pool.QueryRow(ctx, `SELECT `+kbCols+` FROM knowledge_bases WHERE id = $1 AND deleted_at IS NULL`, id))
}

// GetOwned returns the KB only when owner owns it.
func (r *KBRepo) GetOwned(ctx context.Context, id, owner uuid.UUID) (types.KnowledgeBase, error) {
	return scanKB(r.pool.QueryRow(ctx, `SELECT `+kbCols+` FROM knowledge_bases WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL`, id, owner))
}

// List returns the owner's knowledge bases, newest first.
func (r *KBRepo) List(ctx context.Context, owner uuid.UUID, includeTemp bool) ([]types.KnowledgeBase, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+kbCols+` FROM knowledge_bases
		WHERE owner_id = $1 AND deleted_at IS NULL AND ($2 OR NOT is_temporary) ORDER BY created_at DESC`, owner, includeTemp)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.KnowledgeBase
	for rows.Next() {
		kb, err := scanKB(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, kb)
	}
	return out, rows.Err()
}

// KBPatch holds optional updates.
type KBPatch struct {
	Name           *string
	Description    *string
	Config         *types.KBConfig
	MetadataSchema **types.MetadataSchema
	GraphSchemaID  **uuid.UUID
}

// Update applies a patch for the owner.
func (r *KBRepo) Update(ctx context.Context, id, owner uuid.UUID, p KBPatch) (types.KnowledgeBase, error) {
	sets := []string{"updated_at = now()"}
	var a sqlArgs
	a.add(id)
	a.add(owner)
	if p.Name != nil {
		sets = append(sets, "name = "+a.add(cleanText(*p.Name)))
	}
	if p.Description != nil {
		sets = append(sets, "description = "+a.add(cleanText(*p.Description)))
	}
	if p.Config != nil {
		b, _ := json.Marshal(p.Config)
		sets = append(sets, "config = "+a.add(b))
	}
	if p.MetadataSchema != nil {
		var v any
		if *p.MetadataSchema != nil {
			b, _ := json.Marshal(*p.MetadataSchema)
			v = b
		}
		sets = append(sets, "metadata_schema = "+a.add(v))
	}
	if p.GraphSchemaID != nil {
		sets = append(sets, "graph_schema_id = "+a.add(*p.GraphSchemaID))
	}
	return scanKB(r.pool.QueryRow(ctx, `UPDATE knowledge_bases SET `+strings.Join(sets, ", ")+`
		WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL RETURNING `+kbCols, a.vals...))
}

// SoftDelete marks the KB deleted and its documents deleting.
func (r *KBRepo) SoftDelete(ctx context.Context, id, owner uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `UPDATE knowledge_bases SET deleted_at = now() WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL`, id, owner)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	_, err = r.pool.Exec(ctx, `UPDATE documents SET status = 'deleting', deleted_at = now() WHERE kb_id = $1 AND deleted_at IS NULL`, id)
	return err
}

// CreateBatch records an upload batch.
func (r *KBRepo) CreateBatch(ctx context.Context, b types.UploadBatch) (types.UploadBatch, error) {
	meta, _ := cleanJSON(nonNilMap(b.Metadata))
	err := r.pool.QueryRow(ctx, `INSERT INTO upload_batches (kb_id, created_by, metadata, file_count)
		VALUES ($1, $2, $3, $4) RETURNING id, created_at`, b.KBID, b.CreatedBy, meta, b.FileCount).Scan(&b.ID, &b.CreatedAt)
	return b, err
}

// FinishBatch stores accepted/rejected counts.
func (r *KBRepo) FinishBatch(ctx context.Context, id uuid.UUID, accepted, rejected int) error {
	_, err := r.pool.Exec(ctx, `UPDATE upload_batches SET accepted = $2, rejected = $3 WHERE id = $1`, id, accepted, rejected)
	return err
}

// DocumentsRepo persists documents.
type DocumentsRepo struct{ pool *pgxpool.Pool }

const docCols = `d.id, d.kb_id, d.batch_id, coalesce(d.created_by, '00000000-0000-0000-0000-000000000000'::uuid), d.file_name, d.mime_type, d.size_bytes, d.sha256, d.storage_key,
	d.page_count, d.gen, d.status, d.parse_status, d.index_status, d.graph_status, d.pages_done, d.pages_failed, d.pages_text_layer,
	d.pdfa_part, d.pdfa_conformance, d.pdf_info, d.engine, d.markdown_key, d.error, d.metadata, d.title, d.doc_type, d.summary,
	d.interactive, d.created_at, d.updated_at`

func scanDoc(row pgx.Row) (types.Document, error) {
	var d types.Document
	err := row.Scan(&d.ID, &d.KBID, &d.BatchID, &d.CreatedBy, &d.FileName, &d.MimeType, &d.SizeBytes, &d.SHA256, &d.StorageKey,
		&d.PageCount, &d.Gen, &d.Status, &d.ParseStatus, &d.IndexStatus, &d.GraphStatus, &d.PagesDone, &d.PagesFailed, &d.PagesTextLayer,
		&d.PDFAPart, &d.PDFAConformance, &metaScanner{&d.PDFInfo}, &d.Engine, &d.MarkdownKey, &d.Error, &metaScanner{&d.Metadata},
		&d.Title, &d.DocType, &d.Summary, &d.Interactive, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

// Create inserts a document. On a (kb_id, sha256) conflict it returns the
// existing document and ErrDuplicate.
func (r *DocumentsRepo) Create(ctx context.Context, d types.Document, interactive bool) (types.Document, error) {
	meta, err := cleanJSON(nonNilMap(d.Metadata))
	if err != nil {
		return d, err
	}
	var createdBy any
	if d.CreatedBy != uuid.Nil {
		createdBy = d.CreatedBy
	}
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	out, err := scanDoc(r.pool.QueryRow(ctx, `
		INSERT INTO documents AS d (id, kb_id, batch_id, created_by, file_name, mime_type, size_bytes, sha256, storage_key,
			status, parse_status, pdfa_part, pdfa_conformance, engine, metadata, interactive)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'queued', 'pending', $10, $11, $12, $13, $14)
		RETURNING `+docCols,
		d.ID, d.KBID, d.BatchID, createdBy, cleanText(d.FileName), d.MimeType, d.SizeBytes, d.SHA256, d.StorageKey,
		d.PDFAPart, d.PDFAConformance, d.Engine, meta, interactive))
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		existing, gerr := r.FindBySHA(ctx, d.KBID, d.SHA256)
		if gerr != nil {
			return d, gerr
		}
		return existing, ErrDuplicate
	}
	return out, err
}

// CountByKB counts live documents of a knowledge base.
func (r *DocumentsRepo) CountByKB(ctx context.Context, kb uuid.UUID) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM documents WHERE kb_id = $1 AND deleted_at IS NULL`, kb).Scan(&n)
	return n, err
}

// FindBySHA returns the live document with this content in the KB.
func (r *DocumentsRepo) FindBySHA(ctx context.Context, kb uuid.UUID, sha string) (types.Document, error) {
	return scanDoc(r.pool.QueryRow(ctx, `SELECT `+docCols+` FROM documents d WHERE d.kb_id = $1 AND d.sha256 = $2 AND d.deleted_at IS NULL`, kb, sha))
}

// Get returns a live document (no owner check).
func (r *DocumentsRepo) Get(ctx context.Context, id uuid.UUID) (types.Document, error) {
	return scanDoc(r.pool.QueryRow(ctx, `SELECT `+docCols+` FROM documents d WHERE d.id = $1 AND d.deleted_at IS NULL`, id))
}

// GetAny returns a document even when soft-deleted (delete task).
func (r *DocumentsRepo) GetAny(ctx context.Context, id uuid.UUID) (types.Document, error) {
	return scanDoc(r.pool.QueryRow(ctx, `SELECT `+docCols+` FROM documents d WHERE d.id = $1`, id))
}

// GetOwned returns a live document whose KB the owner owns.
func (r *DocumentsRepo) GetOwned(ctx context.Context, id, owner uuid.UUID) (types.Document, error) {
	return scanDoc(r.pool.QueryRow(ctx, `SELECT `+docCols+` FROM documents d JOIN knowledge_bases kb ON kb.id = d.kb_id
		WHERE d.id = $1 AND kb.owner_id = $2 AND d.deleted_at IS NULL AND kb.deleted_at IS NULL`, id, owner))
}

// DocumentFilter selects documents for listing and search scoping.
type DocumentFilter struct {
	OwnerID     uuid.UUID
	KBIDs       []uuid.UUID
	DocumentIDs []uuid.UUID
	Statuses    []string
	BatchID     *uuid.UUID
	Metadata    types.MetadataFilter
	Schema      *types.MetadataSchema
	Query       string // full-text over file name, card and metadata values
	Limit       int
	Before      *time.Time // keyset cursor on created_at
}

func (f DocumentFilter) where(a *sqlArgs) (string, error) {
	conds := []string{"d.deleted_at IS NULL", "kb.deleted_at IS NULL"}
	if f.OwnerID != uuid.Nil {
		conds = append(conds, "kb.owner_id = "+a.add(f.OwnerID))
	}
	if len(f.KBIDs) > 0 {
		conds = append(conds, "d.kb_id = ANY("+a.add(f.KBIDs)+")")
	}
	if len(f.DocumentIDs) > 0 {
		conds = append(conds, "d.id = ANY("+a.add(f.DocumentIDs)+")")
	}
	if len(f.Statuses) > 0 {
		conds = append(conds, "d.status = ANY("+a.add(f.Statuses)+")")
	}
	if f.BatchID != nil {
		conds = append(conds, "d.batch_id = "+a.add(*f.BatchID))
	}
	if f.Query != "" {
		p := a.add(f.Query)
		conds = append(conds, fmt.Sprintf("(d.meta_tsv @@ websearch_to_tsquery('simple', unaccent_vi(%s)) OR unaccent_vi(d.file_name || ' ' || jsonb_values_text(d.metadata)) LIKE '%%' || unaccent_vi(%s) || '%%')", p, p))
	}
	if f.Before != nil {
		conds = append(conds, "d.created_at < "+a.add(*f.Before))
	}
	mf, err := metaFilterSQL("d.metadata", f.Metadata, f.Schema, a)
	if err != nil {
		return "", err
	}
	if mf != "" {
		conds = append(conds, mf)
	}
	return strings.Join(conds, " AND "), nil
}

// List returns documents matching the filter, newest first.
func (r *DocumentsRepo) List(ctx context.Context, f DocumentFilter) ([]types.Document, error) {
	var a sqlArgs
	where, err := f.where(&a)
	if err != nil {
		return nil, err
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `SELECT `+docCols+` FROM documents d JOIN knowledge_bases kb ON kb.id = d.kb_id
		WHERE `+where+fmt.Sprintf(` ORDER BY d.created_at DESC LIMIT %d`, limit), a.vals...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.Document
	for rows.Next() {
		d, err := scanDoc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RankByText orders the filtered documents by full-text relevance of query
// against the document card/metadata and their pages; returns ids.
func (r *DocumentsRepo) RankByText(ctx context.Context, f DocumentFilter, query string, limit int) ([]uuid.UUID, error) {
	var a sqlArgs
	f.Query = ""
	where, err := f.where(&a)
	if err != nil {
		return nil, err
	}
	q := a.add(query)
	rows, err := r.pool.Query(ctx, fmt.Sprintf(`
		WITH q AS (SELECT websearch_to_tsquery('simple', unaccent_vi(%s)) AS tq)
		SELECT d.id,
		       ts_rank_cd(d.meta_tsv, q.tq) * 2 +
		       coalesce((SELECT max(ts_rank_cd(p.tsv, q.tq)) FROM document_pages p
		                 WHERE p.document_id = d.id AND p.gen = d.gen AND p.tsv @@ q.tq), 0) AS score
		FROM documents d JOIN knowledge_bases kb ON kb.id = d.kb_id, q
		WHERE %s
		ORDER BY score DESC, d.created_at DESC
		LIMIT %d`, q, where, max(limit, 1)), a.vals...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		var score float64
		if err := rows.Scan(&id, &score); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// DocUpdate is a partial document update used by the pipeline.
type DocUpdate struct {
	Status, ParseStatus, IndexStatus, GraphStatus *string
	Error                                         *string
	PageCount                                     *int
	PDFInfo                                       map[string]any
	MarkdownKey                                   *string
	PagesTextLayer                                *int
	Title, DocType, Summary                       *string
}

// Update applies u when the document is still at generation gen and not
// cancelled/deleting (unless the update itself sets such a status).
func (r *DocumentsRepo) Update(ctx context.Context, id uuid.UUID, gen int, u DocUpdate) (bool, error) {
	sets := []string{"updated_at = now()"}
	var a sqlArgs
	a.add(id)
	a.add(gen)
	str := func(col string, v *string) {
		if v != nil {
			sets = append(sets, col+" = "+a.add(cleanText(*v)))
		}
	}
	str("status", u.Status)
	str("parse_status", u.ParseStatus)
	str("index_status", u.IndexStatus)
	str("graph_status", u.GraphStatus)
	str("error", u.Error)
	str("markdown_key", u.MarkdownKey)
	str("title", u.Title)
	str("doc_type", u.DocType)
	str("summary", u.Summary)
	if u.PageCount != nil {
		sets = append(sets, "page_count = "+a.add(*u.PageCount))
	}
	if u.PagesTextLayer != nil {
		sets = append(sets, "pages_text_layer = "+a.add(*u.PagesTextLayer))
	}
	if u.PDFInfo != nil {
		b, _ := cleanJSON(u.PDFInfo)
		sets = append(sets, "pdf_info = "+a.add(b))
	}
	guard := "AND status NOT IN ('cancelled', 'deleting')"
	if u.Status != nil && (*u.Status == types.DocCancelled || *u.Status == types.DocDeleting) {
		guard = ""
	}
	tag, err := r.pool.Exec(ctx, `UPDATE documents SET `+strings.Join(sets, ", ")+` WHERE id = $1 AND gen = $2 `+guard, a.vals...)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// PageCounters is the result of counting a finished page.
type PageCounters struct {
	PagesDone, PagesFailed, PageCount int
	Status                            string
}

// CountPage atomically bumps pages_done or pages_failed (§4.3) and returns the
// new totals, so exactly one finisher sees done+failed == page_count.
func (r *DocumentsRepo) CountPage(ctx context.Context, id uuid.UUID, gen int, failed bool) (PageCounters, error) {
	col := "pages_done"
	if failed {
		col = "pages_failed"
	}
	var c PageCounters
	err := r.pool.QueryRow(ctx, `UPDATE documents SET `+col+` = `+col+` + 1, updated_at = now()
		WHERE id = $1 AND gen = $2 RETURNING pages_done, pages_failed, page_count, status`, id, gen).
		Scan(&c.PagesDone, &c.PagesFailed, &c.PageCount, &c.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// WithLock runs fn while holding a transaction-scoped advisory lock on the
// document, serializing pipeline dispatch decisions (§4.3 Advance).
// fn must run its queries on tx so a busy pool cannot starve lock holders.
func (r *DocumentsRepo) WithLock(ctx context.Context, id uuid.UUID, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 7))`, id.String()); err != nil {
		return err
	}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AdjustCounters corrects pages_done/pages_failed after a page reparse.
func (r *DocumentsRepo) AdjustCounters(ctx context.Context, id uuid.UUID, gen, doneDelta, failedDelta int) error {
	_, err := r.pool.Exec(ctx, `UPDATE documents SET pages_done = greatest(0, pages_done + $3), pages_failed = greatest(0, pages_failed + $4),
		updated_at = now() WHERE id = $1 AND gen = $2`, id, gen, doneDelta, failedDelta)
	return err
}

// BumpGen starts a new parse generation (reparse) and resets counters.
func (r *DocumentsRepo) BumpGen(ctx context.Context, id uuid.UUID) (int, error) {
	var gen int
	err := r.pool.QueryRow(ctx, `UPDATE documents SET gen = gen + 1, status = 'queued', parse_status = 'pending',
		index_status = 'pending', pages_done = 0, pages_failed = 0, pages_text_layer = 0, error = '', updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL AND status <> 'deleting' RETURNING gen`, id).Scan(&gen)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return gen, err
}

// SetEngine changes the parser engine used for (re)parsing.
func (r *DocumentsRepo) SetEngine(ctx context.Context, id uuid.UUID, engine string) error {
	_, err := r.pool.Exec(ctx, `UPDATE documents SET engine = $2, updated_at = now() WHERE id = $1`, id, engine)
	return err
}

// SoftDelete marks a document deleting.
func (r *DocumentsRepo) SoftDelete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `UPDATE documents SET status = 'deleting', deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Purge removes the document row (cascades to pages, sections, tree...).
func (r *DocumentsRepo) Purge(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM documents WHERE id = $1`, id)
	return err
}

// SetMetadata replaces a document's metadata.
func (r *DocumentsRepo) SetMetadata(ctx context.Context, id uuid.UUID, meta map[string]any) (types.Document, error) {
	b, err := cleanJSON(nonNilMap(meta))
	if err != nil {
		return types.Document{}, err
	}
	return scanDoc(r.pool.QueryRow(ctx, `UPDATE documents AS d SET metadata = $2, updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL RETURNING `+docCols, id, b))
}

// BulkUpdateMetadata merges set and removes unset keys on every matching
// document; returns the number updated.
func (r *DocumentsRepo) BulkUpdateMetadata(ctx context.Context, f DocumentFilter, set map[string]any, unset []string) (int64, error) {
	var a sqlArgs
	where, err := f.where(&a)
	if err != nil {
		return 0, err
	}
	b, err := cleanJSON(nonNilMap(set))
	if err != nil {
		return 0, err
	}
	sp := a.add(b)
	up := a.add(append([]string{}, unset...))
	tag, err := r.pool.Exec(ctx, fmt.Sprintf(`UPDATE documents AS d SET metadata = (d.metadata - %s::text[]) || %s::jsonb, updated_at = now()
		FROM knowledge_bases kb WHERE kb.id = d.kb_id AND %s`, up, sp, where), a.vals...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// MetadataValue is one distinct value and its document count.
type MetadataValue struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// MetadataValues lists distinct values of key in a KB.
func (r *DocumentsRepo) MetadataValues(ctx context.Context, kb uuid.UUID, key, prefix string, limit int) ([]MetadataValue, error) {
	if !metaKeyRe.MatchString(key) {
		return nil, fmt.Errorf("invalid metadata key %q", key)
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, fmt.Sprintf(`SELECT metadata->>$2 AS v, count(*) FROM documents
		WHERE kb_id = $1 AND deleted_at IS NULL AND metadata ? $2 AND ($3 = '' OR unaccent_vi(metadata->>$2) LIKE unaccent_vi($3) || '%%')
		GROUP BY 1 ORDER BY 2 DESC, 1 LIMIT %d`, limit), kb, key, prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MetadataValue
	for rows.Next() {
		var v MetadataValue
		if err := rows.Scan(&v.Value, &v.Count); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// MetadataKeys lists metadata keys in use in a KB (for KBs without schema).
func (r *DocumentsRepo) MetadataKeys(ctx context.Context, kb uuid.UUID) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT DISTINCT jsonb_object_keys(metadata) FROM documents WHERE kb_id = $1 AND deleted_at IS NULL LIMIT 200`, kb)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// CreateMetadataIndex builds an expression index for a frequently filtered
// metadata key (metadata_schema field with indexed: true).
func (r *DocumentsRepo) CreateMetadataIndex(ctx context.Context, key string) error {
	if !metaKeyRe.MatchString(key) || strings.ContainsAny(key, ".-") {
		return fmt.Errorf("metadata key %q cannot be indexed", key)
	}
	_, err := r.pool.Exec(ctx, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS documents_md_%s ON documents (kb_id, (metadata->>'%s')) WHERE deleted_at IS NULL`, key, key))
	return err
}

// Stuck returns documents in non-terminal pipeline states not updated since
// before; housekeeping advances them.
func (r *DocumentsRepo) Stuck(ctx context.Context, before time.Time, limit int) ([]types.Document, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+docCols+` FROM documents d
		WHERE d.deleted_at IS NULL AND d.status IN ('queued','splitting','parsing','assembling','indexing','enriching')
		AND d.updated_at < $1 ORDER BY d.updated_at LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.Document
	for rows.Next() {
		d, err := scanDoc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Deleting returns documents marked deleting.
func (r *DocumentsRepo) Deleting(ctx context.Context, limit int) ([]types.Document, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+docCols+` FROM documents d WHERE d.status = 'deleting' ORDER BY d.updated_at LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.Document
	for rows.Next() {
		d, err := scanDoc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func nonNilMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
