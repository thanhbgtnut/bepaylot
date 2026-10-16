package sheets_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"

	"github.com/thanhenti/bepaylot/docpipe/parser/pdf/pdftest"
	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/application/service/docmodel"
	"github.com/thanhenti/bepaylot/internal/application/service/sheets"
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

// fakeExtract stands in for the agent's extraction turn: it answers the JSON
// the prompt asks for from the "Label: value" lines of the document text.
type fakeExtract struct {
	mu      sync.Mutex
	prompts []string
	delay   time.Duration // how long a turn takes
}

var (
	askedRe = regexp.MustCompile(`(?m)^- (\w+) · `)
	pageRe  = regexp.MustCompile(`^<page n="(\d+)">$`)
	lineRe  = regexp.MustCompile(`^\[L(\d+)\] (?:\([0-9.]+\) )?(.*)$`)
)

// where each key is written in the test documents.
var keyLine = map[string]string{"so_hop_dong": "So hop dong", "gia_tri": "Gia tri hop dong", "ngay_ky": "Ngay ky",
	"so_hoa_don": "So hoa don", "tong_tien": "Tong thanh toan"}

func (f *fakeExtract) RunExtract(_ context.Context, _ types.User, _, _ uuid.UUID, text string) (string, error) {
	f.mu.Lock()
	f.prompts = append(f.prompts, text)
	f.mu.Unlock()
	time.Sleep(f.delay)
	var fields []map[string]any
	for _, m := range askedRe.FindAllStringSubmatch(text, -1) {
		key, page := m[1], 0
		for _, l := range strings.Split(text, "\n") {
			if pm := pageRe.FindStringSubmatch(l); pm != nil {
				page, _ = strconv.Atoi(pm[1])
			}
			lm := lineRe.FindStringSubmatch(l)
			if lm == nil || keyLine[key] == "" || !strings.HasPrefix(lm[2], keyLine[key]+": ") {
				continue
			}
			line, _ := strconv.Atoi(lm[1])
			fields = append(fields, map[string]any{"key": key, "value": strings.TrimPrefix(lm[2], keyLine[key]+": "), "confidence": 0.9, "page": page, "line": line})
			break
		}
	}
	b, _ := json.Marshal(map[string]any{"fields": fields})
	return "```json\n" + string(b) + "\n```", nil
}

func docType(name, title string, fields ...types.SheetField) types.DocType {
	return types.DocType{ClassLabel: types.ClassLabel{Name: name, Title: title}, Fields: fields}
}

// U68–U70, U80–U82 end to end on the database (scripted page types, fake
// extraction turn): the fields of each document type are set on the KB,
// extracted only when a sheet is built and kept for the next sheets, one row
// per document and one sub-table per type.
func TestDocTypeSheetFlow(t *testing.T) {
	h := testkit.New(t)
	h.Index.SetSplit(docmodel.NewSplit(docmodel.SplitDeps{Store: h.Store, Queue: h.Queue, Cases: h.Cases, Tree: h.Index, Config: h.Config}))
	agent := &fakeExtract{}
	ex := docmodel.NewExtractor(h.Store, agent, nil)

	cfg := types.KBConfig{DocTypes: []types.DocType{
		docType("hop_dong", "Hợp đồng", types.SheetField{Key: "so_hop_dong", Label: "Số hợp đồng", Description: "Số ở đầu trang,\nsau chữ So hop dong"}, types.SheetField{Key: "gia_tri", Label: "Giá trị", ValueType: "money"}),
		docType("hoa_don", "Hoá đơn", types.SheetField{Key: "so_hoa_don", Label: "Số hoá đơn"}, types.SheetField{Key: "tong_tien", Label: "Tổng thanh toán", ValueType: "money"},
			types.SheetField{Key: "mst", Label: "Mã số thuế"}),
		docType("bien_ban", "Biên bản"), // no field: no sub-table
	}}
	kb := h.KB(cfg, nil)
	h.PageTypes.Labels = []string{"hop_dong", "hop_dong", "hoa_don", "hoa_don"}
	h.PageTypes.FirstPages = []bool{true, false, true, true}
	doc := h.Upload(kb.ID, "bo-chung-tu.pdf", nil, []pdftest.Page{
		{"HOP DONG MUA BAN", "So hop dong: 012/2026/HDMB", "Ngay ky: 2026-03-01"},
		{"Dieu 2 thanh toan", "Gia tri hop dong: 660.000.000"},
		{"HOA DON GIA TRI GIA TANG", "So hoa don: 0001234", "Tong thanh toan: 550.000.000"},
		{"HOA DON GIA TRI GIA TANG", "So hoa don: 0001240", "Tong thanh toan: 110.000.000"},
	})
	h.Drain()
	cs := h.Case(kb.ID, testkit.DefaultCase)

	// Processing a file asks the agent for nothing.
	if len(agent.prompts) != 0 {
		t.Fatalf("processing the file ran %d extraction turns", len(agent.prompts))
	}
	sq := &stubQueue{}
	svc := sheets.New(sheets.Deps{Store: h.Store, Queue: sq, Cases: h.Cases, Extractor: ex, Config: h.Config})
	build := func(tables ...string) sheets.View {
		t.Helper()
		sh, err := svc.Create(h.Ctx, h.Owner, cs.ID, tables)
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.Handlers()[types.TaskCaseSheet](h.Ctx, sq.payloads[len(sq.payloads)-1]); err != nil {
			t.Fatal(err)
		}
		v, err := svc.Get(h.Ctx, h.Owner.ID, sh.ID)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	// A sheet of one document type extracts that type only.
	only := build("hoa_don")
	if len(agent.prompts) != 2 || len(only.TablesView) != 1 || strings.Contains(strings.Join(agent.prompts, ""), "So hop dong") {
		t.Fatalf("sheet of invoices: %d turns, %d tables", len(agent.prompts), len(only.TablesView))
	}
	agent.prompts = nil
	if err := svc.Delete(h.Ctx, h.Owner.ID, only.ID); err != nil {
		t.Fatal(err)
	}

	// The first sheet of every type asks for the contract, with the text of
	// its pages and the description of the field, and once more for the field
	// the invoices did not have (U82). The values are saved on the document
	// (segment) with evidence.
	sv := build()
	if !strings.Contains(agent.prompts[0], "- so_hop_dong · Số hợp đồng · string · Số ở đầu trang, sau chữ So hop dong\n") ||
		!strings.Contains(agent.prompts[0], "- gia_tri · Giá trị · money\n") || !strings.Contains(agent.prompts[1], "- mst · ") ||
		strings.Contains(agent.prompts[1], "- so_hoa_don · ") {
		t.Fatalf("extraction prompts:\n%s", strings.Join(agent.prompts, "\n----\n"))
	}
	if len(agent.prompts) != 3 || !strings.Contains(agent.prompts[0], "Hợp đồng") || !strings.Contains(agent.prompts[0], `<page n="2">`) ||
		strings.Contains(agent.prompts[0], "So hoa don") {
		t.Fatalf("extraction turns:\n%s", strings.Join(agent.prompts, "\n----\n"))
	}
	fields, err := h.Store.Fields.ListByCase(h.Ctx, cs.ID, &doc, "gia_tri")
	if err != nil || len(fields) != 1 {
		t.Fatalf("fields = %+v %v", fields, err)
	}
	if f := fields[0]; f.SegmentID == nil || f.ValueText != "660.000.000" || f.ValueType != "money" || f.Status != types.FieldProposed ||
		len(f.Evidence) != 1 || f.Evidence[0].PageNo != 2 || !f.ValueMatched {
		t.Fatalf("field = %+v", f)
	}

	// Everything is saved: later sheets ask for nothing, not even the field
	// that was not found twice, also after the earlier sheet is deleted.
	for i := 0; i < 2; i++ {
		again := build()
		if len(agent.prompts) != 3 {
			t.Fatalf("building the sheet again ran %d more turns", len(agent.prompts)-3)
		}
		if err := svc.Delete(h.Ctx, h.Owner.ID, again.ID); err != nil {
			t.Fatal(err)
		}
	}
	if sv.Status != types.SheetDone || len(sv.TablesView) != 2 || sv.Total != 8 || sv.Filled != 6 {
		t.Fatalf("sheet = %s tables=%d %d/%d", sv.Status, len(sv.TablesView), sv.Filled, sv.Total)
	}
	hd, inv := sv.TablesView[0], sv.TablesView[1]
	if len(hd.Rows) != 1 || hd.Rows[0].SegmentID == nil || hd.Rows[0].PageEnd != 2 || hd.Rows[0].CellsView["so_hop_dong"].Value != "012/2026/HDMB" {
		t.Fatalf("contracts = %+v", hd.Rows)
	}
	if len(inv.Rows) != 2 || inv.Rows[0].CellsView["so_hoa_don"].Value != "0001234" || inv.Rows[1].CellsView["tong_tien"].Value != "110.000.000" {
		t.Fatalf("invoices = %+v", inv.Rows)
	}
	if c := inv.Rows[0].CellsView["mst"]; c.FieldID != nil || c.Note == "" || c.Value != "" {
		t.Fatalf("a field the document does not have = %+v", c)
	}

	// A field added to a type afterwards is extracted when the next sheet is
	// built, and only that field is asked for. Changing the description of a
	// field that has a value asks for nothing.
	cfg.DocTypes[0].Fields[0].Description = "Số hợp đồng ở dòng thứ hai"
	cfg.DocTypes[0].Fields = append(cfg.DocTypes[0].Fields, types.SheetField{Key: "ngay_ky", Label: "Ngày ký", ValueType: "date"})
	if _, err := h.Docs.UpdateKB(h.Ctx, h.Owner.ID, kb.ID, postgres.KBPatch{Config: &cfg}); err != nil {
		t.Fatal(err)
	}
	sv2 := build()
	if len(agent.prompts) != 4 || !strings.Contains(agent.prompts[3], "- ngay_ky · ") || strings.Contains(agent.prompts[3], "- so_hop_dong · ") {
		t.Fatalf("turns after a new field:\n%s", strings.Join(agent.prompts[3:], "\n----\n"))
	}
	if got := sv2.TablesView[0].Rows[0].CellsView["ngay_ky"].Value; got != "2026-03-01" {
		t.Fatalf("new field = %q", got)
	}
	if len(sv.TablesView[0].Fields) != 2 {
		t.Fatal("the first sheet keeps the columns it was made with")
	}

	// A user edit is a confirmed field of that document, recorded as edited.
	seg := inv.Rows[0].SegmentID
	sv, err = svc.SaveEdits(h.Ctx, h.Owner, sv.ID, []sheets.EditInput{{Table: "hoa_don", SegmentID: seg, Key: "so_hoa_don", Value: "0001235"}})
	if err != nil {
		t.Fatal(err)
	}
	if c := sv.TablesView[1].Rows[0].CellsView["so_hoa_don"]; !c.Edited || c.Value != "0001235" || c.Status != types.FieldConfirmed {
		t.Fatalf("after edit = %+v", c)
	}

	// .xlsx: one worksheet per document type, one row per document.
	var buf bytes.Buffer
	if _, err := svc.XLSX(h.Ctx, h.Owner.ID, sv.ID, nil, &buf); err != nil {
		t.Fatal(err)
	}
	wb, err := excelize.OpenReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(wb.GetSheetList(), ","); got != "Hợp đồng,_bp,Hoá đơn" && got != "Hợp đồng,Hoá đơn,_bp" {
		t.Fatalf("worksheets = %s", got)
	}
	rows, _ := wb.GetRows("Hoá đơn")
	if len(rows) != 3 || strings.Join(rows[0], "|") != "Loại tài liệu|Bộ|Số hoá đơn|Tổng thanh toán|Mã số thuế" || rows[1][0] != "Hoá đơn" || rows[1][2] != "0001235" {
		t.Fatalf("invoice worksheet = %v", rows)
	}
	_ = wb.SetCellValue("Hoá đơn", "D3", "120.000.000")
	var edited bytes.Buffer
	if err := wb.Write(&edited); err != nil {
		t.Fatal(err)
	}
	imp, err := svc.Import(h.Ctx, h.Owner.ID, sv.ID, bytes.NewReader(edited.Bytes()))
	if err != nil || len(imp.Edits) != 1 || imp.Edits[0].Key != "tong_tien" || imp.Edits[0].SegmentID == nil {
		t.Fatalf("import = %+v %v", imp, err)
	}

	// Deleting a sheet removes only that sheet: the other one and the fields
	// of the case stay, and someone else's sheet is not found.
	if err := svc.Delete(h.Ctx, uuid.New(), sv2.ID); !errors.Is(err, sheets.ErrNotFound) {
		t.Fatalf("delete by another user: %v", err)
	}
	if err := svc.Delete(h.Ctx, h.Owner.ID, sv2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(h.Ctx, h.Owner.ID, sv2.ID); !errors.Is(err, sheets.ErrNotFound) {
		t.Fatalf("deleted sheet: %v", err)
	}
	if list, err := svc.List(h.Ctx, h.Owner.ID, cs.ID); err != nil || len(list) != 1 || list[0].ID != sv.ID {
		t.Fatalf("sheets after delete = %+v %v", list, err)
	}
	if fields, err := h.Store.Fields.ListByCase(h.Ctx, cs.ID, &doc, "ngay_ky"); err != nil || len(fields) != 1 {
		t.Fatalf("fields after delete = %+v %v", fields, err)
	}

	// A KB whose document types have no field has nothing to build.
	bare := h.KB(types.KBConfig{DocTypes: []types.DocType{docType("hop_dong", "Hợp đồng")}}, nil)
	h.PageTypes.Labels = append(h.PageTypes.Labels, "hop_dong")
	h.UploadTo(bare.ID, "HS-BARE", "a.pdf", nil, []pdftest.Page{{"HOP DONG"}})
	h.Drain()
	if _, err := svc.Create(h.Ctx, h.Owner, h.Case(bare.ID, "HS-BARE").ID, nil); !errors.Is(err, sheets.ErrNoTables) {
		t.Fatalf("sheet without fields: %v", err)
	}
}

// U80: two sheets built at the same time ask for each document once, and a
// file added later is the only one the next sheet asks for.
func TestSheetsAskOnce(t *testing.T) {
	h := testkit.New(t)
	h.Index.SetSplit(docmodel.NewSplit(docmodel.SplitDeps{Store: h.Store, Queue: h.Queue, Cases: h.Cases, Tree: h.Index, Config: h.Config}))
	agent := &fakeExtract{delay: 100 * time.Millisecond}
	kb := h.KB(types.KBConfig{DocTypes: []types.DocType{docType("hoa_don", "Hoá đơn", types.SheetField{Key: "so_hoa_don", Label: "Số hoá đơn"})}}, nil)
	h.PageTypes.Labels = []string{"hoa_don", "hoa_don", "hoa_don"}
	h.PageTypes.FirstPages = []bool{true, true, true}
	h.Upload(kb.ID, "a.pdf", nil, []pdftest.Page{
		{"HOA DON GIA TRI GIA TANG", "So hoa don: 0000001", "Tong thanh toan: 550.000.000"},
		{"HOA DON GIA TRI GIA TANG", "So hoa don: 0000002", "Tong thanh toan: 110.000.000"},
	})
	h.Drain()
	cs := h.Case(kb.ID, testkit.DefaultCase)

	sq := &stubQueue{}
	svc := sheets.New(sheets.Deps{Store: h.Store, Queue: sq, Cases: h.Cases, Extractor: docmodel.NewExtractor(h.Store, agent, nil), Config: h.Config})
	create := func() []byte {
		t.Helper()
		if _, err := svc.Create(h.Ctx, h.Owner, cs.ID, nil); err != nil {
			t.Fatal(err)
		}
		return sq.payloads[len(sq.payloads)-1]
	}
	tasks := [][]byte{create(), create()}
	var wg sync.WaitGroup
	errs := make([]error, len(tasks))
	for i, p := range tasks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = svc.Handlers()[types.TaskCaseSheet](h.Ctx, p)
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
	if len(agent.prompts) != 2 {
		t.Fatalf("two sheets at once ran %d turns for 2 documents:\n%s", len(agent.prompts), strings.Join(agent.prompts, "\n----\n"))
	}
	list, err := svc.List(h.Ctx, h.Owner.ID, cs.ID)
	if err != nil || len(list) != 2 || list[0].Filled != 2 || list[1].Filled != 2 {
		t.Fatalf("sheets = %+v %v", list, err)
	}

	h.Upload(kb.ID, "b.pdf", nil, []pdftest.Page{{"HOA DON GIA TRI GIA TANG", "So hoa don: 0000003", "Tong thanh toan: 220.000.000"}})
	h.Drain()
	if err := svc.Handlers()[types.TaskCaseSheet](h.Ctx, create()); err != nil {
		t.Fatal(err)
	}
	if len(agent.prompts) != 3 || !strings.Contains(agent.prompts[2], "0000003") {
		t.Fatalf("after a new file: %d turns", len(agent.prompts))
	}
}

// A sheet with no document of its types does not end as an empty table: it
// fails and says what each file of the case is classified as.
func TestEmptySheetSaysWhy(t *testing.T) {
	h := testkit.New(t)
	h.Index.SetSplit(docmodel.NewSplit(docmodel.SplitDeps{Store: h.Store, Queue: h.Queue, Cases: h.Cases, Tree: h.Index, Config: h.Config}))
	agent := &fakeExtract{}
	kb := h.KB(types.KBConfig{DocTypes: []types.DocType{docType("hop_dong", "Hợp đồng", types.SheetField{Key: "so_hop_dong", Label: "Số hợp đồng"})}}, nil)
	h.Upload(kb.ID, "scan.pdf", nil, []pdftest.Page{{"HOP DONG MUA BAN", "So hop dong: 012"}}) // pages not classified
	h.Drain()
	cs := h.Case(kb.ID, testkit.DefaultCase)
	sq := &stubQueue{}
	svc := sheets.New(sheets.Deps{Store: h.Store, Queue: sq, Cases: h.Cases, Extractor: docmodel.NewExtractor(h.Store, agent, nil), Config: h.Config})
	sh, err := svc.Create(h.Ctx, h.Owner, cs.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Handlers()[types.TaskCaseSheet](h.Ctx, sq.payloads[0]); err != nil {
		t.Fatal(err)
	}
	v, err := svc.Get(h.Ctx, h.Owner.ID, sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != types.SheetFailed || !strings.Contains(v.Error, "hop_dong") || !strings.Contains(v.Error, "scan.pdf: chưa được phân loại") || len(agent.prompts) != 0 {
		t.Fatalf("sheet = %s %q, %d turns", v.Status, v.Error, len(agent.prompts))
	}
}
