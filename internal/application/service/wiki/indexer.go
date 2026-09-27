package wiki

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
)

// indexLine is one entry of the index before rendering. The index is read
// by every query, so a line is only its id, title and one-line summary: the
// id is resolved to the page by the server, the slug is never printed.
type indexLine struct {
	id       string
	group    string
	head     string // "[w2] file.pdf (18 tr.)", "[w5] Công ty A"
	summary  string
	branches []string // "[w2.n5] Điều 7. Thanh toán (tr. 8–9)"
}

// buildIndex renders the index of a case wiki by code (§6.6): pages grouped
// by kind, source pages with the first branches of their file tree, and a
// temporary line for every searchable file not in the wiki yet. only, when
// set, keeps source lines of those files and other pages citing one of them.
func (s *Service) buildIndex(ctx context.Context, w *postgres.WikiRepo, c types.Case, only map[uuid.UUID]bool) (*types.WikiIndex, error) {
	pages, err := w.Pages(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	docs, err := s.st.Documents.ByCase(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	docByID := map[uuid.UUID]types.Document{}
	var searchable []types.Document
	for _, d := range docs {
		if types.Searchable(d.Status) {
			docByID[d.ID] = d
			searchable = append(searchable, d)
		}
	}
	var cites map[uuid.UUID][]types.WikiFootnote
	if only != nil {
		ids := make([]uuid.UUID, len(pages))
		for i, p := range pages {
			ids[i] = p.ID
		}
		if cites, err = w.Footnotes(ctx, ids); err != nil {
			return nil, err
		}
	}
	keep := func(p types.WikiPage) bool {
		if only == nil {
			return true
		}
		if p.Kind == types.WikiKindSource {
			return p.DocumentID != nil && only[*p.DocumentID]
		}
		for _, f := range cites[p.ID] {
			if only[f.DocumentID] {
				return true
			}
		}
		return false
	}
	schema := s.schemaFor(ctx, c)

	idx := &types.WikiIndex{CaseID: c.ID, Version: c.WikiVersion, Refs: map[string]types.WikiRef{}, DocGens: map[string]int{}}
	var lines []indexLine
	n := 0
	next := func() string { n++; return fmt.Sprintf("w%d", n) }
	addPage := func(p types.WikiPage, group string) {
		id := next()
		pid := p.ID
		ref := types.WikiRef{PageID: &pid, Slug: p.Slug, Kind: p.Kind}
		l := indexLine{id: id, group: group, head: fmt.Sprintf("[%s] %s", id, textutil.Truncate(p.Title, 100)), summary: p.Summary}
		if p.Kind == types.WikiKindSource && p.DocumentID != nil {
			doc := *p.DocumentID
			ref.DocumentID = &doc
			if d, ok := docByID[doc]; ok {
				l.head = fmt.Sprintf("[%s] %s (%d tr.)", id, d.FileName, d.PageCount)
				l.branches = s.branches(ctx, idx, id, d)
				idx.DocGens[doc.String()] = d.Gen
			}
		}
		idx.Refs[id] = ref
		lines = append(lines, l)
	}
	covered := map[uuid.UUID]bool{}
	byKind := map[string][]types.WikiPage{}
	for _, p := range pages {
		if p.Kind == types.WikiKindSource && p.DocumentID != nil {
			if _, live := docByID[*p.DocumentID]; !live {
				continue // file deleted or reparsing: its retract/ingest is queued
			}
			covered[*p.DocumentID] = true
		}
		if keep(p) {
			byKind[p.Kind] = append(byKind[p.Kind], p)
		}
	}
	for _, p := range byKind[types.WikiKindOverview] {
		addPage(p, "Tổng quan")
	}
	sources := byKind[types.WikiKindSource]
	order := map[uuid.UUID]int{}
	for i, d := range searchable {
		order[d.ID] = i
	}
	sort.SliceStable(sources, func(i, j int) bool { return order[*sources[i].DocumentID] < order[*sources[j].DocumentID] })
	for _, p := range sources {
		addPage(p, "Nguồn")
	}
	for _, d := range searchable {
		if covered[d.ID] || (only != nil && !only[d.ID]) {
			continue
		}
		id := next()
		doc := d.ID
		idx.Refs[id] = types.WikiRef{DocumentID: &doc, Kind: "pending"}
		idx.DocGens[doc.String()] = d.Gen
		lines = append(lines, indexLine{id: id, group: "Nguồn", head: fmt.Sprintf("[%s] (chưa vào wiki) %s (%d tr.)", id, d.FileName, d.PageCount),
			summary: textutil.CollapseSpace(d.Summary), branches: s.branches(ctx, idx, id, d)})
	}
	groups := []string{}
	entityTitle := map[string]string{}
	for _, et := range schema.EntityTypes {
		title := et.Title
		if title == "" {
			title = et.Name
		}
		entityTitle[et.Name] = title
		groups = append(groups, et.Name)
	}
	var unknown []string
	for _, p := range byKind[types.WikiKindEntity] {
		if _, ok := entityTitle[p.EntityType]; !ok && !contains(unknown, p.EntityType) {
			unknown = append(unknown, p.EntityType)
			entityTitle[p.EntityType] = p.EntityType
		}
	}
	for _, g := range append(groups, unknown...) {
		for _, p := range byKind[types.WikiKindEntity] {
			if p.EntityType == g {
				addPage(p, entityTitle[g])
			}
		}
	}
	for _, p := range byKind[types.WikiKindTopic] {
		addPage(p, "Chủ đề")
	}
	for _, p := range byKind[types.WikiKindNote] {
		addPage(p, "Ghi chú")
	}

	header := fmt.Sprintf("<wiki case=%q title=%q files=\"%d\" pages=\"%d\" version=\"%d\">\n", c.Code, s.cases.CaseType(c.CaseType).Title,
		len(searchable), len(pages), c.WikiVersion)
	if c.WikiVersion == 0 && len(pages) == 0 {
		header = fmt.Sprintf("<wiki case=%q files=\"%d\" status=\"chưa có wiki: chỉ có thẻ tài liệu và mục lục\">\n", c.Code, len(searchable))
	}
	budget := max(s.cfg.Wiki.Index.MaxTokens, 500)
	for stage := 0; stage <= 3; stage++ {
		idx.Content = header + render(lines, stage) + "</wiki>"
		idx.TokenCount = textutil.EstimateTokens(idx.Content)
		if idx.TokenCount <= budget {
			break
		}
	}
	for _, l := range lines {
		if len(l.branches) > 0 && idx.TokenCount > budget {
			r := idx.Refs[l.id]
			r.Collapsed = true
			idx.Refs[l.id] = r
		}
	}
	return idx, nil
}

// render prints the index lines; stage 1 drops tree branches, stage 2
// shortens summaries, stage 3 keeps titles only (§6.6 budget).
func render(lines []indexLine, stage int) string {
	var sb strings.Builder
	group := ""
	for _, l := range lines {
		if l.group != group {
			group = l.group
			fmt.Fprintf(&sb, "## %s\n", group)
		}
		sb.WriteString(l.head)
		switch {
		case stage >= 3:
		case stage == 2:
			fmt.Fprintf(&sb, " — %s", textutil.Truncate(l.summary, 80))
		default:
			if l.summary != "" {
				fmt.Fprintf(&sb, " — %s", textutil.Truncate(l.summary, 300))
			}
		}
		if len(l.branches) > 0 {
			if stage >= 1 {
				sb.WriteString(" +")
			} else {
				sb.WriteString("\n  " + strings.Join(l.branches, "   "))
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// branches renders the first level of a file's tree as index sub-entries and
// registers their refs.
func (s *Service) branches(ctx context.Context, idx *types.WikiIndex, id string, d types.Document) []string {
	nodes, err := s.secs.Tree(ctx, d.ID, d.Gen)
	if err != nil || len(nodes) == 0 {
		return nil
	}
	var root uuid.UUID
	for _, n := range nodes {
		if n.ParentID == nil {
			root = n.ID
		}
	}
	var out []string
	doc := d.ID
	for _, n := range nodes {
		if n.ParentID == nil || *n.ParentID != root {
			continue
		}
		sid := id + "." + n.ShortID
		idx.Refs[sid] = types.WikiRef{DocumentID: &doc, NodeID: n.ShortID}
		out = append(out, fmt.Sprintf("[%s] %s (tr. %s)", sid, textutil.Truncate(n.Title, 80), pageRange(n.PageStart, n.PageEnd)))
		if len(out) >= 12 {
			break
		}
	}
	if len(out) <= 1 {
		return nil // a single branch says nothing the file line does not
	}
	return out
}

func pageRange(a, b int) string {
	if a == b {
		return fmt.Sprint(a)
	}
	return fmt.Sprintf("%d–%d", a, b)
}

// storeIndex rebuilds and stores the index at the case's current version.
func (s *Service) storeIndex(ctx context.Context, w *postgres.WikiRepo, c types.Case) error {
	idx, err := s.buildIndex(ctx, w, c, nil)
	if err != nil {
		return err
	}
	return w.SaveIndex(ctx, *idx)
}

// IndexView implements interfaces.WikiReader: the stored index when it still
// describes the searchable files, else one built on the fly.
func (s *Service) IndexView(ctx context.Context, caseID uuid.UUID, docs []uuid.UUID) (*types.WikiIndex, error) {
	c, err := s.cases.GetCase(ctx, caseID)
	if err != nil {
		return nil, ErrNotFound
	}
	if docs == nil {
		if stored, err := s.st.Wiki.LatestIndex(ctx, caseID); err == nil && stored.Version == c.WikiVersion && s.fresh(ctx, caseID, stored) {
			return stored, nil
		} else if err != nil && !errors.Is(err, postgres.ErrNotFound) {
			return nil, err
		}
		return s.buildIndex(ctx, s.st.Wiki, c, nil)
	}
	only := map[uuid.UUID]bool{}
	for _, d := range docs {
		only[d] = true
	}
	return s.buildIndex(ctx, s.st.Wiki, c, only)
}

// fresh reports whether a stored index lists exactly the searchable files
// of the case at their current generations.
func (s *Service) fresh(ctx context.Context, caseID uuid.UUID, idx *types.WikiIndex) bool {
	docs, err := s.st.Documents.ByCase(ctx, caseID)
	if err != nil {
		return false
	}
	n := 0
	for _, d := range docs {
		if !types.Searchable(d.Status) {
			continue
		}
		n++
		if g, ok := idx.DocGens[d.ID.String()]; !ok || g != d.Gen {
			return false
		}
	}
	return n == len(idx.DocGens)
}

// Expand implements interfaces.WikiReader: the branches of an index entry
// that were cut, rendered as further w<n>.n<k> entries.
func (s *Service) Expand(ctx context.Context, caseID uuid.UUID, ref string) (string, error) {
	view, err := s.IndexView(ctx, caseID, nil)
	if err != nil {
		return "", err
	}
	base, node, _ := strings.Cut(ref, ".")
	r, ok := view.Refs[base]
	if !ok || r.DocumentID == nil {
		return "", nil
	}
	d, err := s.docs.GetDocument(ctx, *r.DocumentID)
	if err != nil || d.CaseID != caseID {
		return "", nil
	}
	nodes, err := s.secs.Tree(ctx, d.ID, d.Gen)
	if err != nil {
		return "", err
	}
	children := map[uuid.UUID][]types.TreeNode{}
	var top *types.TreeNode
	for i, n := range nodes {
		if n.ParentID == nil {
			if node == "" {
				top = &nodes[i]
			}
		} else {
			children[*n.ParentID] = append(children[*n.ParentID], n)
		}
		if node != "" && n.ShortID == node {
			top = &nodes[i]
		}
	}
	if top == nil {
		return "", nil
	}
	for k := range children {
		c := children[k]
		sort.Slice(c, func(i, j int) bool { return c[i].Ord < c[j].Ord })
	}
	var sb strings.Builder
	var walk func(id uuid.UUID, depth int)
	walk = func(id uuid.UUID, depth int) {
		for _, n := range children[id] {
			fmt.Fprintf(&sb, "%s[%s.%s] %s (tr. %s) — %s\n", strings.Repeat("  ", depth), base, n.ShortID, textutil.Truncate(n.Title, 100),
				pageRange(n.PageStart, n.PageEnd), textutil.Truncate(n.Summary, 160))
			if depth < 2 {
				walk(n.ID, depth+1)
			}
		}
	}
	walk(top.ID, 0)
	return sb.String(), nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
