package sheets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/thanhenti/bepaylot/docpipe/textutil"

	"github.com/thanhenti/bepaylot/internal/types"
)

func openForm(t *testing.T) *excelize.File {
	t.Helper()
	f, err := excelize.OpenFile("testdata/bang_ke_hoa_don.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestDetectFormBangKe(t *testing.T) {
	l, err := DetectForm(openForm(t))
	if err != nil {
		t.Fatal(err)
	}
	if l.HeaderTop != 3 || l.HeaderBottom != 4 || l.DataStart != 5 || l.DataRows != 9 || l.TotalRow != 14 || l.TotalLabel != "B14" {
		t.Fatalf("regions: %+v", l)
	}
	if len(l.Columns) != 8 || l.Columns[0].Header != "TT" || l.Columns[7].Col != "H" {
		t.Fatalf("columns: %+v", l.Columns)
	}
	if l.Columns[4].Note != "Được đóng dấu hoặc ghi trên hóa đơn" || l.Columns[7].Note != "Tích V - đạt hoặc X - không đạt" {
		t.Fatalf("footnotes: %+v", l.Columns)
	}
	if len(l.Placeholders) != 2 || l.Placeholders[0].Cell != "C16" || l.Placeholders[1].Cell != "G17" {
		t.Fatalf("placeholders: %+v", l.Placeholders)
	}
}

func TestDetectFormNoTable(t *testing.T) {
	f := excelize.NewFile()
	_ = f.SetCellStr("Sheet1", "A1", "chỉ có chữ")
	if _, err := DetectForm(f); err != ErrNoFormTable {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckFormula(t *testing.T) {
	cols := map[string]bool{"D": true, "G": true}
	ok := []string{"=SUM(D{first}:D{last})", "SUMIF($G${first}:$G${last},G{r},$D${first}:$D${last})", `IF(D{r}>0,D{r},"")`, ""}
	for _, f := range ok {
		if _, err := checkFormula(f, cols, false); err != nil {
			t.Errorf("%q: %v", f, err)
		}
	}
	bad := []string{"SUM(D5:D13)", "SUM(X{first}:X{last})", "VLOOKUP(D{r},A1:B2,2)", "SUM(D{first}:D{last}", "INDIRECT(\"A1\")"}
	for _, f := range bad {
		if _, err := checkFormula(f, cols, false); err == nil {
			t.Errorf("%q passed", f)
		}
	}
	if _, err := checkFormula("D{r}*2", cols, true); err == nil {
		t.Error("{r} on the total row passed")
	}
}

func TestParseNumber(t *testing.T) {
	cases := map[string]float64{"15.000.000": 15e6, "15,000,000": 15e6, "1.234,5": 1234.5, "1,234.5": 1234.5,
		"15 000 000 đ": 15e6, "2.000 VND": 2000, "12,5": 12.5, "3.5": 3.5, "15.000": 15000}
	for in, want := range cases {
		if got, ok := textutil.ParseNumber(in); !ok || got != want {
			t.Errorf("%q = %v %v, want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "abc", "01/02/2025"} {
		if _, ok := textutil.ParseNumber(in); ok {
			t.Errorf("%q parsed", in)
		}
	}
}

func bangKeMapping() types.ExportMapping {
	return types.ExportMapping{DocType: "hoa_don", Columns: []types.ExportColumnMap{
		{Col: "A", Kind: types.ExportSeq},
		{Col: "B", Kind: types.ExportField, Key: "so_hoa_don"},
		{Col: "C", Kind: types.ExportField, Key: "ngay_lap"},
		{Col: "D", Kind: types.ExportField, Key: "gia_tri", Total: "SUM(D{first}:D{last})"},
		{Col: "E", Kind: types.ExportBlank},
		{Col: "F", Kind: types.ExportFormula, Formula: "SUMIF($G${first}:$G${last},G{r},$D${first}:$D${last})"},
		{Col: "G", Kind: types.ExportField, Key: "don_vi"},
		{Col: "H", Kind: types.ExportBlank},
	}, Placeholders: []types.ExportFillMap{{Cell: "C16", DocType: "dkkd", Key: "ten"}, {Cell: "G17"}}}
}

var bangKeTypes = map[string]string{"gia_tri": "money", "so_hoa_don": "string"}

func render(t *testing.T, n int) *excelize.File {
	t.Helper()
	f := openForm(t)
	l, err := DetectForm(f)
	if err != nil {
		t.Fatal(err)
	}
	var recs []formRecord
	for i := 0; i < n; i++ {
		recs = append(recs, formRecord{"so_hoa_don": fmt.Sprintf("HD%02d", i+1), "ngay_lap": "01/02/2025",
			"gia_tri": "1.000.000", "don_vi": []string{"Công ty A", "Công ty B"}[i%2]})
	}
	if err := RenderForm(f, l, bangKeMapping(), bangKeTypes, recs, map[string]string{"C16": "TNHH ABC"}); err != nil {
		t.Fatal(err)
	}
	// Round-trip through bytes like a download.
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	g, err := excelize.OpenReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	return g
}

func cell(t *testing.T, f *excelize.File, c string) string {
	t.Helper()
	v, _ := f.GetCellValue("BM.PL QTTD KHDN MB 10.04", c)
	return v
}

func calc(t *testing.T, f *excelize.File, c string) string {
	t.Helper()
	v, err := f.CalcCellValue("BM.PL QTTD KHDN MB 10.04", c)
	if err != nil {
		t.Fatalf("calc %s: %v", c, err)
	}
	return v
}

func TestRenderFormFewRows(t *testing.T) {
	f := render(t, 3)
	if cell(t, f, "A5") != "1" || cell(t, f, "B7") != "HD03" || cell(t, f, "B8") != "" {
		t.Fatalf("rows: %q %q %q", cell(t, f, "A5"), cell(t, f, "B7"), cell(t, f, "B8"))
	}
	if cell(t, f, "B14") != "TỔNG" || calc(t, f, "D14") != "3000000" {
		t.Fatalf("total: %q %q", cell(t, f, "B14"), calc(t, f, "D14"))
	}
	if calc(t, f, "F5") != "2000000" || calc(t, f, "F6") != "1000000" {
		t.Fatalf("group sums: %q %q", calc(t, f, "F5"), calc(t, f, "F6"))
	}
	if cell(t, f, "C16") != "CÔNG TY TNHH ABC[3]" || cell(t, f, "G17") != "CHI NHÁNH …" {
		t.Fatalf("placeholders: %q %q", cell(t, f, "C16"), cell(t, f, "G17"))
	}
}

func TestRenderFormInsertsRows(t *testing.T) {
	f := render(t, 12) // 9 empty rows in the form
	if cell(t, f, "B16") != "HD12" || cell(t, f, "B17") != "TỔNG" {
		t.Fatalf("rows: B16=%q B17=%q", cell(t, f, "B16"), cell(t, f, "B17"))
	}
	if calc(t, f, "D17") != "12000000" || calc(t, f, "F16") != "6000000" {
		t.Fatalf("formulas: D17=%q F16=%q", calc(t, f, "D17"), calc(t, f, "F16"))
	}
	if cell(t, f, "C19") != "CÔNG TY TNHH ABC[3]" || cell(t, f, "A31") != "[3] Ký, đóng dấu trong trường hợp Khách hàng lập biểu" {
		t.Fatalf("below the table: C19=%q A31=%q", cell(t, f, "C19"), cell(t, f, "A31"))
	}
	st5, _ := f.GetCellStyle("BM.PL QTTD KHDN MB 10.04", "D5")
	st16, _ := f.GetCellStyle("BM.PL QTTD KHDN MB 10.04", "D16")
	if st5 != st16 {
		t.Fatalf("inserted row style %d, want %d", st16, st5)
	}
}

type fakeCompleter struct{ reply string }

func (c fakeCompleter) CompleteJSON(_ context.Context, _, _ string, out any) error {
	return json.Unmarshal([]byte(c.reply), out)
}

func TestProposeMapping(t *testing.T) {
	l, err := DetectForm(openForm(t))
	if err != nil {
		t.Fatal(err)
	}
	kb := types.KBConfig{DocTypes: []types.DocType{
		{ClassLabel: types.ClassLabel{Name: "hoa_don", Title: "Hoá đơn"}, Fields: []types.SheetField{
			{Key: "so_hoa_don", Label: "Số hoá đơn"}, {Key: "ngay_lap", Label: "Ngày lập", ValueType: "date"},
			{Key: "gia_tri", Label: "Giá trị", ValueType: "money"}, {Key: "don_vi", Label: "Đơn vị bán"}}},
		{ClassLabel: types.ClassLabel{Name: "dkkd", Title: "GCN ĐKKD"}, Fields: []types.SheetField{{Key: "ten", Label: "Tên doanh nghiệp"}}},
	}}
	reply := `{"doc_type":"hoa_don","columns":[
		{"col":"A","kind":"seq"},{"col":"B","kind":"field","key":"so_hoa_don"},{"col":"C","kind":"field","key":"ngay_lap"},
		{"col":"D","kind":"field","key":"gia_tri","total":"SUM(D{first}:D{last})"},
		{"col":"E","kind":"field","new_field":{"label":"Số tiền các NH đã cho vay","value_type":"money","description":"Đóng dấu hoặc ghi trên hoá đơn"},"total":"SUM(E{first}:E{last})"},
		{"col":"F","kind":"formula","formula":"SUMIF($G${first}:$G${last},G{r},$D${first}:$D${last})","total":"SUM(F5:F13)"},
		{"col":"G","kind":"field","key":"khong_co"},{"col":"H","kind":"blank"}],
		"placeholders":[{"cell":"C16","doc_type":"dkkd","key":"ten"},{"cell":"G17"},{"cell":"Z99","text":"x"}]}`
	m, err := ProposeMapping(context.Background(), fakeCompleter{reply}, l, kb)
	if err != nil {
		t.Fatal(err)
	}
	if m.DocType != "hoa_don" || len(m.Columns) != 8 {
		t.Fatalf("mapping: %+v", m)
	}
	if e := m.Columns[4]; e.Kind != types.ExportField || e.Key != "so_tien_cac_nh_da_cho_vay" || e.Total == "" {
		t.Fatalf("new field column: %+v", e)
	}
	if len(m.NewFields) != 1 || m.NewFields[0].Field.ValueType != "money" {
		t.Fatalf("new fields: %+v", m.NewFields)
	}
	if f := m.Columns[5]; f.Kind != types.ExportFormula || f.Total != "" {
		t.Fatalf("a total with a fixed row is dropped: %+v", f)
	}
	if g := m.Columns[6]; g.Kind != types.ExportBlank {
		t.Fatalf("an unknown key is left blank: %+v", g)
	}
	if len(m.Placeholders) != 2 || m.Placeholders[0].Key != "ten" || m.Placeholders[1].Key != "" {
		t.Fatalf("placeholders: %+v", m.Placeholders)
	}
}
