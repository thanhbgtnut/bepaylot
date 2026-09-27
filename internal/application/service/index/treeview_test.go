package index

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/types"
)

// sampleTree is a root with `top` chapters of `sub` sections each.
func sampleTree(top, sub int) []types.TreeNode {
	root := types.TreeNode{ID: uuid.New(), ShortID: "n0", Title: "Báo cáo", PageStart: 1, PageEnd: top * sub}
	nodes := []types.TreeNode{root}
	k := 1
	for i := 0; i < top; i++ {
		ch := types.TreeNode{ID: uuid.New(), ParentID: &root.ID, ShortID: fmt.Sprintf("n%d", k), Ord: i, Level: 1,
			Title: fmt.Sprintf("Chương %d", i+1), PageStart: i*sub + 1, PageEnd: (i + 1) * sub, Summary: "Tóm tắt chương"}
		k++
		nodes = append(nodes, ch)
		for j := 0; j < sub; j++ {
			nodes = append(nodes, types.TreeNode{ID: uuid.New(), ParentID: &ch.ID, ShortID: fmt.Sprintf("n%d", k), Ord: j, Level: 2,
				Title: fmt.Sprintf("Mục %d.%d", i+1, j+1), PageStart: i*sub + j + 1, PageEnd: i*sub + j + 1, Summary: "Tóm tắt mục"})
			k++
		}
	}
	return nodes
}

// N37: a tree within the budget is rendered whole; a bigger one keeps its
// first level and collapses the rest with expand markers.
func TestTreeRenderWholeOrCut(t *testing.T) {
	nodes := sampleTree(3, 4)
	FillTreeTokens(nodes)
	if nodes[0].TreeTokens == 0 || nodes[0].TreeTokens != nodes[1].TreeTokens+nodes[6].TreeTokens+nodes[11].TreeTokens {
		t.Fatalf("root tree tokens = %d", nodes[0].TreeTokens)
	}
	tv := newTreeView(nodes)

	var whole strings.Builder
	if cut := tv.render(&whole, tv.root.ID, "d1.", 100000); cut {
		t.Fatal("a tree within the budget must not be cut")
	}
	if n := strings.Count(whole.String(), "[d1.n"); n != 15 || strings.Contains(whole.String(), "expand") {
		t.Fatalf("whole tree:\n%s", whole.String())
	}
	if !strings.Contains(whole.String(), "  [d1.n2] Mục 1.1 (tr. 1)") {
		t.Fatalf("children must be indented under their chapter:\n%s", whole.String())
	}

	var cut strings.Builder
	if !tv.render(&cut, tv.root.ID, "", 1) {
		t.Fatal("a tree over the budget must report the cut")
	}
	if n := strings.Count(cut.String(), "\n"); n != 3 || !strings.Contains(cut.String(), "[n1] Chương 1 (tr. 1–4) — Tóm tắt chương   (+4 mục, expand n1)") {
		t.Fatalf("cut tree:\n%s", cut.String())
	}

	// A stored tree without tree_tokens gets them computed when read.
	old := sampleTree(3, 4)
	if got := newTreeView(old).treeTokens(old[0].ID); got != nodes[0].TreeTokens {
		t.Fatalf("computed tree tokens = %d, want %d", got, nodes[0].TreeTokens)
	}
}

// The case TOC drops branches, then shortens summaries, to fit its budget;
// an expanded file shows its whole tree.
func TestCaseTOCBudget(t *testing.T) {
	c := types.Case{Code: "RT112233", Title: "Hồ sơ thanh toán"}
	var views []*docView
	for i := 0; i < 3; i++ {
		d := types.Document{ID: uuid.New(), FileName: fmt.Sprintf("f%d.pdf", i+1), PageCount: 12,
			Summary: strings.Repeat("hợp đồng thanh toán ", 40), Metadata: map[string]any{"loai": "HD"}}
		views = append(views, &docView{ref: fmt.Sprintf("d%d", i+1), doc: d, tree: newTreeView(sampleTree(3, 4))})
	}
	views = append(views, &docView{ref: "d4", doc: types.Document{ID: uuid.New(), FileName: "scan.pdf", PageCount: 1}})

	full, cut := tocText(c, views, nil, 100000, 8000)
	if cut || !strings.Contains(full, "[d1.n1] Chương 1 (tr. 1–4) +") || !strings.Contains(full, `{"loai":"HD"}`) ||
		!strings.Contains(full, "[d4] scan.pdf (1 tr.) (chưa có mục lục)") {
		t.Fatalf("full toc:\n%s", full)
	}
	small, cut := tocText(c, views, nil, 200, 8000)
	if !cut || strings.Contains(small, "[d1.n1]") || !strings.Contains(small, "(+15 mục, expand d1)") {
		t.Fatalf("small toc:\n%s", small)
	}
	open, _ := tocText(c, views, map[string]bool{"d2": true}, 100000, 8000)
	if !strings.Contains(open, "  [d2.n2] Mục 1.1 (tr. 1)") || strings.Contains(open, "  [d1.n2]") {
		t.Fatalf("expanded toc:\n%s", open)
	}
}

func TestResolvePick(t *testing.T) {
	v := &docView{ref: "d1", tree: newTreeView(sampleTree(2, 2))}
	byRef := map[string]*docView{"d1": v}
	if p, ok := resolvePick(byRef, "d1"); !ok || p.node != nil {
		t.Fatal("d1 is the whole file")
	}
	if p, ok := resolvePick(byRef, "d1.n2"); !ok || p.node == nil || p.node.Title != "Mục 1.1" {
		t.Fatalf("d1.n2 = %+v", p)
	}
	for _, bad := range []string{"d2", "d1.n99", uuid.NewString(), ""} {
		if _, ok := resolvePick(byRef, bad); ok {
			t.Fatalf("%q must not resolve", bad)
		}
	}
}
