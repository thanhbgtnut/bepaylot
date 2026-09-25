package postgres

import (
	"strings"
	"testing"

	"github.com/thanhenti/bepaylot/internal/types"
)

func TestMetaFilterSQL(t *testing.T) {
	var a sqlArgs
	schema := &types.MetadataSchema{Fields: []types.MetadataField{{Key: "so_tien", Type: "number"}}}
	sql, err := metaFilterSQL("d.metadata", types.MetadataFilter{
		"ma_ho_so":     "HS-2026-000123",
		"loai_giay_to": map[string]any{"in": []any{"GCN_HKD", "CCCD"}},
		"ngay_nop":     map[string]any{"gte": "2026-01-01"},
		"so_tien":      map[string]any{"lt": 5e7},
		"chi_nhanh":    map[string]any{"prefix": "Binh_"},
		"ghi_chu":      map[string]any{"exists": true},
	}, schema, &a)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"d.metadata @> $", "= ANY($", "::date", "::numeric", "LIKE $", "d.metadata ? $",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("sql missing %q:\n%s", want, sql)
		}
	}
	joined := ""
	for _, v := range a.vals {
		if s, ok := v.(string); ok {
			joined += s + "|"
		}
	}
	if !strings.Contains(joined, `{"ma_ho_so":"HS-2026-000123"}`) || !strings.Contains(joined, `Binh\_%`) {
		t.Fatalf("args = %v", a.vals)
	}
}

func TestMetaFilterRejectsBadKeysAndOps(t *testing.T) {
	var a sqlArgs
	if _, err := metaFilterSQL("m", types.MetadataFilter{"bad key'; --": "x"}, nil, &a); err == nil {
		t.Fatal("want error for invalid key")
	}
	if _, err := metaFilterSQL("m", types.MetadataFilter{"k": map[string]any{"regex": ".*"}}, nil, &a); err == nil {
		t.Fatal("want error for unknown operator")
	}
}
