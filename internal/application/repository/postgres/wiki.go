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

// querier is what both the pool and a transaction offer.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// WikiRepo persists the case wikis (§6.6–6.9). Every page query is scoped by
// case_id. InTx gives the same methods bound to one transaction.
type WikiRepo struct {
	pool *pgxpool.Pool
	db   querier
}

// InTx runs fn with a WikiRepo bound to a new transaction.
func (r *WikiRepo) InTx(ctx context.Context, fn func(w *WikiRepo) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(&WikiRepo{pool: r.pool, db: tx}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// TryCaseLock runs fn while holding the case's wiki advisory lock; it
// returns false when another worker holds it (one ingest stream per case).
func (r *WikiRepo) TryCaseLock(ctx context.Context, caseID uuid.UUID, fn func(ctx context.Context) error) (bool, error) {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()
	key := "wiki:active:" + caseID.String()
	var ok bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 11))`, key).Scan(&ok); err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	defer conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtextextended($1, 11))`, key)
	return true, fn(ctx)
}

// ---- schemas ----

// UpsertSchema stores a schema version (same name+version is replaced).
func (r *WikiRepo) UpsertSchema(ctx context.Context, s types.WikiSchema) (types.WikiSchema, error) {
	spec, err := json.Marshal(s)
	if err != nil {
		return s, err
	}
	return scanWikiSchema(r.db.QueryRow(ctx, `INSERT INTO wiki_schemas (name, version, spec) VALUES ($1, $2, $3)
		ON CONFLICT (name, version) DO UPDATE SET spec = EXCLUDED.spec RETURNING id, spec, created_at`, s.Name, s.Version, spec))
}

func scanWikiSchema(row pgx.Row) (types.WikiSchema, error) {
	var s types.WikiSchema
	var id uuid.UUID
	var spec []byte
	var at time.Time
	err := row.Scan(&id, &spec, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrNotFound
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(spec, &s); err != nil {
		return s, err
	}
	s.ID, s.CreatedAt = id, at
	return s, nil
}

// LatestSchema returns the highest version of a schema.
func (r *WikiRepo) LatestSchema(ctx context.Context, name string) (types.WikiSchema, error) {
	return scanWikiSchema(r.db.QueryRow(ctx, `SELECT id, spec, created_at FROM wiki_schemas WHERE name = $1 ORDER BY version DESC LIMIT 1`, name))
}

// Schemas lists every version of name, or the latest version of every
// schema when name is empty.
func (r *WikiRepo) Schemas(ctx context.Context, name string) ([]types.WikiSchema, error) {
	q := `SELECT DISTINCT ON (name) id, spec, created_at FROM wiki_schemas ORDER BY name, version DESC`
	args := []any{}
	if name != "" {
		q = `SELECT id, spec, created_at FROM wiki_schemas WHERE name = $1 ORDER BY version DESC`
		args = append(args, name)
	}
	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.WikiSchema
	for rows.Next() {
		s, err := scanWikiSchema(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ---- pages ----

const wikiPageCols = `p.id, p.case_id, p.slug, p.kind, coalesce(p.entity_type, ''), coalesce(p.identity_key, ''), p.document_id,
	p.title, p.aliases, p.summary, p.content, p.attributes, p.ord, p.version, p.last_edit_source, p.proposed_content, p.proposed_attributes,
	p.created_at, p.updated_at`

func scanWikiPage(row pgx.Row) (types.WikiPage, error) {
	var p types.WikiPage
	var attrs, proposed []byte
	err := row.Scan(&p.ID, &p.CaseID, &p.Slug, &p.Kind, &p.EntityType, &p.IdentityKey, &p.DocumentID,
		&p.Title, &p.Aliases, &p.Summary, &p.Content, &attrs, &p.Ord, &p.Version, &p.LastEditSource, &p.ProposedContent, &proposed,
		&p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	if len(attrs) > 0 {
		_ = json.Unmarshal(attrs, &p.Attributes)
	}
	if len(proposed) > 0 && string(proposed) != "null" {
		_ = json.Unmarshal(proposed, &p.ProposedAttributes)
	}
	return p, nil
}

func (r *WikiRepo) pages(ctx context.Context, where string, args ...any) ([]types.WikiPage, error) {
	rows, err := r.db.Query(ctx, `SELECT `+wikiPageCols+` FROM wiki_pages p WHERE `+where+
		` ORDER BY CASE p.kind WHEN 'overview' THEN 0 WHEN 'source' THEN 1 WHEN 'entity' THEN 2 WHEN 'topic' THEN 3 ELSE 4 END,
		  coalesce(p.entity_type, ''), p.ord, p.title`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.WikiPage
	for rows.Next() {
		p, err := scanWikiPage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Pages lists every page of a case in TOC order.
func (r *WikiRepo) Pages(ctx context.Context, caseID uuid.UUID) ([]types.WikiPage, error) {
	return r.pages(ctx, `p.case_id = $1`, caseID)
}

// PagesByID returns pages of the case with these ids.
func (r *WikiRepo) PagesByID(ctx context.Context, caseID uuid.UUID, ids []uuid.UUID) ([]types.WikiPage, error) {
	return r.pages(ctx, `p.case_id = $1 AND p.id = ANY($2)`, caseID, ids)
}

// PageBySlug returns one page of the case.
func (r *WikiRepo) PageBySlug(ctx context.Context, caseID uuid.UUID, slug string) (types.WikiPage, error) {
	return scanWikiPage(r.db.QueryRow(ctx, `SELECT `+wikiPageCols+` FROM wiki_pages p WHERE p.case_id = $1 AND p.slug = $2`, caseID, slug))
}

// PageByID returns one page of the case.
func (r *WikiRepo) PageByID(ctx context.Context, caseID, id uuid.UUID) (types.WikiPage, error) {
	return scanWikiPage(r.db.QueryRow(ctx, `SELECT `+wikiPageCols+` FROM wiki_pages p WHERE p.case_id = $1 AND p.id = $2`, caseID, id))
}

// PageByIdentity returns the entity page with this normalized identity.
func (r *WikiRepo) PageByIdentity(ctx context.Context, caseID uuid.UUID, entityType, key string) (types.WikiPage, error) {
	return scanWikiPage(r.db.QueryRow(ctx, `SELECT `+wikiPageCols+` FROM wiki_pages p
		WHERE p.case_id = $1 AND p.entity_type = $2 AND p.identity_key = $3`, caseID, entityType, key))
}

// SourcePage returns the source page of a document.
func (r *WikiRepo) SourcePage(ctx context.Context, caseID, doc uuid.UUID) (types.WikiPage, error) {
	return scanWikiPage(r.db.QueryRow(ctx, `SELECT `+wikiPageCols+` FROM wiki_pages p
		WHERE p.case_id = $1 AND p.kind = 'source' AND p.document_id = $2`, caseID, doc))
}

// SimilarEntities returns entity pages of a type whose title or an alias is
// trigram-similar to name (entity resolution without embeddings, §6.8).
func (r *WikiRepo) SimilarEntities(ctx context.Context, caseID uuid.UUID, entityType, name string, threshold float64, limit int) ([]types.WikiPage, error) {
	rows, err := r.db.Query(ctx, `SELECT `+wikiPageCols+` FROM wiki_pages p
		WHERE p.case_id = $1 AND p.kind = 'entity' AND p.entity_type = $2
		  AND greatest(similarity(unaccent_vi(p.title), unaccent_vi($3)),
		               coalesce((SELECT max(similarity(unaccent_vi(a), unaccent_vi($3))) FROM unnest(p.aliases) a), 0)) >= $4
		ORDER BY similarity(unaccent_vi(p.title), unaccent_vi($3)) DESC LIMIT $5`, caseID, entityType, name, threshold, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.WikiPage
	for rows.Next() {
		p, err := scanWikiPage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SavePage inserts (ID nil) or updates a page, bumps its version and writes a
// revision. The returned page carries the stored id and version.
func (r *WikiRepo) SavePage(ctx context.Context, p types.WikiPage, source string, editor *uuid.UUID, logID *int64) (types.WikiPage, error) {
	attrs, err := json.Marshal(nonNilAttrs(p.Attributes))
	if err != nil {
		return p, err
	}
	var entityType, identity any
	if p.EntityType != "" {
		entityType = p.EntityType
	}
	if p.IdentityKey != "" {
		identity = p.IdentityKey
	}
	aliases := p.Aliases
	if aliases == nil {
		aliases = []string{}
	}
	var out types.WikiPage
	if p.ID == uuid.Nil {
		out, err = scanWikiPage(r.db.QueryRow(ctx, `INSERT INTO wiki_pages AS p (case_id, slug, kind, entity_type, identity_key, document_id,
			title, aliases, summary, content, attributes, ord, last_edit_source)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) RETURNING `+wikiPageCols,
			p.CaseID, p.Slug, p.Kind, entityType, identity, p.DocumentID, cleanText(p.Title), aliases, cleanText(p.Summary),
			cleanText(p.Content), attrs, p.Ord, source))
	} else {
		out, err = scanWikiPage(r.db.QueryRow(ctx, `UPDATE wiki_pages AS p SET slug = $3, title = $4, aliases = $5, summary = $6,
			content = $7, attributes = $8, ord = $9, last_edit_source = $10, entity_type = $11, identity_key = $12,
			version = p.version + 1, updated_at = now()
			WHERE p.case_id = $1 AND p.id = $2 RETURNING `+wikiPageCols,
			p.CaseID, p.ID, p.Slug, cleanText(p.Title), aliases, cleanText(p.Summary), cleanText(p.Content), attrs, p.Ord, source,
			entityType, identity))
	}
	if err != nil {
		return out, err
	}
	_, err = r.db.Exec(ctx, `INSERT INTO wiki_page_revisions (page_id, version, title, content, attributes, edit_source, editor_id, log_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT (page_id, version) DO NOTHING`,
		out.ID, out.Version, out.Title, out.Content, attrs, source, editor, logID)
	return out, err
}

// SetProposal stores (or clears, with nil) an ingest proposal for a page the
// user edited (§7.4).
func (r *WikiRepo) SetProposal(ctx context.Context, caseID, id uuid.UUID, content *string, attrs map[string]types.WikiAttribute) error {
	var a any
	if attrs != nil {
		b, _ := json.Marshal(attrs)
		a = b
	}
	_, err := r.db.Exec(ctx, `UPDATE wiki_pages SET proposed_content = $3, proposed_attributes = $4, updated_at = now()
		WHERE case_id = $1 AND id = $2`, caseID, id, content, a)
	return err
}

// DeletePage removes a page (footnotes, links and revisions cascade).
func (r *WikiRepo) DeletePage(ctx context.Context, caseID, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `DELETE FROM wiki_pages WHERE case_id = $1 AND id = $2`, caseID, id)
	return err
}

// DeleteCase removes every wiki row of a case.
func (r *WikiRepo) DeleteCase(ctx context.Context, caseID uuid.UUID, keepNotesAndEdited bool) error {
	if keepNotesAndEdited {
		_, err := r.db.Exec(ctx, `DELETE FROM wiki_pages WHERE case_id = $1 AND kind <> 'note' AND last_edit_source <> 'user'`, caseID)
		if err != nil {
			return err
		}
		_, err = r.db.Exec(ctx, `DELETE FROM wiki_index WHERE case_id = $1`, caseID)
		return err
	}
	for _, q := range []string{
		`DELETE FROM wiki_pages WHERE case_id = $1`, `DELETE FROM wiki_links WHERE case_id = $1`, `DELETE FROM wiki_index WHERE case_id = $1`,
		`DELETE FROM wiki_log WHERE case_id = $1`, `DELETE FROM wiki_lint_issues WHERE case_id = $1`,
	} {
		if _, err := r.db.Exec(ctx, q, caseID); err != nil {
			return err
		}
	}
	return nil
}

// Revisions lists a page's history, newest first.
func (r *WikiRepo) Revisions(ctx context.Context, page uuid.UUID) ([]types.WikiRevision, error) {
	rows, err := r.db.Query(ctx, `SELECT version, title, content, attributes, edit_source, editor_id, log_id, edited_at
		FROM wiki_page_revisions WHERE page_id = $1 ORDER BY version DESC`, page)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.WikiRevision
	for rows.Next() {
		var v types.WikiRevision
		var attrs []byte
		if err := rows.Scan(&v.Version, &v.Title, &v.Content, &attrs, &v.EditSource, &v.EditorID, &v.LogID, &v.EditedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(attrs, &v.Attributes)
		out = append(out, v)
	}
	return out, rows.Err()
}

// ---- footnotes ----

// ReplaceFootnotes replaces the footnotes of a page.
func (r *WikiRepo) ReplaceFootnotes(ctx context.Context, page uuid.UUID, fns []types.WikiFootnote) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM wiki_footnotes WHERE page_id = $1`, page); err != nil {
		return err
	}
	for _, f := range fns {
		st := f.Status
		if st == "" {
			st = types.FootnoteValid
		}
		if _, err := r.db.Exec(ctx, `INSERT INTO wiki_footnotes (page_id, n, document_id, gen, page_no, line_from, line_to, quote, citation_id, status)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`, page, f.N, f.DocumentID, f.Gen, f.PageNo, f.LineFrom, f.LineTo,
			cleanText(f.Quote), f.CitationID, st); err != nil {
			return err
		}
	}
	return nil
}

// Footnotes returns the footnotes of pages, keyed by page id, with file names.
func (r *WikiRepo) Footnotes(ctx context.Context, pages []uuid.UUID) (map[uuid.UUID][]types.WikiFootnote, error) {
	rows, err := r.db.Query(ctx, `SELECT f.page_id, f.n, f.document_id, f.gen, f.page_no, f.line_from, f.line_to, f.quote, f.citation_id,
		f.status, coalesce(d.file_name, '')
		FROM wiki_footnotes f LEFT JOIN documents d ON d.id = f.document_id
		WHERE f.page_id = ANY($1) ORDER BY f.page_id, f.n`, pages)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID][]types.WikiFootnote{}
	for rows.Next() {
		var pid uuid.UUID
		var f types.WikiFootnote
		if err := rows.Scan(&pid, &f.N, &f.DocumentID, &f.Gen, &f.PageNo, &f.LineFrom, &f.LineTo, &f.Quote, &f.CitationID, &f.Status, &f.FileName); err != nil {
			return nil, err
		}
		out[pid] = append(out[pid], f)
	}
	return out, rows.Err()
}

// FootnoteRef is a footnote with its page.
type FootnoteRef struct {
	PageID uuid.UUID
	Slug   string
	types.WikiFootnote
}

// CaseFootnotes returns footnotes of the case, optionally of one document
// (and generation when gen > 0).
func (r *WikiRepo) CaseFootnotes(ctx context.Context, caseID uuid.UUID, doc *uuid.UUID, gen int) ([]FootnoteRef, error) {
	var docArg any
	if doc != nil {
		docArg = *doc
	}
	rows, err := r.db.Query(ctx, `SELECT f.page_id, p.slug, f.n, f.document_id, f.gen, f.page_no, f.line_from, f.line_to, f.quote, f.citation_id, f.status
		FROM wiki_footnotes f JOIN wiki_pages p ON p.id = f.page_id
		WHERE p.case_id = $1 AND ($2::uuid IS NULL OR f.document_id = $2) AND ($3 <= 0 OR f.gen = $3)
		ORDER BY p.slug, f.n`, caseID, docArg, gen)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FootnoteRef
	for rows.Next() {
		var f FootnoteRef
		if err := rows.Scan(&f.PageID, &f.Slug, &f.N, &f.DocumentID, &f.Gen, &f.PageNo, &f.LineFrom, &f.LineTo, &f.Quote, &f.CitationID, &f.Status); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// SetFootnoteStatus marks footnotes of a page of the case valid or stale.
func (r *WikiRepo) SetFootnoteStatus(ctx context.Context, caseID, page uuid.UUID, ns []int, status string) error {
	_, err := r.db.Exec(ctx, `UPDATE wiki_footnotes f SET status = $4, checked_at = now()
		FROM wiki_pages p WHERE p.id = f.page_id AND p.case_id = $1 AND f.page_id = $2 AND f.n = ANY($3)`, caseID, page, ns, status)
	return err
}

// ---- links ----

// LinkRow is a stored link from a page.
type LinkRow struct {
	To         uuid.UUID
	Relation   string
	Attributes map[string]any
	FootnoteN  *int
}

// ReplaceLinks replaces the outgoing links of a page.
func (r *WikiRepo) ReplaceLinks(ctx context.Context, caseID, from uuid.UUID, links []LinkRow) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM wiki_links WHERE from_page = $1`, from); err != nil {
		return err
	}
	for _, l := range links {
		if l.To == from {
			continue
		}
		attrs, _ := cleanJSON(nonNilMap(l.Attributes))
		if _, err := r.db.Exec(ctx, `INSERT INTO wiki_links (case_id, from_page, to_page, relation, attributes, footnote_n)
			VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (from_page, to_page, relation) DO UPDATE SET attributes = EXCLUDED.attributes,
			footnote_n = EXCLUDED.footnote_n`, caseID, from, l.To, l.Relation, attrs, l.FootnoteN); err != nil {
			return err
		}
	}
	return nil
}

// AddLink inserts one link unless it exists.
func (r *WikiRepo) AddLink(ctx context.Context, caseID, from, to uuid.UUID, relation string) error {
	_, err := r.db.Exec(ctx, `INSERT INTO wiki_links (case_id, from_page, to_page, relation) VALUES ($1, $2, $3, $4)
		ON CONFLICT DO NOTHING`, caseID, from, to, relation)
	return err
}

// Links returns links of the case; page limits to links from (out) or to
// (in) one page, relation to one relation.
func (r *WikiRepo) Links(ctx context.Context, caseID uuid.UUID, page *uuid.UUID, direction, relation string) ([]types.WikiLink, error) {
	var a sqlArgs
	conds := []string{"l.case_id = " + a.add(caseID)}
	if page != nil {
		p := a.add(*page)
		switch direction {
		case "in":
			conds = append(conds, "l.to_page = "+p)
		case "out":
			conds = append(conds, "l.from_page = "+p)
		default:
			conds = append(conds, fmt.Sprintf("(l.from_page = %s OR l.to_page = %s)", p, p))
		}
	}
	if relation != "" {
		conds = append(conds, "l.relation = "+a.add(relation))
	}
	rows, err := r.db.Query(ctx, `SELECT f.slug, f.title, t.slug, t.title, l.relation, l.attributes, l.footnote_n
		FROM wiki_links l JOIN wiki_pages f ON f.id = l.from_page JOIN wiki_pages t ON t.id = l.to_page
		WHERE `+strings.Join(conds, " AND ")+` ORDER BY f.slug, t.slug, l.relation`, a.vals...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.WikiLink
	for rows.Next() {
		var l types.WikiLink
		if err := rows.Scan(&l.From, &l.FromTitle, &l.To, &l.ToTitle, &l.Relation, &metaScanner{&l.Attributes}, &l.FootnoteN); err != nil {
			return nil, err
		}
		if len(l.Attributes) == 0 {
			l.Attributes = nil
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ---- search ----

// WikiHit is a full-text match on a wiki page.
type WikiHit struct {
	PageID  uuid.UUID
	Slug    string
	Title   string
	Kind    string
	Snippet string
	Score   float64
}

// Search runs full-text (accent-insensitive) plus title trigram over the
// pages of a case.
func (r *WikiRepo) Search(ctx context.Context, caseID uuid.UUID, query string, limit int) ([]WikiHit, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.db.Query(ctx, `
		WITH q AS (SELECT websearch_to_tsquery('simple', unaccent_vi($2)) AS tq)
		SELECT p.id, p.slug, p.title, p.kind,
		       ts_headline('simple', p.summary || ' ' || p.content, q.tq, 'MaxWords=30, MinWords=8, ShortWord=2'),
		       ts_rank_cd(p.tsv, q.tq) + similarity(unaccent_vi(p.title), unaccent_vi($2)) AS score
		FROM wiki_pages p, q
		WHERE p.case_id = $1 AND (p.tsv @@ q.tq OR similarity(unaccent_vi(p.title), unaccent_vi($2)) > 0.3)
		ORDER BY score DESC LIMIT $3`, caseID, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WikiHit
	for rows.Next() {
		var h WikiHit
		if err := rows.Scan(&h.PageID, &h.Slug, &h.Title, &h.Kind, &h.Snippet, &h.Score); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ---- case wiki state, index ----

// BumpVersion increments cases.wiki_version and returns the new value.
func (r *WikiRepo) BumpVersion(ctx context.Context, caseID uuid.UUID) (int, error) {
	var v int
	err := r.db.QueryRow(ctx, `UPDATE cases SET wiki_version = wiki_version + 1, wiki_built_at = now(), updated_at = now()
		WHERE id = $1 RETURNING wiki_version`, caseID).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return v, err
}

// SetCaseWiki sets the wiki status and covered-document count of a case.
func (r *WikiRepo) SetCaseWiki(ctx context.Context, caseID uuid.UUID, status string, covered int) error {
	_, err := r.db.Exec(ctx, `UPDATE cases SET wiki_status = $2, wiki_docs_covered = $3, updated_at = now() WHERE id = $1`, caseID, status, covered)
	return err
}

// CoveredDocs counts the live documents of a case that have a source page.
func (r *WikiRepo) CoveredDocs(ctx context.Context, caseID uuid.UUID) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `SELECT count(*) FROM wiki_pages p JOIN documents d ON d.id = p.document_id AND d.deleted_at IS NULL
		WHERE p.case_id = $1 AND p.kind = 'source'`, caseID).Scan(&n)
	return n, err
}

// SaveIndex stores an index build and keeps the latest two versions.
func (r *WikiRepo) SaveIndex(ctx context.Context, idx types.WikiIndex) error {
	refs, _ := json.Marshal(idx.Refs)
	gens, _ := json.Marshal(idx.DocGens)
	if _, err := r.db.Exec(ctx, `INSERT INTO wiki_index (case_id, version, content, refs, token_count, doc_gens) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (case_id, version) DO UPDATE SET content = EXCLUDED.content, refs = EXCLUDED.refs, token_count = EXCLUDED.token_count,
		doc_gens = EXCLUDED.doc_gens, built_at = now()`, idx.CaseID, idx.Version, cleanText(idx.Content), refs, idx.TokenCount, gens); err != nil {
		return err
	}
	_, err := r.db.Exec(ctx, `DELETE FROM wiki_index WHERE case_id = $1 AND version < (SELECT max(version) - 1 FROM wiki_index WHERE case_id = $1)`, idx.CaseID)
	return err
}

// LatestIndex returns the newest stored index of a case.
func (r *WikiRepo) LatestIndex(ctx context.Context, caseID uuid.UUID) (*types.WikiIndex, error) {
	var idx types.WikiIndex
	var refs, gens []byte
	err := r.db.QueryRow(ctx, `SELECT case_id, version, content, refs, token_count, doc_gens, built_at FROM wiki_index
		WHERE case_id = $1 ORDER BY version DESC LIMIT 1`, caseID).Scan(&idx.CaseID, &idx.Version, &idx.Content, &refs, &idx.TokenCount, &gens, &idx.BuiltAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(refs, &idx.Refs)
	_ = json.Unmarshal(gens, &idx.DocGens)
	return &idx, nil
}

// ---- log ----

// AppendLog writes one log line and returns its id.
func (r *WikiRepo) AppendLog(ctx context.Context, e types.WikiLogEntry) (int64, error) {
	pages := e.Pages
	if pages == nil {
		pages = []string{}
	}
	actor := e.Actor
	if actor == "" {
		actor = "system"
	}
	var id int64
	err := r.db.QueryRow(ctx, `INSERT INTO wiki_log (case_id, op, ref, document_id, pages, summary, actor, llm_calls, tokens_in, tokens_out)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`, e.CaseID, e.Op, cleanText(e.Ref), e.DocumentID, pages,
		cleanText(e.Summary), actor, e.LLMCalls, e.TokensIn, e.TokensOut).Scan(&id)
	return id, err
}

// SetLogResult fills the touched pages and summary of a log line written
// before the work was known.
func (r *WikiRepo) SetLogResult(ctx context.Context, id int64, pages []string, summary string) error {
	if pages == nil {
		pages = []string{}
	}
	_, err := r.db.Exec(ctx, `UPDATE wiki_log SET pages = $2, summary = $3 WHERE id = $1`, id, pages, cleanText(summary))
	return err
}

// LogFilter selects log lines.
type LogFilter struct {
	Op         string
	DocumentID *uuid.UUID
	Before     *time.Time
	After      int64 // id cursor for event streams
	Limit      int
}

// Log returns log lines of a case, newest first (oldest first with After).
func (r *WikiRepo) Log(ctx context.Context, caseID uuid.UUID, f LogFilter) ([]types.WikiLogEntry, error) {
	var a sqlArgs
	conds := []string{"case_id = " + a.add(caseID)}
	if f.Op != "" {
		conds = append(conds, "op = "+a.add(f.Op))
	}
	if f.DocumentID != nil {
		conds = append(conds, "document_id = "+a.add(*f.DocumentID))
	}
	if f.Before != nil {
		conds = append(conds, "at < "+a.add(*f.Before))
	}
	order := "at DESC, id DESC"
	if f.After > 0 {
		conds = append(conds, "id > "+a.add(f.After))
		order = "id"
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.Query(ctx, `SELECT id, case_id, at, op, ref, document_id, pages, summary, actor, llm_calls, tokens_in, tokens_out
		FROM wiki_log WHERE `+strings.Join(conds, " AND ")+fmt.Sprintf(` ORDER BY %s LIMIT %d`, order, limit), a.vals...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.WikiLogEntry
	for rows.Next() {
		var e types.WikiLogEntry
		if err := rows.Scan(&e.ID, &e.CaseID, &e.At, &e.Op, &e.Ref, &e.DocumentID, &e.Pages, &e.Summary, &e.Actor, &e.LLMCalls, &e.TokensIn, &e.TokensOut); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LastLogID returns the newest log id of a case (0 when none).
func (r *WikiRepo) LastLogID(ctx context.Context, caseID uuid.UUID) (int64, error) {
	var id int64
	err := r.db.QueryRow(ctx, `SELECT coalesce(max(id), 0) FROM wiki_log WHERE case_id = $1`, caseID).Scan(&id)
	return id, err
}

// ---- lint ----

// AddIssue records an open issue unless the same fingerprint is open.
func (r *WikiRepo) AddIssue(ctx context.Context, is types.WikiLintIssue, fingerprint string) (bool, error) {
	detail, _ := cleanJSON(nonNilMap(is.Detail))
	ids := is.PageIDs
	if ids == nil {
		ids = []uuid.UUID{}
	}
	tag, err := r.db.Exec(ctx, `INSERT INTO wiki_lint_issues (case_id, kind, page_ids, detail, fingerprint) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (case_id, fingerprint) WHERE status = 'open' AND fingerprint <> '' DO NOTHING`, is.CaseID, is.Kind, ids, detail, fingerprint)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ResolveMissing marks open issues of these kinds fixed when their
// fingerprint was not found again.
func (r *WikiRepo) ResolveMissing(ctx context.Context, caseID uuid.UUID, kinds []string, seen []string) error {
	if seen == nil {
		seen = []string{}
	}
	_, err := r.db.Exec(ctx, `UPDATE wiki_lint_issues SET status = 'fixed', resolved_at = now()
		WHERE case_id = $1 AND status = 'open' AND kind = ANY($2) AND NOT (fingerprint = ANY($3))`, caseID, kinds, seen)
	return err
}

// Issues lists lint issues of a case.
func (r *WikiRepo) Issues(ctx context.Context, caseID uuid.UUID, status, kind string, limit int) ([]types.WikiLintIssue, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := r.db.Query(ctx, `SELECT i.id, i.case_id, i.kind, i.page_ids, i.detail, i.status, i.found_at, i.resolved_at, i.resolved_by,
		coalesce((SELECT array_agg(p.slug ORDER BY p.slug) FROM wiki_pages p WHERE p.id = ANY(i.page_ids)), '{}')
		FROM wiki_lint_issues i WHERE i.case_id = $1 AND ($2 = '' OR i.status = $2) AND ($3 = '' OR i.kind = $3)
		ORDER BY i.found_at DESC LIMIT $4`, caseID, status, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.WikiLintIssue
	for rows.Next() {
		var is types.WikiLintIssue
		if err := rows.Scan(&is.ID, &is.CaseID, &is.Kind, &is.PageIDs, &metaScanner{&is.Detail}, &is.Status, &is.FoundAt, &is.ResolvedAt,
			&is.ResolvedBy, &is.Pages); err != nil {
			return nil, err
		}
		out = append(out, is)
	}
	return out, rows.Err()
}

// CountOpenIssues counts open lint issues of a case.
func (r *WikiRepo) CountOpenIssues(ctx context.Context, caseID uuid.UUID) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `SELECT count(*) FROM wiki_lint_issues WHERE case_id = $1 AND status = 'open'`, caseID).Scan(&n)
	return n, err
}

// SetIssueStatus resolves or dismisses an issue of the case.
func (r *WikiRepo) SetIssueStatus(ctx context.Context, caseID uuid.UUID, id int64, status string, by *uuid.UUID) error {
	tag, err := r.db.Exec(ctx, `UPDATE wiki_lint_issues SET status = $3, resolved_at = CASE WHEN $3 = 'open' THEN NULL ELSE now() END,
		resolved_by = $4 WHERE case_id = $1 AND id = $2`, caseID, id, status, by)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func nonNilAttrs(m map[string]types.WikiAttribute) map[string]types.WikiAttribute {
	if m == nil {
		return map[string]types.WikiAttribute{}
	}
	return m
}
