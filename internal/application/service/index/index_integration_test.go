package index_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/config"
	"github.com/thanhenti/bepaylot/internal/parser/pdf/pdftest"
	"github.com/thanhenti/bepaylot/internal/testkit"
	"github.com/thanhenti/bepaylot/internal/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
)

var (
	locLineRe = regexp.MustCompile(`\[L(\d+)\] (.*)`)
	locPageRe = regexp.MustCompile(`<page n="(\d+)" doc="(d\d+)"`)
	locQRe    = regexp.MustCompile(`Question: (.*)`)
)

// locateWithFabrication answers step 4b with the lines holding the first
// question word, plus a fabricated quote that verification must drop.
func locateWithFabrication(user string) map[string]any {
	q := strings.Fields(textutil.Normalize(locQRe.FindStringSubmatch(user)[1]))
	var hits []map[string]any
	page, doc := 0, ""
	for _, ln := range strings.Split(user, "\n") {
		if m := locPageRe.FindStringSubmatch(ln); m != nil {
			fmt.Sscan(m[1], &page)
			doc = m[2]
			continue
		}
		if m := locLineRe.FindStringSubmatch(ln); m != nil && strings.Contains(textutil.Normalize(m[2]), q[0]) {
			var n int
			fmt.Sscan(m[1], &n)
			hits = append(hits, map[string]any{"doc": doc, "page": page, "lines": []int{n}, "quote": m[2], "relevance": 0.9})
		}
	}
	hits = append(hits, map[string]any{"doc": doc, "page": page, "lines": []int{0}, "quote": "Vốn kinh doanh: 999.999.999 USD", "relevance": 1})
	return map[string]any{"hits": hits, "not_found": false}
}

func TestIndexAndReasoningSearch(t *testing.T) {
	h := testkit.New(t, func(c *config.Config) {
		c.Search.FullDocTokenBudget = 5 // force tree navigation
		c.Index.Tree.FlatMaxPages = 5
	})
	h.LLM.Locate = locateWithFabrication
	kb := h.KB(types.KBConfig{}, nil)
	docA := h.UploadTo(kb.ID, "HS-A", "a.pdf", nil, []pdftest.Page{
		{"THONG TIN CHUNG", "Ten ho kinh doanh: VAT LIEU XAY DUNG"},
		{"VON KINH DOANH", "Von kinh doanh: 50.000.000 dong"},
		{"CHU HO", "Ho va ten: NGUYEN VAN TINH"},
	})
	docB := h.UploadTo(kb.ID, "HS-B", "b.pdf", nil, []pdftest.Page{
		{"THONG TIN CHUNG", "Ten ho kinh doanh: TAP HOA"},
		{"VON KINH DOANH", "Von kinh doanh: 20.000.000 dong"},
	})
	h.Drain()
	for _, id := range []uuid.UUID{docA, docB} {
		d, err := h.Store.Documents.Get(h.Ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		// The tree is the last stage: completed right after it (§4.6).
		if d.Status != types.DocCompleted || d.IndexStatus != types.StageDone || d.Title == "" {
			t.Fatalf("doc %s: status %s index %s title %q err %q", id, d.Status, d.IndexStatus, d.Title, d.Error)
		}
	}
	caseA, caseB := h.Case(kb.ID, "HS-A"), h.Case(kb.ID, "HS-B")
	tree, err := h.Index.DocumentTree(h.Ctx, h.Owner.ID, docA)
	if err != nil || len(tree) != 4 || tree[1].Origin != "bookmark" || tree[1].Summary == "" {
		t.Fatalf("tree = %+v err %v", tree, err)
	}
	if tree[0].TreeTokens == 0 || tree[0].TreeTokens != tree[1].TreeTokens+tree[2].TreeTokens+tree[3].TreeTokens {
		t.Fatalf("tree tokens not recorded: %+v", tree)
	}
	// The tree text is whole when it fits (N37): every node is listed.
	text, err := h.Index.DocumentTreeText(h.Ctx, h.Owner.ID, docA, "")
	if err != nil || strings.Count(text, "[n") != 3 || strings.Contains(text, "expand") {
		t.Fatalf("tree text = %q err %v", text, err)
	}

	// Reasoning search in case A: the tree leads to page 2 only, whose
	// lines are read, and the fabricated quote is dropped (N13, N21).
	treeCalls, locateCalls := h.LLM.Calls["tree_search"], h.LLM.Calls["locate"]
	resp, err := h.Index.Search(h.Ctx, types.SearchRequest{Query: "Vốn kinh doanh là bao nhiêu?", CaseIDs: []uuid.UUID{caseA.ID}, OwnerID: h.Owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Hits) == 0 {
		t.Fatalf("no hits; trace %+v", resp.Trace)
	}
	var top types.SearchHit
	for _, hit := range resp.Hits {
		if hit.DocumentID != docA {
			t.Fatalf("hit from wrong document: %+v", hit)
		}
		if strings.Contains(hit.Quote, "999.999.999") {
			t.Fatalf("fabricated quote not dropped: %+v", hit)
		}
		if strings.Contains(hit.Quote, "50.000.000") {
			top = hit
		}
	}
	if top.PageNo != 2 || len(top.BBoxes) == 0 || top.BBoxes[0].IsZero() || top.Via != "tree" {
		t.Fatalf("top hit = %+v", top)
	}
	if resp.Trace.DroppedHits == 0 || resp.Trace.LLMCalls != 2 || len(resp.Trace.Cases) != 1 || resp.Trace.TokensIn == 0 ||
		fmt.Sprint(resp.Trace.PagesRead[docA.String()]) != "[2]" {
		t.Fatalf("trace = %+v", resp.Trace)
	}
	// One call on the whole tree, one on the pages of the chosen node.
	if h.LLM.Calls["tree_search"]-treeCalls != 1 || h.LLM.Calls["locate"]-locateCalls != 1 {
		t.Fatalf("calls = %v", h.LLM.Calls)
	}
	treePrompt := h.LLM.Prompts["tree_search"][treeCalls]
	pagesPrompt := h.LLM.Prompts["locate"][locateCalls]
	if !strings.Contains(treePrompt, "CHU HO") || strings.Contains(treePrompt, "50.000.000") {
		t.Fatalf("step 2 must show the whole tree and no page text:\n%s", treePrompt)
	}
	if !strings.Contains(pagesPrompt, `<page n="2"`) || strings.Contains(pagesPrompt, `<page n="1"`) || strings.Contains(pagesPrompt, `<page n="3"`) {
		t.Fatalf("only the pages of the chosen node are read:\n%s", pagesPrompt)
	}

	// The case TOC lists the file with its first branches (§6.6).
	toc, err := h.Index.CaseTOC(h.Ctx, h.Owner.ID, caseA.ID, nil, nil)
	if err != nil || len(toc.Documents) != 1 || len(toc.Documents[0].Branches) != 3 || !strings.Contains(toc.Text, "[d1] a.pdf (3 tr.)") ||
		!strings.Contains(toc.Text, "[d1.n") {
		t.Fatalf("toc = %+v err %v", toc, err)
	}

	// A KB-wide search covers both cases.
	resp, err = h.Index.Search(h.Ctx, types.SearchRequest{Query: "Vốn kinh doanh", KBIDs: []uuid.UUID{kb.ID}, OwnerID: h.Owner.ID})
	if err != nil || resp.Trace.CandidateDocs != 2 || len(resp.Trace.Cases) != 2 {
		t.Fatalf("kb-wide trace = %+v err %v", resp.Trace, err)
	}

	// Keyword mode, accent-insensitive.
	resp, err = h.Index.Search(h.Ctx, types.SearchRequest{Query: "nguyễn văn tình", KBIDs: []uuid.UUID{kb.ID}, Mode: types.SearchKeyword, OwnerID: h.Owner.ID})
	if err != nil || len(resp.Hits) == 0 || resp.Hits[0].DocumentID != docA || resp.Hits[0].PageNo != 3 || resp.Hits[0].CaseCode != "HS-A" {
		t.Fatalf("keyword = %+v err %v", resp.Hits, err)
	}

	// Metadata mode lists the documents of a case.
	resp, err = h.Index.Search(h.Ctx, types.SearchRequest{CaseIDs: []uuid.UUID{caseB.ID}, Mode: types.SearchMetadata, OwnerID: h.Owner.ID})
	if err != nil || len(resp.Documents) != 1 || resp.Documents[0].ID != docB {
		t.Fatalf("metadata mode = %+v err %v", resp.Documents, err)
	}

	// Citation round trip and reading pages as numbered lines.
	loc, err := h.Index.Locate(h.Ctx, h.Owner.ID, top.CitationID)
	if err != nil || len(loc) != 1 || !strings.Contains(loc[0].Quote, "50.000.000") {
		t.Fatalf("locate = %+v err %v", loc, err)
	}
	text, err = h.Index.ReadPages(h.Ctx, h.Owner.ID, docA, 2, 2)
	if err != nil || !strings.Contains(text, "[L1] Von kinh doanh: 50.000.000 dong") {
		t.Fatalf("read pages = %q err %v", text, err)
	}
	ph, err := h.Index.FindInDocument(h.Ctx, h.Owner.ID, docA, "von kinh", types.SearchKeyword, 0, 0)
	if err != nil || len(ph) == 0 || ph[0].PageNo != 2 {
		t.Fatalf("find in document = %+v err %v", ph, err)
	}
	// Another owner cannot search the case or the KB.
	if _, err := h.Index.Search(h.Ctx, types.SearchRequest{Query: "x", CaseIDs: []uuid.UUID{caseA.ID}, OwnerID: uuid.New()}); err == nil {
		t.Fatal("foreign owner should not search the case")
	}
	if _, err := h.Index.Search(h.Ctx, types.SearchRequest{Query: "x", OwnerID: h.Owner.ID}); err == nil {
		t.Fatal("a search without case_ids or kb_ids must be refused")
	}
}
