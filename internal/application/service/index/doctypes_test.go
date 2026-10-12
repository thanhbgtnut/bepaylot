package index

import (
	"reflect"
	"testing"

	"github.com/thanhenti/bepaylot/internal/types"
)

func TestSegmentsFromPages(t *testing.T) {
	pt := func(n int, label string, conf float64) types.PageDocType {
		return types.PageDocType{PageNo: n, Label: label, Confidence: conf}
	}
	type seg struct {
		a, b            int
		label, proposed string
	}
	cases := []struct {
		name  string
		pages []types.PageDocType
		want  []seg
	}{
		{"không trang nào được phân loại", []types.PageDocType{pt(1, "", 0), pt(2, "", 0)}, nil},
		{"trang liền nhau cùng loại gom lại, đổi loại thì cắt",
			[]types.PageDocType{pt(1, "hop_dong", 0.9), pt(2, "hop_dong", 0.8), pt(3, "hoa_don", 0.95), pt(4, "hop_dong", 0.9)},
			[]seg{{1, 2, "hop_dong", ""}, {3, 3, "hoa_don", ""}, {4, 4, "hop_dong", ""}}},
		{"trang dưới ngưỡng hoặc lỗi nối vào giấy tờ trước",
			[]types.PageDocType{pt(1, "hop_dong", 0.9), pt(2, "hoa_don", 0.3), pt(3, "", 0), pt(4, "hoa_don", 0.9)},
			[]seg{{1, 3, "hop_dong", ""}, {4, 4, "hoa_don", ""}}},
		{"cùng loại liền nhau: trang is_break mở giấy tờ mới, trang tiếp theo thì nối",
			[]types.PageDocType{
				{PageNo: 1, Label: "hop_dong", Confidence: 0.9, Break: true}, pt(2, "hop_dong", 0.9),
				{PageNo: 3, Label: "hop_dong", Confidence: 0.9, Break: true}, {PageNo: 4, Label: "hop_dong", Confidence: 0.9, Break: true},
				{PageNo: 5, Label: "hoa_don", Confidence: 0.3, Break: true}, {PageNo: 6, Label: types.LabelOther, Confidence: 0.9, Break: true},
			},
			[]seg{{1, 2, "hop_dong", ""}, {3, 3, "hop_dong", ""}, {4, 6, "hop_dong", ""}}},
		{"đầu file dưới ngưỡng là unknown",
			[]types.PageDocType{pt(1, "hoa_don", 0.4), pt(2, "", 0), pt(3, "hop_dong", 0.9)},
			[]seg{{1, 2, types.LabelUnknown, "hoa_don"}, {3, 3, "hop_dong", ""}}},
	}
	other := types.LabelOther
	cases = append(cases, struct {
		name  string
		pages []types.PageDocType
		want  []seg
	}{"trang trắng là other; trang other không có tiêu đề nối vào giấy tờ trước; other có tiêu đề thì tách",
		[]types.PageDocType{
			pt(1, "unc", 0.98), {PageNo: 2, Label: "unc", Confidence: 0.9, Blank: true},
			{PageNo: 3, Label: "gcn", Confidence: 0.87, Opens: true}, pt(4, other, 0.74),
			{PageNo: 5, Label: other, Confidence: 0.9, Opens: true}, {PageNo: 6, Blank: true},
			pt(7, other, 0.9), {PageNo: 8, Label: "unc", Confidence: 0.98, Opens: true},
		},
		[]seg{{1, 1, "unc", ""}, {2, 2, other, ""}, {3, 4, "gcn", ""}, {5, 7, other, ""}, {8, 8, "unc", ""}}})
	for _, c := range cases {
		var got []seg
		for _, g := range SegmentsFromPages(c.pages, 0.6) {
			got = append(got, seg{g.PageStart, g.PageEnd, g.Label, g.ProposedLabel})
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	// Confidence is the mean of the confident pages only.
	got := SegmentsFromPages([]types.PageDocType{pt(1, "hop_dong", 0.9), pt(2, "hop_dong", 0.2), pt(3, "hop_dong", 0.7)}, 0.6)
	if len(got) != 1 || got[0].PageEnd != 3 || got[0].Confidence < 0.79 || got[0].Confidence > 0.81 {
		t.Errorf("confidence: got %+v", got)
	}
}

func TestDocSpans(t *testing.T) {
	rule := types.ClassificationRule{Labels: []types.ClassLabel{{Name: "hop_dong", Title: "Hợp đồng"}, {Name: "hoa_don", Title: "Hoá đơn"}}}
	if got := docSpans(nil, 5, rule); got != nil {
		t.Fatalf("no segment must give no span, got %v", got)
	}
	// The user's segment wins over the proposal overlapping it; gaps are unknown.
	got := docSpans([]types.Segment{
		{PageStart: 1, PageEnd: 3, Label: "hop_dong", Source: types.SegmentPipeline},
		{PageStart: 3, PageEnd: 4, Label: "hoa_don", Source: types.SegmentUser},
	}, 6, rule)
	want := []docSpan{
		{label: types.LabelUnknown, title: "Chưa rõ", pageStart: 1, pageEnd: 2},
		{label: "hoa_don", title: "Hoá đơn", pageStart: 3, pageEnd: 4},
		{label: types.LabelUnknown, title: "Chưa rõ", pageStart: 5, pageEnd: 6},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// The documents of a file are the top nodes of its tree: headings nest under
// the document of their page and never run over into the next document.
func TestBuildTreeWithDocSpans(t *testing.T) {
	results := map[int]*pageResult{
		1: {nodes: []pageNode{{title: "HỢP ĐỒNG MUA BÁN", level: 1, tokens: 50}, {title: "Điều 1", level: 2, tokens: 80}}},
		2: {leadTokens: 100, leadSummary: "tiếp điều 1"},
		3: {nodes: []pageNode{{title: "HOÁ ĐƠN GTGT", level: 1, tokens: 60}}},
	}
	spans := []docSpan{{label: "hop_dong", title: "Hợp đồng", pageStart: 1, pageEnd: 2}, {label: "hoa_don", title: "Hoá đơn", pageStart: 3, pageEnd: 3}}
	root := finish(BuildTree(3, nil, results, nil, 60, spans))
	if len(root.children) != 2 {
		t.Fatalf("want 2 document nodes, got %d", len(root.children))
	}
	hd, inv := root.children[0], root.children[1]
	if hd.origin != "doctype" || hd.docType != "hop_dong" || hd.title != "Hợp đồng" || hd.pageStart != 1 || hd.pageEnd != 2 {
		t.Errorf("contract node: %+v", hd)
	}
	if inv.docType != "hoa_don" || inv.pageStart != 3 || inv.pageEnd != 3 {
		t.Errorf("invoice node: %+v", inv)
	}
	if len(hd.children) != 1 || hd.children[0].title != "HỢP ĐỒNG MUA BÁN" || hd.children[0].pageEnd != 2 {
		t.Fatalf("contract headings: %+v", hd.children)
	}
	if d1 := hd.children[0].children; len(d1) != 1 || d1[0].title != "Điều 1" || d1[0].pageEnd != 2 {
		t.Errorf("Điều 1 must stay inside the contract (tr. 1–2): %+v", d1)
	}
	if len(inv.children) != 1 || inv.children[0].title != "HOÁ ĐƠN GTGT" {
		t.Errorf("invoice headings: %+v", inv.children)
	}
	nodes := flatten(root, [16]byte{}, 1, nil)
	if nodes[1].DocType != "hop_dong" || nodes[1].Origin != "doctype" {
		t.Errorf("stored node: %+v", nodes[1])
	}
	if got := nodeTitle(nodes[1], 0); got != "Hợp đồng [loại: hop_dong]" {
		t.Errorf("rendered title: %q", got)
	}
}
