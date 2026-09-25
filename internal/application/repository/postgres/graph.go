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

// GraphRepo persists graph schemas, entities, relations, mentions and wiki
// pages (§7).
type GraphRepo struct{ pool *pgxpool.Pool }

// ---- schemas ----

// UpsertSchema stores a schema version (idempotent on name+version).
func (r *GraphRepo) UpsertSchema(ctx context.Context, s types.GraphSchema) (types.GraphSchema, error) {
	spec, err := json.Marshal(s)
	if err != nil {
		return s, err
	}
	err = r.pool.QueryRow(ctx, `INSERT INTO graph_schemas (name, version, spec) VALUES ($1, $2, $3)
		ON CONFLICT (name, version) DO UPDATE SET spec = EXCLUDED.spec RETURNING id, created_at`, s.Name, s.Version, spec).Scan(&s.ID, &s.CreatedAt)
	return s, err
}

func scanSchema(row pgx.Row) (types.GraphSchema, error) {
	var s types.GraphSchema
	var id uuid.UUID
	var created time.Time
	var spec []byte
	err := row.Scan(&id, &spec, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrNotFound
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(spec, &s); err != nil {
		return s, err
	}
	s.ID, s.CreatedAt = id, created
	return s, nil
}

// LatestSchema returns the highest version of a named schema.
func (r *GraphRepo) LatestSchema(ctx context.Context, name string) (types.GraphSchema, error) {
	return scanSchema(r.pool.QueryRow(ctx, `SELECT id, spec, created_at FROM graph_schemas WHERE name = $1 ORDER BY version DESC LIMIT 1`, name))
}

// SchemaByID returns one schema version.
func (r *GraphRepo) SchemaByID(ctx context.Context, id uuid.UUID) (types.GraphSchema, error) {
	return scanSchema(r.pool.QueryRow(ctx, `SELECT id, spec, created_at FROM graph_schemas WHERE id = $1`, id))
}

// Schemas lists schemas (every version when name is set, else latest per name).
func (r *GraphRepo) Schemas(ctx context.Context, name string) ([]types.GraphSchema, error) {
	q := `SELECT DISTINCT ON (name) id, spec, created_at FROM graph_schemas ORDER BY name, version DESC`
	args := []any{}
	if name != "" {
		q = `SELECT id, spec, created_at FROM graph_schemas WHERE name = $1 ORDER BY version DESC`
		args = append(args, name)
	}
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.GraphSchema
	for rows.Next() {
		s, err := scanSchema(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ---- entities, relations, mentions ----

// EntityUpsert is an extracted entity to merge into the KB graph.
type EntityUpsert struct {
	KBID       uuid.UUID
	SchemaID   uuid.UUID
	Type       string
	Name       string
	NormKey    string
	Attributes map[string]any
	Source     string // document id, recorded in attribute history
}

// UpsertEntity inserts or merges an entity by (kb, type, norm_key). A new
// value for an existing attribute is kept in attributes_history and flags a
// conflict instead of silently overwriting (§7.4).
func (r *GraphRepo) UpsertEntity(ctx context.Context, e EntityUpsert) (uuid.UUID, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)
	var id uuid.UUID
	var attrsRaw, histRaw []byte
	var aliases []string
	var name string
	err = tx.QueryRow(ctx, `SELECT id, name, aliases, attributes, attributes_history FROM kg_entities
		WHERE kb_id = $1 AND type = $2 AND norm_key = $3 FOR UPDATE`, e.KBID, e.Type, e.NormKey).Scan(&id, &name, &aliases, &attrsRaw, &histRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		attrs, _ := cleanJSON(nonNilMap(e.Attributes))
		err = tx.QueryRow(ctx, `INSERT INTO kg_entities (kb_id, schema_id, type, name, norm_key, attributes)
			VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (kb_id, type, norm_key) DO UPDATE SET updated_at = now() RETURNING id`,
			e.KBID, e.SchemaID, e.Type, cleanText(e.Name), e.NormKey, attrs).Scan(&id)
		if err != nil {
			return uuid.Nil, err
		}
		return id, tx.Commit(ctx)
	}
	if err != nil {
		return uuid.Nil, err
	}
	attrs := map[string]any{}
	_ = json.Unmarshal(attrsRaw, &attrs)
	var hist []map[string]any
	_ = json.Unmarshal(histRaw, &hist)
	conflict := false
	for k, v := range e.Attributes {
		old, ok := attrs[k]
		if !ok || old == nil || fmt.Sprint(old) == "" {
			attrs[k] = v
			continue
		}
		if fmt.Sprint(old) != fmt.Sprint(v) {
			conflict = true
			hist = append(hist, map[string]any{"attribute": k, "value": v, "source": e.Source, "at": time.Now().UTC().Format(time.RFC3339)})
		}
	}
	if e.Name != name && !contains(aliases, e.Name) {
		aliases = append(aliases, cleanText(e.Name))
	}
	ab, _ := cleanJSON(attrs)
	hb, _ := cleanJSON(hist)
	_, err = tx.Exec(ctx, `UPDATE kg_entities SET attributes = $2, attributes_history = $3, aliases = $4,
		conflict = conflict OR $5, updated_at = now() WHERE id = $1`, id, ab, hb, aliases, conflict)
	if err != nil {
		return uuid.Nil, err
	}
	return id, tx.Commit(ctx)
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// UpsertRelation inserts or merges a relation.
func (r *GraphRepo) UpsertRelation(ctx context.Context, kb uuid.UUID, typ string, src, dst uuid.UUID, attrs map[string]any) (uuid.UUID, error) {
	ab, _ := cleanJSON(nonNilMap(attrs))
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `INSERT INTO kg_relations (kb_id, type, source_id, target_id, attributes) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (kb_id, type, source_id, target_id) DO UPDATE SET attributes = kg_relations.attributes || EXCLUDED.attributes
		RETURNING id`, kb, typ, src, dst, ab).Scan(&id)
	return id, err
}

// InsertMention records evidence.
func (r *GraphRepo) InsertMention(ctx context.Context, m types.Mention, gen int) error {
	spans, _ := json.Marshal(m.SourceSpans)
	_, err := r.pool.Exec(ctx, `INSERT INTO kg_mentions (entity_id, relation_id, document_id, gen, section_id, evidence, source_spans)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, m.EntityID, m.RelationID, m.DocumentID, gen, m.SectionID, cleanText(m.Evidence), spans)
	return err
}

// DeleteDocumentMentions removes a document's mentions (re-extraction) and
// entities/relations left without any evidence.
func (r *GraphRepo) DeleteDocumentMentions(ctx context.Context, doc uuid.UUID) error {
	if _, err := r.pool.Exec(ctx, `DELETE FROM kg_mentions WHERE document_id = $1`, doc); err != nil {
		return err
	}
	return r.PruneOrphans(ctx)
}

// PruneOrphans deletes relations and entities that no mention supports.
func (r *GraphRepo) PruneOrphans(ctx context.Context) error {
	if _, err := r.pool.Exec(ctx, `DELETE FROM kg_relations rel WHERE NOT EXISTS (SELECT 1 FROM kg_mentions m WHERE m.relation_id = rel.id)`); err != nil {
		return err
	}
	_, err := r.pool.Exec(ctx, `DELETE FROM kg_entities e WHERE NOT EXISTS (SELECT 1 FROM kg_mentions m WHERE m.entity_id = e.id)
		AND NOT EXISTS (SELECT 1 FROM kg_relations rel WHERE rel.source_id = e.id OR rel.target_id = e.id)`)
	return err
}

const entityCols = `e.id, e.kb_id, e.type, e.name, e.aliases, e.attributes, e.conflict, e.summary,
	(SELECT count(*) FROM kg_mentions m WHERE m.entity_id = e.id),
	coalesce((SELECT w.slug FROM wiki_pages w WHERE w.entity_id = e.id LIMIT 1), '')`

func scanEntities(rows pgx.Rows) ([]types.Entity, error) {
	defer rows.Close()
	var out []types.Entity
	for rows.Next() {
		var e types.Entity
		if err := rows.Scan(&e.ID, &e.KBID, &e.Type, &e.Name, &e.Aliases, &metaScanner{&e.Attributes}, &e.Conflict, &e.Summary, &e.Mentions, &e.WikiSlug); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Entity returns one entity with its KB owner.
func (r *GraphRepo) Entity(ctx context.Context, id uuid.UUID) (types.Entity, uuid.UUID, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+entityCols+`, kb.owner_id FROM kg_entities e JOIN knowledge_bases kb ON kb.id = e.kb_id WHERE e.id = $1`, id)
	if err != nil {
		return types.Entity{}, uuid.Nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return types.Entity{}, uuid.Nil, ErrNotFound
	}
	var e types.Entity
	var owner uuid.UUID
	if err := rows.Scan(&e.ID, &e.KBID, &e.Type, &e.Name, &e.Aliases, &metaScanner{&e.Attributes}, &e.Conflict, &e.Summary, &e.Mentions, &e.WikiSlug, &owner); err != nil {
		return e, owner, err
	}
	return e, owner, nil
}

// EntitiesByID loads several entities.
func (r *GraphRepo) EntitiesByID(ctx context.Context, ids []uuid.UUID) ([]types.Entity, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+entityCols+` FROM kg_entities e WHERE e.id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	return scanEntities(rows)
}

// SearchEntities finds entities by name/alias (accent-insensitive, trigram).
func (r *GraphRepo) SearchEntities(ctx context.Context, kb uuid.UUID, query, typ string, limit int) ([]types.Entity, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx, `SELECT `+entityCols+` FROM kg_entities e
		WHERE e.kb_id = $1 AND ($3 = '' OR e.type = $3)
		  AND ($2 = '' OR unaccent_vi(e.name) LIKE '%' || unaccent_vi($2) || '%' OR unaccent_vi($2) <% unaccent_vi(e.name)
		       OR EXISTS (SELECT 1 FROM unnest(e.aliases) a WHERE unaccent_vi(a) LIKE '%' || unaccent_vi($2) || '%')
		       OR unaccent_vi(e.attributes::text) LIKE '%' || unaccent_vi($2) || '%')
		ORDER BY (CASE WHEN $2 = '' THEN 0 ELSE word_similarity(unaccent_vi($2), unaccent_vi(e.name)) END) DESC, e.name
		LIMIT $4`, kb, query, typ, limit)
	if err != nil {
		return nil, err
	}
	return scanEntities(rows)
}

// SimilarPairs lists same-type entity pairs with similar names (resolve).
func (r *GraphRepo) SimilarPairs(ctx context.Context, kb uuid.UUID, threshold float64, limit int) ([][2]types.Entity, error) {
	rows, err := r.pool.Query(ctx, `SELECT a.id, b.id FROM kg_entities a JOIN kg_entities b
		ON a.kb_id = b.kb_id AND a.type = b.type AND a.id < b.id
		WHERE a.kb_id = $1 AND similarity(unaccent_vi(a.name), unaccent_vi(b.name)) >= $2 LIMIT $3`, kb, threshold, limit)
	if err != nil {
		return nil, err
	}
	var pairs [][2]uuid.UUID
	for rows.Next() {
		var p [2]uuid.UUID
		if err := rows.Scan(&p[0], &p[1]); err != nil {
			rows.Close()
			return nil, err
		}
		pairs = append(pairs, p)
	}
	rows.Close()
	var out [][2]types.Entity
	for _, p := range pairs {
		es, err := r.EntitiesByID(ctx, p[:])
		if err != nil || len(es) != 2 {
			continue
		}
		out = append(out, [2]types.Entity{es[0], es[1]})
	}
	return out, nil
}

// MergeEntities moves mentions and relations of from into into, then deletes
// from and records its name as an alias.
func (r *GraphRepo) MergeEntities(ctx context.Context, into, from uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	stmts := []string{
		`UPDATE kg_mentions SET entity_id = $1 WHERE entity_id = $2`,
		`UPDATE kg_relations r SET source_id = $1 WHERE source_id = $2 AND NOT EXISTS (
			SELECT 1 FROM kg_relations x WHERE x.kb_id = r.kb_id AND x.type = r.type AND x.source_id = $1 AND x.target_id = r.target_id)`,
		`UPDATE kg_relations r SET target_id = $1 WHERE target_id = $2 AND NOT EXISTS (
			SELECT 1 FROM kg_relations x WHERE x.kb_id = r.kb_id AND x.type = r.type AND x.target_id = $1 AND x.source_id = r.source_id)`,
		`UPDATE kg_entities SET aliases = array_append(aliases, (SELECT name FROM kg_entities WHERE id = $2)),
			attributes = (SELECT attributes FROM kg_entities WHERE id = $2) || attributes WHERE id = $1`,
		`DELETE FROM kg_entities WHERE id = $2`,
	}
	for _, q := range stmts {
		if _, err := tx.Exec(ctx, q, into, from); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// Mentions lists evidence for an entity or relation.
func (r *GraphRepo) Mentions(ctx context.Context, entity *uuid.UUID, relation *uuid.UUID, limit int) ([]types.Mention, error) {
	rows, err := r.pool.Query(ctx, `SELECT m.id, m.entity_id, m.relation_id, m.document_id, d.file_name, m.section_id, m.evidence, m.source_spans
		FROM kg_mentions m JOIN documents d ON d.id = m.document_id AND d.deleted_at IS NULL
		WHERE ($1::uuid IS NULL OR m.entity_id = $1) AND ($2::uuid IS NULL OR m.relation_id = $2)
		ORDER BY m.id LIMIT $3`, entity, relation, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.Mention
	for rows.Next() {
		var m types.Mention
		var spans []byte
		if err := rows.Scan(&m.ID, &m.EntityID, &m.RelationID, &m.DocumentID, &m.FileName, &m.SectionID, &m.Evidence, &spans); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(spans, &m.SourceSpans)
		out = append(out, m)
	}
	return out, rows.Err()
}

// Relations lists relations touching an entity.
func (r *GraphRepo) Relations(ctx context.Context, entity uuid.UUID) ([]types.Relation, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, type, source_id, target_id, attributes FROM kg_relations WHERE source_id = $1 OR target_id = $1 ORDER BY type`, entity)
	if err != nil {
		return nil, err
	}
	return scanRelations(rows)
}

func scanRelations(rows pgx.Rows) ([]types.Relation, error) {
	defer rows.Close()
	var out []types.Relation
	for rows.Next() {
		var rel types.Relation
		if err := rows.Scan(&rel.ID, &rel.Type, &rel.SourceID, &rel.TargetID, &metaScanner{&rel.Attributes}); err != nil {
			return nil, err
		}
		out = append(out, rel)
	}
	return out, rows.Err()
}

// Neighborhood returns entities within depth hops (≤3) and their relations.
func (r *GraphRepo) Neighborhood(ctx context.Context, start uuid.UUID, relTypes []string, depth, limit int) (*types.Subgraph, error) {
	depth = max(1, min(depth, 3))
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := r.pool.Query(ctx, `
		WITH RECURSIVE walk(id, d) AS (
			SELECT $1::uuid, 0
			UNION
			SELECT CASE WHEN r.source_id = w.id THEN r.target_id ELSE r.source_id END, w.d + 1
			FROM walk w JOIN kg_relations r ON (r.source_id = w.id OR r.target_id = w.id)
			WHERE w.d < $2 AND (cardinality($3::text[]) = 0 OR r.type = ANY($3))
		)
		SELECT DISTINCT id FROM walk LIMIT $4`, start, depth, relTypes, limit)
	if err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	ents, err := r.EntitiesByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	relRows, err := r.pool.Query(ctx, `SELECT id, type, source_id, target_id, attributes FROM kg_relations
		WHERE source_id = ANY($1) AND target_id = ANY($1) AND (cardinality($2::text[]) = 0 OR type = ANY($2))`, ids, relTypes)
	if err != nil {
		return nil, err
	}
	rels, err := scanRelations(relRows)
	if err != nil {
		return nil, err
	}
	return &types.Subgraph{Entities: ents, Relations: rels}, nil
}

// Path finds a shortest relation path between two entities (≤ maxDepth).
func (r *GraphRepo) Path(ctx context.Context, from, to uuid.UUID, maxDepth int) (*types.Subgraph, error) {
	maxDepth = max(1, min(maxDepth, 4))
	var path []uuid.UUID
	err := r.pool.QueryRow(ctx, `
		WITH RECURSIVE walk(id, path, d) AS (
			SELECT $1::uuid, ARRAY[$1::uuid], 0
			UNION ALL
			SELECT CASE WHEN r.source_id = w.id THEN r.target_id ELSE r.source_id END,
			       w.path || CASE WHEN r.source_id = w.id THEN r.target_id ELSE r.source_id END, w.d + 1
			FROM walk w JOIN kg_relations r ON (r.source_id = w.id OR r.target_id = w.id)
			WHERE w.d < $3 AND NOT (CASE WHEN r.source_id = w.id THEN r.target_id ELSE r.source_id END) = ANY(w.path)
		)
		SELECT path FROM walk WHERE id = $2 ORDER BY d LIMIT 1`, from, to, maxDepth).Scan(&path)
	if errors.Is(err, pgx.ErrNoRows) {
		return &types.Subgraph{}, nil
	}
	if err != nil {
		return nil, err
	}
	ents, err := r.EntitiesByID(ctx, path)
	if err != nil {
		return nil, err
	}
	var rels []types.Relation
	for i := 0; i+1 < len(path); i++ {
		rows, err := r.pool.Query(ctx, `SELECT id, type, source_id, target_id, attributes FROM kg_relations
			WHERE (source_id = $1 AND target_id = $2) OR (source_id = $2 AND target_id = $1) LIMIT 1`, path[i], path[i+1])
		if err != nil {
			return nil, err
		}
		rs, err := scanRelations(rows)
		if err != nil {
			return nil, err
		}
		rels = append(rels, rs...)
	}
	return &types.Subgraph{Entities: ents, Relations: rels}, nil
}

// WikiCandidates lists entities of a KB worth a wiki page.
func (r *GraphRepo) WikiCandidates(ctx context.Context, kb uuid.UUID, ids []uuid.UUID, minMentions int, pageTypes []string) ([]types.Entity, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+entityCols+` FROM kg_entities e WHERE e.kb_id = $1 AND (cardinality($2::uuid[]) = 0 OR e.id = ANY($2))
		AND ((SELECT count(*) FROM kg_mentions m WHERE m.entity_id = e.id) >= $3 OR e.type = ANY($4))`, kb, ids, minMentions, pageTypes)
	if err != nil {
		return nil, err
	}
	return scanEntities(rows)
}

// ---- wiki ----

const wikiCols = `id, kb_id, entity_id, slug, title, page_type, summary, content, aliases, source_refs, in_links, out_links, version, last_edit_source, updated_at`

func scanWiki(rows pgx.Rows) ([]types.WikiPage, error) {
	defer rows.Close()
	var out []types.WikiPage
	for rows.Next() {
		var w types.WikiPage
		if err := rows.Scan(&w.ID, &w.KBID, &w.EntityID, &w.Slug, &w.Title, &w.PageType, &w.Summary, &w.Content, &w.Aliases,
			&w.SourceRefs, &w.InLinks, &w.OutLinks, &w.Version, &w.LastEditSource, &w.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// WikiPage returns a page by slug.
func (r *GraphRepo) WikiPage(ctx context.Context, kb uuid.UUID, slug string) (*types.WikiPage, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+wikiCols+` FROM wiki_pages WHERE kb_id = $1 AND slug = $2`, kb, slug)
	if err != nil {
		return nil, err
	}
	ws, err := scanWiki(rows)
	if err != nil {
		return nil, err
	}
	if len(ws) == 0 {
		return nil, ErrNotFound
	}
	return &ws[0], nil
}

// WikiPageByEntity returns the page of an entity.
func (r *GraphRepo) WikiPageByEntity(ctx context.Context, entity uuid.UUID) (*types.WikiPage, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+wikiCols+` FROM wiki_pages WHERE entity_id = $1`, entity)
	if err != nil {
		return nil, err
	}
	ws, err := scanWiki(rows)
	if err != nil {
		return nil, err
	}
	if len(ws) == 0 {
		return nil, ErrNotFound
	}
	return &ws[0], nil
}

// WikiPages lists a KB's pages (content omitted when brief).
func (r *GraphRepo) WikiPages(ctx context.Context, kb uuid.UUID) ([]types.WikiPage, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+wikiCols+` FROM wiki_pages WHERE kb_id = $1 ORDER BY page_type, title`, kb)
	if err != nil {
		return nil, err
	}
	return scanWiki(rows)
}

// SaveWikiPage inserts or updates a page and records a revision.
func (r *GraphRepo) SaveWikiPage(ctx context.Context, w types.WikiPage, editor *uuid.UUID) (types.WikiPage, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return w, err
	}
	defer tx.Rollback(ctx)
	nz := func(v []string) []string {
		if v == nil {
			return []string{}
		}
		return v
	}
	err = tx.QueryRow(ctx, `INSERT INTO wiki_pages (kb_id, entity_id, slug, title, page_type, summary, content, aliases, source_refs, out_links, last_edit_source)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (kb_id, slug) DO UPDATE SET entity_id = EXCLUDED.entity_id, title = EXCLUDED.title, page_type = EXCLUDED.page_type,
			summary = EXCLUDED.summary, content = EXCLUDED.content, aliases = EXCLUDED.aliases, source_refs = EXCLUDED.source_refs,
			out_links = EXCLUDED.out_links, last_edit_source = EXCLUDED.last_edit_source, version = wiki_pages.version + 1, updated_at = now()
		RETURNING id, version, updated_at`,
		w.KBID, w.EntityID, w.Slug, cleanText(w.Title), w.PageType, cleanText(w.Summary), cleanText(w.Content), nz(w.Aliases), nz(w.SourceRefs), nz(w.OutLinks), w.LastEditSource).
		Scan(&w.ID, &w.Version, &w.UpdatedAt)
	if err != nil {
		return w, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO wiki_page_revisions (page_id, version, content, summary, edit_source, editor_id) VALUES ($1, $2, $3, $4, $5, $6)`,
		w.ID, w.Version, cleanText(w.Content), cleanText(w.Summary), w.LastEditSource, editor); err != nil {
		return w, err
	}
	return w, tx.Commit(ctx)
}

// SetWikiLinks stores computed in/out links and the cleaned content.
func (r *GraphRepo) SetWikiLinks(ctx context.Context, id uuid.UUID, content string, in, out []string) error {
	if in == nil {
		in = []string{}
	}
	if out == nil {
		out = []string{}
	}
	_, err := r.pool.Exec(ctx, `UPDATE wiki_pages SET content = $2, in_links = $3, out_links = $4 WHERE id = $1`, id, cleanText(content), in, out)
	return err
}

// WikiRevision is one stored revision.
type WikiRevision struct {
	Version    int        `json:"version"`
	Summary    string     `json:"summary"`
	Content    string     `json:"content"`
	EditSource string     `json:"edit_source"`
	EditorID   *uuid.UUID `json:"editor_id,omitempty"`
	EditedAt   time.Time  `json:"edited_at"`
}

// WikiRevisions lists a page's revisions, newest first.
func (r *GraphRepo) WikiRevisions(ctx context.Context, page uuid.UUID) ([]WikiRevision, error) {
	rows, err := r.pool.Query(ctx, `SELECT version, summary, content, edit_source, editor_id, edited_at FROM wiki_page_revisions WHERE page_id = $1 ORDER BY version DESC`, page)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WikiRevision
	for rows.Next() {
		var w WikiRevision
		if err := rows.Scan(&w.Version, &w.Summary, &w.Content, &w.EditSource, &w.EditorID, &w.EditedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// TryKBLock takes a session-less advisory try-lock inside a transaction and
// runs fn while held; it returns false when another worker holds it.
func (r *GraphRepo) TryKBLock(ctx context.Context, kb uuid.UUID, fn func(ctx context.Context) error) (bool, error) {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()
	var ok bool
	key := "wiki:" + kb.String()
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 11))`, key).Scan(&ok); err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	defer conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtextextended($1, 11))`, key)
	return true, fn(ctx)
}
