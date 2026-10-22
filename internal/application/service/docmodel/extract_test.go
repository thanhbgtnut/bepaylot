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

// The agent reads each page's markdown first (main source), then its TurboOCR
// lines with their confidence as reference: a line the VLM rewrote shows its
// OCR text, a line the VLM made or read below minConf is left out; a page
// with only markdown is still sent.
func TestExtractPromptMarkdownThenOCR(t *testing.T) {
	table := types.SheetTable{Label: "hoa_don", Title: "Hoá đơn"}
	fields := []types.SheetField{{Key: "tong_tien", Label: "Tổng thanh toán", ValueType: "money"}}
	title := line(1, 1, "HOA DON")
	title.Confidence, title.BBox = 0.98, types.BBox{X0: 100, Y0: 50, X1: 1100, Y1: 150}
	rewritten := line(1, 2, "Tổng thanh toán 550.000.000")
	rewritten.TextOCR, rewritten.TextSource, rewritten.Confidence = "Tong thanh toan 550.000.000", types.TextSourceVLM, 0.9
	poor := line(1, 3, "Ngày surta thhnng am")
	poor.Confidence = 0.3
	made := line(2, 1, "Ký tên: Nguyễn Văn A")
	made.TextSource = types.TextSourceVLM
	lines := []postgres.PageLine{title, rewritten, poor, made}
	md := map[int]string{1: "# HOA DON\n\n| Tổng thanh toán | 550.000.000 |", 2: "Ký tên: Nguyễn Văn A"}
	size := map[int][2]int{1: {2000, 1000}} // page 2 size unknown, L2 has no box: no coordinates
	got := extractPrompt(table, fields, lines, md, size, 0.6)
	want := "<page n=\"1\">\n<markdown>\n# HOA DON\n\n| Tổng thanh toán | 550.000.000 |\n</markdown>\n" +
		"<ocr>\n[L1] (0.98) @50,50,550,150 HOA DON\n[L2] (0.90) Tong thanh toan 550.000.000\n</ocr>\n</page>\n" +
		"<page n=\"2\">\n<markdown>\nKý tên: Nguyễn Văn A\n</markdown>\n</page>\n"
	if !strings.Contains(got, want) || strings.Contains(got, "surta") {
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
