package index

import (
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
)

// node is the in-memory tree used while building (§6.4).
type node struct {
	title     string
	origin    string
	pageStart int
	pageEnd   int
	children  []*node
	sections  []int // indexes into the section slice
	summary   string
	tokens    int
	shortID   string
	id        uuid.UUID
}

// SkeletonOptions tunes skeleton selection.
type SkeletonOptions struct {
	FlatMaxPages int
	// GroupPages is the size of page groups for long documents without any
	// structure (and no LLM grouping).
	GroupPages int
}

// BuildSkeleton picks the best structure available: PDF bookmarks, then
// headings, then one node per page (short documents), then page groups.
func BuildSkeleton(pageCount int, bookmarks []types.PDFBookmark, secs []types.Section, pageTitles map[int]string, opt SkeletonOptions) (*node, string) {
	if opt.GroupPages <= 0 {
		opt.GroupPages = 10
	}
	root := &node{title: "", origin: "root", pageStart: 1, pageEnd: max(pageCount, 1)}
	if children := fromBookmarks(bookmarks, 1, pageCount); countNodes(children) >= 2 {
		root.children = children
		assignByPage(root, secs)
		return finish(root, secs), "bookmark"
	}
	if children := fromHeadings(secs); countNodes(children) >= 2 {
		root.children = children
		return finish(root, secs), "heading"
	}
	if pageCount <= opt.FlatMaxPages {
		for p := 1; p <= pageCount; p++ {
			root.children = append(root.children, &node{title: pageTitle(pageTitles, p), origin: "page", pageStart: p, pageEnd: p})
		}
	} else {
		for p := 1; p <= pageCount; p += opt.GroupPages {
			end := min(p+opt.GroupPages-1, pageCount)
			root.children = append(root.children, &node{title: fmt.Sprintf("Trang %d–%d: %s", p, end, pageTitle(pageTitles, p)), origin: "page", pageStart: p, pageEnd: end})
		}
	}
	assignByPage(root, secs)
	return finish(root, secs), "page"
}

// FromTOC builds children from an LLM-proposed table of contents (title +
// start page), clamped and ordered, ends derived from the next start.
func FromTOC(entries []TOCEntry, pageCount int) []*node {
	var clean []TOCEntry
	for _, e := range entries {
		if e.PageStart >= 1 && e.PageStart <= pageCount && strings.TrimSpace(e.Title) != "" {
			clean = append(clean, e)
		}
	}
	sort.SliceStable(clean, func(i, j int) bool { return clean[i].PageStart < clean[j].PageStart })
	var out []*node
	for i, e := range clean {
		if i > 0 && e.PageStart == clean[i-1].PageStart {
			continue
		}
		end := pageCount
		for j := i + 1; j < len(clean); j++ {
			if clean[j].PageStart > e.PageStart {
				end = clean[j].PageStart - 1
				break
			}
		}
		out = append(out, &node{title: strings.TrimSpace(e.Title), origin: "llm", pageStart: e.PageStart, pageEnd: end})
	}
	if len(out) > 0 && out[0].pageStart > 1 {
		out = append([]*node{{title: "Phần đầu", origin: "llm", pageStart: 1, pageEnd: out[0].pageStart - 1}}, out...)
	}
	return out
}

// TOCEntry is one LLM-proposed table-of-contents entry.
type TOCEntry struct {
	Title     string `json:"title"`
	PageStart int    `json:"page_start"`
}

func pageTitle(titles map[int]string, p int) string {
	if t := strings.TrimSpace(titles[p]); t != "" {
		return textutil.Truncate(t, 80)
	}
	return fmt.Sprintf("Trang %d", p)
}

func fromBookmarks(bms []types.PDFBookmark, start, end int) []*node {
	var valid []types.PDFBookmark
	for _, b := range bms {
		if b.Page >= start && b.Page <= end && strings.TrimSpace(b.Title) != "" {
			valid = append(valid, b)
		}
	}
	sort.SliceStable(valid, func(i, j int) bool { return valid[i].Page < valid[j].Page })
	var out []*node
	for i, b := range valid {
		e := end
		if i+1 < len(valid) {
			e = max(b.Page, valid[i+1].Page-1)
		}
		n := &node{title: strings.TrimSpace(b.Title), origin: "bookmark", pageStart: b.Page, pageEnd: e}
		n.children = fromBookmarks(b.Children, b.Page, e)
		out = append(out, n)
	}
	if len(out) > 0 && out[0].pageStart > start {
		out = append([]*node{{title: "Phần đầu", origin: "bookmark", pageStart: start, pageEnd: out[0].pageStart - 1}}, out...)
	}
	return out
}

// fromHeadings turns section heading paths into a hierarchy. Sections before
// the first heading form a leading node titled by their first words.
func fromHeadings(secs []types.Section) []*node {
	root := &node{}
	for i, s := range secs {
		cur := root
		path := s.HeadingPath
		if len(path) == 0 {
			// Leading content without heading.
			if len(cur.children) == 0 || cur.children[0].origin != "lead" {
				lead := &node{title: textutil.Truncate(firstWords(s.Content, 12), 80), origin: "lead", pageStart: s.PageStart, pageEnd: s.PageEnd}
				cur.children = append([]*node{lead}, cur.children...)
			}
			cur.children[0].sections = append(cur.children[0].sections, i)
			continue
		}
		for _, h := range path {
			var next *node
			if n := len(cur.children); n > 0 && cur.children[n-1].title == h && cur.children[n-1].origin == "heading" {
				next = cur.children[n-1]
			}
			if next == nil {
				next = &node{title: h, origin: "heading", pageStart: s.PageStart, pageEnd: s.PageEnd}
				cur.children = append(cur.children, next)
			}
			cur = next
		}
		cur.sections = append(cur.sections, i)
	}
	return root.children
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

// finish fixes page ranges bottom-up, computes token counts and short ids.
func finish(root *node, secs []types.Section) *node {
	var fix func(n *node) (int, int, int)
	fix = func(n *node) (int, int, int) {
		lo, hi, tok := n.pageStart, n.pageEnd, 0
		for _, i := range n.sections {
			lo, hi = min(lo, secs[i].PageStart), max(hi, secs[i].PageEnd)
			tok += secs[i].TokenCount
		}
		for _, c := range n.children {
			a, b, t := fix(c)
			lo, hi = min(lo, a), max(hi, b)
			tok += t
		}
		// Ranges from bookmarks, pages or an LLM table of contents are
		// authoritative; heading trees take their range from their sections.
		if declaredRange(n.origin) {
			n.tokens = max(tok, 1)
			return n.pageStart, n.pageEnd, tok
		}
		if lo > 0 {
			n.pageStart = lo
		}
		n.pageEnd = max(hi, n.pageStart)
		n.tokens = tok
		return n.pageStart, n.pageEnd, tok
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

func declaredRange(origin string) bool {
	return origin == "bookmark" || origin == "page" || origin == "llm"
}

func countNodes(ns []*node) int {
	c := 0
	for _, n := range ns {
		c += 1 + countNodes(n.children)
	}
	return c
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

// nodeText concatenates a node's own sections (not descendants) up to budget
// tokens, used to summarize leaves.
func nodeText(n *node, secs []types.Section, budget int) string {
	var sb strings.Builder
	used := 0
	for _, i := range n.sections {
		t := secs[i].Content
		tk := secs[i].TokenCount
		if used+tk > budget {
			r := []rune(t)
			keep := max(0, (budget-used)*3)
			if keep < len(r) {
				t = string(r[:keep])
			}
			sb.WriteString(t)
			break
		}
		sb.WriteString(t)
		sb.WriteString("\n\n")
		used += tk
	}
	return sb.String()
}

func extractiveSummary(text string, words int) string {
	return firstWords(text, words)
}
