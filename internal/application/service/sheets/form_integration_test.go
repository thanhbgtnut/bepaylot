package sheets_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/thanhenti/bepaylot/docpipe/parser/pdf/pdftest"
	"github.com/thanhenti/bepaylot/internal/application/service/docmodel"
	"github.com/thanhenti/bepaylot/internal/application/service/sheets"
	"github.com/thanhenti/bepaylot/internal/storage"
	"github.com/thanhenti/bepaylot/internal/testkit"
	"github.com/thanhenti/bepaylot/internal/types"
)

// formLLM answers the mapping of the bảng kê form as the model would.
type formLLM struct{ calls int }

func (f *formLLM) CompleteJSON(_ context.Context, _, user string, out any) error {
	f.calls++
	return json.Unmarshal([]byte(`{"doc_type":"hoa_don","columns":[
		{"col":"A","kind":"seq"},{"col":"B","kind":"field","key":"so_hoa_don"},{"col":"C","kind":"blank"},
		{"col":"D","kind":"field","key":"tong_tien","total":"SUM(D{first}:D{last})"},
		{"col":"E","kind":"field","new_field":{"label":"Số tiền các NH đã cho vay","value_type":"money"}},
		{"col":"F","kind":"formula","formula":"SUMIF($G${first}:$G${last},G{r},$D${first}:$D${last})","total":"SUM(F{first}:F{last})"},
		{"col":"G","kind":"blank"},{"col":"H","kind":"blank"}],
		"placeholders":[{"cell":"C16","doc_type":"hop_dong","key":"so_hop_dong"},{"cell":"G17"}]}`), out)
}

// U85 end to end on the database: a prompt editor imports the form, the
// code finds its regions and the model's mapping is checked; saving adds the
// new field to the KB; Studio builds the sheet of the form (extracting only
// its document types) and the download is the form filled with the rows,
// the formulas and the placeholders.
func TestExportFormFlow(t *testing.T) {
	h := testkit.New(t)
	h.Index.SetSplit(docmodel.NewSplit(docmodel.SplitDeps{Store: h.Store, Queue: h.Queue, Cases: h.Cases, Tree: h.Index, Config: h.Config}))
	agent := &fakeExtract{}
	ex := docmodel.NewExtractor(h.Store, agent, nil)
	kb := h.KB(types.KBConfig{DocTypes: []types.DocType{
		docType("hop_dong", "Hợp đồng", types.SheetField{Key: "so_hop_dong", Label: "Số hợp đồng"}),
		docType("hoa_don", "Hoá đơn", types.SheetField{Key: "so_hoa_don", Label: "Số hoá đơn"}, types.SheetField{Key: "tong_tien", Label: "Tổng thanh toán", ValueType: "money"}),
	}}, nil)
	h.PageTypes.Labels = []string{"hop_dong", "hoa_don", "hoa_don"}
	h.PageTypes.FirstPages = []bool{true, true, true}
	h.Upload(kb.ID, "bo.pdf", nil, []pdftest.Page{
		{"HOP DONG MUA BAN", "So hop dong: 012/2026/HDMB"},
		{"HOA DON GIA TRI GIA TANG", "So hoa don: 0001234", "Tong thanh toan: 550.000.000"},
		{"HOA DON GIA TRI GIA TANG", "So hoa don: 0001240", "Tong thanh toan: 110.000.000"},
	})
	h.Drain()
	cs := h.Case(kb.ID, testkit.DefaultCase)

	llm := &formLLM{}
	sq := &stubQueue{}
	svc := sheets.New(sheets.Deps{Store: h.Store, Queue: sq, Cases: h.Cases, Extractor: ex, Config: h.Config,
		Objects: storage.NewMemory(), FormLLM: llm})

	data, err := os.ReadFile("testdata/bang_ke_hoa_don.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UploadForm(h.Ctx, h.Owner, kb.ID, "x.xlsx", []byte("not a workbook")); err == nil {
		t.Fatal("a file that is not .xlsx was accepted")
	}
	tpl, err := svc.UploadForm(h.Ctx, h.Owner, kb.ID, "QT.B4 Bang ke.xlsx", data)
	if err != nil {
		t.Fatal(err)
	}
	if llm.calls != 1 || tpl.Name != "Bảng kê hóa đơn chứng từ giải ngân" || tpl.Layout.DataRows != 9 {
		t.Fatalf("upload: %d calls, name %q, layout %+v", llm.calls, tpl.Name, tpl.Layout)
	}
	if len(tpl.Mapping.NewFields) != 1 || tpl.Mapping.Columns[4].Key == "" {
		t.Fatalf("the new field: %+v", tpl.Mapping)
	}

	// A formula with a fixed row is refused when saving.
	bad := tpl.Mapping
	bad.Columns = append([]types.ExportColumnMap(nil), bad.Columns...)
	bad.Columns[3].Total = "SUM(D5:D13)"
	if _, err := svc.SaveForm(h.Ctx, h.Owner, tpl.ID, "", bad); err == nil {
		t.Fatal("a total with fixed rows was saved")
	}
	tpl, err = svc.SaveForm(h.Ctx, h.Owner, tpl.ID, "Bảng kê giải ngân", tpl.Mapping)
	if err != nil {
		t.Fatal(err)
	}
	k, _ := h.Store.KBs.Get(h.Ctx, kb.ID)
	if len(tpl.Mapping.NewFields) != 0 || len(k.Config.DocTypes[1].Fields) != 3 || k.Config.DocTypes[1].Fields[2].Label != "Số tiền các NH đã cho vay" {
		t.Fatalf("saving adds the new field to the KB: %+v", k.Config.DocTypes[1].Fields)
	}
	if list, _ := svc.ListForms(h.Ctx, h.Owner.ID, kb.ID); len(list) != 1 || list[0].Name != "Bảng kê giải ngân" {
		t.Fatalf("list: %+v", list)
	}

	sh, err := svc.CreateForForm(h.Ctx, h.Owner, cs.ID, tpl.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sh.Name != "Bảng kê giải ngân" || sh.FormID == nil || len(sh.Tables) != 2 {
		t.Fatalf("sheet of the form: %+v", sh)
	}
	if err := svc.Handlers()[types.TaskCaseSheet](h.Ctx, sq.payloads[len(sq.payloads)-1]); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	name, err := svc.FormXLSX(h.Ctx, h.Owner.ID, sh.ID, nil, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(name, "Bảng-kê-giải-ngân.xlsx") {
		t.Fatalf("file name %q", name)
	}
	f, err := excelize.OpenReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ws := tpl.Layout.Sheet
	get := func(c string) string { v, _ := f.GetCellValue(ws, c); return v }
	sum, err := f.CalcCellValue(ws, "D14")
	if err != nil {
		t.Fatal(err)
	}
	if get("A5") != "1" || get("B5") != "0001234" || get("B6") != "0001240" || get("A7") != "" || sum != "660000000" {
		t.Fatalf("rows: A5=%q B5=%q B6=%q A7=%q D14=%q", get("A5"), get("B5"), get("B6"), get("A7"), sum)
	}
	if !strings.Contains(get("C16"), "012/2026/HDMB") {
		t.Fatalf("placeholder: %q", get("C16"))
	}

	// Another sheet of the form reuses the stored fields: no new turn.
	turns := len(agent.prompts)
	sh2, _ := svc.CreateForForm(h.Ctx, h.Owner, cs.ID, tpl.ID)
	if err := svc.Handlers()[types.TaskCaseSheet](h.Ctx, sq.payloads[len(sq.payloads)-1]); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.FormXLSX(h.Ctx, h.Owner.ID, sh2.ID, nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(agent.prompts) != turns {
		// Only the empty new field may be asked again once (U82).
		for _, p := range agent.prompts[turns:] {
			if strings.Contains(p, "so_hoa_don ·") {
				t.Fatalf("a field with a value was asked again:\n%s", p)
			}
		}
	}
	if err := svc.DeleteForm(h.Ctx, h.Owner.ID, tpl.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(h.Ctx, h.Owner.ID, sh.ID); err != nil {
		t.Fatalf("the sheet of a deleted form stays: %v", err)
	}
}
