package metadata

import (
	"testing"

	"github.com/thanhenti/bepaylot/internal/types"
)

var schema = &types.MetadataSchema{Fields: []types.MetadataField{
	{Key: "ma_ho_so", Type: "string", Normalize: "upper_trim", Indexed: true},
	{Key: "loai_giay_to", Type: "enum", Values: []string{"GCN_HKD", "BCTC", "CCCD"}},
	{Key: "ngay_nop", Type: "date"},
	{Key: "so_tien", Type: "number"},
	{Key: "chi_nhanh", Type: "string", Required: true},
}}

func TestValidateNormalizes(t *testing.T) {
	out, errs := Validate(schema, map[string]any{
		"ma_ho_so": " hs-2026-000123 ", "loai_giay_to": "gcn_hkd", "ngay_nop": "05/03/2026",
		"so_tien": "50,000,000", "chi_nhanh": "Bình Phước", "extra_note": "ok",
	})
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if out["ma_ho_so"] != "HS-2026-000123" || out["loai_giay_to"] != "GCN_HKD" || out["ngay_nop"] != "2026-03-05" || out["so_tien"] != 5e7 {
		t.Fatalf("out = %v", out)
	}
}

func TestValidateErrors(t *testing.T) {
	_, errs := Validate(schema, map[string]any{"loai_giay_to": "X", "ngay_nop": "tomorrow", "BadKey": 1})
	if len(errs) != 4 { // enum, date, key, required chi_nhanh
		t.Fatalf("errs = %v", errs)
	}
	strict := &types.MetadataSchema{Strict: true}
	if _, errs := Validate(strict, map[string]any{"x": "1"}); len(errs) != 1 {
		t.Fatalf("strict errs = %v", errs)
	}
	if _, errs := Validate(nil, map[string]any{"nested": map[string]any{"a": 1}}); len(errs) != 1 {
		t.Fatalf("nested object should be rejected: %v", errs)
	}
}

func TestMergeAndNormalizeFilter(t *testing.T) {
	m := Merge(map[string]any{"ma_ho_so": "A", "x": 1}, map[string]any{"x": 2})
	if m["ma_ho_so"] != "A" || m["x"] != 2 {
		t.Fatalf("merge = %v", m)
	}
	f := NormalizeFilter(schema, types.MetadataFilter{"ma_ho_so": "hs-2026-000123 ", "ngay_nop": map[string]any{"gte": "01/01/2026"}})
	if f["ma_ho_so"] != "HS-2026-000123" || f["ngay_nop"].(map[string]any)["gte"] != "2026-01-01" {
		t.Fatalf("filter = %v", f)
	}
}
