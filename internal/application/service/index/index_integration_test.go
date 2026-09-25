package index_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/application/service/document"
	"github.com/thanhenti/bepaylot/internal/application/service/index"
	"github.com/thanhenti/bepaylot/internal/config"
	"github.com/thanhenti/bepaylot/internal/llm"
	"github.com/thanhenti/bepaylot/internal/parser"
	"github.com/thanhenti/bepaylot/internal/parser/pdf"
	"github.com/thanhenti/bepaylot/internal/parser/pdf/pdftest"
	"github.com/thanhenti/bepaylot/internal/queue"
	"github.com/thanhenti/bepaylot/internal/storage"
	"github.com/thanhenti/bepaylot/internal/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
)

// textOCR "recognizes" pages by returning the PDF text layer lines verbatim;
// it is enough to drive the pipeline without an OCR service.
type textOCR struct{ pages map[string][][]string } // file marker → page lines

func (textOCR) Name() string                 { return "fake" }
func (textOCR) Health(context.Context) error { return nil }
func (f textOCR) ParsePage(_ context.Context, in parser.PageImage, _ parser.PageOptions) (*parser.RawPage, error) {
	return &parser.RawPage{Width: in.Width, Height: in.Height, Raw: []byte("{}")}, nil
}

// scripted is a fake LLM that answers each prompt kind deterministically.
type scripted struct {
	mu    sync.Mutex
	calls map[string]int
}

var (
	lineRe     = regexp.MustCompile(`\[L(\d+)\] (.*)`)
	pageRe     = regexp.MustCompile(`<page n="(\d+)" doc="(d\d+)"`)
	nodeRe     = regexp.MustCompile(`\[(n\d+)\] ([^(]*)`)
	partRe     = regexp.MustCompile(`<part id="(n\d+)"`)
	questionRe = regexp.MustCompile(`Question: (.*)`)
)

func (s *scripted) CompleteJSON(_ context.Context, system, user string, out any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kind := strings.SplitN(system, "\n", 2)[0]
	s.calls[kind]++
	var reply any
	switch {
	case strings.Contains(system, "summarize parts"):
		m := map[string]string{}
		for _, x := range partRe.FindAllStringSubmatch(user, -1) {
			m[x[1]] = "Tóm tắt " + x[1]
		}
		reply = map[string]any{"summaries": m}
	case strings.Contains(system, "catalogue card"):
		reply = map[string]any{"title": "Giấy chứng nhận đăng ký hộ kinh doanh", "doc_type": "Giấy chứng nhận", "summary": "Hộ kinh doanh vật liệu xây dựng"}
	case strings.Contains(system, "pick which documents"):
		reply = map[string]any{"select": []map[string]string{{"doc": "d1"}}}
	case strings.Contains(system, "navigate"):
		q := strings.ToLower(textutil.Unaccent(questionRe.FindStringSubmatch(user)[1]))
		var sel []map[string]string
		for _, m := range nodeRe.FindAllStringSubmatch(user, -1) {
			if strings.Contains(q, "von") && strings.Contains(textutil.Unaccent(m[2]), "von") {
				sel = append(sel, map[string]string{"node_id": m[1]})
			}
		}
		if len(sel) == 0 {
			m := nodeRe.FindStringSubmatch(user)
			sel = []map[string]string{{"node_id": m[1]}}
		}
		reply = map[string]any{"select": sel, "expand": []string{}, "answerable": true}
	case strings.Contains(system, "exact lines"):
		q := textutil.Normalize(questionRe.FindStringSubmatch(user)[1])
		var hits []map[string]any
		curPage, curDoc := 0, ""
		for _, ln := range strings.Split(user, "\n") {
			if m := pageRe.FindStringSubmatch(ln); m != nil {
				fmt.Sscan(m[1], &curPage)
				curDoc = m[2]
				continue
			}
			if m := lineRe.FindStringSubmatch(ln); m != nil && strings.Contains(textutil.Normalize(m[2]), strings.Fields(q)[0]) {
				var n int
				fmt.Sscan(m[1], &n)
				hits = append(hits, map[string]any{"doc": curDoc, "page": curPage, "lines": []int{n}, "quote": m[2], "relevance": 0.9})
			}
		}
		// A fabricated answer that must be dropped by quote verification.
		hits = append(hits, map[string]any{"doc": curDoc, "page": curPage, "lines": []int{0}, "quote": "Vốn kinh doanh: 999.999.999 USD", "relevance": 1})
		reply = map[string]any{"hits": hits, "not_found": false}
	default:
		return fmt.Errorf("unexpected prompt: %s", kind)
	}
	b, _ := json.Marshal(reply)
	return llm.DecodeJSON(string(b), out)
}

func TestIndexAndReasoningSearch(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" || testing.Short() {
		t.Skip("set TEST_DATABASE_URL to run index integration tests")
	}
	ctx := context.Background()
	st, err := postgres.Open(ctx, config.DB{DSN: dsn, MaxConns: 8, MinConns: 1, AutoMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	user, _ := st.Users.Create(ctx, fmt.Sprintf("idx-test-%s@bepaylot.local", uuid.NewString()), "t")
	defer st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, user.ID)

	cfg := config.Defaults()
	cfg.Parser.DefaultEngine = "fake"
	cfg.Parser.Render.DPI = 72
	cfg.Search.FullDocTokenBudget = 5 // force tree navigation
	cfg.Search.MaxDocsDirect = 1      // force document selection
	cfg.Index.Tree.FlatMaxPages = 5

	r, err := pdf.New(pdf.Config{Mode: "webassembly", Workers: 1, PageTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	objects := storage.NewMemory()
	files, _ := storage.NewFileCache(t.TempDir(), 1<<30, objects)
	engines := parser.NewRegistry("fake")
	engines.Register(textOCR{})
	q := queue.NewInline()
	docs := document.New(document.Deps{Store: st, Objects: objects, Files: files, Queue: q, Renderer: r, Engines: engines, Config: cfg})
	llmFake := &scripted{calls: map[string]int{}}
	idx := index.New(index.Deps{Store: st, Docs: docs, Queue: q, TreeLLM: llmFake, SearchLLM: llmFake, Config: cfg})
	h := docs.Handlers()
	for k, v := range idx.Handlers() {
		h[k] = v
	}
	q.Register(h, nil)

	kb, err := docs.CreateKB(ctx, user.ID, "KB", "", types.KBConfig{}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	upload := func(name, code string, pages []pdftest.Page) uuid.UUID {
		up, err := docs.BeginUpload(ctx, user.ID, kb.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		up.SetSharedMetadata(map[string]any{"ma_ho_so": code})
		if err := up.AddFile(ctx, name, "", bytes.NewReader(pdftest.Build(pages, pdftest.Options{Outline: true}))); err != nil {
			t.Fatal(err)
		}
		res, err := up.Finish(ctx)
		if err != nil || len(res.Documents) != 1 {
			t.Fatalf("upload: %+v %v", res, err)
		}
		return res.Documents[0].DocumentID
	}
	docA := upload("a.pdf", "HS-A", []pdftest.Page{
		{"THONG TIN CHUNG", "Ten ho kinh doanh: VAT LIEU XAY DUNG"},
		{"VON KINH DOANH", "Von kinh doanh: 50.000.000 dong"},
		{"CHU HO", "Ho va ten: NGUYEN VAN TINH"},
	})
	docB := upload("b.pdf", "HS-B", []pdftest.Page{
		{"THONG TIN CHUNG", "Ten ho kinh doanh: TAP HOA"},
		{"VON KINH DOANH", "Von kinh doanh: 20.000.000 dong"},
	})
	if err := q.Drain(ctx, 500); err != nil {
		t.Fatal(err)
	}
	if len(q.Failed) > 0 {
		t.Fatalf("failed tasks: %v", q.Failed)
	}
	for _, id := range []uuid.UUID{docA, docB} {
		d, gerr := st.Documents.Get(ctx, id)
		if gerr != nil {
			t.Fatalf("get %s: %v (failed: %v)", id, gerr, q.Failed)
		}
		if d.Status != types.DocCompleted || d.IndexStatus != types.StageDone || d.Title == "" {
			t.Fatalf("doc %s: status %s index %s title %q err %q", id, d.Status, d.IndexStatus, d.Title, d.Error)
		}
	}
	tree, err := idx.DocumentTree(ctx, user.ID, docA)
	if err != nil || len(tree) != 4 || tree[1].Origin != "bookmark" || tree[1].Summary == "" {
		t.Fatalf("tree = %+v err %v", tree, err)
	}

	// Reasoning search scoped by metadata only sees HS-A.
	resp, err := idx.Search(ctx, types.SearchRequest{Query: "Vốn kinh doanh là bao nhiêu?", KBIDs: []uuid.UUID{kb.ID},
		Metadata: types.MetadataFilter{"ma_ho_so": "HS-A"}, OwnerID: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Hits) == 0 {
		t.Fatalf("no hits; trace %+v", resp.Trace)
	}
	for _, h := range resp.Hits {
		if h.DocumentID != docA {
			t.Fatalf("hit from wrong document: %+v", h)
		}
		if strings.Contains(h.Quote, "999.999.999") {
			t.Fatalf("fabricated quote not dropped: %+v", h)
		}
	}
	var top types.SearchHit
	for _, h := range resp.Hits {
		if strings.Contains(h.Quote, "50.000.000") {
			top = h
		}
	}
	if top.PageNo != 2 || !strings.Contains(top.Quote, "50.000.000") || len(top.BBoxes) == 0 || top.BBoxes[0].IsZero() {
		t.Fatalf("top hit = %+v", top)
	}
	if resp.Trace.DroppedHits == 0 || resp.Trace.LLMCalls == 0 {
		t.Fatalf("trace = %+v", resp.Trace)
	}

	// Without metadata, document selection runs (MaxDocsDirect=1).
	resp, err = idx.Search(ctx, types.SearchRequest{Query: "Vốn kinh doanh", KBIDs: []uuid.UUID{kb.ID}, OwnerID: user.ID})
	if err != nil || len(resp.Trace.SelectedDocs) != 1 || resp.Trace.CandidateDocs != 2 {
		t.Fatalf("selection trace = %+v err %v", resp.Trace, err)
	}

	// Keyword mode, accent-insensitive.
	resp, err = idx.Search(ctx, types.SearchRequest{Query: "nguyễn văn tình", KBIDs: []uuid.UUID{kb.ID}, Mode: types.SearchKeyword, OwnerID: user.ID})
	if err != nil || len(resp.Hits) == 0 || resp.Hits[0].DocumentID != docA || resp.Hits[0].PageNo != 3 {
		t.Fatalf("keyword = %+v err %v", resp.Hits, err)
	}

	// Metadata mode lists documents of a code.
	resp, err = idx.Search(ctx, types.SearchRequest{KBIDs: []uuid.UUID{kb.ID}, Mode: types.SearchMetadata, Metadata: types.MetadataFilter{"ma_ho_so": "HS-B"}, OwnerID: user.ID})
	if err != nil || len(resp.Documents) != 1 || resp.Documents[0].ID != docB {
		t.Fatalf("metadata mode = %+v err %v", resp.Documents, err)
	}

	// Citation round trip and reading pages as numbered lines.
	loc, err := idx.Locate(ctx, user.ID, top.CitationID)
	if err != nil || len(loc) != 1 || !strings.Contains(loc[0].Quote, "50.000.000") {
		t.Fatalf("locate = %+v err %v", loc, err)
	}
	text, err := idx.ReadPages(ctx, user.ID, docA, 2, 2)
	if err != nil || !strings.Contains(text, "[L1] Von kinh doanh: 50.000.000 dong") {
		t.Fatalf("read pages = %q err %v", text, err)
	}
	ph, err := idx.FindInDocument(ctx, user.ID, docA, "von kinh", types.SearchKeyword)
	if err != nil || len(ph) == 0 || ph[0].PageNo != 2 {
		t.Fatalf("find in document = %+v err %v", ph, err)
	}
	// Another owner cannot search this KB.
	if _, err := idx.Search(ctx, types.SearchRequest{Query: "x", KBIDs: []uuid.UUID{kb.ID}, OwnerID: uuid.New()}); err == nil {
		t.Fatal("foreign owner should not search the KB")
	}
}
