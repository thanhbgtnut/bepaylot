package sheets

import (
	"testing"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/types"
)

// A file row of a labelled sub-table (sheets made before 0.25) keeps its
// fields under "<label>.<key>", so two sub-tables do not share a value on the
// same file; a document (segment) row uses the column key.
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
}
