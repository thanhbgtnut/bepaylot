package index

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
)

// node is the in-memory tree used while building (§6.5).
type node struct {
	title     string
	origin    string
	level     int
	pageStart int
	pageEnd   int
	children  []*node
	sections  []int // indexes into the section slice
	summary   string
	tokens    int
	shortID   string
	id        uuid.UUID
}

// maxHeadingWords: a title/heading block longer than this is a paragraph the
// layout model promoted, not a heading.
const maxHeadingWords = 25

// numbered matches a heading that starts its own numbering ("I.", "1.",
// "1.1", "a)"), so it is not the second line of the heading before it.
var numbered = regexp.MustCompile(`^(?i)([IVXLC]+|\d+(\.\d+)*|[a-zđ])[.)]\s`)

// layoutGroup is one heading of a page with the body under it, or the
// page's leading content (no heading) that continues the previous page.
type layoutGroup struct {
	id      string // g1, g2… within the page
	heading string // "" for leading content
	level   int    // 0 for leading content
	body    string
	tokens  int // heading + body
}

func (g layoutGroup) text() string {
	if g.heading == "" {
		return g.body
	}
	return strings.TrimSpace(g.heading + "\n" + g.body)
}

// PageGroups merges a page's layout blocks into groups (§6.5 step 1):
// title/heading blocks open a group and the blocks after them are its body;
// consecutive headings of one level are one heading split over lines
// (unless the first is a label ending in ":" or the second is numbered); a
// heading with less than minTokens under it and another heading after it on
// the page (form labels, captions promoted by the layout) joins the group
// before it. Furniture is skipped. It is pure.
func PageGroups(pg *types.ParsedPage, minTokens int) []layoutGroup {
	runes := []rune(pg.Markdown)
	blocks := append([]types.ParsedBlock(nil), pg.Blocks...)
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].BlockNo < blocks[j].BlockNo })
	var groups []layoutGroup
	for _, b := range blocks {
		if b.IsFurniture || b.MdStart < 0 || b.MdEnd <= b.MdStart || b.MdEnd > len(runes) {
			continue
		}
		switch b.Type {
		case types.BlockHeader, types.BlockFooter, types.BlockPageNumber:
			continue
		}
		md := strings.TrimSpace(string(runes[b.MdStart:b.MdEnd]))
		if md == "" {
			continue
		}
		tok := textutil.EstimateTokens(md)
		if (b.Type == types.BlockTitle || b.Type == types.BlockHeading) && len(strings.Fields(md)) <= maxHeadingWords {
			lvl, title := headingLevel(md, b.Type), cleanHeading(md)
			if n := len(groups); n > 0 && groups[n-1].heading != "" && groups[n-1].level == lvl && groups[n-1].body == "" &&
				!strings.HasSuffix(groups[n-1].heading, ":") && !numbered.MatchString(title) {
				groups[n-1].heading += " " + title
				groups[n-1].tokens += tok
				continue
			}
			groups = append(groups, layoutGroup{heading: title, level: lvl, tokens: tok})
			continue
		}
		if len(groups) == 0 {
			groups = append(groups, layoutGroup{})
		}
		g := &groups[len(groups)-1]
		g.body = strings.TrimSpace(g.body + "\n\n" + md)
		g.tokens += tok
	}
	var out []layoutGroup
	for i, g := range groups {
		if n := len(out); n > 0 && g.heading != "" && g.tokens < minTokens && i+1 < len(groups) && g.level >= out[n-1].level {
			out[n-1].body = strings.TrimSpace(out[n-1].body + "\n\n" + g.text())
			out[n-1].tokens += g.tokens
			continue
		}
		out = append(out, g)
	}
	for i := range out {
		out[i].id = fmt.Sprintf("g%d", i+1)
	}
	return out
}

func cleanHeading(md string) string {
	return textutil.CollapseSpace(stripMD(strings.TrimLeft(md, "# ")))
}

// pageNode is a node that starts on a page.
type pageNode struct {
	title   string
	level   int // 1 = top part of the document
	summary string
	tokens  int
}

// pageResult is what one page adds to the tree: content continuing the node
// open at the end of the previous page, then the nodes starting on it.
type pageResult struct {
	leadTokens  int
	leadSummary string
	nodes       []pageNode
}

// draftResult reads a page's groups as they are: every heading group starts
// a node, summaries are the first words.
func draftResult(groups []layoutGroup, words int) *pageResult {
	r := &pageResult{}
	for _, g := range groups {
		if g.heading == "" {
			r.leadTokens += g.tokens
			r.leadSummary = extractiveSummary(g.body, words)
			continue
		}
		src := g.body
		if src == "" {
			src = g.heading
		}
		r.nodes = append(r.nodes, pageNode{title: textutil.Truncate(g.heading, 120), level: g.level, tokens: g.tokens, summary: extractiveSummary(src, words)})
	}
	return r
}

// bookmark is one PDF outline entry flattened in document order.
type bookmark struct {
	title string
	page  int
	depth int // 1 = top level
}

func flattenBookmarks(bms []types.PDFBookmark, pageCount, depth int) []bookmark {
	var out []bookmark
	for _, b := range bms {
		if b.Page >= 1 && b.Page <= pageCount && strings.TrimSpace(b.Title) != "" {
			out = append(out, bookmark{title: strings.TrimSpace(b.Title), page: b.Page, depth: depth})
		}
		out = append(out, flattenBookmarks(b.Children, pageCount, depth+1)...)
	}
	if depth == 1 {
		sort.SliceStable(out, func(i, j int) bool { return out[i].page < out[j].page })
	}
	return out
}

// BuildTree joins the page results in page order into the document tree
// (§6.5 step 3). A node stays open until a node of the same or a higher
// level starts; leading content of a page extends the open node. PDF
// bookmarks are the top levels; page nodes nest under the deepest open
// bookmark, and a page node repeating a bookmark title on its page is that
// bookmark. firstLines titles a node for content before any heading. It is
// pure.
func BuildTree(pageCount int, bookmarks []types.PDFBookmark, results map[int]*pageResult, firstLines map[int]string, words int) *node {
	pageCount = max(pageCount, 1)
	root := &node{origin: "root", pageStart: 1, pageEnd: pageCount}
	stack := []*node{root}
	last := root
	push := func(n *node) {
		for len(stack) > 1 && stack[len(stack)-1].level >= n.level {
			stack = stack[:len(stack)-1]
		}
		parent := stack[len(stack)-1]
		n.level = max(n.level, parent.level+1)
		parent.children = append(parent.children, n)
		stack = append(stack, n)
		last = n
	}
	bms := flattenBookmarks(bookmarks, pageCount, 1)
	bi := 0
	for p := 1; p <= pageCount; p++ {
		var started []*node
		for bi < len(bms) && bms[bi].page == p {
			n := &node{title: bms[bi].title, origin: "bookmark", level: bms[bi].depth, pageStart: p, pageEnd: p}
			push(n)
			started = append(started, n)
			bi++
		}
		base := 0
		for _, n := range stack {
			if n.origin == "bookmark" {
				base = n.level
			}
		}
		if r := results[p]; r != nil {
			if r.leadTokens > 0 {
				if last == root {
					push(&node{title: pageTitle(firstLines, p), origin: "layout", level: base + 1, pageStart: p, pageEnd: p})
				}
				last.tokens += r.leadTokens
				last.summary = joinSummary(last.summary, r.leadSummary, words)
				for _, n := range stack[1:] {
					n.pageEnd = p
				}
			}
			for _, pn := range r.nodes {
				if b := sameTitle(started, pn.title); b != nil {
					b.tokens += pn.tokens
					b.summary = joinSummary(b.summary, pn.summary, words)
					continue
				}
				push(&node{title: pn.title, origin: "layout", level: base + pn.level, pageStart: p, pageEnd: p, summary: pn.summary, tokens: pn.tokens})
			}
		}
		for _, n := range stack[1:] {
			n.pageEnd = p
		}
	}
	return root
}

// sameTitle returns the bookmark started on the page whose title the page
// node repeats.
func sameTitle(started []*node, title string) *node {
	t := textutil.Normalize(title)
	for _, b := range started {
		if bt := textutil.Normalize(b.title); t != "" && (bt == t || strings.HasPrefix(t, bt) || strings.HasPrefix(bt, t)) {
			return b
		}
	}
	return nil
}

// joinSummary appends the summary of a continuation page; a node spanning
// pages keeps at most twice the summary length.
func joinSummary(a, b string, words int) string {
	switch {
	case strings.TrimSpace(b) == "":
		return a
	case strings.TrimSpace(a) == "":
		return b
	case len(strings.Fields(a)) >= 2*words:
		return a
	}
	return firstWords(a+"; "+b, 2*words)
}

func pageTitle(titles map[int]string, p int) string {
	if t := strings.TrimSpace(titles[p]); t != "" {
		return textutil.Truncate(t, 80)
	}
	return fmt.Sprintf("Trang %d", p)
}

// assignByPage puts each section in the deepest node containing its first page.
func assignByPage(root *node, secs []types.Section) {
	for i, s := range secs {
		n := root
		for {
			var next *node
			for _, c := range n.children {
				if s.PageStart >= c.pageStart && s.PageStart <= c.pageEnd {
					next = c
					break
				}
			}
			if next == nil {
				break
			}
			n = next
		}
		n.sections = append(n.sections, i)
	}
}

// finish sums token counts bottom-up, gives nodes without a summary the
// titles of their children, and sets ids and short ids.
func finish(root *node) *node {
	var fix func(n *node) int
	fix = func(n *node) int {
		for _, c := range n.children {
			n.tokens += fix(c)
		}
		n.pageEnd = max(n.pageEnd, n.pageStart)
		if strings.TrimSpace(n.summary) == "" && len(n.children) > 0 && n.origin != "root" {
			var titles []string
			for _, c := range n.children {
				titles = append(titles, c.title)
			}
			n.summary = textutil.Truncate(strings.Join(titles, "; "), 300)
		}
		n.tokens = max(n.tokens, 1)
		return n.tokens
	}
	fix(root)
	// Breadth-first short ids: n0 is the root (document card).
	queue := []*node{root}
	i := 0
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		n.shortID = fmt.Sprintf("n%d", i)
		n.id = uuid.New()
		i++
		queue = append(queue, n.children...)
	}
	return root
}

func firstWords(s string, n int) string {
	s = strings.NewReplacer("#", "", "|", " ", "*", "", ">", "").Replace(s)
	w := strings.Fields(s)
	if len(w) > n {
		w = w[:n]
	}
	return strings.Join(w, " ")
}

// flatten returns nodes parents-first with parent links, as stored rows.
func flatten(root *node, docID uuid.UUID, gen int, secs []types.Section) []types.TreeNode {
	var out []types.TreeNode
	var walk func(n *node, parent *uuid.UUID, level, ord int)
	walk = func(n *node, parent *uuid.UUID, level, ord int) {
		ids := make([]uuid.UUID, 0, len(n.sections))
		for _, i := range n.sections {
			ids = append(ids, secs[i].ID)
		}
		out = append(out, types.TreeNode{
			ID: n.id, DocumentID: docID, Gen: gen, ParentID: parent, ShortID: n.shortID, Ord: ord, Level: level,
			Title: n.title, Origin: n.origin, PageStart: n.pageStart, PageEnd: n.pageEnd, Summary: n.summary,
			SectionIDs: ids, TokenCount: n.tokens,
		})
		id := n.id
		for i, c := range n.children {
			walk(c, &id, level+1, i)
		}
	}
	walk(root, nil, 0, 0)
	return out
}

func extractiveSummary(text string, words int) string {
	return firstWords(text, words)
}
