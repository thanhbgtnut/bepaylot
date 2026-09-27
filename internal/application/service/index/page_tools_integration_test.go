package index_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/parser/pdf/pdftest"
	"github.com/thanhenti/bepaylot/internal/testkit"
	"github.com/thanhenti/bepaylot/internal/types"
)

// A file bundling an ID card, a certificate and a lease is not classified at
// index time: the agent reads the page overview, picks pages and searches
// only those.
func TestPageOverviewAndPageRangeSearch(t *testing.T) {
	h := testkit.New(t)
	kb := h.KB(types.KBConfig{}, nil)
	doc := h.Upload(kb.ID, "ho-so.pdf", map[string]any{"group_code": "G9"}, []pdftest.Page{
		{"CAN CUOC CONG DAN", "So 001099000123", "Tien su: khong"},
		{"GIAY CHUNG NHAN DANG KY HO KINH DOANH", "Ma so 0101234567", "Von kinh doanh 500000000"},
		{"HOP DONG THUE KHO", "Tien thue hang thang 12000000", "Thoi han 24 thang"},
	})
	h.Drain()

	all, err := h.Index.PageOverview(h.Ctx, h.Owner.ID, doc, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].PageNo != 1 || all[2].PageNo != 3 || all[0].Lines == 0 {
		t.Fatalf("overview = %+v", all)
	}
	for i, want := range []string{"CAN CUOC", "GIAY CHUNG NHAN", "HOP DONG"} {
		if !strings.Contains(all[i].Preview, want) {
			t.Fatalf("page %d preview %q lacks %q", i+1, all[i].Preview, want)
		}
	}
	if some, _ := h.Index.PageOverview(h.Ctx, h.Owner.ID, doc, 2, 9); len(some) != 2 || some[0].PageNo != 2 {
		t.Fatalf("overview 2..9 = %+v", some)
	}
	if _, err := h.Index.PageOverview(h.Ctx, uuid.New(), doc, 0, 0); err == nil {
		t.Fatal("another owner must not see the overview")
	}

	// "tien" is on the ID card (page 1) and the lease (page 3).
	pages := func(ph []types.PageSearchHit) []int {
		var out []int
		for _, p := range ph {
			out = append(out, p.PageNo)
		}
		return out
	}
	whole, err := h.Index.FindInDocument(h.Ctx, h.Owner.ID, doc, "tien", types.SearchKeyword, 0, 0)
	if err != nil || len(whole) != 2 {
		t.Fatalf("find tien = %v %v", pages(whole), err)
	}
	lease, err := h.Index.FindInDocument(h.Ctx, h.Owner.ID, doc, "tien", types.SearchKeyword, 3, 3)
	if err != nil || len(lease) != 1 || lease[0].PageNo != 3 {
		t.Fatalf("find tien on page 3 = %v %v", pages(lease), err)
	}
	for _, mode := range []string{types.SearchKeyword, types.SearchReasoning} {
		resp, err := h.Index.Search(h.Ctx, types.SearchRequest{Query: "tien", Mode: mode, CaseIDs: []uuid.UUID{h.Case(kb.ID, testkit.DefaultCase).ID},
			DocumentIDs: []uuid.UUID{doc}, PageFrom: 3, PageTo: 3, OwnerID: h.Owner.ID})
		if err != nil || len(resp.Hits) == 0 {
			t.Fatalf("%s page 3: %+v %v", mode, resp, err)
		}
		for _, hit := range resp.Hits {
			if hit.PageNo != 3 {
				t.Fatalf("%s: hit outside page 3: %+v", mode, hit)
			}
		}
	}
}
