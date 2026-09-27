package index_test

import (
	"bytes"
	"testing"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/parser/pdf/pdftest"
	"github.com/thanhenti/bepaylot/internal/testkit"
	"github.com/thanhenti/bepaylot/internal/types"
	"github.com/thanhenti/bepaylot/internal/types/interfaces"
)

// Two cases of one KB hold the same file. Search, the per-document scope
// check, metadata counts and the wiki never leave the case (N13, N16, N18,
// N23).
func TestSearchNeverLeavesTheCase(t *testing.T) {
	h := testkit.New(t)
	kb := h.KB(types.KBConfig{}, nil)
	same := []pdftest.Page{{"Hop dong thue kho", "Tien thue 12000000"}}
	a := h.UploadTo(kb.ID, "RT-A", "hd.pdf", map[string]any{"loai": "HD"}, same)
	a2 := h.UploadTo(kb.ID, "RT-A", "coc.pdf", map[string]any{"loai": 123}, []pdftest.Page{{"Phu luc hop dong", "Tien coc 5000000"}})
	b := h.UploadTo(kb.ID, "RT-B", "hd.pdf", map[string]any{"loai": "HD"}, same)
	h.Drain()
	if a == b {
		t.Fatal("the same file in two cases must be two documents")
	}
	caseA, caseB := h.Case(kb.ID, "RT-A"), h.Case(kb.ID, "RT-B")

	// Re-uploading in A is a duplicate of A's document, never B's.
	up, _ := h.Docs.BeginUpload(h.Ctx, h.Owner.ID, kb.ID, false)
	up.SetCase(interfaces.CaseRef{Code: "RT-A", Create: true})
	_ = up.AddFile(h.Ctx, "again.pdf", "", bytes.NewReader(pdftest.Build(same, pdftest.Options{Outline: true})))
	res, err := up.Finish(h.Ctx)
	if err != nil || len(res.Documents) != 1 || !res.Documents[0].Duplicate || res.Documents[0].DocumentID != a {
		t.Fatalf("duplicate in A = %+v %v", res, err)
	}

	for _, mode := range []string{types.SearchKeyword, types.SearchReasoning} {
		resp, err := h.Index.Search(h.Ctx, types.SearchRequest{Query: "tien thue", Mode: mode, CaseIDs: []uuid.UUID{caseA.ID}, OwnerID: h.Owner.ID})
		if err != nil || len(resp.Hits) == 0 {
			t.Fatalf("%s: %+v %v", mode, resp, err)
		}
		for _, hit := range resp.Hits {
			if (hit.DocumentID != a && hit.DocumentID != a2) || hit.CaseID != caseA.ID || hit.CaseCode != "RT-A" {
				t.Fatalf("%s: hit outside case A: %+v", mode, hit)
			}
		}
		// document_ids of another case only narrow: nothing is found.
		resp, err = h.Index.Search(h.Ctx, types.SearchRequest{Query: "tien thue", Mode: mode, CaseIDs: []uuid.UUID{caseA.ID},
			DocumentIDs: []uuid.UUID{b}, OwnerID: h.Owner.ID})
		if err != nil || len(resp.Hits) != 0 || resp.Trace.CandidateDocs != 0 {
			t.Fatalf("%s with B's document: %+v %v", mode, resp, err)
		}
	}

	// An LLM that names index ids it was never shown gets nothing from
	// another case (N16 e).
	h.LLM.WikiIndex = func(string) map[string]any {
		return map[string]any{"wiki": []string{"w99"}, "raw": []map[string]string{{"ref": "w42.n1"}, {"ref": b.String()}}}
	}
	resp, err := h.Index.Search(h.Ctx, types.SearchRequest{Query: "tien thue", CaseIDs: []uuid.UUID{caseA.ID}, OwnerID: h.Owner.ID})
	h.LLM.WikiIndex = nil
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range resp.Hits {
		if hit.CaseID != caseA.ID {
			t.Fatalf("hit from another case: %+v", hit)
		}
	}

	for doc, want := range map[uuid.UUID]bool{a: true, a2: true, b: false} {
		if ok, err := h.Index.DocumentInCase(h.Ctx, h.Owner.ID, doc, caseA.ID); err != nil || ok != want {
			t.Fatalf("DocumentInCase(%s, A) = %v %v, want %v", doc, ok, err, want)
		}
	}
	if ok, _ := h.Index.DocumentInCase(h.Ctx, uuid.New(), a, caseA.ID); ok {
		t.Fatal("another owner's document must be out of scope")
	}
	vals, err := h.Index.MetadataValues(h.Ctx, h.Owner.ID, caseA.ID, "loai")
	if err != nil || len(vals) != 2 {
		t.Fatalf("values in A = %+v %v", vals, err)
	}
	if bv, _ := h.Index.MetadataValues(h.Ctx, h.Owner.ID, caseB.ID, "loai"); len(bv) != 1 || bv[0].Count != 1 {
		t.Fatalf("values in B = %+v", bv)
	}
	// A value stored as a number matches the same value sent as a string.
	docs, err := h.Index.ListDocuments(h.Ctx, h.Owner.ID, caseA.ID, types.MetadataFilter{"loai": "123"}, nil, 10)
	if err != nil || len(docs) != 1 || docs[0].ID != a2 {
		t.Fatalf("string filter on a numeric value: %+v %v", docs, err)
	}

	// The wiki of each case is its own (N23): B's source page is not
	// readable in A, and A's pages never cite B's file.
	srcB, err := h.Store.Wiki.SourcePage(h.Ctx, caseB.ID, b)
	if err != nil {
		t.Fatalf("source page of B: %v", err)
	}
	if p, err := h.Wiki.Page(h.Ctx, caseA.ID, srcB.Slug); err == nil && p.DocumentID != nil && *p.DocumentID == b {
		t.Fatal("case A reads the source page of B's file")
	}
	pages, err := h.Store.Wiki.Pages(h.Ctx, caseA.ID)
	if err != nil || len(pages) == 0 {
		t.Fatalf("wiki of A: %v", err)
	}
	for _, p := range pages {
		full, err := h.Wiki.Page(h.Ctx, caseA.ID, p.Slug)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range full.Footnotes {
			if f.DocumentID == b {
				t.Fatalf("wiki page %s of A cites a file of B", p.Slug)
			}
		}
	}
}
