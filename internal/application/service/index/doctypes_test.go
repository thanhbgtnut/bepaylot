package index

import (
	"reflect"
	"testing"

	"github.com/thanhenti/bepaylot/internal/types"
)

func TestDocSpans(t *testing.T) {
	rule := types.ClassificationRule{Labels: []types.ClassLabel{{Name: "hop_dong", Title: "Hợp đồng"}, {Name: "hoa_don", Title: "Hoá đơn"}}}
	if got := docSpans(nil, 5, nil, rule); got != nil {
		t.Fatalf("no segment must give no span, got %v", got)
	}
	// The user's segment wins over the proposal overlapping it; gaps are unknown.
	got := docSpans([]types.Segment{
		{PageStart: 1, PageEnd: 3, Label: "hop_dong", Source: types.SegmentPipeline},
		{PageStart: 3, PageEnd: 4, Label: "hoa_don", Source: types.SegmentUser},
	}, 6, nil, rule)
	want := []docSpan{
		{label: types.LabelUnknown, title: "Chưa rõ", pageStart: 1, pageEnd: 2},
		{label: "hoa_don", title: "Hoá đơn", pageStart: 3, pageEnd: 4},
		{label: types.LabelUnknown, title: "Chưa rõ", pageStart: 5, pageEnd: 6},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// Pages not picked for processing (U111) are their own spans, apart from
// the unknown pages next to them.
func TestDocSpansSkippedPages(t *testing.T) {
	rule := types.ClassificationRule{Labels: []types.ClassLabel{{Name: "hoa_don", Title: "Hoá đơn"}}}
	got := docSpans([]types.Segment{{PageStart: 1, PageEnd: 2, Label: "hoa_don", Source: types.SegmentPipeline}}, 7,
		map[int]bool{4: true, 5: true, 7: true}, rule)
	want := []docSpan{
		{label: "hoa_don", title: "Hoá đơn", pageStart: 1, pageEnd: 2},
		{label: types.LabelUnknown, title: "Chưa rõ", pageStart: 3, pageEnd: 3},
		{label: types.LabelNotProcessed, title: "Chưa xử lý", pageStart: 4, pageEnd: 5},
		{label: types.LabelUnknown, title: "Chưa rõ", pageStart: 6, pageEnd: 6},
		{label: types.LabelNotProcessed, title: "Chưa xử lý", pageStart: 7, pageEnd: 7},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// A gap of pages not picked (U111) ends a document: the pages after it are
// grouped on their own, and a page without is_first_page after the gap
// starts a document.
func TestSegmentsFromPagesGap(t *testing.T) {
	got := SegmentsFromPages([]types.PageDocType{
		{PageNo: 1, Label: "hop_dong", Confidence: 0.9, FirstPage: true},
		{PageNo: 2, Label: "hop_dong", Confidence: 0.9},
		{PageNo: 6, Label: "hop_dong", Confidence: 0.9},
		{PageNo: 7, Label: "hoa_don", Confidence: 0.9, FirstPage: true},
	}, 0.5)
	want := []types.Segment{
		{PageStart: 1, PageEnd: 2, Label: "hop_dong", Confidence: 0.9},
		{PageStart: 6, PageEnd: 6, Label: "hop_dong", Confidence: 0.9},
		{PageStart: 7, PageEnd: 7, Label: "hoa_don", Confidence: 0.9},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if !uncovered([]types.PageDocType{{PageNo: 3, Label: "hoa_don"}}, want) || uncovered([]types.PageDocType{{PageNo: 2, Label: "hop_dong"}, {PageNo: 4}}, want) {
		t.Fatal("uncovered: a classified page outside every segment must count, an unclassified one must not")
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
