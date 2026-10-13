package sheets

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/types"
)

// A sub-table of a document type the case does not classify has file rows:
// its fields are stored under "<label>.<key>", so two such sub-tables do not
// share a value on the same file, and the agent is told to find the document.
func TestFileRowOfLabelledTable(t *testing.T) {
	doc, seg := uuid.New(), uuid.New()
	file := types.SheetRow{Table: "hoa_don", DocumentID: doc}
	other := types.SheetRow{Table: "hop_dong", DocumentID: doc}
	split := types.SheetRow{Table: "hoa_don", DocumentID: doc, SegmentID: &seg}
	whole := types.SheetRow{DocumentID: doc}

	if got := fieldKey(file, "so"); got != "hoa_don.so" {
		t.Fatalf("file row key = %q", got)
	}
	if fieldKey(split, "so") != "so" || fieldKey(whole, "so") != "so" {
		t.Fatal("segment and whole-file rows keep the column key")
	}
	if slotOf(file, "so") == slotOf(other, "so") {
		t.Fatal("two labelled sub-tables share a slot on the same file")
	}

	fields := []types.SheetField{{Key: "so", Label: "Số"}}
	items := []promptItem{{ref: "d1", title: "Hoá đơn", file: "a.pdf", pages: "tr. 1–3", fields: fields, row: file}}
	p := sheetPrompt("Bóc tách.", "", items)
	if !strings.Contains(p, "[d1] Hoá đơn (tìm trong file)") || !strings.Contains(p, "- hoa_don.so · Số · string") {
		t.Fatalf("prompt:\n%s", p)
	}
	if strings.Contains(sheetPrompt("Bóc tách.", "", []promptItem{{ref: "d1.s1", title: "Hoá đơn", fields: fields, row: split}}), "tìm trong file") {
		t.Fatal("a reviewed document is not searched for")
	}
	if n := missingNotes("d1 hoa_don.so: file không có hoá đơn", items); n[slotOf(file, "so")] != "file không có hoá đơn" {
		t.Fatalf("notes = %v", n)
	}
}
