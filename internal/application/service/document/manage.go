package document

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/application/service/metadata"
	"github.com/thanhenti/bepaylot/internal/queue"
	"github.com/thanhenti/bepaylot/internal/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
)

func notFound(err error) error {
	if errors.Is(err, postgres.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// ---- knowledge bases ----

// CreateKB creates a knowledge base.
func (s *Service) CreateKB(ctx context.Context, owner uuid.UUID, name, desc string, cfg types.KBConfig, schema *types.MetadataSchema, temporary bool) (types.KnowledgeBase, error) {
	if strings.TrimSpace(name) == "" {
		return types.KnowledgeBase{}, fmt.Errorf("%w: name is required", ErrBadRequest)
	}
	if err := metadata.ValidateSchema(schema); err != nil {
		return types.KnowledgeBase{}, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	kb, err := s.st.KBs.Create(ctx, types.KnowledgeBase{OwnerID: owner, Name: name, Description: desc, Config: cfg, MetadataSchema: schema, IsTemporary: temporary})
	if err != nil {
		return kb, err
	}
	s.indexMetadataFields(ctx, schema)
	return kb, nil
}

func (s *Service) indexMetadataFields(ctx context.Context, schema *types.MetadataSchema) {
	if schema == nil {
		return
	}
	for _, f := range schema.Fields {
		if f.Indexed {
			if err := s.st.Documents.CreateMetadataIndex(ctx, f.Key); err != nil {
				s.log.Warn("metadata index", "key", f.Key, "err", err)
			}
		}
	}
}

// ListKBs lists the owner's knowledge bases.
func (s *Service) ListKBs(ctx context.Context, owner uuid.UUID) ([]types.KnowledgeBase, error) {
	return s.st.KBs.List(ctx, owner, false)
}

// GetKBOwned returns a KB the owner owns.
func (s *Service) GetKBOwned(ctx context.Context, owner, id uuid.UUID) (types.KnowledgeBase, error) {
	kb, err := s.st.KBs.GetOwned(ctx, id, owner)
	return kb, notFound(err)
}

// UpdateKB applies a patch; a new metadata schema is validated.
func (s *Service) UpdateKB(ctx context.Context, owner, id uuid.UUID, p postgres.KBPatch) (types.KnowledgeBase, error) {
	if p.MetadataSchema != nil {
		if err := metadata.ValidateSchema(*p.MetadataSchema); err != nil {
			return types.KnowledgeBase{}, fmt.Errorf("%w: %v", ErrBadRequest, err)
		}
	}
	kb, err := s.st.KBs.Update(ctx, id, owner, p)
	if err != nil {
		return kb, notFound(err)
	}
	if p.MetadataSchema != nil {
		s.indexMetadataFields(ctx, *p.MetadataSchema)
	}
	return kb, nil
}

// DeleteKB soft-deletes a KB; housekeeping purges its documents.
func (s *Service) DeleteKB(ctx context.Context, owner, id uuid.UUID) error {
	if err := notFound(s.st.KBs.SoftDelete(ctx, id, owner)); err != nil {
		return err
	}
	docs, _ := s.st.Documents.Deleting(ctx, 10000)
	for _, d := range docs {
		if d.KBID == id {
			_ = s.q.Enqueue(ctx, types.TaskDocumentDelete, types.DocTaskPayload{DocumentID: d.ID, KBID: d.KBID, Gen: -1}, queue.Opts{TaskID: "del:" + d.ID.String()})
		}
	}
	return nil
}

// ---- documents ----

// ListDocuments lists documents with metadata filtering (§6.2).
func (s *Service) ListDocuments(ctx context.Context, owner, kbID uuid.UUID, f postgres.DocumentFilter) ([]types.Document, error) {
	kb, err := s.st.KBs.GetOwned(ctx, kbID, owner)
	if err != nil {
		return nil, notFound(err)
	}
	f.OwnerID = owner
	f.KBIDs = []uuid.UUID{kbID}
	f.Schema = kb.MetadataSchema
	f.Metadata = metadata.NormalizeFilter(kb.MetadataSchema, f.Metadata)
	docs, err := s.st.Documents.List(ctx, f)
	if err != nil && strings.Contains(err.Error(), "metadata filter") {
		return nil, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	return docs, err
}

// DocumentDetail is a document plus its failed pages and recent spans.
type DocumentDetail struct {
	types.Document
	Progress    float64         `json:"progress"`
	FailedPages []FailedPage    `json:"failed_pages,omitempty"`
	Spans       []postgres.Span `json:"spans,omitempty"`
}

// FailedPage lists a page that failed for good.
type FailedPage struct {
	PageNo int    `json:"page_no"`
	Error  string `json:"error"`
}

// GetOwned returns a document the owner can access.
func (s *Service) GetOwned(ctx context.Context, owner, id uuid.UUID) (types.Document, error) {
	d, err := s.st.Documents.GetOwned(ctx, id, owner)
	return d, notFound(err)
}

// Detail returns the document with failed pages and spans.
func (s *Service) Detail(ctx context.Context, owner, id uuid.UUID, withSpans bool) (*DocumentDetail, error) {
	d, err := s.GetOwned(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	out := &DocumentDetail{Document: d, Progress: d.Progress()}
	if d.PagesFailed > 0 {
		pages, err := s.st.Pages.List(ctx, d.ID, d.Gen, 0, 0)
		if err == nil {
			for _, p := range pages {
				if p.Status == types.PageFailed {
					out.FailedPages = append(out.FailedPages, FailedPage{PageNo: p.PageNo, Error: p.Error})
				}
			}
		}
	}
	if withSpans {
		out.Spans, _ = s.st.Tasks.Spans(ctx, d.ID, d.Gen, 2000)
	}
	return out, nil
}

// Cancel stops a document's pipeline; finished pages are kept.
func (s *Service) Cancel(ctx context.Context, owner, id uuid.UUID) error {
	d, err := s.GetOwned(ctx, owner, id)
	if err != nil {
		return err
	}
	if types.Terminal(d.Status) {
		return fmt.Errorf("%w: document is already %s", ErrBadRequest, d.Status)
	}
	_, err = s.st.Documents.Update(ctx, d.ID, d.Gen, postgres.DocUpdate{Status: strp(types.DocCancelled)})
	return err
}

// Delete marks a document deleting and enqueues its purge.
func (s *Service) Delete(ctx context.Context, owner, id uuid.UUID) error {
	d, err := s.GetOwned(ctx, owner, id)
	if err != nil {
		return err
	}
	if err := notFound(s.st.Documents.SoftDelete(ctx, d.ID)); err != nil {
		return err
	}
	return s.q.Enqueue(ctx, types.TaskDocumentDelete, types.DocTaskPayload{DocumentID: d.ID, KBID: d.KBID, Gen: -1}, queue.Opts{TaskID: "del:" + d.ID.String()})
}

// ReparseRequest selects what to reparse.
type ReparseRequest struct {
	Pages  []int  `json:"pages,omitempty"`
	Engine string `json:"engine,omitempty"`
}

// Reparse re-runs the pipeline for the whole document (new generation) or
// only OCR+assembly for selected pages of the current generation.
func (s *Service) Reparse(ctx context.Context, owner, id uuid.UUID, req ReparseRequest) (types.Document, error) {
	d, err := s.GetOwned(ctx, owner, id)
	if err != nil {
		return d, err
	}
	if req.Engine != "" {
		if _, err := s.engines.Get(req.Engine); err != nil {
			return d, fmt.Errorf("%w: %v", ErrBadRequest, err)
		}
		if err := s.st.Documents.SetEngine(ctx, d.ID, req.Engine); err != nil {
			return d, err
		}
	}
	if len(req.Pages) == 0 {
		gen, err := s.st.Documents.BumpGen(ctx, d.ID)
		if err != nil {
			return d, notFound(err)
		}
		d.Gen, d.Status = gen, types.DocQueued
		return d, s.enqueue(ctx, types.TaskDocumentSplit, d, nil, fmt.Sprintf("split:%s:%d", d.ID, gen))
	}
	if !types.Terminal(d.Status) || d.Status == types.DocCancelled {
		return d, fmt.Errorf("%w: page reparse needs a finished document (status %s)", ErrBadRequest, d.Status)
	}
	for _, p := range req.Pages {
		if p < 1 || p > d.PageCount {
			return d, fmt.Errorf("%w: page %d out of range 1..%d", ErrBadRequest, p, d.PageCount)
		}
	}
	doneN, failedN, err := s.st.Pages.ResetPages(ctx, d.ID, d.Gen, req.Pages, types.PageRendered)
	if err != nil {
		return d, err
	}
	if err := s.st.Documents.AdjustCounters(ctx, d.ID, d.Gen, -doneN, -failedN); err != nil {
		return d, err
	}
	if _, err := s.st.Documents.Update(ctx, d.ID, d.Gen, postgres.DocUpdate{Status: strp(types.DocParsing), ParseStatus: strp(types.StageProcessing)}); err != nil {
		return d, err
	}
	d.Status = types.DocParsing
	return d, s.Advance(ctx, d)
}

// UpdateMetadata merges or replaces a document's metadata after validation.
func (s *Service) UpdateMetadata(ctx context.Context, owner, id uuid.UUID, meta map[string]any, replace bool) (types.Document, error) {
	d, err := s.GetOwned(ctx, owner, id)
	if err != nil {
		return d, err
	}
	kb, err := s.st.KBs.Get(ctx, d.KBID)
	if err != nil {
		return d, err
	}
	next := meta
	if !replace {
		next = metadata.Merge(d.Metadata, meta)
		for k, v := range meta {
			if v == nil {
				delete(next, k)
			}
		}
	}
	clean, errs := metadata.Validate(kb.MetadataSchema, next)
	if len(errs) > 0 {
		return d, fmt.Errorf("%w: %v", ErrBadRequest, errs)
	}
	out, err := s.st.Documents.SetMetadata(ctx, d.ID, clean)
	return out, notFound(err)
}

// BulkMetadata updates metadata on every document matching the filter.
func (s *Service) BulkMetadata(ctx context.Context, owner, kbID uuid.UUID, f postgres.DocumentFilter, set map[string]any, unset []string) (int64, error) {
	kb, err := s.st.KBs.GetOwned(ctx, kbID, owner)
	if err != nil {
		return 0, notFound(err)
	}
	if len(f.Metadata) == 0 && len(f.DocumentIDs) == 0 && f.BatchID == nil {
		return 0, fmt.Errorf("%w: a filter (metadata, document_ids or batch_id) is required", ErrBadRequest)
	}
	clean, errs := metadata.Validate(&types.MetadataSchema{Fields: fieldsOf(kb.MetadataSchema)}, set)
	if len(errs) > 0 {
		return 0, fmt.Errorf("%w: %v", ErrBadRequest, errs)
	}
	f.OwnerID, f.KBIDs, f.Schema = owner, []uuid.UUID{kbID}, kb.MetadataSchema
	f.Metadata = metadata.NormalizeFilter(kb.MetadataSchema, f.Metadata)
	return s.st.Documents.BulkUpdateMetadata(ctx, f, clean, unset)
}

func fieldsOf(s *types.MetadataSchema) []types.MetadataField {
	if s == nil {
		return nil
	}
	out := make([]types.MetadataField, len(s.Fields))
	for i, f := range s.Fields {
		f.Required = false // partial updates need not repeat required fields
		out[i] = f
	}
	return out
}

// MetadataValues lists distinct values of a metadata key in a KB.
func (s *Service) MetadataValues(ctx context.Context, owner, kbID uuid.UUID, key, prefix string, limit int) ([]postgres.MetadataValue, error) {
	if _, err := s.st.KBs.GetOwned(ctx, kbID, owner); err != nil {
		return nil, notFound(err)
	}
	return s.st.Documents.MetadataValues(ctx, kbID, key, prefix, limit)
}

// MetadataKeys lists the metadata keys a KB exposes: the schema's fields, or
// the keys in use when there is no schema.
func (s *Service) MetadataKeys(ctx context.Context, kb types.KnowledgeBase) []types.MetadataField {
	if kb.MetadataSchema != nil && len(kb.MetadataSchema.Fields) > 0 {
		return kb.MetadataSchema.Fields
	}
	keys, _ := s.st.Documents.MetadataKeys(ctx, kb.ID)
	sort.Strings(keys)
	out := make([]types.MetadataField, len(keys))
	for i, k := range keys {
		out[i] = types.MetadataField{Key: k, Type: "string"}
	}
	return out
}

// ---- parsed content ----

// PageView is a page with its blocks and lines.
type PageView struct {
	types.DocumentPage
	Blocks []postgres.PageBlock `json:"blocks"`
	Lines  []postgres.PageLine  `json:"lines"`
}

// Pages lists page rows (without blocks/lines).
func (s *Service) Pages(ctx context.Context, owner, id uuid.UUID, from, to int) ([]types.DocumentPage, error) {
	d, err := s.GetOwned(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	return s.st.Pages.List(ctx, d.ID, d.Gen, from, to)
}

// Page returns one page with blocks and lines.
func (s *Service) Page(ctx context.Context, owner, id uuid.UUID, pageNo int) (*PageView, error) {
	d, err := s.GetOwned(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	pg, err := s.st.Pages.Get(ctx, d.ID, pageNo)
	if err != nil || pg.Gen != d.Gen {
		return nil, ErrNotFound
	}
	blocks, err := s.st.Pages.Blocks(ctx, d.ID, pageNo, pageNo)
	if err != nil {
		return nil, err
	}
	lines, err := s.st.Pages.Lines(ctx, d.ID, pageNo, pageNo)
	if err != nil {
		return nil, err
	}
	return &PageView{DocumentPage: pg, Blocks: blocks, Lines: lines}, nil
}

// PageImage returns a presigned URL (presign on) or a stream of the image.
func (s *Service) PageImage(ctx context.Context, owner, id uuid.UUID, pageNo int) (url string, body io.ReadCloser, size int64, err error) {
	d, err := s.GetOwned(ctx, owner, id)
	if err != nil {
		return "", nil, 0, err
	}
	pg, err := s.st.Pages.Get(ctx, d.ID, pageNo)
	if err != nil || pg.ImageKey == "" {
		return "", nil, 0, ErrNotFound
	}
	if s.cfg.Storage.PresignEnabled() {
		u, err := s.objects.PresignGet(ctx, pg.ImageKey, s.cfg.Storage.PresignTTL)
		return u, nil, 0, err
	}
	rc, info, err := s.objects.Get(ctx, pg.ImageKey)
	return "", rc, info.Size, err
}

// SourceFile streams the original upload.
func (s *Service) SourceFile(ctx context.Context, owner, id uuid.UUID) (types.Document, io.ReadCloser, int64, error) {
	d, err := s.GetOwned(ctx, owner, id)
	if err != nil {
		return d, nil, 0, err
	}
	rc, info, err := s.objects.Get(ctx, d.StorageKey)
	return d, rc, info.Size, err
}

// Markdown returns the markdown of pages [from, to] (0 = all), each page
// prefixed with its page marker.
func (s *Service) Markdown(ctx context.Context, owner, id uuid.UUID, from, to int) (string, error) {
	d, err := s.GetOwned(ctx, owner, id)
	if err != nil {
		return "", err
	}
	pages, err := s.st.Pages.List(ctx, d.ID, d.Gen, from, to)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, p := range pages {
		if p.Status != types.PageDone {
			continue
		}
		fmt.Fprintf(&sb, "<!-- page:%d -->\n\n%s\n\n", p.PageNo, p.Markdown)
	}
	return sb.String(), nil
}

// LocateRequest is one of: a line, a markdown range, or a text query.
type LocateRequest struct {
	Page    int    `json:"page,omitempty"`
	Line    *int   `json:"line,omitempty"`
	MdStart *int   `json:"md_start,omitempty"`
	MdEnd   *int   `json:"md_end,omitempty"`
	Text    string `json:"text,omitempty"`
	Fuzzy   bool   `json:"fuzzy,omitempty"`
}

// Location is one resolved position on a page (§5.6).
type Location struct {
	PageNo int        `json:"page_no"`
	LineNo int        `json:"line_no"`
	Text   string     `json:"text"`
	BBox   types.BBox `json:"bbox"`
	// BBoxPt is the box in PDF points (origin top-left of the page).
	BBoxPt  types.BBox `json:"bbox_pt"`
	Quad    types.Quad `json:"quad"`
	BlockNo int        `json:"block_no"`
	Score   float64    `json:"score,omitempty"`
}

// Locate resolves a line, a full-markdown range or a text to page positions.
func (s *Service) Locate(ctx context.Context, owner, id uuid.UUID, req LocateRequest) ([]Location, error) {
	d, err := s.GetOwned(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	dpi := map[int]float64{}
	pt := func(page int, b types.BBox) types.BBox {
		r, ok := dpi[page]
		if !ok {
			if pg, err := s.st.Pages.Get(ctx, d.ID, page); err == nil && pg.DPI > 0 {
				r = 72 / pg.DPI
			}
			dpi[page] = r
		}
		return types.BBox{X0: b.X0 * r, Y0: b.Y0 * r, X1: b.X1 * r, Y1: b.Y1 * r}
	}
	toLoc := func(l postgres.PageLine) Location {
		return Location{PageNo: l.PageNo, LineNo: l.LineNo, Text: l.Text, BBox: l.BBox, BBoxPt: pt(l.PageNo, l.BBox), Quad: l.Quad, BlockNo: l.BlockNo}
	}
	var out []Location
	switch {
	case req.Line != nil:
		if req.Page < 1 {
			return nil, fmt.Errorf("%w: page is required with line", ErrBadRequest)
		}
		lines, err := s.st.Pages.LinesByNumber(ctx, d.ID, req.Page, []int{*req.Line})
		if err != nil {
			return nil, err
		}
		for _, l := range lines {
			out = append(out, toLoc(l))
		}
	case req.MdStart != nil && req.MdEnd != nil:
		pages, err := s.st.Pages.List(ctx, d.ID, d.Gen, 0, 0)
		if err != nil {
			return nil, err
		}
		for _, p := range pages {
			pageLen := textutil.RuneLen(p.Markdown)
			if p.DocMdOffset+pageLen < *req.MdStart || p.DocMdOffset > *req.MdEnd {
				continue
			}
			lines, err := s.st.Pages.Lines(ctx, d.ID, p.PageNo, p.PageNo)
			if err != nil {
				return nil, err
			}
			for _, l := range lines {
				if l.MdStart < 0 {
					continue
				}
				a, b := p.DocMdOffset+l.MdStart, p.DocMdOffset+l.MdEnd
				if b > *req.MdStart && a < *req.MdEnd {
					out = append(out, toLoc(l))
				}
			}
		}
	case strings.TrimSpace(req.Text) != "":
		from, to := 0, 0
		if req.Page > 0 {
			from, to = req.Page, req.Page
		}
		hits, err := s.st.Pages.SearchLines(ctx, d.ID, d.Gen, req.Text, from, to, 50)
		if err != nil {
			return nil, err
		}
		for _, h := range hits {
			if !req.Fuzzy && !textutil.ContainsFold(h.Text, req.Text) {
				continue
			}
			out = append(out, Location{PageNo: h.PageNo, LineNo: h.LineNo, Text: h.Text, BBox: h.BBox, BBoxPt: pt(h.PageNo, h.BBox), Score: h.Score})
		}
	default:
		return nil, fmt.Errorf("%w: give line+page, md_start+md_end, or text", ErrBadRequest)
	}
	return out, nil
}

// EnsureTempKB returns (creating once) the temporary KB used for chat
// attachments of a session.
func (s *Service) EnsureTempKB(ctx context.Context, owner uuid.UUID, sessionID uuid.UUID) (types.KnowledgeBase, error) {
	name := "session:" + sessionID.String()
	kbs, err := s.st.KBs.List(ctx, owner, true)
	if err != nil {
		return types.KnowledgeBase{}, err
	}
	for _, kb := range kbs {
		if kb.IsTemporary && kb.Name == name {
			return kb, nil
		}
	}
	return s.st.KBs.Create(ctx, types.KnowledgeBase{OwnerID: owner, Name: name, Description: "chat attachments", IsTemporary: true})
}
