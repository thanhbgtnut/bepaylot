package postgres

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thanhenti/bepaylot/internal/types"
)

// IndexRepo persists sections and the vectorless document tree.
type IndexRepo struct{ pool *pgxpool.Pool }

// ReplaceSections swaps the sections of (doc, gen).
func (r *IndexRepo) ReplaceSections(ctx context.Context, doc uuid.UUID, gen int, secs []types.Section) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM sections WHERE document_id = $1 AND gen = $2`, doc, gen); err != nil {
		return err
	}
	rows := make([][]any, len(secs))
	for i, s := range secs {
		spans, _ := json.Marshal(s.SourceSpans)
		hp := s.HeadingPath
		if hp == nil {
			hp = []string{}
		}
		rows[i] = []any{s.ID, doc, s.KBID, gen, s.Seq, s.Kind, cleanText(s.Content), hp, cleanText(strings.Join(hp, " › ")),
			s.PageStart, s.PageEnd, s.LineFrom, s.LineTo, s.DocMdStart, s.DocMdEnd, spans, s.TokenCount}
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"sections"}, []string{"id", "document_id", "kb_id", "gen", "seq", "kind", "content",
		"heading_path", "heading_text", "page_start", "page_end", "line_from", "line_to", "doc_md_start", "doc_md_end", "source_spans", "token_count"},
		pgx.CopyFromRows(rows)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

const sectionCols = `id, document_id, kb_id, gen, seq, kind, content, heading_path, page_start, page_end, line_from, line_to,
	doc_md_start, doc_md_end, source_spans, token_count`

func scanSections(rows pgx.Rows) ([]types.Section, error) {
	defer rows.Close()
	var out []types.Section
	for rows.Next() {
		var s types.Section
		var spans []byte
		if err := rows.Scan(&s.ID, &s.DocumentID, &s.KBID, &s.Gen, &s.Seq, &s.Kind, &s.Content, &s.HeadingPath, &s.PageStart, &s.PageEnd,
			&s.LineFrom, &s.LineTo, &s.DocMdStart, &s.DocMdEnd, &spans, &s.TokenCount); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(spans, &s.SourceSpans)
		out = append(out, s)
	}
	return out, rows.Err()
}

// Sections lists the sections of (doc, gen) in order.
func (r *IndexRepo) Sections(ctx context.Context, doc uuid.UUID, gen int) ([]types.Section, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+sectionCols+` FROM sections WHERE document_id = $1 AND gen = $2 ORDER BY seq`, doc, gen)
	if err != nil {
		return nil, err
	}
	return scanSections(rows)
}

// Section returns one section.
func (r *IndexRepo) Section(ctx context.Context, id uuid.UUID) (types.Section, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+sectionCols+` FROM sections WHERE id = $1`, id)
	if err != nil {
		return types.Section{}, err
	}
	out, err := scanSections(rows)
	if err != nil {
		return types.Section{}, err
	}
	if len(out) == 0 {
		return types.Section{}, ErrNotFound
	}
	return out[0], nil
}

// SectionHit is a keyword hit on a section.
type SectionHit struct {
	types.Section
	Score   float64
	Snippet string
}

// SearchSections ranks sections of the given documents (current gen only).
func (r *IndexRepo) SearchSections(ctx context.Context, docs []uuid.UUID, query string, pageFrom, pageTo, limit int) ([]SectionHit, error) {
	if pageTo <= 0 {
		pageTo = 1 << 30
	}
	rows, err := r.pool.Query(ctx, `
		WITH q AS (SELECT websearch_to_tsquery('simple', unaccent_vi($2)) AS tq,
		                  websearch_to_tsquery('simple', lower($2)) AS tqx, unaccent_vi($2) AS t)
		SELECT `+prefixCols("s.", sectionCols)+`,
		       ts_rank_cd(s.tsv, q.tq) + 0.5 * ts_rank_cd(s.tsv_exact, q.tqx) + 0.3 * word_similarity(q.t, unaccent_vi(s.content)) AS score,
		       ts_headline('simple', s.content, q.tq, 'StartSel=<mark>,StopSel=</mark>,MaxFragments=2,MaxWords=30,MinWords=10')
		FROM sections s JOIN documents d ON d.id = s.document_id AND d.gen = s.gen, q
		WHERE s.document_id = ANY($1) AND s.page_end >= $3 AND s.page_start <= $4
		  AND (s.tsv @@ q.tq OR q.t <% unaccent_vi(s.content))
		ORDER BY score DESC LIMIT $5`, docs, query, pageFrom, pageTo, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SectionHit
	for rows.Next() {
		var h SectionHit
		var spans []byte
		if err := rows.Scan(&h.ID, &h.DocumentID, &h.KBID, &h.Gen, &h.Seq, &h.Kind, &h.Content, &h.HeadingPath, &h.PageStart, &h.PageEnd,
			&h.LineFrom, &h.LineTo, &h.DocMdStart, &h.DocMdEnd, &spans, &h.TokenCount, &h.Score, &h.Snippet); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(spans, &h.SourceSpans)
		out = append(out, h)
	}
	return out, rows.Err()
}

// ReplaceTree swaps the tree of (doc, gen). Nodes must be ordered so that a
// parent precedes its children.
func (r *IndexRepo) ReplaceTree(ctx context.Context, doc uuid.UUID, gen int, nodes []types.TreeNode) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM doc_tree_nodes WHERE document_id = $1 AND gen = $2`, doc, gen); err != nil {
		return err
	}
	rows := make([][]any, len(nodes))
	for i, n := range nodes {
		ids := n.SectionIDs
		if ids == nil {
			ids = []uuid.UUID{}
		}
		rows[i] = []any{n.ID, doc, gen, n.ParentID, n.ShortID, n.Ord, n.Level, cleanText(n.Title), n.Origin, n.PageStart, n.PageEnd,
			cleanText(n.Summary), ids, n.TokenCount}
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"doc_tree_nodes"}, []string{"id", "document_id", "gen", "parent_id", "short_id", "ord",
		"level", "title", "origin", "page_start", "page_end", "summary", "section_ids", "token_count"}, pgx.CopyFromRows(rows)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Tree returns every node of (doc, gen), parents before children.
func (r *IndexRepo) Tree(ctx context.Context, doc uuid.UUID, gen int) ([]types.TreeNode, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, document_id, gen, parent_id, short_id, ord, level, title, origin, page_start, page_end,
		summary, section_ids, token_count FROM doc_tree_nodes WHERE document_id = $1 AND gen = $2 ORDER BY level, ord`, doc, gen)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.TreeNode
	for rows.Next() {
		var n types.TreeNode
		if err := rows.Scan(&n.ID, &n.DocumentID, &n.Gen, &n.ParentID, &n.ShortID, &n.Ord, &n.Level, &n.Title, &n.Origin,
			&n.PageStart, &n.PageEnd, &n.Summary, &n.SectionIDs, &n.TokenCount); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// DeleteOldGens removes pages, sections and tree nodes of generations older
// than keep.
func (r *IndexRepo) DeleteOldGens(ctx context.Context, doc uuid.UUID, keep int) error {
	for _, q := range []string{
		`DELETE FROM sections WHERE document_id = $1 AND gen < $2`,
		`DELETE FROM doc_tree_nodes WHERE document_id = $1 AND gen < $2`,
		`DELETE FROM kg_mentions WHERE document_id = $1 AND gen < $2`,
	} {
		if _, err := r.pool.Exec(ctx, q, doc, keep); err != nil {
			return err
		}
	}
	return nil
}

func prefixCols(prefix, cols string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = prefix + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}
