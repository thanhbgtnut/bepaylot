package graph

import (
	"strings"
	"testing"

	"github.com/thanhenti/bepaylot/internal/types"
)

func hkdSchema(t *testing.T) types.GraphSchema {
	t.Helper()
	ss, err := LoadSchemaFiles("../../../../configs/graph_schemas")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range ss {
		if s.Name == "ho_kinh_doanh" {
			return s
		}
	}
	t.Fatal("ho_kinh_doanh schema missing")
	return types.GraphSchema{}
}

func TestSchemasValidate(t *testing.T) {
	g := GenericSchema()
	if err := ValidateSchema(&g); err != nil {
		t.Fatal(err)
	}
	s := hkdSchema(t)
	if s.EntityType("HoKinhDoanh") == nil || s.RelationType("CHU_HO") == nil {
		t.Fatal("schema incomplete")
	}
	bad := s
	bad.Relations = append([]types.RelationTypeDef{}, types.RelationTypeDef{Name: "X", Source: "Nope", Target: "CaNhan"})
	if err := ValidateSchema(&bad); err == nil {
		t.Fatal("unknown relation endpoint must fail")
	}
	if p := ExtractPrompt(s); !strings.Contains(p, "HoKinhDoanh") || !strings.Contains(p, "CHU_HO: CaNhan → HoKinhDoanh") {
		t.Fatalf("prompt = %s", p)
	}
}

func TestValidateExtraction(t *testing.T) {
	s := hkdSchema(t)
	units := map[string]string{
		"u1": "Tên hộ kinh doanh viết bằng tiếng Việt: HỘ KINH DOANH VẬT LIỆU XÂY DỰNG TƯ MƯƠI III\nMã số hộ kinh doanh: 070082001498\nVốn kinh doanh (Bằng số): 50.000.000 đồng",
		"u2": "Họ và tên: NGUYỄN VĂN TÌNH\nSố định danh cá nhân: 070082001498",
	}
	ex := Extraction{
		Entities: []ExtractedEntity{
			{Type: "HoKinhDoanh", Name: "HỘ KINH DOANH VẬT LIỆU XÂY DỰNG TƯ MƯƠI III", Unit: "u1",
				Attributes: map[string]any{"ten": "Vật liệu xây dựng Tư Mươi III", "ma_so": "070082001498", "von_kinh_doanh": "50.000.000 đồng", "bogus": 1},
				Evidence:   "Mã số hộ kinh doanh: 070082001498"},
			{Type: "CaNhan", Name: "NGUYỄN VĂN TÌNH", Unit: "u1", // wrong unit, evidence in u2
				Attributes: map[string]any{"ho_ten": "Nguyễn Văn Tình", "so_dinh_danh": "070082001498"}, Evidence: "Họ và tên: NGUYỄN VĂN TÌNH"},
			{Type: "CaNhan", Name: "Trần Thị B", Attributes: map[string]any{"ho_ten": "Trần Thị B"}, Evidence: "Chủ hộ: Trần Thị B"}, // hallucinated
			{Type: "CaNhan", Name: "No Name Attr", Attributes: map[string]any{}, Evidence: "Họ và tên: NGUYỄN VĂN TÌNH"},             // missing required
			{Type: "Unknown", Name: "x", Evidence: "Mã số"},
		},
		Relations: []ExtractedRelation{
			{Type: "CHU_HO", Source: EntityRef{"CaNhan", "NGUYỄN VĂN TÌNH"}, Target: EntityRef{"HoKinhDoanh", "HỘ KINH DOANH VẬT LIỆU XÂY DỰNG TƯ MƯƠI III"}, Evidence: "Họ và tên: NGUYỄN VĂN TÌNH"},
			{Type: "CHU_HO", Source: EntityRef{"HoKinhDoanh", "a"}, Target: EntityRef{"CaNhan", "b"}, Evidence: "Họ và tên: NGUYỄN VĂN TÌNH"}, // reversed
		},
	}
	out, warns := ValidateExtraction(s, ex, units)
	if len(out.Entities) != 2 || len(out.Relations) != 1 || len(warns) != 4 {
		t.Fatalf("entities %d relations %d warnings %v", len(out.Entities), len(out.Relations), warns)
	}
	hkd := out.Entities[0]
	if hkd.Attributes["von_kinh_doanh"] != 5e7 || hkd.Attributes["bogus"] != nil {
		t.Fatalf("attrs = %v", hkd.Attributes)
	}
	if out.Entities[1].Unit != "u2" {
		t.Fatalf("evidence unit = %s", out.Entities[1].Unit)
	}
}

func TestNormKey(t *testing.T) {
	s := hkdSchema(t)
	p := s.EntityType("CaNhan")
	a := NormKey(p, "Ông NGUYỄN VĂN TÌNH", map[string]any{}, nil)
	b := NormKey(p, "Nguyễn Văn Tình", map[string]any{}, nil)
	if a != b {
		t.Fatalf("%q != %q", a, b)
	}
	withID := NormKey(p, "x", map[string]any{"so_dinh_danh": "0700 82"}, nil)
	if !strings.HasPrefix(withID, "so_dinh_danh=") {
		t.Fatalf("identity key = %q", withID)
	}
	h := s.EntityType("HoKinhDoanh")
	if k := NormKey(h, "A", map[string]any{"ma_so": "1"}, map[string]any{"ma_ho_so": "HS-A"}); !strings.HasPrefix(k, "ma_ho_so=HS-A|") {
		t.Fatalf("scoped key = %q", k)
	}
}
