package sheets_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/application/service/docmodel"
	"github.com/thanhenti/bepaylot/internal/application/service/sheets"
	"github.com/thanhenti/bepaylot/internal/parser/pdf/pdftest"
	"github.com/thanhenti/bepaylot/internal/queue"
	"github.com/thanhenti/bepaylot/internal/testkit"
	"github.com/thanhenti/bepaylot/internal/types"
)

// stubQueue keeps what is enqueued; the test runs the task itself.
type stubQueue struct{ payloads [][]byte }

func (q *stubQueue) Enqueue(_ context.Context, _ string, payload any, _ queue.Opts) error {
	b, err := json.Marshal(payload)
	q.payloads = append(q.payloads, b)
	return err
}

// fakeRunner stands in for the agent turn: it records the prompt and
// answers that the purpose field was not found.
type fakeRunner struct{ prompts []string }

func (r *fakeRunner) RunSheet(_ context.Context, _ types.User, _, _ uuid.UUID, text string) (string, error) {
	r.prompts = append(r.prompts, text)
	return "Đã ghi 2 trường.\n- muc_dich_vay: không có trang nào nêu mục đích vay", nil
}

// U43/U44 end to end on the database: agent fields with evidence, a sheet
// that reuses confirmed values, user edits recorded as corrections, and the
// .xlsx round trip that finds the edited cell (§6.9.6).
func TestSheetFlow(t *testing.T) {
	h := testkit.New(t)
	kb := h.KB(types.KBConfig{}, nil)
	doc := h.UploadTo(kb.ID, "PA-001", "phuong-an.pdf", nil, []pdftest.Page{
		{"PHUONG AN VAY VON", "So tien vay 15.000.000.000 dong", "Loi nhuan sau thue 6.124.800.000"},
	})
	h.Drain()
	cs := h.Case(kb.ID, "PA-001")
	cite := func(q string) string {
		pages, err := h.Index.FindInDocument(h.Ctx, h.Owner.ID, doc, q, types.SearchKeyword, 0, 0)
		if err != nil || len(pages) == 0 || len(pages[0].Hits) == 0 {
			t.Fatalf("find %q: %+v %v", q, pages, err)
		}
		l := pages[0].Hits[0].LineNo
		return fmt.Sprintf("doc:%s:p%d:l%d-%d", doc, pages[0].PageNo, l, l)
	}

	dm := docmodel.New(h.Store, h.Index)
	// A value not in the quote needs a note.
	res := dm.SaveAgentFields(h.Ctx, h.Owner.ID, cs.ID, nil, []types.FieldInput{
		{DocumentID: doc, Key: "loi_nhuan_sau_thue", Value: "6.214.800.000", Confidence: 0.6, Citations: []string{cite("loi nhuan")}},
	})
	if res[0].Error == "" {
		t.Fatalf("unmatched value without note was saved: %+v", res)
	}
	res = dm.SaveAgentFields(h.Ctx, h.Owner.ID, cs.ID, nil, []types.FieldInput{
		{DocumentID: doc, Key: "loi_nhuan_sau_thue", Value: "6.214.800.000", Confidence: 0.6, Citations: []string{cite("loi nhuan")}, Note: "đọc từ dòng lợi nhuận"},
		{DocumentID: doc, Key: "so_tien_vay", Value: float64(15000000000), ValueType: "money", Confidence: 0.95, Citations: []string{cite("tien vay")}},
		{DocumentID: doc, Key: "muc_dich_vay", Value: "x", Confidence: 0.9, Citations: []string{"doc:" + uuid.NewString() + ":p1:l1-1"}},
	})
	if res[0].Error != "" || res[0].ValueMatched || res[1].Error != "" || !res[1].ValueMatched || res[2].Error == "" {
		t.Fatalf("save fields = %+v", res)
	}

	tpl, err := h.Store.Templates.Create(h.Ctx, postgres.TemplateDraft{Kind: types.TemplateSheet, Slug: "pa_" + uuid.NewString()[:8], Name: "Bảng phương án",
		Body: "Bóc tách.", Fields: []types.SheetField{{Key: "loi_nhuan_sau_thue", Label: "Lợi nhuận sau thuế"}, {Key: "so_tien_vay", Label: "Số tiền vay", ValueType: "money"}, {Key: "muc_dich_vay", Label: "Mục đích vay"}}, By: h.Owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Store.Templates.Publish(h.Ctx, tpl.ID, 1); err != nil {
		t.Fatal(err)
	}

	q, runner := &stubQueue{}, &fakeRunner{}
	svc := sheets.New(sheets.Deps{Store: h.Store, Queue: q, Cases: h.Cases, Runner: runner, Config: h.Config})
	build := func() sheets.View {
		t.Helper()
		sh, err := svc.Create(h.Ctx, h.Owner, cs.ID, tpl.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.Handlers()[types.TaskCaseSheet](h.Ctx, q.payloads[len(q.payloads)-1]); err != nil {
			t.Fatal(err)
		}
		v, err := svc.Get(h.Ctx, h.Owner.ID, sh.ID)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	v := build()
	if v.Status != types.SheetDone || v.Filled != 2 || len(v.RowsView) != 3 {
		t.Fatalf("sheet = %s %d/%d", v.Status, v.Filled, len(v.RowsView))
	}
	if p := runner.prompts[0]; !strings.Contains(p, "loi_nhuan_sau_thue") || !strings.Contains(p, "muc_dich_vay") {
		t.Fatalf("first build must ask for every unconfirmed field:\n%s", p)
	}
	if v.RowsView[2].Note == "" || v.RowsView[0].Edited || !v.RowsView[0].Calc {
		t.Fatalf("rows = %+v", v.RowsView)
	}

	// Format-only change: no correction; a real change: one correction.
	v, err = svc.SaveEdits(h.Ctx, h.Owner, v.ID, []sheets.EditInput{{Key: "so_tien_vay", Value: "15.000.000.000"}, {Key: "loi_nhuan_sau_thue", Value: "6.124.800.000"}})
	if err != nil {
		t.Fatal(err)
	}
	if !v.RowsView[0].Edited || v.RowsView[1].Edited || v.RowsView[0].Status != types.FieldConfirmed || !v.RowsView[0].Matched {
		t.Fatalf("after edits = %+v", v.RowsView[:2])
	}
	stats, err := svc.CorrectionStats(h.Ctx, tpl.ID)
	if err != nil {
		t.Fatal(err)
	}
	edited := map[string]int{}
	for _, s := range stats {
		edited[s.Key] = s.Edited
	}
	if edited["loi_nhuan_sau_thue"] != 1 || edited["so_tien_vay"] != 0 {
		t.Fatalf("corrections = %+v", stats)
	}
	// A row without a field needs its source file.
	if _, err := svc.SaveEdits(h.Ctx, h.Owner, v.ID, []sheets.EditInput{{Key: "muc_dich_vay", Value: "Bổ sung vốn"}}); !errors.Is(err, sheets.ErrNeedSource) {
		t.Fatalf("edit without source: %v", err)
	}

	// The second sheet reuses the confirmed values.
	v2 := build()
	if p := runner.prompts[1]; strings.Contains(p, "loi_nhuan_sau_thue") || strings.Contains(p, "so_tien_vay") || !strings.Contains(p, "muc_dich_vay") {
		t.Fatalf("second build must ask only for the missing field:\n%s", p)
	}
	if v2.RowsView[0].Value != "6.124.800.000" || v2.RowsView[0].Edited {
		t.Fatalf("second sheet row = %+v", v2.RowsView[0])
	}

	// .xlsx round trip: one changed cell and one row typed by hand.
	var buf bytes.Buffer
	if _, err := svc.XLSX(h.Ctx, h.Owner.ID, v.ID, &buf); err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	_ = f.SetCellValue("Tổng hợp", "B5", "16.000.000.000")
	_ = f.SetCellValue("Tổng hợp", "A9", "Ghi chú thẩm định: khách hàng quen")
	var edited2 bytes.Buffer
	if err := f.Write(&edited2); err != nil {
		t.Fatal(err)
	}
	imp, err := svc.Import(h.Ctx, h.Owner.ID, v.ID, bytes.NewReader(edited2.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if len(imp.Edits) != 1 || imp.Edits[0].Key != "so_tien_vay" || len(imp.Ignored) != 1 || len(imp.Conflicts) != 0 {
		t.Fatalf("import = %+v", imp)
	}
	if _, err := svc.Import(h.Ctx, h.Owner.ID, v2.ID, bytes.NewReader(edited2.Bytes())); !errors.Is(err, sheets.ErrBadFile) {
		t.Fatalf("a file of another sheet must be refused: %v", err)
	}
}
