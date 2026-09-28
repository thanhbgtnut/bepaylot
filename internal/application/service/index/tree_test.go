package index

import (
	"strings"
	"testing"

	"github.com/thanhenti/bepaylot/internal/types"
)

// page builds a parsed page from (type, markdown) blocks.
func page(no int, blocks ...[2]string) *types.ParsedPage {
	p := &types.ParsedPage{PageNo: no}
	var md strings.Builder
	for i, b := range blocks {
		start := len([]rune(md.String()))
		md.WriteString(b[1])
		p.Blocks = append(p.Blocks, types.ParsedBlock{BlockNo: i, Type: types.BlockType(b[0]), MdStart: start, MdEnd: len([]rune(md.String()))})
		md.WriteString("\n\n")
	}
	p.Markdown = md.String()
	return p
}

var longBody = strings.Repeat("Nội dung chi tiết của mục này gồm nhiều câu. ", 12)

func TestPageGroupsMergesLayout(t *testing.T) {
	p := page(1,
		[2]string{"paragraph", "Phần tiếp của trang trước."},
		[2]string{"title", "BÁO CÁO"},
		[2]string{"title", "TÀI CHÍNH"}, // same heading over two lines
		[2]string{"paragraph", longBody},
		[2]string{"header", "Công ty X"},      // furniture class
		[2]string{"heading", "## Họ và tên:"}, // label with nothing under it
		[2]string{"heading", "## 1. Tài sản"},
		[2]string{"paragraph", longBody},
	)
	gs := PageGroups(p, 40)
	if len(gs) != 3 {
		t.Fatalf("groups = %+v", gs)
	}
	if gs[0].heading != "" || gs[0].id != "g1" {
		t.Fatalf("lead = %+v", gs[0])
	}
	if gs[1].heading != "BÁO CÁO TÀI CHÍNH" || !strings.Contains(gs[1].body, "Họ và tên") || strings.Contains(gs[1].body, "Công ty X") {
		t.Fatalf("title group = %+v", gs[1])
	}
	if gs[2].heading != "1. Tài sản" || gs[2].level != 2 {
		t.Fatalf("heading group = %+v", gs[2])
	}
}

func TestBuildTreeJoinsPages(t *testing.T) {
	results := map[int]*pageResult{
		1: {nodes: []pageNode{{title: "I. Thông tin chung", level: 1, summary: "Công ty X", tokens: 100}}},
		2: {leadTokens: 50, leadSummary: "MST 0101", nodes: []pageNode{{title: "II. Tài sản", level: 1, summary: "Tổng tài sản", tokens: 80}}},
		3: {nodes: []pageNode{{title: "1. Ngắn hạn", level: 2, summary: "Tiền", tokens: 60}}},
		4: {leadTokens: 30, leadSummary: "Phải thu"},
		5: {nodes: []pageNode{{title: "III. Nguồn vốn", level: 1, summary: "Vốn", tokens: 40}}},
	}
	root := finish(BuildTree(5, nil, results, nil, 60))
	if len(root.children) != 3 {
		t.Fatalf("top nodes = %d", len(root.children))
	}
	info, assets, capital := root.children[0], root.children[1], root.children[2]
	if info.pageStart != 1 || info.pageEnd != 2 || info.tokens != 150 || info.summary != "Công ty X; MST 0101" {
		t.Fatalf("continued node = %+v", info)
	}
	if assets.pageStart != 2 || assets.pageEnd != 4 || len(assets.children) != 1 || assets.tokens != 170 {
		t.Fatalf("parent = %+v", assets)
	}
	if c := assets.children[0]; c.pageStart != 3 || c.pageEnd != 4 || c.summary != "Tiền; Phải thu" {
		t.Fatalf("child = %+v", c)
	}
	if capital.pageStart != 5 || capital.pageEnd != 5 || root.shortID != "n0" || capital.shortID != "n3" {
		t.Fatalf("last = %+v", capital)
	}
}

func TestBuildTreeUnderBookmarks(t *testing.T) {
	bms := []types.PDFBookmark{{Title: "THÔNG TIN CHUNG", Page: 1}, {Title: "VỐN", Page: 2}}
	results := map[int]*pageResult{
		1: {nodes: []pageNode{{title: "Thông tin chung", level: 1, summary: "Hộ kinh doanh A", tokens: 20}, {title: "Chủ hộ", level: 1, summary: "Ông B", tokens: 10}}},
		2: {leadTokens: 15, leadSummary: "50 triệu"},
	}
	root := finish(BuildTree(2, bms, results, nil, 60))
	if len(root.children) != 2 {
		t.Fatalf("top = %+v", root.children)
	}
	info, capital := root.children[0], root.children[1]
	if info.origin != "bookmark" || info.summary != "Hộ kinh doanh A" || len(info.children) != 1 || info.children[0].title != "Chủ hộ" {
		t.Fatalf("bookmark 1 = %+v", info)
	}
	if capital.origin != "bookmark" || capital.summary != "50 triệu" || capital.pageEnd != 2 {
		t.Fatalf("bookmark 2 = %+v", capital)
	}
}

func TestBuildTreeLeadWithoutHeading(t *testing.T) {
	results := map[int]*pageResult{1: {leadTokens: 10, leadSummary: "Giấy chứng nhận"}}
	root := finish(BuildTree(1, nil, results, map[int]string{1: "GIẤY CHỨNG NHẬN"}, 60))
	if len(root.children) != 1 || root.children[0].title != "GIẤY CHỨNG NHẬN" || root.children[0].summary != "Giấy chứng nhận" {
		t.Fatalf("tree = %+v", root.children)
	}
}

func TestPageReplyResult(t *testing.T) {
	gs := []layoutGroup{{id: "g1", body: "tiếp", tokens: 5}, {id: "g2", heading: "Nhãn", level: 2, tokens: 3}, {id: "g3", heading: "A", level: 1, body: "x", tokens: 7}}
	var r pageReply
	r.Lead = "phần trước"
	r.Nodes = append(r.Nodes, struct {
		From    string `json:"from"`
		Title   string `json:"title"`
		Level   int    `json:"level"`
		Summary string `json:"summary"`
	}{From: "g3", Title: "A", Level: 1, Summary: "tóm tắt"})
	res, ok := r.result(gs, 60)
	if !ok || res.leadTokens != 8 || res.leadSummary != "phần trước" || len(res.nodes) != 1 || res.nodes[0].tokens != 7 {
		t.Fatalf("result = %+v ok %v", res, ok)
	}
	r.Nodes[0].From = "g9"
	if _, ok := r.result(gs, 60); ok {
		t.Fatal("unknown group accepted")
	}
}
