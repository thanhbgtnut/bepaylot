package sheets_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/docpipe/parser/pdf/pdftest"
	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/application/service/docmodel"
	"github.com/thanhenti/bepaylot/internal/application/service/sheets"
	"github.com/thanhenti/bepaylot/internal/testkit"
	"github.com/thanhenti/bepaylot/internal/types"
)

// U67 on the database with a dummy agent: three files nobody classified (a
// KB without document types, no split) still build the sheet. The agent
// reads each file once and fills the keys of the template; a file without
// the document type of a sub-table has no row in it.
func TestSheetOfUnclassifiedFiles(t *testing.T) {
	h := testkit.New(t)
	kb := h.KB(types.KBConfig{}, nil)
	code := fmt.Sprintf("PA%06d", uuid.New().ID()%1000000)
	h.UploadTo(kb.ID, code, "ho_so_dot1.pdf", nil, []pdftest.Page{
		{"HOP DONG MUA BAN", "So hop dong: 012/2026/HDMB-AP", "Gia tri hop dong: 660.000.000"},
		{"HOA DON GIA TRI GIA TANG", "So hoa don: 0001234", "Tong thanh toan: 550.000.000"},
	})
	h.UploadTo(kb.ID, code, "hoa_don_dot2.pdf", nil, []pdftest.Page{
		{"HOA DON GIA TRI GIA TANG", "So hoa don: 0000877", "Tong thanh toan: 110.000.000"},
	})
	h.UploadTo(kb.ID, code, "cong_van.pdf", nil, []pdftest.Page{
		{"CONG VAN DE NGHI", "Kinh gui: Phong ke toan"},
	})
	h.Drain()
	cs := h.Case(kb.ID, code)

	// Nothing is classified or reviewed.
	split := docmodel.NewSplit(docmodel.SplitDeps{Store: h.Store, Queue: &stubQueue{}, Cases: h.Cases, Tree: h.Index, Config: h.Config})
	v, err := split.View(h.Ctx, h.Owner.ID, cs.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Files) != 3 || v.Reviewed {
		t.Fatalf("split = %d files, reviewed=%v", len(v.Files), v.Reviewed)
	}
	for _, f := range v.Files {
		if f.Status != types.SplitNone {
			t.Fatalf("%s is %s, want not classified", f.FileName, f.Status)
		}
	}

	tpl, err := h.Store.Templates.Create(h.Ctx, postgres.TemplateDraft{Kind: types.TemplateSheet, Slug: "u67_" + uuid.NewString()[:8], Name: "Bộ chứng từ",
		Body: "Bóc tách.", Tables: []types.SheetTable{
			{Label: "hop_dong", Title: "Hợp đồng", Fields: []types.SheetField{{Key: "so_hop_dong", Label: "Số hợp đồng"}, {Key: "gia_tri", Label: "Giá trị", ValueType: "money"}}},
			{Label: "hoa_don", Title: "Hoá đơn", Fields: []types.SheetField{{Key: "so_hoa_don", Label: "Số hoá đơn"}, {Key: "tong_tien", Label: "Tổng thanh toán", ValueType: "money"}}},
		}, By: h.Owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Store.Templates.Publish(h.Ctx, tpl.ID, 1); err != nil {
		t.Fatal(err)
	}
	refs, err := h.Index.CaseRefs(h.Ctx, h.Owner.ID, cs.ID)
	if err != nil {
		t.Fatal(err)
	}
	agent := &fakeAgent{h: h, dm: docmodel.New(h.Store, h.Index), caseID: cs.ID, refs: refs, skip: map[string]bool{}}
	q := &stubQueue{}
	svc := sheets.New(sheets.Deps{Store: h.Store, Queue: q, Cases: h.Cases, Searcher: h.Index, Runner: agent, Config: h.Config})
	sh, err := svc.Create(h.Ctx, h.Owner, cs.ID, tpl.ID, nil)
	if err != nil {
		t.Fatalf("create with unclassified files: %v", err)
	}
	if err := svc.Handlers()[types.TaskCaseSheet](h.Ctx, q.payloads[len(q.payloads)-1]); err != nil {
		t.Fatal(err)
	}
	sv, err := svc.Get(h.Ctx, h.Owner.ID, sh.ID)
	if err != nil {
		t.Fatal(err)
	}

	for _, tv := range sv.TablesView {
		var head []string
		for _, f := range tv.Fields {
			head = append(head, f.Label)
		}
		t.Logf("[%s] Loại tài liệu | %s", tv.Title, strings.Join(head, " | "))
		for _, r := range tv.Rows {
			var vals []string
			for _, f := range tv.Fields {
				vals = append(vals, r.CellsView[f.Key].Value)
			}
			t.Logf("[%s] %s | %s", tv.Title, tv.Title, strings.Join(vals, " | "))
		}
	}

	if len(agent.prompts) != 3 {
		t.Fatalf("agent turns = %d, want one per file", len(agent.prompts))
	}
	if sv.Status != types.SheetDone || len(sv.TablesView) != 2 || sv.Total != 6 || sv.Filled != 6 {
		t.Fatalf("sheet = %s tables=%d %d/%d", sv.Status, len(sv.TablesView), sv.Filled, sv.Total)
	}
	hd, inv := sv.TablesView[0].Rows, sv.TablesView[1].Rows
	if len(hd) != 1 || hd[0].FileName != "ho_so_dot1.pdf" || hd[0].CellsView["so_hop_dong"].Value != "012/2026/HDMB-AP" || hd[0].CellsView["gia_tri"].Value != "660.000.000" {
		t.Fatalf("contracts = %+v", hd)
	}
	if len(inv) != 2 || inv[0].FileName != "ho_so_dot1.pdf" || inv[0].CellsView["so_hoa_don"].Value != "0001234" ||
		inv[1].FileName != "hoa_don_dot2.pdf" || inv[1].CellsView["tong_tien"].Value != "110.000.000" {
		t.Fatalf("invoices = %+v", inv)
	}
}

// Saving an edited template adds a version: the query that numbers it locks
// the template row (it failed with "FOR UPDATE is not allowed with GROUP BY").
func TestTemplateAddVersion(t *testing.T) {
	h := testkit.New(t)
	draft := postgres.TemplateDraft{Kind: types.TemplateSheet, Slug: "ver_" + uuid.NewString()[:8], Name: "Mẫu", Body: "Bóc tách.",
		Tables: []types.SheetTable{{Label: "hoa_don", Title: "Hoá đơn", Fields: []types.SheetField{{Key: "so_hoa_don", Label: "Số hoá đơn"}}}}, By: h.Owner.ID}
	tpl, err := h.Store.Templates.Create(h.Ctx, draft)
	if err != nil {
		t.Fatal(err)
	}
	for want := 2; want <= 3; want++ {
		if _, err := h.Store.Templates.AddVersion(h.Ctx, tpl.ID, draft); err != nil {
			t.Fatalf("version %d: %v", want, err)
		}
		if _, err := h.Store.Templates.Get(h.Ctx, tpl.ID, want); err != nil {
			t.Fatalf("get version %d: %v", want, err)
		}
	}
}
