package wiki

import (
	"strings"
	"testing"

	"github.com/thanhenti/bepaylot/internal/types"
)

func TestSchemaFilesAndGeneric(t *testing.T) {
	g := GenericSchema()
	if err := ValidateSchema(&g); err != nil {
		t.Fatal(err)
	}
	files, err := LoadSchemaFiles("../../../../configs/wiki_schemas")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]types.WikiSchema{}
	for _, s := range files {
		names[s.Name] = s
	}
	tt, ok := names["thanh_toan"]
	if !ok || names["ho_kinh_doanh"].Name == "" {
		t.Fatalf("schemas = %v", names)
	}
	rel := tt.Relation("chu_tai_khoan")
	if rel == nil || !rel.From.Has("ca_nhan") || !rel.To.Has("tai_khoan") {
		t.Fatalf("relation from/to lists: %+v", rel)
	}
	bad := types.WikiSchema{Name: "x", Version: 1, EntityTypes: []types.WikiEntityType{{Name: "a", Identity: []string{"missing"}}}}
	if err := ValidateSchema(&bad); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("identity outside attributes: %v", err)
	}
	bad = types.WikiSchema{Name: "x", Version: 1, EntityTypes: []types.WikiEntityType{{Name: "a"}},
		Relations: []types.WikiRelationType{{Name: "r", From: types.StringList{"a"}, To: types.StringList{"ghost"}}}}
	if err := ValidateSchema(&bad); err == nil {
		t.Fatal("relation to an unknown entity type must fail")
	}
}

func TestIdentityKeyAndNames(t *testing.T) {
	def := &types.WikiEntityType{Name: "to_chuc", Identity: []string{"ma_so_thue"}}
	if k := IdentityKey(def, map[string]any{"ma_so_thue": " 0101-234.567 "}); k != "0101234567" {
		t.Fatalf("identity key = %q", k)
	}
	if IdentityKey(def, map[string]any{}) != "" {
		t.Fatal("missing identity must give an empty key")
	}
	if NormName("Công ty TNHH Xây dựng Ánh Dương") != "xay dung anh duong" {
		t.Fatalf("norm name = %q", NormName("Công ty TNHH Xây dựng Ánh Dương"))
	}
	if Slugify("Công ty A") != "cong-ty-a" {
		t.Fatal(Slugify("Công ty A"))
	}
}

func TestDropFootnotesAndCitations(t *testing.T) {
	in := "Giá trị 5,2 tỷ[^1]. Bên B là Công ty X[^2]. Không nguồn.\n| a | b[^2] |\n| c | d[^1] |\n- Mục[^2]"
	out := dropFootnotes(in, map[int]bool{2: true})
	if strings.Contains(out, "Công ty X") || strings.Contains(out, "[^2]") || strings.Contains(out, "| a |") || strings.Contains(out, "Mục") {
		t.Fatalf("dropped content kept:\n%s", out)
	}
	if !strings.Contains(out, "Giá trị 5,2 tỷ[^1]") || !strings.Contains(out, "| c | d[^1] |") || !strings.Contains(out, "Không nguồn") {
		t.Fatalf("valid content lost:\n%s", out)
	}
	page := map[int]string{3: "Mã số thuế: 0101234567", 4: "Địa chỉ: Hà Nội"}
	if from, to, ok := matchQuote(page, []int{9}, "Mã số thuế: 0101234567", 0.8); !ok || from != 3 || to != 3 {
		t.Fatalf("quote with wrong line not relocated: %d %d %v", from, to, ok)
	}
	if _, _, ok := matchQuote(page, []int{3}, "Mã số thuế: 9999999999", 0.95); ok {
		t.Fatal("a fabricated quote must not match")
	}
	cleaned, n := sanitizeMermaid("a\n```mermaid\nflowchart LR\n A-->B\n```\nb\n```mermaid\nnot a diagram [\n```\n")
	if n != 1 || !strings.Contains(cleaned, "flowchart") || strings.Contains(cleaned, "not a diagram") {
		t.Fatalf("mermaid sanitize: %d\n%s", n, cleaned)
	}
	if got := wikiLinks("xem [[to-chuc/cong-ty-a]] và [[nguon/hd|hợp đồng]]"); len(got) != 2 || got[1] != "nguon/hd" {
		t.Fatalf("links = %v", got)
	}
}
