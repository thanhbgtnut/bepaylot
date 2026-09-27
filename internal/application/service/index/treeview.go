package index

import (
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
)

// treeView is a stored document tree ready to render for the LLM (§6.5).
type treeView struct {
	root     types.TreeNode
	byShort  map[string]types.TreeNode
	byID     map[uuid.UUID]types.TreeNode
	children map[uuid.UUID][]types.TreeNode
	tokens   map[uuid.UUID]int // subtree tokens computed for old trees
}

func newTreeView(nodes []types.TreeNode) *treeView {
	t := &treeView{byShort: map[string]types.TreeNode{}, byID: map[uuid.UUID]types.TreeNode{},
		children: map[uuid.UUID][]types.TreeNode{}, tokens: map[uuid.UUID]int{}}
	for _, n := range nodes {
		t.byShort[n.ShortID], t.byID[n.ID] = n, n
		if n.ParentID == nil {
			t.root = n
		} else {
			t.children[*n.ParentID] = append(t.children[*n.ParentID], n)
		}
	}
	for k := range t.children {
		c := t.children[k]
		sort.Slice(c, func(i, j int) bool { return c[i].Ord < c[j].Ord })
	}
	return t
}

// nodeLineTokens estimates one rendered TOC line.
func nodeLineTokens(n types.TreeNode) int {
	return textutil.EstimateTokens(n.Title+" "+textutil.Truncate(n.Summary, 300)) + 10
}

// FillTreeTokens sets TreeTokens on every node: the tokens of its subtree
// rendered as a table of contents (§6.5 step 7). The root counts its
// children only (its line is the document card).
func FillTreeTokens(nodes []types.TreeNode) {
	kids := map[uuid.UUID][]int{}
	for i, n := range nodes {
		if n.ParentID != nil {
			kids[*n.ParentID] = append(kids[*n.ParentID], i)
		}
	}
	var sum func(i int) int
	sum = func(i int) int {
		total := 0
		if nodes[i].ParentID != nil {
			total = nodeLineTokens(nodes[i])
		}
		for _, k := range kids[nodes[i].ID] {
			total += sum(k)
		}
		nodes[i].TreeTokens = total
		return total
	}
	for i, n := range nodes {
		if n.ParentID == nil {
			sum(i)
		}
	}
}

// treeTokens returns the subtree tokens of id, computing them for trees
// stored before tree_tokens was recorded.
func (t *treeView) treeTokens(id uuid.UUID) int {
	if n, ok := t.byID[id]; ok && n.TreeTokens > 0 {
		return n.TreeTokens
	}
	if v, ok := t.tokens[id]; ok {
		return v
	}
	total := 0
	if n := t.byID[id]; n.ParentID != nil {
		total = nodeLineTokens(n)
	}
	for _, c := range t.children[id] {
		total += t.treeTokens(c.ID)
	}
	t.tokens[id] = total
	return total
}

// view picks the nodes under top to show: whole levels, breadth-first,
// while they fit budget; the first level is always shown. A tree within the
// budget is thus shown whole, a bigger one is cut from its deepest levels.
func (t *treeView) view(top uuid.UUID, budget int) map[uuid.UUID]bool {
	shown := map[uuid.UUID]bool{}
	level := t.children[top]
	used := 0
	for len(level) > 0 {
		cost := 0
		for _, n := range level {
			cost += nodeLineTokens(n)
		}
		if used > 0 && used+cost > budget {
			break
		}
		used += cost
		var next []types.TreeNode
		for _, n := range level {
			shown[n.ID] = true
			next = append(next, t.children[n.ID]...)
		}
		level = next
	}
	return shown
}

// descendants counts the nodes under id.
func (t *treeView) descendants(id uuid.UUID) int {
	n := 0
	for _, c := range t.children[id] {
		n += 1 + t.descendants(c.ID)
	}
	return n
}

// render writes the subtree of top, one node per line
// "[<prefix>n<k>] title (tr. a–b) — summary", indented by depth. Collapsed
// nodes end with "(+k mục, expand <prefix>n<k>)". It reports whether
// anything was cut.
func (t *treeView) render(sb *strings.Builder, top uuid.UUID, prefix string, budget int) bool {
	shown := t.view(top, budget)
	cut := false
	var walk func(parent uuid.UUID, depth int)
	walk = func(parent uuid.UUID, depth int) {
		for _, n := range t.children[parent] {
			if !shown[n.ID] {
				continue
			}
			fmt.Fprintf(sb, "%s[%s%s] %s (tr. %s)", strings.Repeat("  ", depth), prefix, n.ShortID, n.Title, pageRange(n.PageStart, n.PageEnd))
			if s := strings.TrimSpace(n.Summary); s != "" {
				sb.WriteString(" — " + textutil.Truncate(textutil.CollapseSpace(s), 300))
			}
			if kids := t.children[n.ID]; len(kids) > 0 && !shown[kids[0].ID] {
				fmt.Fprintf(sb, "   (+%d mục, expand %s%s)", t.descendants(n.ID), prefix, n.ShortID)
				cut = true
			}
			sb.WriteString("\n")
			walk(n.ID, depth+1)
		}
	}
	walk(top, 0)
	return cut
}

// pageRange formats "a–b", or "a" for one page.
func pageRange(a, b int) string {
	if a == b {
		return fmt.Sprint(a)
	}
	return fmt.Sprintf("%d–%d", a, b)
}

func (t *treeView) path(n types.TreeNode) []string {
	var p []string
	for n.ParentID != nil {
		p = append([]string{n.Title}, p...)
		n = t.byID[*n.ParentID]
	}
	return p
}

// pathForPage returns the path of the deepest node containing page.
func (t *treeView) pathForPage(page int) []string {
	n := t.root
	for {
		var next *types.TreeNode
		for _, c := range t.children[n.ID] {
			if page >= c.PageStart && page <= c.PageEnd {
				c := c
				next = &c
				break
			}
		}
		if next == nil {
			return t.path(n)
		}
		n = *next
	}
}
