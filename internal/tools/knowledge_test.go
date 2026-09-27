package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/types"
	"github.com/thanhenti/bepaylot/internal/types/interfaces"
)

// fakeSearcher records requests and admits only the documents of its case.
type fakeSearcher struct {
	interfaces.Searcher
	caseID  uuid.UUID
	alive   bool
	allowed map[uuid.UUID]bool
	got     types.SearchRequest
	listed  uuid.UUID
	counted uuid.UUID
	toc     uuid.UUID
	read    int
	pages   [2]int
}

func (f *fakeSearcher) Search(_ context.Context, req types.SearchRequest) (*types.SearchResponse, error) {
	f.got = req
	return &types.SearchResponse{Hits: []types.SearchHit{{CitationID: "doc:x:p1:l1-1", FileName: "a.pdf", PageNo: 1, Quote: "q", Via: "tree"}}}, nil
}

func (f *fakeSearcher) DocumentInCase(_ context.Context, _, doc, caseID uuid.UUID) (bool, error) {
	return caseID == f.caseID && f.allowed[doc], nil
}

func (f *fakeSearcher) CaseAlive(_ context.Context, _, caseID uuid.UUID) bool {
	return f.alive && caseID == f.caseID
}

func (f *fakeSearcher) ReadPages(context.Context, uuid.UUID, uuid.UUID, int, int) (string, error) {
	f.read++
	return "[L1] text", nil
}

func (f *fakeSearcher) PageOverview(context.Context, uuid.UUID, uuid.UUID, int, int) ([]types.PageOverview, error) {
	f.read++
	return []types.PageOverview{{PageNo: 1, Title: "CĂN CƯỚC CÔNG DÂN"}}, nil
}

func (f *fakeSearcher) FindInDocument(_ context.Context, _, _ uuid.UUID, _, _ string, from, to int) ([]types.PageSearchHit, error) {
	f.pages = [2]int{from, to}
	return nil, nil
}

func (f *fakeSearcher) DocumentTreeText(context.Context, uuid.UUID, uuid.UUID, string) (string, error) {
	return "<tree>\n[n1] Điều 1 (tr. 1)\n</tree>\n", nil
}

func (f *fakeSearcher) CaseTOC(_ context.Context, _, caseID uuid.UUID, _ types.MetadataFilter, _ []string) (*types.CaseTOC, error) {
	f.toc = caseID
	return &types.CaseTOC{Text: "[d1] a.pdf (3 tr.)\n", Documents: []types.TOCDoc{{Ref: "d1", DocumentID: uuid.New()}}}, nil
}

func (f *fakeSearcher) Locate(_ context.Context, _ uuid.UUID, c string) ([]types.SearchHit, error) {
	id, _ := uuid.Parse(strings.Split(c, ":")[1])
	return []types.SearchHit{{DocumentID: id, CitationID: c}}, nil
}

func (f *fakeSearcher) ListDocuments(_ context.Context, _, caseID uuid.UUID, _ types.MetadataFilter, _ []string, _ int) ([]types.DocumentBrief, error) {
	f.listed = caseID
	return nil, nil
}

func (f *fakeSearcher) MetadataValues(_ context.Context, _, caseID uuid.UUID, _ string) ([]interfaces.MetadataValue, error) {
	f.counted = caseID
	return nil, nil
}

func invoke(t *testing.T, ctx context.Context, s *Session, name, args string) (string, error) {
	t.Helper()
	for _, bt := range s.Executable() {
		info, _ := bt.Info(ctx)
		if info.Name == name {
			return bt.(tool.InvokableTool).InvokableRun(ctx, args)
		}
	}
	t.Fatalf("tool %s not bound", name)
	return "", nil
}

// TestCaseToolsStayInCase: every tool works on the session's case only; a
// file of another case is refused like a missing one (N16 a–c).
func TestCaseToolsStayInCase(t *testing.T) {
	reg, err := NewRegistry(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	caseA, in, other := uuid.New(), uuid.New(), uuid.New()
	fs := &fakeSearcher{caseID: caseA, alive: true, allowed: map[uuid.UUID]bool{in: true}}
	if err := reg.SetKnowledgeTools(fs); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.NewSession().VisibleDescriptions()["kb_search"]; ok {
		t.Fatal("kb_search must not be bound without a case")
	}
	s := reg.NewSession()
	s.EnableKnowledge()
	for _, name := range []string{"kb_case_toc", "kb_search", "kb_document_tree", "kb_read_pages"} {
		if _, ok := s.VisibleDescriptions()[name]; !ok {
			t.Fatalf("%s not bound after EnableKnowledge", name)
		}
	}
	owner := uuid.New()
	ctx := WithCaseScope(context.Background(), CaseScope{Owner: owner, CaseID: caseA})

	out, err := invoke(t, ctx, s, "kb_search", `{"query":"chủ hộ","page_from":2,"metadata":{"loai":"GCN"},"kb_ids":["`+uuid.NewString()+`"],"case_ids":["`+uuid.NewString()+`"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if fs.got.OwnerID != owner || len(fs.got.CaseIDs) != 1 || fs.got.CaseIDs[0] != caseA || len(fs.got.KBIDs) != 0 {
		t.Fatalf("search must be limited to the session's case: %+v", fs.got)
	}
	if fs.got.PageFrom != 2 || fs.got.Metadata["loai"] != "GCN" {
		t.Fatalf("filters not passed: %+v", fs.got)
	}
	var res struct {
		Hits []kbHit `json:"hits"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || len(res.Hits) != 1 || res.Hits[0].CitationID == "" || res.Hits[0].Via != "tree" {
		t.Fatalf("result = %s", out)
	}
	if _, err := invoke(t, ctx, s, "kb_list_documents", `{}`); err != nil || fs.listed != caseA {
		t.Fatalf("list must be the case's: %v %v", fs.listed, err)
	}
	if _, err := invoke(t, ctx, s, "kb_metadata_values", `{"key":"loai"}`); err != nil || fs.counted != caseA {
		t.Fatalf("values must be counted in the case: %v %v", fs.counted, err)
	}

	if _, err := invoke(t, ctx, s, "kb_read_pages", `{"document_id":"`+in.String()+`","page_from":1}`); err != nil || fs.read != 1 {
		t.Fatalf("in-case read: %v", err)
	}
	for _, c := range []struct{ tool, args string }{
		{"kb_read_pages", `{"document_id":"` + other.String() + `","page_from":1}`},
		{"kb_document_tree", `{"document_id":"` + other.String() + `"}`},
		{"kb_find_in_document", `{"document_id":"` + other.String() + `","query":"x"}`},
		{"kb_page_overview", `{"document_id":"` + other.String() + `"}`},
		{"kb_locate", `{"citation_id":"doc:` + other.String() + `:p1"}`},
	} {
		_, err := invoke(t, ctx, s, c.tool, c.args)
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("%s on another case's file: err = %v", c.tool, err)
		}
	}
	if fs.read != 1 {
		t.Fatal("a file of another case was read")
	}
	if _, err := invoke(t, ctx, s, "kb_find_in_document", `{"document_id":"`+in.String()+`","query":"so","page_from":2,"page_to":4}`); err != nil || fs.pages != [2]int{2, 4} {
		t.Fatalf("find page range = %v %v", fs.pages, err)
	}
	tree, err := invoke(t, ctx, s, "kb_document_tree", `{"document_id":"`+in.String()+`"}`)
	if err != nil || !strings.Contains(tree, "Điều 1") {
		t.Fatalf("tree level: %s %v", tree, err)
	}

	toc, err := invoke(t, ctx, s, "kb_case_toc", `{}`)
	if err != nil || fs.toc != caseA || !strings.Contains(toc, "[d1] a.pdf") || !strings.Contains(toc, "document_ids") {
		t.Fatalf("case toc must be the case's: %s %v %v", toc, fs.toc, err)
	}
	for _, gone := range []string{"wiki_index", "wiki_read", "wiki_search", "wiki_links"} {
		if _, ok := s.VisibleDescriptions()[gone]; ok {
			t.Fatalf("%s must not exist any more", gone)
		}
	}

	// Without a scope the tools refuse to run.
	if _, err := invoke(t, context.Background(), s, "kb_search", `{"query":"x"}`); err == nil {
		t.Fatal("kb_search without a case should fail")
	}
	// A deleted case is reported as such.
	fs.alive = false
	if _, err := invoke(t, ctx, s, "kb_read_pages", `{"document_id":"`+other.String()+`","page_from":1}`); err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Fatalf("deleted case: %v", err)
	}
}
