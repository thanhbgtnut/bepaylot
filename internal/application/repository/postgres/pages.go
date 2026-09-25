package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thanhenti/bepaylot/internal/types"
)

// dbtx is satisfied by *pgxpool.Pool and pgx.Tx.
type dbtx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
	CopyFrom(ctx context.Context, table pgx.Identifier, cols []string, src pgx.CopyFromSource) (int64, error)
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}

var (
	_ dbtx = (*pgxpool.Pool)(nil)
	_ dbtx = (pgx.Tx)(nil)
)

// PagesRepo persists document pages, blocks and lines.
type PagesRepo struct{ pool dbtx }

// WithTx returns a repo bound to tx (used inside Documents.WithLock).
func (r *PagesRepo) WithTx(tx pgx.Tx) *PagesRepo { return &PagesRepo{pool: tx} }

// PageSizePt is a page's size in points.
type PageSizePt struct{ W, H float64 }

// Init creates the pending page rows of a generation (idempotent).
func (r *PagesRepo) Init(ctx context.Context, doc uuid.UUID, gen int, sizes []PageSizePt) error {
	rows := make([][]any, len(sizes))
	for i, s := range sizes {
		rows[i] = []any{doc, i + 1, gen, types.PagePending, s.W, s.H}
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM document_pages WHERE document_id = $1`, doc); err != nil {
		return err
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"document_pages"},
		[]string{"document_id", "page_no", "gen", "status", "width_pt", "height_pt"}, pgx.CopyFromRows(rows)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Counts summarises page states of a generation.
func (r *PagesRepo) Counts(ctx context.Context, doc uuid.UUID, gen int) (types.PageStatusCounts, error) {
	var c types.PageStatusCounts
	rows, err := r.pool.Query(ctx, `SELECT status, count(*) FROM document_pages WHERE document_id = $1 AND gen = $2 GROUP BY status`, doc, gen)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			return c, err
		}
		switch s {
		case types.PagePending:
			c.Pending = n
		case types.PageRendering:
			c.Rendering = n
		case types.PageRendered:
			c.Rendered = n
		case types.PageOCR:
			c.OCR = n
		case types.PageDone:
			c.Done = n
		case types.PageFailed:
			c.Failed = n
		}
	}
	return c, rows.Err()
}

// Claim is a claimed page and its attempt number (for unique task ids).
type Claim struct{ Page, Attempt int }

// claim moves up to n pages from one state to another, lowest page first.
// SKIP LOCKED keeps concurrent claimers apart.
func (r *PagesRepo) claim(ctx context.Context, doc uuid.UUID, gen int, from, to string, n int) ([]Claim, error) {
	if n <= 0 {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, `
		UPDATE document_pages p SET status = $4, started_at = now(), attempts = attempts + 1
		WHERE (p.document_id, p.page_no) IN (
			SELECT document_id, page_no FROM document_pages
			WHERE document_id = $1 AND gen = $2 AND status = $3
			ORDER BY page_no LIMIT $5 FOR UPDATE SKIP LOCKED)
		RETURNING p.page_no, p.attempts`, doc, gen, from, to, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Claim
	for rows.Next() {
		var c Claim
		if err := rows.Scan(&c.Page, &c.Attempt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Page < out[j-1].Page; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, rows.Err()
}

// ClaimForRender reserves up to n consecutive-first pending pages.
func (r *PagesRepo) ClaimForRender(ctx context.Context, doc uuid.UUID, gen, n int) ([]Claim, error) {
	return r.claim(ctx, doc, gen, types.PagePending, types.PageRendering, n)
}

// ClaimForOCR reserves up to n rendered pages.
func (r *PagesRepo) ClaimForOCR(ctx context.Context, doc uuid.UUID, gen, n int) ([]Claim, error) {
	return r.claim(ctx, doc, gen, types.PageRendered, types.PageOCR, n)
}

// Rendered records a rendered page.
type Rendered struct {
	Width, Height     int
	DPI               float64
	WidthPt, HeightPt float64
	Rotation          int
	ImageKey          string
	TextLayerKey      string
	RenderMs          int
}

// MarkRendered stores render output and moves the page to rendered.
func (r *PagesRepo) MarkRendered(ctx context.Context, doc uuid.UUID, gen, page int, v Rendered) error {
	_, err := r.pool.Exec(ctx, `UPDATE document_pages SET status = 'rendered', width = $4, height = $5, dpi = $6,
		width_pt = $7, height_pt = $8, rotation = $9, image_key = $10, text_layer_key = $11, render_ms = $12, error = ''
		WHERE document_id = $1 AND gen = $2 AND page_no = $3`,
		doc, gen, page, v.Width, v.Height, v.DPI, v.WidthPt, v.HeightPt, v.Rotation, v.ImageKey, v.TextLayerKey, v.RenderMs)
	return err
}

// SetStatus moves a page to status with an optional error.
func (r *PagesRepo) SetStatus(ctx context.Context, doc uuid.UUID, gen, page int, status, errMsg string) error {
	_, err := r.pool.Exec(ctx, `UPDATE document_pages SET status = $4, error = $5,
		finished_at = CASE WHEN $4 IN ('done','failed') THEN now() ELSE finished_at END
		WHERE document_id = $1 AND gen = $2 AND page_no = $3`, doc, gen, page, status, cleanText(errMsg))
	return err
}

// Get returns one page row.
func (r *PagesRepo) Get(ctx context.Context, doc uuid.UUID, page int) (types.DocumentPage, error) {
	rows, err := r.pool.Query(ctx, pageSelect+` WHERE document_id = $1 AND page_no = $2`, doc, page)
	if err != nil {
		return types.DocumentPage{}, err
	}
	pages, err := scanPages(rows)
	if err != nil {
		return types.DocumentPage{}, err
	}
	if len(pages) == 0 {
		return types.DocumentPage{}, ErrNotFound
	}
	return pages[0], nil
}

// List returns pages in [from, to] (0 = unbounded), ordered.
func (r *PagesRepo) List(ctx context.Context, doc uuid.UUID, gen, from, to int) ([]types.DocumentPage, error) {
	if to <= 0 {
		to = 1 << 30
	}
	rows, err := r.pool.Query(ctx, pageSelect+` WHERE document_id = $1 AND gen = $2 AND page_no BETWEEN $3 AND $4 ORDER BY page_no`, doc, gen, from, to)
	if err != nil {
		return nil, err
	}
	return scanPages(rows)
}

const pageSelect = `SELECT document_id, page_no, gen, status, attempts, width, height, dpi, width_pt, height_pt, rotation,
	image_key, raw_key, text_layer_key, text_source, text_quality, render_ms, ocr_ms, engine, markdown, text_plain,
	doc_md_offset, is_blank, error, started_at, finished_at FROM document_pages`

func scanPages(rows pgx.Rows) ([]types.DocumentPage, error) {
	defer rows.Close()
	var out []types.DocumentPage
	for rows.Next() {
		var p types.DocumentPage
		var dpi, wpt, hpt, tq float32
		if err := rows.Scan(&p.DocumentID, &p.PageNo, &p.Gen, &p.Status, &p.Attempts, &p.Width, &p.Height, &dpi, &wpt, &hpt, &p.Rotation,
			&p.ImageKey, &p.RawKey, &p.TextLayerKey, &p.TextSource, &tq, &p.RenderMs, &p.OCRMs, &p.Engine, &p.Markdown, &p.TextPlain,
			&p.DocMdOffset, &p.IsBlank, &p.Error, &p.StartedAt, &p.FinishedAt); err != nil {
			return nil, err
		}
		p.DPI, p.WidthPt, p.HeightPt, p.TextQuality = float64(dpi), float64(wpt), float64(hpt), float64(tq)
		out = append(out, p)
	}
	return out, rows.Err()
}

// SaveParsed stores a parsed page (row, blocks, lines) and marks it done.
// It only writes when the page still belongs to generation gen.
func (r *PagesRepo) SaveParsed(ctx context.Context, doc uuid.UUID, gen int, p *types.ParsedPage, rawKey, textPlain string, ocrMs int) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE document_pages SET status = 'done', raw_key = $4, text_source = $5, text_quality = $6,
		ocr_ms = $7, engine = $8, markdown = $9, text_plain = $10, is_blank = $11, error = '', finished_at = now()
		WHERE document_id = $1 AND gen = $2 AND page_no = $3`,
		doc, gen, p.PageNo, rawKey, p.TextSource, p.TextQuality, ocrMs, p.Engine, cleanText(p.Markdown), cleanText(textPlain), p.IsBlank)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound // superseded generation
	}
	if err := writeBlocksLines(ctx, tx, doc, p); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// UpdateMarkdown rewrites a page's markdown, blocks and lines after a
// document-level pass (furniture) changed them.
func (r *PagesRepo) UpdateMarkdown(ctx context.Context, doc uuid.UUID, gen int, p *types.ParsedPage, textPlain string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE document_pages SET markdown = $4, text_plain = $5, is_blank = $6
		WHERE document_id = $1 AND gen = $2 AND page_no = $3`, doc, gen, p.PageNo, cleanText(p.Markdown), cleanText(textPlain), p.IsBlank); err != nil {
		return err
	}
	if err := writeBlocksLines(ctx, tx, doc, p); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func writeBlocksLines(ctx context.Context, tx pgx.Tx, doc uuid.UUID, p *types.ParsedPage) error {
	if _, err := tx.Exec(ctx, `DELETE FROM page_lines WHERE document_id = $1 AND page_no = $2`, doc, p.PageNo); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM page_blocks WHERE document_id = $1 AND page_no = $2`, doc, p.PageNo); err != nil {
		return err
	}
	blocks := make([][]any, len(p.Blocks))
	for i, b := range p.Blocks {
		blocks[i] = []any{doc, p.PageNo, b.BlockNo, b.SourceID, string(b.Type), b.RawClass, float32(b.Confidence), f32(b.BBox.Array()),
			cleanText(b.Text), cleanText(b.HTML), cleanText(b.LaTeX), b.AssetKey, b.IsFurniture, b.MdStart, b.MdEnd}
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"page_blocks"}, []string{"document_id", "page_no", "block_no", "source_id", "type",
		"raw_class", "confidence", "bbox", "text", "html", "latex", "asset_key", "is_furniture", "md_start", "md_end"}, pgx.CopyFromRows(blocks)); err != nil {
		return fmt.Errorf("copy blocks: %w", err)
	}
	lines := make([][]any, len(p.Lines))
	for i, l := range p.Lines {
		lines[i] = []any{doc, p.PageNo, l.LineNo, l.SourceID, l.BlockNo, cleanText(l.Text), cleanText(l.TextOCR), cleanText(l.TextLayer),
			nz(l.TextSource, types.TextSourceOCR), float32(l.Confidence), f32(l.Quad.Flat()), f32(l.BBox.Array()), l.InFigure, l.LowConfidence, l.MdStart, l.MdEnd}
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"page_lines"}, []string{"document_id", "page_no", "line_no", "source_id", "block_no",
		"text", "text_ocr", "text_layer", "text_source", "confidence", "quad", "bbox", "in_figure", "low_confidence", "md_start", "md_end"}, pgx.CopyFromRows(lines)); err != nil {
		return fmt.Errorf("copy lines: %w", err)
	}
	return nil
}

// LoadParsed rebuilds the ParsedPage of a stored page.
func (r *PagesRepo) LoadParsed(ctx context.Context, doc uuid.UUID, page int) (*types.ParsedPage, error) {
	pg, err := r.Get(ctx, doc, page)
	if err != nil {
		return nil, err
	}
	p := &types.ParsedPage{PageNo: page, Width: pg.Width, Height: pg.Height, DPI: pg.DPI, Rotation: pg.Rotation,
		Engine: pg.Engine, TextSource: pg.TextSource, TextQuality: pg.TextQuality, IsBlank: pg.IsBlank, Markdown: pg.Markdown}
	blocks, err := r.Blocks(ctx, doc, page, page)
	if err != nil {
		return nil, err
	}
	for _, b := range blocks {
		p.Blocks = append(p.Blocks, b.ParsedBlock)
	}
	lines, err := r.Lines(ctx, doc, page, page)
	if err != nil {
		return nil, err
	}
	for _, l := range lines {
		p.Lines = append(p.Lines, l.ParsedLine)
	}
	return p, nil
}

// PageBlock is a stored block with its page number.
type PageBlock struct {
	PageNo int `json:"page_no"`
	types.ParsedBlock
}

// Blocks returns blocks of pages [from, to].
func (r *PagesRepo) Blocks(ctx context.Context, doc uuid.UUID, from, to int) ([]PageBlock, error) {
	rows, err := r.pool.Query(ctx, `SELECT page_no, block_no, source_id, type, raw_class, confidence, bbox, text, html, latex,
		asset_key, is_furniture, md_start, md_end FROM page_blocks WHERE document_id = $1 AND page_no BETWEEN $2 AND $3
		ORDER BY page_no, block_no`, doc, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PageBlock
	for rows.Next() {
		var b PageBlock
		var typ string
		var conf float32
		var bbox []float32
		if err := rows.Scan(&b.PageNo, &b.BlockNo, &b.SourceID, &typ, &b.RawClass, &conf, &bbox, &b.Text, &b.HTML, &b.LaTeX,
			&b.AssetKey, &b.IsFurniture, &b.MdStart, &b.MdEnd); err != nil {
			return nil, err
		}
		b.Type, b.Confidence, b.BBox = types.BlockType(typ), float64(conf), types.BBoxFromArray(f64(bbox))
		out = append(out, b)
	}
	return out, rows.Err()
}

// PageLine is a stored line with its page number.
type PageLine struct {
	PageNo int `json:"page_no"`
	types.ParsedLine
}

// Lines returns lines of pages [from, to] in reading order.
func (r *PagesRepo) Lines(ctx context.Context, doc uuid.UUID, from, to int) ([]PageLine, error) {
	return r.queryLines(ctx, `WHERE document_id = $1 AND page_no BETWEEN $2 AND $3 ORDER BY page_no, line_no`, doc, from, to)
}

// LinesByNumber returns specific lines of one page.
func (r *PagesRepo) LinesByNumber(ctx context.Context, doc uuid.UUID, page int, lineNos []int) ([]PageLine, error) {
	return r.queryLines(ctx, `WHERE document_id = $1 AND page_no = $2 AND line_no = ANY($3) ORDER BY line_no`, doc, page, lineNos)
}

func (r *PagesRepo) queryLines(ctx context.Context, where string, args ...any) ([]PageLine, error) {
	rows, err := r.pool.Query(ctx, `SELECT page_no, line_no, source_id, block_no, text, text_ocr, text_layer, text_source,
		confidence, quad, bbox, in_figure, low_confidence, md_start, md_end FROM page_lines `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PageLine
	for rows.Next() {
		var l PageLine
		var conf float32
		var quad, bbox []float32
		if err := rows.Scan(&l.PageNo, &l.LineNo, &l.SourceID, &l.BlockNo, &l.Text, &l.TextOCR, &l.TextLayer, &l.TextSource,
			&conf, &quad, &bbox, &l.InFigure, &l.LowConfidence, &l.MdStart, &l.MdEnd); err != nil {
			return nil, err
		}
		l.Confidence, l.Quad, l.BBox = float64(conf), types.QuadFromFlat(f64(quad)), types.BBoxFromArray(f64(bbox))
		out = append(out, l)
	}
	return out, rows.Err()
}

// LineMatchRow is a keyword/trigram hit on a line.
type LineMatchRow struct {
	PageNo, LineNo int
	Text           string
	BBox           types.BBox
	Score          float64
}

// SearchLines finds lines of a document matching query: words must all
// appear (accent-insensitive) or the trigram similarity must be high.
func (r *PagesRepo) SearchLines(ctx context.Context, doc uuid.UUID, gen int, query string, pageFrom, pageTo, limit int) ([]LineMatchRow, error) {
	if pageTo <= 0 {
		pageTo = 1 << 30
	}
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.pool.Query(ctx, `
		WITH q AS (SELECT unaccent_vi($2) AS t)
		SELECT l.page_no, l.line_no, l.text, l.bbox,
		       greatest(word_similarity(q.t, unaccent_vi(l.text)), CASE WHEN unaccent_vi(l.text) LIKE '%' || q.t || '%' THEN 1 ELSE 0 END) AS score
		FROM page_lines l JOIN document_pages p ON p.document_id = l.document_id AND p.page_no = l.page_no AND p.gen = $3, q
		WHERE l.document_id = $1 AND l.page_no BETWEEN $4 AND $5
		  AND (unaccent_vi(l.text) LIKE '%' || q.t || '%' OR q.t <% unaccent_vi(l.text))
		ORDER BY score DESC, l.page_no, l.line_no LIMIT $6`, doc, query, gen, pageFrom, pageTo, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LineMatchRow
	for rows.Next() {
		var m LineMatchRow
		var bbox []float32
		if err := rows.Scan(&m.PageNo, &m.LineNo, &m.Text, &bbox, &m.Score); err != nil {
			return nil, err
		}
		m.BBox = types.BBoxFromArray(f64(bbox))
		out = append(out, m)
	}
	return out, rows.Err()
}

// PageHit is a page-level full-text hit.
type PageHit struct {
	DocumentID uuid.UUID
	PageNo     int
	Score      float64
	Snippet    string
}

// SearchPages ranks pages of the given documents by full-text match.
func (r *PagesRepo) SearchPages(ctx context.Context, docs []uuid.UUID, query string, pageFrom, pageTo, limit int) ([]PageHit, error) {
	if pageTo <= 0 {
		pageTo = 1 << 30
	}
	rows, err := r.pool.Query(ctx, `
		WITH q AS (SELECT websearch_to_tsquery('simple', unaccent_vi($2)) AS tq)
		SELECT p.document_id, p.page_no, ts_rank_cd(p.tsv, q.tq) AS score,
		       ts_headline('simple', p.text_plain, q.tq, 'StartSel=<mark>,StopSel=</mark>,MaxFragments=2,MaxWords=25,MinWords=8')
		FROM document_pages p JOIN documents d ON d.id = p.document_id AND d.gen = p.gen, q
		WHERE p.document_id = ANY($1) AND p.page_no BETWEEN $3 AND $4 AND p.tsv @@ q.tq
		ORDER BY score DESC, p.document_id, p.page_no LIMIT $5`, docs, query, pageFrom, pageTo, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PageHit
	for rows.Next() {
		var h PageHit
		if err := rows.Scan(&h.DocumentID, &h.PageNo, &h.Score, &h.Snippet); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// SetDocOffsets stores each page's offset into the full-document markdown.
func (r *PagesRepo) SetDocOffsets(ctx context.Context, doc uuid.UUID, gen int, offsets map[int]int) error {
	batch := &pgx.Batch{}
	for page, off := range offsets {
		batch.Queue(`UPDATE document_pages SET doc_md_offset = $4 WHERE document_id = $1 AND gen = $2 AND page_no = $3`, doc, gen, page, off)
	}
	return r.pool.SendBatch(ctx, batch).Close()
}

// ResetStale returns pages stuck in rendering/ocr since before to their
// previous state, so housekeeping can re-dispatch them.
func (r *PagesRepo) ResetStale(ctx context.Context, before time.Time) (int64, error) {
	t1, err := r.pool.Exec(ctx, `UPDATE document_pages SET status = 'pending' WHERE status = 'rendering' AND started_at < $1`, before)
	if err != nil {
		return 0, err
	}
	t2, err := r.pool.Exec(ctx, `UPDATE document_pages SET status = 'rendered' WHERE status = 'ocr' AND started_at < $1`, before)
	if err != nil {
		return 0, err
	}
	return t1.RowsAffected() + t2.RowsAffected(), nil
}

func f32(v []float64) []float32 {
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(x)
	}
	return out
}

func f64(v []float32) []float64 {
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = float64(x)
	}
	return out
}

func nz(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// ResetPages moves pages back to a state (page reparse) and returns how many
// were done/failed before, so counters can be corrected.
func (r *PagesRepo) ResetPages(ctx context.Context, doc uuid.UUID, gen int, pages []int, to string) (done, failed int, err error) {
	err = r.pool.QueryRow(ctx, `WITH old AS (
			SELECT page_no, status FROM document_pages WHERE document_id = $1 AND gen = $2 AND page_no = ANY($3) FOR UPDATE),
		upd AS (UPDATE document_pages p SET status = $4, error = '' FROM old WHERE p.document_id = $1 AND p.page_no = old.page_no RETURNING old.status)
		SELECT count(*) FILTER (WHERE status = 'done'), count(*) FILTER (WHERE status = 'failed') FROM upd`, doc, gen, pages, to).Scan(&done, &failed)
	return
}
