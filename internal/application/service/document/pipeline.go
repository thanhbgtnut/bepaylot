package document

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // decode config of page images
	_ "image/png"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/parser"
	"github.com/thanhenti/bepaylot/internal/parser/assemble"
	"github.com/thanhenti/bepaylot/internal/parser/imagefile"
	"github.com/thanhenti/bepaylot/internal/parser/pdf"
	"github.com/thanhenti/bepaylot/internal/parser/textlayer"
	"github.com/thanhenti/bepaylot/internal/queue"
	"github.com/thanhenti/bepaylot/internal/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
	_ "golang.org/x/image/tiff"
)

// Handlers returns the task handlers owned by Module 1.
func (s *Service) Handlers() map[string]queue.Handler {
	return map[string]queue.Handler{
		types.TaskDocumentSplit:    s.handle(s.split),
		types.TaskPageRender:       s.handle(s.render),
		types.TaskPageOCR:          s.handle(s.ocr),
		types.TaskDocumentAssemble: s.handle(s.assemble),
		types.TaskDocumentDelete:   s.handleDeleting(s.purge),
		types.TaskGenCleanup:       s.handle(s.genCleanup),
		types.TaskHousekeeping:     func(ctx context.Context, _ []byte) error { return s.Housekeeping(ctx) },
	}
}

// handle decodes the payload and skips work for documents that are gone,
// cancelled, being deleted or superseded by a newer generation (§4.1
// cancelGuard).
func (s *Service) handle(fn func(ctx context.Context, d types.Document, p types.DocTaskPayload) error) queue.Handler {
	return s.handleWith(fn, false)
}

// handleDeleting is handle for tasks that must run on deleting documents.
func (s *Service) handleDeleting(fn func(ctx context.Context, d types.Document, p types.DocTaskPayload) error) queue.Handler {
	return s.handleWith(fn, true)
}

func (s *Service) handleWith(fn func(ctx context.Context, d types.Document, p types.DocTaskPayload) error, onDeleting bool) queue.Handler {
	return func(ctx context.Context, raw []byte) error {
		var p types.DocTaskPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return fmt.Errorf("%w: bad payload: %v", queue.ErrSkipRetry, err)
		}
		d, err := s.st.Documents.GetAny(ctx, p.DocumentID)
		if errors.Is(err, postgres.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if onDeleting {
			return fn(ctx, d, p)
		}
		if d.Status == types.DocDeleting || d.Status == types.DocCancelled || (p.Gen > 0 && d.Gen != p.Gen) {
			return nil
		}
		return fn(ctx, d, p)
	}
}

func (s *Service) span(ctx context.Context, d types.Document, stage, ref string) func(error) {
	id, err := s.st.Tasks.StartSpan(ctx, d.ID, d.Gen, stage, ref)
	if err != nil {
		return func(error) {}
	}
	return func(e error) {
		msg := ""
		if e != nil {
			msg = e.Error()
		}
		s.st.Tasks.FinishSpan(context.WithoutCancel(ctx), id, msg)
	}
}

func strp(s string) *string { return &s }

// fail marks the document failed with a permanent error.
func (s *Service) fail(ctx context.Context, d types.Document, stage string, err error) error {
	st := types.StageFailed
	_, uerr := s.st.Documents.Update(context.WithoutCancel(ctx), d.ID, d.Gen, postgres.DocUpdate{
		Status: strp(types.DocFailed), ParseStatus: &st, Error: strp(stage + ": " + err.Error()),
	})
	if uerr != nil {
		s.log.Error("mark failed", "doc", d.ID, "err", uerr)
	}
	return fmt.Errorf("%w: %v", queue.ErrSkipRetry, err)
}

// split probes the file and creates the page rows (§4.2).
func (s *Service) split(ctx context.Context, d types.Document, _ types.DocTaskPayload) (err error) {
	done := s.span(ctx, d, "split", "")
	defer func() { done(err) }()
	if _, err := s.st.Documents.Update(ctx, d.ID, d.Gen, postgres.DocUpdate{Status: strp(types.DocSplitting), ParseStatus: strp(types.StageProcessing)}); err != nil {
		return err
	}
	path, release, err := s.files.Acquire(ctx, d.StorageKey)
	if err != nil {
		return err
	}
	defer release()

	var sizes []postgres.PageSizePt
	info := map[string]any{}
	if d.PDFAPart != nil {
		info["pdfa"] = map[string]any{"part": *d.PDFAPart, "conformance": d.PDFAConformance}
	}
	if d.MimeType == "application/pdf" {
		di, err := s.renderer.Probe(ctx, path)
		if err != nil {
			if queue.IsFinalAttempt(ctx) || strings.Contains(err.Error(), "password") {
				return s.fail(ctx, d, "split", err)
			}
			return err
		}
		for _, p := range di.Pages {
			sizes = append(sizes, postgres.PageSizePt{W: p.WidthPt, H: p.HeightPt})
		}
		for k, v := range di.Info {
			info[k] = v
		}
		if len(di.Bookmarks) > 0 {
			info["bookmarks"] = di.Bookmarks
		}
	} else {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		cfg, _, derr := image.DecodeConfig(f)
		f.Close()
		if derr != nil {
			return s.fail(ctx, d, "split", fmt.Errorf("decode image: %w", derr))
		}
		sizes = []postgres.PageSizePt{{W: float64(cfg.Width), H: float64(cfg.Height)}}
	}
	if len(sizes) == 0 {
		return s.fail(ctx, d, "split", errors.New("document has no pages"))
	}
	if err := s.st.Pages.Init(ctx, d.ID, d.Gen, sizes); err != nil {
		return err
	}
	n := len(sizes)
	if _, err := s.st.Documents.Update(ctx, d.ID, d.Gen, postgres.DocUpdate{Status: strp(types.DocParsing), PageCount: &n, PDFInfo: info}); err != nil {
		return err
	}
	d.PageCount = n
	return s.Advance(ctx, d)
}

// Advance tops up the document's render and OCR windows and triggers
// assembly when every page is finished (§4.3). It is idempotent.
func (s *Service) Advance(ctx context.Context, d types.Document) error {
	w := s.cfg.Workers
	batch := max(1, s.cfg.Parser.Render.BatchPages)
	return s.st.Documents.WithLock(ctx, d.ID, func(ctx context.Context, tx pgx.Tx) error {
		pages := s.st.Pages.WithTx(tx)
		c, err := pages.Counts(ctx, d.ID, d.Gen)
		if err != nil {
			return err
		}
		// OCR window.
		if free := w.OCRInflightPages - c.OCR; free > 0 && c.Rendered > 0 {
			claims, err := pages.ClaimForOCR(ctx, d.ID, d.Gen, free)
			if err != nil {
				return err
			}
			for _, cl := range claims {
				if err := s.enqueue(ctx, types.TaskPageOCR, d, []int{cl.Page}, fmt.Sprintf("ocr:%s:%d:%d:%d", d.ID, d.Gen, cl.Page, cl.Attempt)); err != nil {
					return err
				}
			}
		}
		// Render window with backpressure on pages waiting for OCR.
		inflight := (c.Rendering + batch - 1) / batch
		ahead := c.Rendered + c.OCR
		pending := c.Pending
		for pending > 0 && inflight < w.RenderInflightBatches && ahead < w.RenderAheadPages {
			claims, err := pages.ClaimForRender(ctx, d.ID, d.Gen, batch)
			if err != nil {
				return err
			}
			if len(claims) == 0 {
				break
			}
			nums := make([]int, len(claims))
			for i, cl := range claims {
				nums[i] = cl.Page
			}
			if err := s.enqueue(ctx, types.TaskPageRender, d, nums, fmt.Sprintf("render:%s:%d:%d:%d", d.ID, d.Gen, nums[0], claims[0].Attempt)); err != nil {
				return err
			}
			inflight++
			ahead += len(claims)
			pending -= len(claims)
		}
		total := c.Pending + c.Rendering + c.Rendered + c.OCR + c.Done + c.Failed
		if total > 0 && c.Done+c.Failed == total {
			return s.enqueue(ctx, types.TaskDocumentAssemble, d, nil, fmt.Sprintf("assemble:%s:%d", d.ID, d.Gen))
		}
		return nil
	})
}

func (s *Service) textLayerEnabled(d types.Document) bool {
	switch s.cfg.Parser.TextLayer.Enabled {
	case "off":
		return false
	case "pdfa_only":
		return d.PDFAPart != nil
	}
	return true
}

func (s *Service) textLayerOpts(d types.Document) textlayer.Options {
	tl := s.cfg.Parser.TextLayer
	return textlayer.Options{
		MinChars: tl.MinChars, MaxBadCharRatio: tl.MaxBadCharRatio, MergeMinSimilarity: tl.MergeMinSimilarity,
		Trusted: d.PDFAPart != nil && (d.PDFAConformance == "a" || d.PDFAConformance == "u"),
	}
}

// render renders a batch of pages to S3 (§5.7).
func (s *Service) render(ctx context.Context, d types.Document, p types.DocTaskPayload) (err error) {
	done := s.span(ctx, d, "render", fmt.Sprint(p.Pages))
	defer func() { done(err) }()

	// On retry only render pages still marked rendering.
	var todo []int
	for _, pg := range p.Pages {
		row, err := s.st.Pages.Get(ctx, d.ID, pg)
		if err == nil && row.Gen == d.Gen && row.Status == types.PageRendering {
			todo = append(todo, pg)
		}
	}
	if len(todo) == 0 {
		return nil
	}
	path, release, err := s.files.Acquire(ctx, d.StorageKey)
	if err != nil {
		return err
	}
	defer release()

	rc := s.cfg.Parser.Render
	opt := pdf.RenderOptions{DPI: rc.DPI, MaxLongSide: rc.MaxLongSide, MaxPixels: rc.MaxPixels, JPEGQuality: rc.JPEGQuality, ExtractText: s.textLayerEnabled(d)}
	emit := func(pr pdf.PageRender) error {
		imgKey := s.keys.PageImage(d.KBID, d.ID, d.Gen, pr.PageNo)
		if _, err := s.objects.Put(ctx, imgKey, bytes.NewReader(pr.JPEG), int64(len(pr.JPEG)), "image/jpeg"); err != nil {
			return err
		}
		textKey := ""
		if pr.Text != nil && len(pr.Text.Words) > 0 {
			textKey = s.keys.TextLayer(d.KBID, d.ID, d.Gen, pr.PageNo)
			if err := s.putGzipJSON(ctx, textKey, pr.Text); err != nil {
				return err
			}
		}
		if err := s.st.Pages.MarkRendered(ctx, d.ID, d.Gen, pr.PageNo, postgres.Rendered{
			Width: pr.Width, Height: pr.Height, DPI: pr.DPI, WidthPt: pr.WidthPt, HeightPt: pr.HeightPt,
			Rotation: pr.Rotation, ImageKey: imgKey, TextLayerKey: textKey, RenderMs: pr.RenderMs,
		}); err != nil {
			return err
		}
		return s.Advance(ctx, d)
	}

	if d.MimeType == "application/pdf" {
		err = s.renderer.RenderBatch(ctx, path, todo, opt, emit)
	} else {
		err = s.renderImage(path, opt, emit)
	}
	if err != nil && queue.IsFinalAttempt(ctx) {
		// Give up on the pages that never rendered; the rest of the
		// document continues and ends partial (§4.3).
		for _, pg := range todo {
			row, gerr := s.st.Pages.Get(ctx, d.ID, pg)
			if gerr == nil && row.Status == types.PageRendering {
				_ = s.st.Pages.SetStatus(ctx, d.ID, d.Gen, pg, types.PageFailed, "render: "+err.Error())
				_, _ = s.st.Documents.CountPage(ctx, d.ID, d.Gen, true)
			}
		}
		_ = s.Advance(context.WithoutCancel(ctx), d)
	}
	return err
}

func (s *Service) renderImage(path string, opt pdf.RenderOptions, emit func(pdf.PageRender) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	t0 := time.Now()
	res, err := imagefile.Normalize(f, imagefile.Options{MaxPixels: opt.MaxPixels, JPEGQuality: opt.JPEGQuality})
	if err != nil {
		return fmt.Errorf("%w: %v", queue.ErrSkipRetry, err)
	}
	return emit(pdf.PageRender{PageNo: 1, JPEG: res.JPEG, Width: res.Width, Height: res.Height, DPI: 72,
		WidthPt: float64(res.Width), HeightPt: float64(res.Height), RenderMs: int(time.Since(t0).Milliseconds())})
}

func (s *Service) engineOptions() parser.PageOptions {
	o := s.cfg.Parser.Engines.TurboOCR.Options
	on := func(p *bool) bool { return p == nil || *p }
	return parser.PageOptions{Layout: on(o.Layout), ReadingOrder: on(o.ReadingOrder), Tables: on(o.Tables), Formulas: o.Formulas}
}

// ocr runs the OCR engine on one rendered page, merges the text layer and
// stores the structured page (§5.2–§5.8).
func (s *Service) ocr(ctx context.Context, d types.Document, p types.DocTaskPayload) (err error) {
	if len(p.Pages) != 1 {
		return fmt.Errorf("%w: page:ocr needs exactly one page", queue.ErrSkipRetry)
	}
	pageNo := p.Pages[0]
	done := s.span(ctx, d, "ocr", fmt.Sprint(pageNo))
	defer func() { done(err) }()

	row, err := s.st.Pages.Get(ctx, d.ID, pageNo)
	if err != nil || row.Gen != d.Gen || row.Status == types.PageDone || row.Status == types.PageFailed {
		return nil
	}
	layer := s.loadTextLayer(ctx, row.TextLayerKey)
	tlOpt := s.textLayerOpts(d)

	engine, err := s.engines.Get(d.Engine)
	if err != nil {
		return s.finishPageFailed(ctx, d, pageNo, err)
	}
	t0 := time.Now()
	raw, err := s.callEngine(ctx, engine, row)
	ocrMs := int(time.Since(t0).Milliseconds())
	if err != nil {
		if !queue.IsFinalAttempt(ctx) {
			return err
		}
		// Final attempt: fall back to the text layer when it is usable (§5.8 step 6).
		if layer != nil && textlayer.Usable(textlayer.Quality(layer, "", tlOpt), tlOpt) {
			page := textlayer.PageFromLayer(pageNo, row.Width, row.Height, row.DPI, layer)
			page.TextQuality = textlayer.Quality(layer, "", tlOpt)
			assemble.Render(page)
			return s.savePage(ctx, d, page, "", ocrMs)
		}
		return s.finishPageFailed(ctx, d, pageNo, err)
	}

	page := assemble.Build(raw, assemble.Options{
		PageNo: pageNo, DPI: row.DPI, Engine: engine.Name(), ClassMap: s.cfg.Parser.ClassMap,
		ReadingOrderFix: s.cfg.Parser.ReadingOrderFixEnabled(), LowConfThreshold: s.cfg.Parser.LowConfThreshold,
		AssetKey: func(pg, b int) string { return s.keys.Figure(d.KBID, d.ID, d.Gen, pg, b) },
	})
	page.Rotation = row.Rotation
	if layer != nil {
		q := textlayer.Quality(layer, assemble.PlainText(page), tlOpt)
		page.TextQuality = q
		if textlayer.Usable(q, tlOpt) {
			textlayer.Merge(page, layer, tlOpt)
		}
	}
	assemble.Render(page)
	if err := s.cropFigures(ctx, row, page); err != nil {
		s.log.Warn("crop figures failed", "doc", d.ID, "page", pageNo, "err", err)
	}
	rawKey := s.keys.OCRRaw(d.KBID, d.ID, d.Gen, pageNo)
	if err := s.putGzip(ctx, rawKey, raw.Raw); err != nil {
		return err
	}
	return s.savePage(ctx, d, page, rawKey, ocrMs)
}

func (s *Service) callEngine(ctx context.Context, engine parser.Engine, row types.DocumentPage) (*parser.RawPage, error) {
	rc, info, err := s.objects.Get(ctx, row.ImageKey)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return engine.ParsePage(ctx, parser.PageImage{PageNo: row.PageNo, Body: rc, Size: info.Size, Width: row.Width, Height: row.Height}, s.engineOptions())
}

func (s *Service) savePage(ctx context.Context, d types.Document, page *types.ParsedPage, rawKey string, ocrMs int) error {
	err := s.st.Pages.SaveParsed(ctx, d.ID, d.Gen, page, rawKey, assemble.PlainText(page), ocrMs)
	if errors.Is(err, postgres.ErrNotFound) {
		return nil // superseded by a reparse
	}
	if err != nil {
		return err
	}
	if _, err := s.st.Documents.CountPage(ctx, d.ID, d.Gen, false); err != nil {
		return err
	}
	return s.Advance(ctx, d)
}

func (s *Service) finishPageFailed(ctx context.Context, d types.Document, pageNo int, cause error) error {
	ctx = context.WithoutCancel(ctx)
	_ = s.st.Pages.SetStatus(ctx, d.ID, d.Gen, pageNo, types.PageFailed, cause.Error())
	_, _ = s.st.Documents.CountPage(ctx, d.ID, d.Gen, true)
	_ = s.Advance(ctx, d)
	return fmt.Errorf("%w: page %d: %v", queue.ErrSkipRetry, pageNo, cause)
}

func (s *Service) loadTextLayer(ctx context.Context, key string) *types.TextLayer {
	if key == "" {
		return nil
	}
	var tl types.TextLayer
	if err := s.getGzipJSON(ctx, key, &tl); err != nil {
		s.log.Warn("load text layer failed", "key", key, "err", err)
		return nil
	}
	return &tl
}

// assemble finishes parsing: running headers, full-document markdown and
// offsets, then hands over to Module 2 (§4.2).
func (s *Service) assemble(ctx context.Context, d types.Document, _ types.DocTaskPayload) (err error) {
	done := s.span(ctx, d, "assemble", "")
	defer func() { done(err) }()
	if _, err := s.st.Documents.Update(ctx, d.ID, d.Gen, postgres.DocUpdate{Status: strp(types.DocAssembling)}); err != nil {
		return err
	}
	pages, err := s.LoadPages(ctx, d.ID, d.Gen, 0, 0)
	if err != nil {
		return err
	}
	for _, i := range assemble.MarkFurniture(pages, s.cfg.Parser.FurnitureRepeatRatio) {
		if err := s.st.Pages.UpdateMarkdown(ctx, d.ID, d.Gen, pages[i], assemble.PlainText(pages[i])); err != nil {
			return err
		}
	}
	var sb strings.Builder
	pos := 0
	offsets := make(map[int]int, len(pages))
	textPages := 0
	for _, p := range pages {
		marker := fmt.Sprintf("<!-- page:%d -->\n\n", p.PageNo)
		sb.WriteString(marker)
		pos += textutil.RuneLen(marker)
		offsets[p.PageNo] = pos
		sb.WriteString(p.Markdown)
		sb.WriteString("\n\n")
		pos += textutil.RuneLen(p.Markdown) + 2
		if p.TextSource == types.TextSourceMerged || p.TextSource == types.TextSourceLayerOnly || p.TextQuality >= 0.5 {
			textPages++
		}
	}
	mdKey := s.keys.Markdown(d.KBID, d.ID, d.Gen)
	md := sb.String()
	if _, err := s.objects.Put(ctx, mdKey, strings.NewReader(md), int64(len(md)), "text/markdown; charset=utf-8"); err != nil {
		return err
	}
	if err := s.st.Pages.SetDocOffsets(ctx, d.ID, d.Gen, offsets); err != nil {
		return err
	}
	cur, err := s.st.Documents.Get(ctx, d.ID)
	if err != nil {
		return err
	}
	parse := types.StageDone
	switch {
	case cur.PagesFailed >= cur.PageCount:
		return s.fail(ctx, d, "parse", errors.New("every page failed"))
	case cur.PagesFailed > 0:
		parse = types.StagePartial
	}
	if _, err := s.st.Documents.Update(ctx, d.ID, d.Gen, postgres.DocUpdate{
		Status: strp(types.DocIndexing), ParseStatus: &parse, MarkdownKey: &mdKey, PagesTextLayer: &textPages, IndexStatus: strp(types.StageProcessing),
	}); err != nil {
		return err
	}
	if err := s.enqueue(ctx, types.TaskIndexBuild, d, nil, fmt.Sprintf("index:%s:%d", d.ID, d.Gen)); err != nil {
		return err
	}
	if d.Gen > 1 {
		return s.enqueue(ctx, types.TaskGenCleanup, d, nil, fmt.Sprintf("gencleanup:%s:%d", d.ID, d.Gen))
	}
	return nil
}

// purge removes a deleted document's objects and rows.
func (s *Service) purge(ctx context.Context, d types.Document, _ types.DocTaskPayload) error {
	if d.Status != types.DocDeleting {
		return nil
	}
	if _, err := s.objects.DeletePrefix(ctx, s.keys.Doc(d.KBID, d.ID)); err != nil {
		return err
	}
	s.files.Forget(d.StorageKey)
	return s.st.Documents.Purge(ctx, d.ID)
}

// genCleanup removes objects and rows of generations older than d.Gen.
func (s *Service) genCleanup(ctx context.Context, d types.Document, _ types.DocTaskPayload) error {
	for g := 1; g < d.Gen; g++ {
		if _, err := s.objects.DeletePrefix(ctx, s.keys.Gen(d.KBID, d.ID, g)); err != nil {
			return err
		}
	}
	return s.st.Index.DeleteOldGens(ctx, d.ID, d.Gen)
}

// Housekeeping re-dispatches stalled work (§4.3 restart recovery): pages
// stuck in rendering/ocr return to the queue, documents that stopped moving
// get their stage re-enqueued, deleted documents are purged.
func (s *Service) Housekeeping(ctx context.Context) error {
	stale := 2 * max(s.cfg.Parser.Render.PageTimeout*time.Duration(max(1, s.cfg.Parser.Render.BatchPages)), 3*time.Minute)
	if n, err := s.st.Pages.ResetStale(ctx, time.Now().Add(-stale)); err != nil {
		return err
	} else if n > 0 {
		s.log.Info("housekeeping: reset stale pages", "count", n)
	}
	bucket := time.Now().Unix() / 300
	stuck, err := s.st.Documents.Stuck(ctx, time.Now().Add(-10*time.Minute), 200)
	if err != nil {
		return err
	}
	for _, d := range stuck {
		var err error
		switch d.Status {
		case types.DocQueued, types.DocSplitting:
			err = s.enqueue(ctx, types.TaskDocumentSplit, d, nil, fmt.Sprintf("split:%s:%d:hk%d", d.ID, d.Gen, bucket))
		case types.DocParsing:
			err = s.Advance(ctx, d)
		case types.DocAssembling:
			err = s.enqueue(ctx, types.TaskDocumentAssemble, d, nil, fmt.Sprintf("assemble:%s:%d:hk%d", d.ID, d.Gen, bucket))
		case types.DocIndexing:
			err = s.enqueue(ctx, types.TaskIndexBuild, d, nil, fmt.Sprintf("index:%s:%d:hk%d", d.ID, d.Gen, bucket))
		}
		if err != nil {
			s.log.Warn("housekeeping: re-dispatch failed", "doc", d.ID, "err", err)
		}
	}
	deleting, err := s.st.Documents.Deleting(ctx, 200)
	if err != nil {
		return err
	}
	for _, d := range deleting {
		_ = s.q.Enqueue(ctx, types.TaskDocumentDelete, types.DocTaskPayload{DocumentID: d.ID, KBID: d.KBID, Gen: -1},
			queue.Opts{TaskID: fmt.Sprintf("del:%s:hk%d", d.ID, bucket)})
	}
	return nil
}

func (s *Service) putGzip(ctx context.Context, key string, data []byte) error {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	_, err := s.objects.Put(ctx, key, &buf, int64(buf.Len()), "application/gzip")
	return err
}

func (s *Service) putGzipJSON(ctx context.Context, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.putGzip(ctx, key, b)
}

func (s *Service) getGzipJSON(ctx context.Context, key string, v any) error {
	rc, _, err := s.objects.Get(ctx, key)
	if err != nil {
		return err
	}
	defer rc.Close()
	zr, err := gzip.NewReader(rc)
	if err != nil {
		return err
	}
	defer zr.Close()
	return json.NewDecoder(io.LimitReader(zr, 64<<20)).Decode(v)
}
