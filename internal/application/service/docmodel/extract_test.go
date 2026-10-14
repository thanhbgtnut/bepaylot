package docmodel

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/types"
)

func line(page, no int, text string) postgres.PageLine {
	return postgres.PageLine{PageNo: page, ParsedLine: types.ParsedLine{LineNo: no, Text: text}}
}

// The agent reads each page as numbered lines and as markdown; a page that
// has only markdown (the VLM read it without OCR text) is still sent.
func TestExtractPromptHasLinesAndMarkdown(t *testing.T) {
	table := types.SheetTable{Label: "hoa_don", Title: "Hoá đơn"}
	fields := []types.SheetField{{Key: "tong_tien", Label: "Tổng thanh toán", ValueType: "money"}}
	lines := []postgres.PageLine{line(1, 1, "HOA DON"), line(1, 2, "Tong thanh toan 550.000.000")}
	md := map[int]string{1: "# HOA DON\n\n| Tong thanh toan | 550.000.000 |", 2: "Ky ten: Nguyen Van A"}
	got := extractPrompt(table, fields, lines, md)
	want := "<page n=\"1\">\n<lines>\n[L1] HOA DON\n[L2] Tong thanh toan 550.000.000\n</lines>\n" +
		"<markdown>\n# HOA DON\n\n| Tong thanh toan | 550.000.000 |\n</markdown>\n</page>\n" +
		"<page n=\"2\">\n<markdown>\nKy ten: Nguyen Van A\n</markdown>\n</page>\n"
	if !strings.Contains(got, want) {
		t.Fatalf("prompt:\n%s", got)
	}
}

// A value cited by line keeps the line as evidence; a value found only in the
// markdown of its page is matched against it and cites the page.
func TestExtractedFieldsEvidence(t *testing.T) {
	d := types.Document{ID: uuid.New(), FileName: "a.pdf"}
	g := types.Segment{ID: uuid.New()}
	asked := []types.SheetField{{Key: "so"}, {Key: "tong"}, {Key: "mst"}}
	lines := []postgres.PageLine{line(1, 3, "So hoa don: 0001234")}
	md := map[int]string{2: "| Tong | 550.000.000 |"}
	reply := `{"fields":[{"key":"so","value":"0001234","confidence":0.9,"page":1,"line":3},` +
		`{"key":"tong","value":"550000000","confidence":0.8,"page":2,"line":null}]}`
	ws := extractedFields(d, g, asked, lines, md, reply)
	if len(ws) != 3 {
		t.Fatalf("fields = %+v", ws)
	}
	if so := ws[0]; !so.ValueMatched || len(so.Evidence) != 1 || so.Evidence[0].LineFrom != 3 {
		t.Fatalf("line field = %+v", so)
	}
	if tong := ws[1]; !tong.ValueMatched || len(tong.Evidence) != 1 || tong.Evidence[0].PageNo != 2 || tong.Evidence[0].LineFrom != 0 {
		t.Fatalf("markdown field = %+v", tong)
	}
	if mst := ws[2]; mst.ValueText != "" || mst.Note == "" {
		t.Fatalf("missing field = %+v", mst)
	}
	if extractedFields(d, g, asked, lines, md, "JSON hợp lệ.") != nil {
		t.Fatal("a reply without JSON stored fields")
	}
}
