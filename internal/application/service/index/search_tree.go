package index

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/application/service/metadata"
	"github.com/thanhenti/bepaylot/internal/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
)

// docView is one document of a case as the LLM sees it: its short ref
// (d<n>) and its tree (nil when the tree is not built yet).
type docView struct {
	ref  string
	doc  types.Document
	tree *treeView
}

// fullTokens is the size of the whole tree of the document.
func (v *docView) fullTokens() int {
	if v.tree == nil {
		return 0
	}
	return v.tree.treeTokens(v.tree.root.ID)
}

// caseRefs numbers the documents of a case by their branch in the case
// tree: d<n> is the n-th file added. Filters and other cases never change
// it, and a new file never renames the others.
func (s *Service) caseRefs(ctx context.Context, caseID uuid.UUID) (map[uuid.UUID]int, error) {
	ids, err := s.st.Index.CaseTreeDocs(ctx, caseID)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]int, len(ids))
	for i, id := range ids {
		out[id] = i + 1
	}
	return out, nil
}

// CaseRefs implements interfaces.Searcher: the refs d<n> of a case's
// documents (§6.5 step 8).
func (s *Service) CaseRefs(ctx context.Context, owner, caseID uuid.UUID) (map[string]uuid.UUID, error) {
	if _, err := s.cases.GetCaseOwned(ctx, owner, caseID); err != nil {
		return nil, ErrNotFound
	}
	refs, err := s.caseRefs(ctx, caseID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]uuid.UUID, len(refs))
	for id, n := range refs {
		out[fmt.Sprintf("d%d", n)] = id
	}
	return out, nil
}

// loadViews loads the trees of docs (all of one case) in case tree order,
// named by their case refs d<n>; files not in the case tree yet come last,
// by upload time, numbered after the others.
func (s *Service) loadViews(ctx context.Context, docs []types.Document) ([]*docView, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	refs, err := s.caseRefs(ctx, docs[0].CaseID)
	if err != nil {
		return nil, err
	}
	docs = append([]types.Document(nil), docs...)
	sort.SliceStable(docs, func(i, j int) bool {
		a, aok := refs[docs[i].ID]
		b, bok := refs[docs[j].ID]
		if aok != bok {
			return aok
		}
		if aok {
			return a < b
		}
		return docs[i].CreatedAt.Before(docs[j].CreatedAt)
	})
	next := len(refs)
	out := make([]*docView, len(docs))
	for i, d := range docs {
		n, ok := refs[d.ID]
		if !ok {
			next++
			n = next
		}
		v := &docView{ref: fmt.Sprintf("d%d", n), doc: d}
		nodes, err := s.st.Index.Tree(ctx, d.ID, d.Gen)
		if err != nil {
			return nil, err
		}
		if len(nodes) > 0 {
			v.tree = newTreeView(nodes)
		}
		out[i] = v
	}
	return out, nil
}

// cardLine renders the catalogue line of a document in a case TOC.
func cardLine(v *docView, summaryWords int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "[%s] %s (%d tr.)", v.ref, v.doc.FileName, v.doc.PageCount)
	if len(v.doc.Metadata) > 0 {
		b, _ := json.Marshal(v.doc.Metadata)
		fmt.Fprintf(&sb, " %s", b)
	}
	sum := textutil.CollapseSpace(v.doc.Summary)
	if summaryWords > 0 && sum != "" {
		sb.WriteString(" — " + firstWords(sum, summaryWords))
	}
	if v.tree == nil {
		sb.WriteString(" (chưa có mục lục)")
	}
	return sb.String()
}

// tocText renders the case table of contents within budget (§6.6 step 2):
// cards plus first branches; above budget branches go first, then summaries
// get shorter, then only names remain. Documents in expand are shown with
// their whole tree (within treeBudget) instead.
func tocText(c types.Case, views []*docView, expand map[string]bool, budget, treeBudget int) (string, bool) {
	header := fmt.Sprintf("<case code=%q title=%q files=\"%d\">\n", c.Code, c.Title, len(views))
	type stage struct {
		branches     bool
		summaryWords int
	}
	stages := []stage{{true, 120}, {false, 120}, {false, 40}, {false, 0}}
	var text string
	cut := false
	for i, st := range stages {
		var sb strings.Builder
		sb.WriteString(header)
		cut = i > 0
		for _, v := range views {
			sb.WriteString(cardLine(v, st.summaryWords) + "\n")
			if v.tree == nil {
				continue
			}
			switch {
			case expand[v.ref]:
				v.tree.render(&sb, v.tree.root.ID, v.ref+".", treeBudget)
			case st.branches:
				writeBranches(&sb, v)
			case len(v.tree.children[v.tree.root.ID]) > 0:
				fmt.Fprintf(&sb, "  (+%d mục, expand %s)\n", v.tree.descendants(v.tree.root.ID), v.ref)
			}
		}
		sb.WriteString("</case>\n")
		text = sb.String()
		if textutil.EstimateTokens(text) <= budget {
			break
		}
	}
	return text, cut
}

// writeBranches writes the first-level nodes of a document on one line each
// pair, without summaries (the card already summarizes the file).
func writeBranches(sb *strings.Builder, v *docView) {
	kids := v.tree.children[v.tree.root.ID]
	if len(kids) == 0 {
		return
	}
	parts := make([]string, len(kids))
	for i, n := range kids {
		parts[i] = fmt.Sprintf("[%s.%s] %s (tr. %s)", v.ref, n.ShortID, textutil.Truncate(n.Title, 80), pageRange(n.PageStart, n.PageEnd))
		if len(v.tree.children[n.ID]) > 0 {
			parts[i] += " +"
		}
	}
	for i := 0; i < len(parts); i += 2 {
		sb.WriteString("  " + strings.Join(parts[i:min(i+2, len(parts))], "   ") + "\n")
	}
}

// reasoning runs §6.6 steps 2–5 for every case in scope and merges the hits.
// Every step only sees the documents that passed step 1 (cands): a reference
// the LLM makes to anything else is dropped.
func (s *Service) reasoning(ctx context.Context, req types.SearchRequest, cands []types.Document, resp *types.SearchResponse) error {
	cfg := s.cfg.Search
	b := &budget{}
	b.max.Store(int64(cfg.MaxLLMCalls))
	defer func() {
		resp.Trace.LLMCalls = int(min(b.used.Load(), b.max.Load()))
		resp.Trace.TokensIn = int(b.tokens.Load())
	}()

	byCase := map[uuid.UUID][]types.Document{}
	var order []uuid.UUID
	for _, d := range cands {
		if _, ok := byCase[d.CaseID]; !ok {
			order = append(order, d.CaseID)
		}
		byCase[d.CaseID] = append(byCase[d.CaseID], d)
	}
	views := map[uuid.UUID][]*docView{}
	cases := map[uuid.UUID]types.Case{}
	total := 0
	for _, c := range order {
		vs, err := s.loadViews(ctx, byCase[c])
		if err != nil {
			return err
		}
		views[c] = vs
		if cs, err := s.cases.GetCase(ctx, c); err == nil {
			cases[c] = cs
		}
		txt, _ := tocText(cases[c], vs, nil, cfg.CaseTOCBudget, cfg.TreeTokenBudget)
		total += textutil.EstimateTokens(txt)
	}
	if len(order) > 1 && total > cfg.MapTokenBudget {
		picked, err := s.selectCases(ctx, b, req.Query, order, cases, views)
		if err != nil {
			return err
		}
		if len(picked) == 0 {
			resp.Trace.Fallback = "no_case_selected"
			hits, err := s.keyword(ctx, req, cands, req.TopK)
			resp.Hits = hits
			return err
		}
		order = picked
	}
	resp.Trace.Cases = order
	resp.Trace.SelectedNodes = map[string][]string{}
	resp.Trace.PagesRead = map[string][]int{}

	var mu sync.Mutex
	var all []types.SearchHit
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(max(1, cfg.ParallelDocs))
	for _, c := range order {
		c := c
		g.Go(func() error {
			hits, err := s.searchCase(gctx, b, req, cases[c], views[c], resp, &mu)
			if err != nil {
				return err
			}
			mu.Lock()
			all = append(all, hits...)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Relevance > all[j].Relevance })
	if len(all) > req.TopK {
		all = all[:req.TopK]
	}
	resp.Hits = all
	return nil
}

// selectCases asks the LLM which cases to search when the combined TOC of a
// multi-case (kb_ids) request is too large (§6.6 step 2).
func (s *Service) selectCases(ctx context.Context, b *budget, query string, order []uuid.UUID, cases map[uuid.UUID]types.Case,
	views map[uuid.UUID][]*docView) ([]uuid.UUID, error) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Question: %s\n\nCases:\n", query)
	short := map[string]uuid.UUID{}
	for i, c := range order {
		id := fmt.Sprintf("c%d", i+1)
		short[id] = c
		cs := cases[c]
		code := firstNonEmpty(cs.Code, c.String())
		if cs.Title != "" {
			code += " (" + cs.Title + ")"
		}
		var titles []string
		for _, v := range views[c] {
			if len(titles) == 5 {
				titles = append(titles, "…")
				break
			}
			titles = append(titles, textutil.Truncate(firstNonEmpty(v.doc.Title, v.doc.FileName), 80))
		}
		fmt.Fprintf(&sb, "[%s] %s — %d files: %s", id, code, len(views[c]), strings.Join(titles, "; "))
		if len(cs.Metadata) > 0 {
			m, _ := json.Marshal(cs.Metadata)
			fmt.Fprintf(&sb, " %s", m)
		}
		sb.WriteString("\n")
	}
	var out struct {
		Select []struct {
			Case string `json:"case"`
		} `json:"select"`
	}
	if err := s.call(ctx, b, fmt.Sprintf(promptSelectCases, s.cfg.Search.MaxDocsSelected), sb.String(), &out); err != nil {
		return nil, err
	}
	var picked []uuid.UUID
	seen := map[uuid.UUID]bool{}
	for _, x := range out.Select {
		if c, ok := short[strings.TrimSpace(x.Case)]; ok && !seen[c] {
			seen[c] = true
			picked = append(picked, c)
		}
	}
	return picked, nil
}

// pick is one selection of step 2: a whole document or one of its nodes.
type pick struct {
	view *docView
	node *types.TreeNode // nil = the whole document
}

// searchCase runs steps 2–5 inside one case.
func (s *Service) searchCase(ctx context.Context, b *budget, req types.SearchRequest, c types.Case, views []*docView,
	resp *types.SearchResponse, mu *sync.Mutex) ([]types.SearchHit, error) {
	cfg := s.cfg.Search
	byRef := map[string]*docView{}
	fullTotal := 0
	for _, v := range views {
		byRef[v.ref] = v
		fullTotal += v.fullTokens()
	}

	// Step 2: the case TOC, or every tree whole when they fit together.
	var sb strings.Builder
	fmt.Fprintf(&sb, "Question: %s\n\n", req.Query)
	expanded := map[string]bool{}
	if fullTotal <= cfg.TreeTokenBudget {
		all := map[string]bool{}
		for _, v := range views {
			all[v.ref] = true
			expanded[v.ref] = true
		}
		txt, _ := tocText(c, views, all, 1<<30, cfg.TreeTokenBudget)
		sb.WriteString(txt)
	} else {
		txt, _ := tocText(c, views, nil, cfg.CaseTOCBudget, cfg.TreeTokenBudget)
		sb.WriteString(txt)
	}
	sb.WriteString(s.keywordHints(ctx, req, views))

	system := fmt.Sprintf(promptTreeSearch, cfg.MaxDocsSelected)
	var picks []pick
	pickedKey := map[string]bool{}
	readPages := map[string]map[int]bool{}
	hops := 0
	var hits []types.SearchHit
	var kwDocs []types.Document
	retried := false
	for {
		var out struct {
			Select []struct {
				Node string `json:"node"`
			} `json:"select"`
			Expand []string `json:"expand"`
		}
		err := s.call(ctx, b, system, sb.String(), &out)
		if errors.Is(err, errBudget) {
			break
		}
		if err != nil {
			return nil, err
		}
		// Resolve the selection; nodes too big to read are expanded instead.
		var toExpand []string
		for _, x := range out.Select {
			p, ok := resolvePick(byRef, strings.TrimSpace(x.Node))
			if !ok {
				continue
			}
			key := p.view.ref
			if p.node != nil {
				key += "." + p.node.ShortID
			}
			if pickedKey[key] {
				continue
			}
			// A node too big to read is expanded first, once; if its
			// children are already shown (or hops are spent) its pages are
			// read within the page budget.
			if tooBig(p, cfg.NodeReadBudget, cfg.FullDocTokenBudget) && hops < cfg.MaxHops && !expanded[key] {
				toExpand = append(toExpand, key)
				continue
			}
			pickedKey[key] = true
			picks = append(picks, p)
		}
		toExpand = append(toExpand, out.Expand...)
		more := s.expandText(byRef, toExpand, expanded, cfg.TreeTokenBudget)
		if more != "" && hops < cfg.MaxHops {
			hops++
			sb.WriteString("\nExpanded:\n" + more)
			mu.Lock()
			resp.Trace.Expanded = append(resp.Trace.Expanded, toExpand...)
			mu.Unlock()
			continue
		}

		// Step 3–5: read the pages of the picks and locate the lines.
		sels, noTree := s.pagesOf(req, picks, readPages)
		kwDocs = append(kwDocs, noTree...)
		picks = nil
		found, dropped, truncated, err := s.locate(ctx, b, req, sels)
		if err != nil {
			return nil, err
		}
		hits = append(hits, found...)
		mu.Lock()
		resp.Trace.DroppedHits += dropped
		resp.Trace.Truncated = resp.Trace.Truncated || truncated
		for _, sel := range sels {
			k := sel.doc.ID.String()
			resp.Trace.SelectedDocs = append(resp.Trace.SelectedDocs, sel.doc.ID)
			resp.Trace.SelectedNodes[k] = append(resp.Trace.SelectedNodes[k], sel.nodes...)
			resp.Trace.PagesRead[k] = append(resp.Trace.PagesRead[k], sel.pages...)
		}
		mu.Unlock()
		// Nothing found in the pages read: walk the tree once more (§6.6
		// step 4), telling the LLM what was already read.
		if len(found) > 0 || len(sels) == 0 || retried || hops >= cfg.MaxHops {
			break
		}
		retried = true
		hops++
		sb.WriteString("\nAlready read without an answer: " + readSummary(views, readPages) + ". Choose other nodes or expand.\n")
	}

	if len(kwDocs) > 0 {
		kw, err := s.keyword(ctx, req, kwDocs, req.TopK)
		if err != nil {
			return nil, err
		}
		hits = append(hits, kw...)
	}
	if len(hits) == 0 && len(pickedKey) == 0 {
		// Nothing was chosen on the trees: fall back to full-text over the
		// whole scope, so no file is left out.
		mu.Lock()
		if resp.Trace.Fallback == "" {
			resp.Trace.Fallback = "nothing_selected"
		}
		mu.Unlock()
		docs := make([]types.Document, len(views))
		for i, v := range views {
			docs[i] = v.doc
		}
		return s.keyword(ctx, req, docs, req.TopK)
	}
	return dedupHits(hits), nil
}

// resolvePick maps "d<n>" or "d<n>.n<k>" to a document or node in scope.
func resolvePick(byRef map[string]*docView, ref string) (pick, bool) {
	docRef, nodeRef, hasNode := strings.Cut(ref, ".")
	v, ok := byRef[docRef]
	if !ok {
		return pick{}, false
	}
	if !hasNode || v.tree == nil {
		return pick{view: v}, true
	}
	n, ok := v.tree.byShort[nodeRef]
	if !ok {
		return pick{}, false
	}
	if n.ParentID == nil {
		return pick{view: v}, true
	}
	return pick{view: v, node: &n}, true
}

// tooBig reports a pick whose pages exceed the read budget and that has
// children to choose from instead (§6.6 step 3).
func tooBig(p pick, nodeBudget, fullBudget int) bool {
	if p.view.tree == nil {
		return false
	}
	if p.node == nil {
		root := p.view.tree.root
		return root.TokenCount > fullBudget && len(p.view.tree.children[root.ID]) > 0
	}
	return p.node.TokenCount > nodeBudget && len(p.view.tree.children[p.node.ID]) > 0
}

// expandText renders the trees (or subtrees) named by refs that were not
// shown whole yet.
func (s *Service) expandText(byRef map[string]*docView, refs []string, expanded map[string]bool, treeBudget int) string {
	var sb strings.Builder
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if expanded[ref] {
			continue
		}
		p, ok := resolvePick(byRef, ref)
		if !ok || p.view.tree == nil {
			continue
		}
		expanded[ref] = true
		top := p.view.tree.root.ID
		if p.node != nil {
			top = p.node.ID
			if len(p.view.tree.children[top]) == 0 {
				continue
			}
			fmt.Fprintf(&sb, "<tree doc=%q node=%q>\n", p.view.ref, p.node.ShortID)
		} else {
			fmt.Fprintf(&sb, "<tree doc=%q file=%q pages=\"%d\">\n", p.view.ref, p.view.doc.FileName, p.view.doc.PageCount)
		}
		p.view.tree.render(&sb, top, p.view.ref+".", treeBudget)
		sb.WriteString("</tree>\n")
	}
	return sb.String()
}

// pagesOf turns picks into the pages to read, skipping pages already read
// and keeping each document within twice the page budget. Documents without
// a tree are returned for keyword search.
func (s *Service) pagesOf(req types.SearchRequest, picks []pick, read map[string]map[int]bool) ([]*docSel, []types.Document) {
	cfg := s.cfg.Search
	inRange := func(p int) bool {
		return (req.PageFrom <= 0 || p >= req.PageFrom) && (req.PageTo <= 0 || p <= req.PageTo)
	}
	byDoc := map[string]*docSel{}
	used := map[string]int{}
	var order []string
	var noTree []types.Document
	for _, p := range picks {
		v := p.view
		if v.tree == nil {
			noTree = append(noTree, v.doc)
			continue
		}
		sel := byDoc[v.ref]
		if sel == nil {
			if len(byDoc) >= cfg.MaxDocsSelected {
				continue
			}
			sel = &docSel{doc: v.doc, short: v.ref, paths: map[int][]string{}}
			byDoc[v.ref] = sel
			order = append(order, v.ref)
		}
		if read[v.ref] == nil {
			read[v.ref] = map[int]bool{}
		}
		n := v.tree.root
		if p.node != nil {
			n = *p.node
		}
		perPage := n.TokenCount / max(1, n.PageEnd-n.PageStart+1)
		for pg := n.PageStart; pg <= n.PageEnd; pg++ {
			if read[v.ref][pg] || !inRange(pg) {
				continue
			}
			if used[v.ref]+perPage > cfg.PageTokenBudget*2 && len(sel.pages) > 0 {
				break
			}
			read[v.ref][pg] = true
			used[v.ref] += perPage
			sel.pages = append(sel.pages, pg)
			if p.node != nil {
				sel.paths[pg] = v.tree.path(n)
			} else {
				sel.paths[pg] = v.tree.pathForPage(pg)
			}
		}
		sel.nodes = append(sel.nodes, n.ShortID)
	}
	var out []*docSel
	for _, ref := range order {
		if sel := byDoc[ref]; len(sel.pages) > 0 {
			sort.Ints(sel.pages)
			out = append(out, sel)
		}
	}
	return out, noTree
}

// readSummary lists the pages read so far, e.g. "d1 tr. 8, 9; d2 tr. 1".
func readSummary(views []*docView, read map[string]map[int]bool) string {
	var parts []string
	for _, v := range views {
		var nums []int
		for p := range read[v.ref] {
			nums = append(nums, p)
		}
		if len(nums) == 0 {
			continue
		}
		sort.Ints(nums)
		strs := make([]string, len(nums))
		for i, n := range nums {
			strs[i] = strconv.Itoa(n)
		}
		parts = append(parts, v.ref+" tr. "+strings.Join(strs, ", "))
	}
	return strings.Join(parts, "; ")
}

// keywordHints renders cheap full-text matches next to the TOC (§6.6 step
// 2); they never remove a document.
func (s *Service) keywordHints(ctx context.Context, req types.SearchRequest, views []*docView) string {
	ids := make([]uuid.UUID, len(views))
	refOf := map[uuid.UUID]string{}
	for i, v := range views {
		ids[i], refOf[v.doc.ID] = v.doc.ID, v.ref
	}
	secs, err := s.st.Index.SearchSections(ctx, ids, req.Query, req.PageFrom, req.PageTo, 30)
	if err != nil || len(secs) == 0 {
		return ""
	}
	pages := map[string]map[int]bool{}
	for _, h := range secs {
		ref, ok := refOf[h.DocumentID]
		if !ok {
			continue
		}
		if pages[ref] == nil {
			pages[ref] = map[int]bool{}
		}
		for p := h.PageStart; p <= h.PageEnd && len(pages[ref]) < 8; p++ {
			pages[ref][p] = true
		}
	}
	var sb strings.Builder
	sb.WriteString("\nKeyword hints:\n")
	for _, v := range views {
		ps := pages[v.ref]
		if len(ps) == 0 {
			continue
		}
		var nums []int
		for p := range ps {
			nums = append(nums, p)
		}
		sort.Ints(nums)
		strs := make([]string, len(nums))
		for i, n := range nums {
			strs[i] = strconv.Itoa(n)
		}
		fmt.Fprintf(&sb, "[%s] (khớp: tr. %s)\n", v.ref, strings.Join(strs, ", "))
	}
	return sb.String()
}

// dedupHits keeps the first hit per citation.
func dedupHits(hits []types.SearchHit) []types.SearchHit {
	seen := map[string]bool{}
	out := hits[:0]
	for _, h := range hits {
		if seen[h.CitationID] {
			continue
		}
		seen[h.CitationID] = true
		out = append(out, h)
	}
	return out
}

// CaseTOC implements interfaces.Searcher: the case table of contents built
// from stored cards and trees, without the LLM (§6.6 step 2, §10.3). With
// nothing to expand, every tree is shown whole when they fit
// search.tree_token_budget together.
func (s *Service) CaseTOC(ctx context.Context, owner, caseID uuid.UUID, filter types.MetadataFilter, expand []string) (*types.CaseTOC, error) {
	c, err := s.cases.GetCaseOwned(ctx, owner, caseID)
	if err != nil {
		return nil, ErrNotFound
	}
	schema := s.caseSchema(ctx, c)
	docs, err := s.st.Documents.List(ctx, postgres.DocumentFilter{
		OwnerID: owner, CaseIDs: []uuid.UUID{c.ID}, Metadata: metadata.NormalizeFilter(schema, filter), Schema: schema, Limit: 1000,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	sort.SliceStable(docs, func(i, j int) bool { return docs[i].CreatedAt.Before(docs[j].CreatedAt) })
	out := &types.CaseTOC{Case: c, Documents: []types.TOCDoc{}}
	var ready []types.Document
	for _, d := range docs {
		if types.Searchable(d.Status) {
			ready = append(ready, d)
		} else {
			out.Pending = append(out.Pending, types.TOCPending{DocumentID: d.ID, FileName: d.FileName, Status: d.Status})
		}
	}
	views, err := s.loadViews(ctx, ready)
	if err != nil {
		return nil, err
	}
	for _, v := range views {
		td := types.TOCDoc{Ref: v.ref, DocumentID: v.doc.ID, FileName: v.doc.FileName, Status: v.doc.Status, PageCount: v.doc.PageCount,
			Title: v.doc.Title, Summary: v.doc.Summary, Metadata: v.doc.Metadata, TreeTokens: v.fullTokens()}
		if v.tree != nil {
			for _, n := range v.tree.children[v.tree.root.ID] {
				td.Branches = append(td.Branches, types.TOCBranch{NodeID: n.ShortID, Title: n.Title, PageStart: n.PageStart,
					PageEnd: n.PageEnd, Summary: n.Summary, HasMore: len(v.tree.children[n.ID]) > 0})
			}
		}
		out.Documents = append(out.Documents, td)
	}
	want := map[string]bool{}
	for _, e := range expand {
		want[strings.TrimSpace(e)] = true
	}
	budget := s.cfg.Search.CaseTOCBudget
	if len(want) == 0 {
		// Every tree whole when they fit together, as in search (§6.6).
		total := 0
		for _, v := range views {
			total += v.fullTokens()
		}
		if total <= s.cfg.Search.TreeTokenBudget {
			for _, v := range views {
				want[v.ref] = true
			}
			budget = 1 << 30
		}
	}
	out.Text, out.Truncated = tocText(c, views, want, budget, s.cfg.Search.TreeTokenBudget)
	out.TokenCount = textutil.EstimateTokens(out.Text)
	return out, nil
}

// DocumentTreeText implements interfaces.Searcher: the tree of a document
// (or of one node) the way the LLM reads it (§6.5), whole when it fits
// search.tree_token_budget.
func (s *Service) DocumentTreeText(ctx context.Context, owner, docID uuid.UUID, node string) (string, error) {
	d, err := s.st.Documents.GetOwned(ctx, docID, owner)
	if err != nil {
		return "", ErrNotFound
	}
	nodes, err := s.st.Index.Tree(ctx, d.ID, d.Gen)
	if err != nil {
		return "", err
	}
	if len(nodes) == 0 {
		return "", fmt.Errorf("%w: the document has no table of contents yet (status %s)", ErrBadRequest, d.Status)
	}
	t := newTreeView(nodes)
	top := t.root
	if node = strings.TrimSpace(node); node != "" {
		n, ok := t.byShort[node]
		if !ok {
			return "", ErrNotFound
		}
		top = n
	}
	var sb strings.Builder
	if top.ParentID == nil {
		fmt.Fprintf(&sb, "<tree doc=%q file=%q pages=\"%d\" tokens=\"%d\">\n", d.ID, d.FileName, d.PageCount, t.treeTokens(top.ID))
		if d.Title != "" || d.Summary != "" {
			fmt.Fprintf(&sb, "%s — %s\n", firstNonEmpty(d.Title, d.FileName), textutil.CollapseSpace(d.Summary))
		}
	} else {
		fmt.Fprintf(&sb, "<tree doc=%q node=%q tokens=\"%d\">\n[%s] %s (tr. %s) — %s\n", d.ID, top.ShortID, t.treeTokens(top.ID),
			top.ShortID, top.Title, pageRange(top.PageStart, top.PageEnd), textutil.CollapseSpace(top.Summary))
	}
	t.render(&sb, top.ID, "", s.cfg.Search.TreeTokenBudget)
	sb.WriteString("</tree>\n")
	return sb.String(), nil
}
