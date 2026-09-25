package document

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/config"
	"github.com/thanhenti/bepaylot/internal/parser"
	"github.com/thanhenti/bepaylot/internal/parser/pdf"
	"github.com/thanhenti/bepaylot/internal/parser/pdf/pdftest"
	"github.com/thanhenti/bepaylot/internal/queue"
	"github.com/thanhenti/bepaylot/internal/storage"
	"github.com/thanhenti/bepaylot/internal/types"
)

// fakeOCR returns one text region per page with the lines configured for
// that page, placed where pdftest draws them. Pages in fail always error.
type fakeOCR struct {
	mu    sync.Mutex
	pages map[int][]string
	fail  map[int]bool
	calls map[int]int
	dpi   float64
}

func (f *fakeOCR) Name() string                 { return "fake" }
func (f *fakeOCR) Health(context.Context) error { return nil }
func (f *fakeOCR) ParsePage(_ context.Context, in parser.PageImage, _ parser.PageOptions) (*parser.RawPage, error) {
	f.mu.Lock()
	f.calls[in.PageNo]++
	f.mu.Unlock()
	if f.fail[in.PageNo] {
		return nil, errors.New("ocr service error")
	}
	px := func(pt float64) float64 { return pt * f.dpi / 72 }
	raw := &parser.RawPage{Width: in.Width, Height: in.Height, Raw: []byte(`{}`)}
	lines := f.pages[in.PageNo]
	region := types.BBox{X0: px(60), Y0: px(50), X1: px(560), Y1: px(80 + 18*float64(len(lines)))}
	raw.Regions = []parser.RawRegion{{ID: 0, Class: "text", Confidence: 0.9, Quad: types.QuadFromBBox(region)}}
	for i, t := range lines {
		top := 72 + 18*float64(i)
		b := types.BBox{X0: px(72), Y0: px(top - 12), X1: px(72 + 9*float64(len([]rune(t)))), Y1: px(top + 3)}
		raw.Lines = append(raw.Lines, parser.RawLine{ID: i, Text: t, Confidence: 0.9, LayoutID: 0, Quad: types.QuadFromBBox(b)})
		raw.ReadingOrder = append(raw.ReadingOrder, i)
	}
	return raw, nil
}

func setup(t *testing.T) (*Service, *queue.Inline, *postgres.Store, uuid.UUID, *fakeOCR) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run pipeline integration tests")
	}
	if testing.Short() {
		t.Skip("short mode")
	}
	ctx := context.Background()
	st, err := postgres.Open(ctx, config.DB{DSN: dsn, MaxConns: 8, MinConns: 1, AutoMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	user, err := st.Users.Create(ctx, fmt.Sprintf("doc-test-%s@bepilot.local", uuid.NewString()), "t")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, user.ID) })

	cfg := config.Defaults()
	cfg.Parser.Render.DPI = 100
	cfg.Parser.DefaultEngine = "fake"
	cfg.Parser.Render.BatchPages = 2
	cfg.Workers.OCRInflightPages = 2
	cfg.Workers.RenderAheadPages = 3

	r, err := pdf.New(pdf.Config{Mode: "webassembly", Workers: 1, PageTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	objects := storage.NewMemory()
	files, err := storage.NewFileCache(t.TempDir(), 1<<30, objects)
	if err != nil {
		t.Fatal(err)
	}
	ocr := &fakeOCR{pages: map[int][]string{}, fail: map[int]bool{}, calls: map[int]int{}, dpi: 100}
	engines := parser.NewRegistry("fake")
	engines.Register(ocr)
	q := queue.NewInline()
	svc := New(Deps{Store: st, Objects: objects, Files: files, Queue: q, Renderer: r, Engines: engines, Config: cfg})
	handlers := svc.Handlers()
	handlers[types.TaskIndexBuild] = func(context.Context, []byte) error { return nil } // Module 2 stub
	q.Register(handlers, nil)
	return svc, q, st, user.ID, ocr
}

func TestPipelineEndToEnd(t *testing.T) {
	svc, q, st, owner, ocr := setup(t)
	ctx := context.Background()
	schema := &types.MetadataSchema{Fields: []types.MetadataField{{Key: "ma_ho_so", Type: "string", Normalize: "upper_trim", Indexed: true}}}
	kb, err := svc.CreateKB(ctx, owner, "Hồ sơ vay", "", types.KBConfig{}, schema, false)
	if err != nil {
		t.Fatal(err)
	}

	pdfBytes := pdftest.Build([]pdftest.Page{
		{"UBND XA NHA BÍCH", "Ma so ho kinh doanh: 070082001498"}, // page 1: OCR drops the accent
		{"Von kinh doanh: 50.000.000 dong"},                       // page 2: OCR fails, text layer fallback
		{},                                                        // page 3: OCR fails, no text → failed
		{"Trang cuoi"},
	}, pdftest.Options{PDFAPart: 2, PDFAConformance: "u", Outline: true, Title: "GCN HKD"})
	ocr.pages[1] = []string{"UBND XA NHA BICH", "Ma so ho kinh doanh: 070082001498"}
	ocr.pages[4] = []string{"Trang cuoi"}
	ocr.fail[2], ocr.fail[3] = true, true

	up, err := svc.BeginUpload(ctx, owner, kb.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	up.SetSharedMetadata(map[string]any{"ma_ho_so": " hs-2026-000123 "})
	if err := up.AddFile(ctx, "gcn.pdf", "application/pdf", bytes.NewReader(pdfBytes)); err != nil {
		t.Fatal(err)
	}
	res, err := up.Finish(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Documents) != 1 || res.Documents[0].Metadata["ma_ho_so"] != "HS-2026-000123" {
		t.Fatalf("upload result = %+v", res)
	}
	docID := res.Documents[0].DocumentID

	if err := q.Drain(ctx, 500); err != nil {
		t.Fatal(err)
	}
	d, err := st.Documents.Get(ctx, docID)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range q.Failed {
		t.Log("failed task: ", f)
	}
	if d.PageCount != 4 || d.PagesDone != 3 || d.PagesFailed != 1 || d.ParseStatus != types.StagePartial || d.Status != types.DocIndexing {
		t.Fatalf("doc = status %s parse %s pages %d done %d failed %d err %q", d.Status, d.ParseStatus, d.PageCount, d.PagesDone, d.PagesFailed, d.Error)
	}
	if d.PDFAPart == nil || *d.PDFAPart != 2 || d.PDFInfo["Title"] != "GCN HKD" || d.PDFInfo["bookmarks"] == nil {
		t.Fatalf("pdf info = %v part=%v", d.PDFInfo, d.PDFAPart)
	}
	// OCR retried up to MaxRetry on the failing pages, once elsewhere.
	if ocr.calls[1] != 1 || ocr.calls[2] != 4 {
		t.Fatalf("ocr calls = %v", ocr.calls)
	}

	// Page 1: text layer fixed the accent OCR dropped.
	p1, err := svc.Page(ctx, owner, docID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p1.Markdown, "NHA BÍCH") || p1.TextSource != types.TextSourceMerged {
		t.Fatalf("page 1 markdown %q source %s", p1.Markdown, p1.TextSource)
	}
	if p1.Lines[0].TextOCR != "UBND XA NHA BICH" {
		t.Fatalf("line 0 = %+v", p1.Lines[0])
	}
	// Page 2: OCR failed, rebuilt from text layer.
	p2, _ := svc.Page(ctx, owner, docID, 2)
	if p2.Status != types.PageDone || p2.TextSource != types.TextSourceLayerOnly || !strings.Contains(p2.Markdown, "50.000.000") {
		t.Fatalf("page 2 = %s %s %q", p2.Status, p2.TextSource, p2.Markdown)
	}
	detail, _ := svc.Detail(ctx, owner, docID, false)
	if len(detail.FailedPages) != 1 || detail.FailedPages[0].PageNo != 3 {
		t.Fatalf("failed pages = %+v", detail.FailedPages)
	}

	// Full markdown + locate by text and by markdown offsets.
	md, err := svc.Markdown(ctx, owner, docID, 0, 0)
	if err != nil || !strings.Contains(md, "<!-- page:4 -->") {
		t.Fatalf("markdown err=%v:\n%s", err, md)
	}
	locs, err := svc.Locate(ctx, owner, docID, LocateRequest{Text: "nha bich"})
	if err != nil || len(locs) == 0 || locs[0].PageNo != 1 || locs[0].BBox.X0 < 90 {
		t.Fatalf("locate = %+v err %v", locs, err)
	}
	pg1, _ := st.Pages.Get(ctx, docID, 1)
	start := pg1.DocMdOffset + p1.Lines[1].MdStart
	end := pg1.DocMdOffset + p1.Lines[1].MdEnd
	locs, err = svc.Locate(ctx, owner, docID, LocateRequest{MdStart: &start, MdEnd: &end})
	if err != nil || len(locs) != 1 || locs[0].LineNo != 1 {
		t.Fatalf("locate range = %+v err %v", locs, err)
	}

	// Metadata filter lists the document; a wrong code does not.
	docs, err := svc.ListDocuments(ctx, owner, kb.ID, postgres.DocumentFilter{Metadata: types.MetadataFilter{"ma_ho_so": "hs-2026-000123"}})
	if err != nil || len(docs) != 1 {
		t.Fatalf("filter docs = %d err %v", len(docs), err)
	}
	docs, _ = svc.ListDocuments(ctx, owner, kb.ID, postgres.DocumentFilter{Metadata: types.MetadataFilter{"ma_ho_so": "HS-OTHER"}})
	if len(docs) != 0 {
		t.Fatalf("wrong code matched %d docs", len(docs))
	}

	// Duplicate upload returns the existing document.
	up2, _ := svc.BeginUpload(ctx, owner, kb.ID, false)
	_ = up2.AddFile(ctx, "copy.pdf", "", bytes.NewReader(pdfBytes))
	res2, _ := up2.Finish(ctx)
	if len(res2.Documents) != 1 || !res2.Documents[0].Duplicate || res2.Documents[0].DocumentID != docID {
		t.Fatalf("duplicate = %+v", res2)
	}

	// Page reparse after fixing OCR for page 3 turns the document whole.
	delete(ocr.fail, 3)
	ocr.pages[3] = []string{"Recovered"}
	_, _ = st.Documents.Update(ctx, docID, d.Gen, postgres.DocUpdate{Status: strp(types.DocCompleted)})
	if _, err := svc.Reparse(ctx, owner, docID, ReparseRequest{Pages: []int{3}}); err != nil {
		t.Fatal(err)
	}
	if err := q.Drain(ctx, 100); err != nil {
		t.Fatal(err)
	}
	d, _ = st.Documents.Get(ctx, docID)
	if d.PagesDone != 4 || d.PagesFailed != 0 || d.ParseStatus != types.StageDone {
		t.Fatalf("after page reparse: done %d failed %d parse %s", d.PagesDone, d.PagesFailed, d.ParseStatus)
	}

	// Delete purges rows and objects.
	if err := svc.Delete(ctx, owner, docID); err != nil {
		t.Fatal(err)
	}
	if err := q.Drain(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Documents.GetAny(ctx, docID); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("document not purged: %v", err)
	}
	for _, k := range svc.objects.(*storage.Memory).Keys() {
		if strings.Contains(k, docID.String()) {
			t.Fatalf("object left behind: %s", k)
		}
	}
}
