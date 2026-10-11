package index_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thanhenti/bepaylot/internal/application/service/docmodel"
	"github.com/thanhenti/bepaylot/internal/application/service/document"
	"github.com/thanhenti/bepaylot/internal/parser/pdf/pdftest"
	"github.com/thanhenti/bepaylot/internal/testkit"
	"github.com/thanhenti/bepaylot/internal/types"
)

// A KB with doc_types: every page is classified from its image, consecutive
// pages of one type become a segment, and the segments are the top nodes of
// the file in the case tree. A reviewed split rebuilds the tree of the file.
func TestDocTypesInCaseTree(t *testing.T) {
	h := testkit.New(t)
	split := docmodel.NewSplit(docmodel.SplitDeps{Store: h.Store, Queue: h.Queue, Cases: h.Cases, Tree: h.Index, Config: h.Config})
	h.Index.SetSplit(split)
	h.PageTypes.Labels = []string{"hop_dong", "", "hoa_don"} // page 2 fails: it continues the contract
	kb := h.KB(types.KBConfig{DocTypes: []types.ClassLabel{{Name: "hop_dong", Title: "Hợp đồng"}, {Name: "hoa_don", Title: "Hoá đơn"}}}, nil)
	doc := h.Upload(kb.ID, "bo-chung-tu.pdf", nil, []pdftest.Page{
		{"HOP DONG MUA BAN", "Dieu 1 gia tri 500000000"},
		{"Dieu 2 thanh toan trong 30 ngay"},
		{"HOA DON GIA TRI GIA TANG", "So 0001234"},
	})
	h.Drain()
	cs := h.Case(kb.ID, testkit.DefaultCase)

	segs, err := h.Store.Segments.ByDocument(h.Ctx, doc)
	if err != nil || len(segs) != 2 {
		t.Fatalf("segments = %+v %v", segs, err)
	}
	// The split is confirmed without a review: the segments are the case's
	// own, in one bundle, and the file counts as reviewed.
	if g := segs[0]; g.Label != "hop_dong" || g.PageStart != 1 || g.PageEnd != 2 || g.Source != types.SegmentUser || g.BundleCode != "B01" {
		t.Fatalf("contract segment = %+v", g)
	}
	if v, err := split.View(h.Ctx, h.Owner.ID, cs.ID); err != nil || !v.Reviewed || v.OpensWith == nil {
		t.Fatalf("split view = %+v %v", v, err)
	}
	if g := segs[1]; g.Label != "hoa_don" || g.PageStart != 3 || g.PageEnd != 3 {
		t.Fatalf("invoice segment = %+v", g)
	}

	top := func() []types.TreeNode {
		t.Helper()
		nodes, err := h.Index.DocumentTree(h.Ctx, h.Owner.ID, doc)
		if err != nil {
			t.Fatal(err)
		}
		var out []types.TreeNode
		for _, n := range nodes {
			if n.Level == 1 {
				out = append(out, n)
			}
		}
		return out
	}
	nodes := top()
	if len(nodes) != 2 || nodes[0].DocType != "hop_dong" || nodes[0].Origin != "doctype" || nodes[0].PageEnd != 2 || nodes[1].DocType != "hoa_don" {
		t.Fatalf("top nodes = %+v", nodes)
	}
	toc, err := h.Index.CaseTOC(h.Ctx, h.Owner.ID, cs.ID, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Hợp đồng [loại: hop_dong] (tr. 1–2)", "Hoá đơn [loại: hoa_don] (tr. 3)"} {
		if !strings.Contains(toc.Text, want) {
			t.Fatalf("case tree lacks %q:\n%s", want, toc.Text)
		}
	}
	card := ""
	if all, _ := h.Index.DocumentTree(h.Ctx, h.Owner.ID, doc); len(all) > 0 {
		card = all[0].Title
	}

	// The user says the whole file is one contract: the tree follows.
	var in docmodel.SplitInput
	body := `{"bundles":[{"segments":[{"document_id":"` + doc.String() + `","page_start":1,"page_end":3,"label":"hop_dong"}]}]}`
	if err := json.Unmarshal([]byte(body), &in); err != nil {
		t.Fatal(err)
	}
	if err := split.Save(h.Ctx, h.Owner, cs.ID, in); err != nil {
		t.Fatal(err)
	}
	nodes = top()
	if len(nodes) != 1 || nodes[0].DocType != "hop_dong" || nodes[0].PageStart != 1 || nodes[0].PageEnd != 3 {
		t.Fatalf("top nodes after review = %+v", nodes)
	}
	if all, _ := h.Index.DocumentTree(h.Ctx, h.Owner.ID, doc); len(all) == 0 || all[0].Title != card {
		t.Fatalf("the document card must be kept on rebuild")
	}
}

// A KB without doc_types is not classified: the tree has no document nodes.
func TestNoDocTypesNoClassification(t *testing.T) {
	h := testkit.New(t)
	h.PageTypes.Labels = []string{"hop_dong"}
	kb := h.KB(types.KBConfig{}, nil)
	doc := h.Upload(kb.ID, "a.pdf", nil, []pdftest.Page{{"HOP DONG MUA BAN", "Dieu 1"}})
	h.Drain()
	nodes, err := h.Index.DocumentTree(h.Ctx, h.Owner.ID, doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if n.DocType != "" {
			t.Fatalf("unexpected document node %+v", n)
		}
	}
	if segs, _ := h.Store.Segments.ByDocument(h.Ctx, doc); len(segs) != 0 {
		t.Fatalf("unexpected segments %+v", segs)
	}
}

// A reparse renders the same page image byte for byte, so the model cache
// (keyed by the image) answers it without calling the model again; and the
// cache keeps what it is given.
func TestReparseSendsTheSameImage(t *testing.T) {
	h := testkit.New(t)
	h.PageTypes.Labels = []string{"hop_dong", "hop_dong"}
	kb := h.KB(types.KBConfig{DocTypes: []types.ClassLabel{{Name: "hop_dong", Title: "Hợp đồng"}}}, nil)
	doc := h.Upload(kb.ID, "a.pdf", nil, []pdftest.Page{{"HOP DONG MUA BAN", "Dieu 1"}})
	h.Drain()
	if _, err := h.Docs.Reparse(h.Ctx, h.Owner.ID, doc, document.ReparseRequest{}); err != nil {
		t.Fatal(err)
	}
	h.Drain()
	if seen := h.PageTypes.Seen; len(seen) != 2 || seen[0] != seen[1] {
		t.Fatalf("images asked about = %v", seen)
	}

	key := "test-" + doc.String()
	if _, ok, err := h.Store.ModelCache.Get(h.Ctx, key); err != nil || ok {
		t.Fatalf("empty cache: %v %v", ok, err)
	}
	if err := h.Store.ModelCache.Put(h.Ctx, key, "decision", []byte(`{"label":"hop_dong","confidence":0.9}`)); err != nil {
		t.Fatal(err)
	}
	_ = h.Store.ModelCache.Put(h.Ctx, key, "decision", []byte(`{"label":"khac"}`)) // the first answer is kept
	if b, ok, err := h.Store.ModelCache.Get(h.Ctx, key); err != nil || !ok || !strings.Contains(string(b), "hop_dong") {
		t.Fatalf("cache = %s %v %v", b, ok, err)
	}
}
