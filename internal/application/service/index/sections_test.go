package index

import (
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/parser/assemble"
	"github.com/thanhenti/bepaylot/internal/parser/turboocr"
	"github.com/thanhenti/bepaylot/internal/types"
)

func samplePage(t *testing.T) *types.ParsedPage {
	t.Helper()
	body, err := os.ReadFile("../../../parser/assemble/testdata/output_example.json")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := turboocr.Decode(body)
	if err != nil {
		t.Fatal(err)
	}
	p := assemble.Build(raw, assemble.Options{PageNo: 1, ReadingOrderFix: true})
	assemble.Render(p)
	return p
}

func TestBuildSectionsSample(t *testing.T) {
	p := samplePage(t)
	secs := BuildSections(uuid.New(), uuid.New(), 1, []*types.ParsedPage{p}, 1500)
	if len(secs) < 2 {
		t.Fatalf("sections = %d", len(secs))
	}
	var table *types.Section
	for i := range secs {
		if secs[i].Kind == "table" {
			table = &secs[i]
		}
	}
	if table == nil || !strings.Contains(table.Content, "4673 (Chính)") {
		t.Fatalf("no table section: %+v", secs)
	}
	// The title opens a section whose heading path carries it.
	found := false
	for _, s := range secs {
		if len(s.HeadingPath) > 0 && strings.Contains(s.HeadingPath[0], "GIẤY CHỨNG NHẬN") {
			found = true
		}
		if s.LineFrom < 0 && s.Kind != "table" {
			t.Fatalf("section without lines: %+v", s)
		}
		if s.SourceSpans[0].BBox.IsZero() {
			t.Fatal("span without bbox")
		}
	}
	if !found {
		t.Fatal("title not in heading path")
	}
}

func TestBuildSectionsSplitsByTokens(t *testing.T) {
	p := samplePage(t)
	secs := BuildSections(uuid.New(), uuid.New(), 1, []*types.ParsedPage{p}, 40)
	big := BuildSections(uuid.New(), uuid.New(), 1, []*types.ParsedPage{p}, 5000)
	if len(secs) <= len(big) {
		t.Fatalf("small budget should give more sections: %d vs %d", len(secs), len(big))
	}
	for i, s := range secs {
		if s.Seq != i {
			t.Fatalf("seq %d at %d", s.Seq, i)
		}
	}
}

func TestSplitLongTableRepeatsHeader(t *testing.T) {
	md := "| A | B |\n|---|---|"
	for i := 0; i < 200; i++ {
		md += "\n| row | value |"
	}
	parts := splitTable(piece{md: md, tokens: 99999}, 100)
	if len(parts) < 3 {
		t.Fatalf("parts = %d", len(parts))
	}
	for _, p := range parts {
		if !strings.HasPrefix(p.md, "| A | B |\n|---|---|\n| row") {
			t.Fatalf("part without header: %q", p.md[:30])
		}
	}
}
