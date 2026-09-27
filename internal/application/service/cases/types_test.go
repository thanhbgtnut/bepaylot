package cases

import (
	"testing"

	"github.com/thanhenti/bepaylot/internal/types"
)

func TestLoadTypesAndCodes(t *testing.T) {
	ts, err := LoadTypes("../../../../configs/case_types", "default")
	if err != nil {
		t.Fatal(err)
	}
	tt, ok := ts["thanh_toan"]
	if !ok || ts["default"].Name != "default" || ts["tin_dung_dn"].Name == "" {
		t.Fatalf("types = %v", ts)
	}
	code := NormalizeCode(tt.Code, " rt112233 ")
	if code != "RT112233" || CheckCode(tt.Code, code) != nil {
		t.Fatalf("code = %q", code)
	}
	if CheckCode(tt.Code, NormalizeCode(tt.Code, "RT11")) == nil {
		t.Fatal("a code outside the pattern must be refused")
	}
	if tt.Title == "" || tt.MetadataSchema == nil {
		t.Fatalf("type = %+v", tt)
	}
	if NormalizeCode(types.CaseCodeRule{}, "  ab c ") != "ab c" {
		t.Fatal("default normalization trims only")
	}
	if CheckCode(types.CaseCodeRule{}, "") == nil || CheckCode(types.CaseCodeRule{}, "a\x00b") == nil {
		t.Fatal("empty or control-character codes must be refused")
	}
	if err := ValidateType(types.CaseType{Name: "Bad Name"}); err == nil {
		t.Fatal("bad type name accepted")
	}
}
