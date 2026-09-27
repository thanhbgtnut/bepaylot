package index

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
)

// reasoning runs §6.10 steps 2–5 for every case in scope and merges the
// hits. Every step only sees the documents that passed step 1 (cands): a
// reference the LLM makes to anything else is dropped.
func (s *Service) reasoning(ctx context.Context, req types.SearchRequest, cands []types.Document, resp *types.SearchResponse) error {
	cfg := s.cfg.Search
	b := &budget{}
	b.max.Store(int64(cfg.MaxLLMCalls))
	defer func() { resp.Trace.LLMCalls = int(min(b.used.Load(), b.max.Load())) }()

	byCase := map[uuid.UUID][]types.Document{}
	var order []uuid.UUID
	for _, d := range cands {
		if _, ok := byCase[d.CaseID]; !ok {
			order = append(order, d.CaseID)
		}
		byCase[d.CaseID] = append(byCase[d.CaseID], d)
	}
	narrowed := len(req.DocumentIDs) > 0 || len(req.Metadata) > 0
	views := map[uuid.UUID]*types.WikiIndex{}
	total := 0
	for _, c := range order {
		v, err := s.indexView(ctx, req, c, byCase[c], narrowed)
		if err != nil {
			return err
		}
		views[c] = v
		total += v.TokenCount
	}
	if len(order) > 1 && total > cfg.MapTokenBudget {
		picked, err := s.selectCases(ctx, b, req.Query, order, views)
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
	resp.Trace.WikiVersions = map[string]int{}
	resp.Trace.SelectedNodes = map[string][]string{}
	for _, c := range order {
		resp.Trace.WikiVersions[c.String()] = views[c].Version
	}

	var mu sync.Mutex
	var all []types.SearchHit
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(max(1, cfg.ParallelDocs))
	for _, c := range order {
		c := c
		g.Go(func() error {
			hits, err := s.searchCase(gctx, b, req, c, byCase[c], views[c], resp, &mu)
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

// indexView loads the case's wiki index (step 2 input). Without a wiki
// reader, or with SkipWiki, a plain list of document cards stands in.
func (s *Service) indexView(ctx context.Context, req types.SearchRequest, caseID uuid.UUID, docs []types.Document, narrowed bool) (*types.WikiIndex, error) {
	if s.wiki != nil && !req.SkipWiki {
		var only []uuid.UUID
		if narrowed {
			for _, d := range docs {
				only = append(only, d.ID)
			}
		}
		return s.wiki.IndexView(ctx, caseID, only)
	}
	v := &types.WikiIndex{CaseID: caseID, Refs: map[string]types.WikiRef{}}
	var sb strings.Builder
	sb.WriteString("## Nguồn\n")
	for i, d := range docs {
		id := fmt.Sprintf("w%d", i+1)
		doc := d.ID
		v.Refs[id] = types.WikiRef{DocumentID: &doc, Kind: "pending"}
		fmt.Fprintf(&sb, "[%s] (chưa vào wiki) %s (%d tr.): %s\n", id, d.FileName, d.PageCount, textutil.Truncate(textutil.CollapseSpace(d.Summary), 300))
	}
	v.Content = sb.String()
	v.TokenCount = textutil.EstimateTokens(v.Content)
	return v, nil
}

// selectCases asks the LLM which cases to search when the combined index of
// a multi-case (kb_ids) request is too large (§6.10 step 2).
func (s *Service) selectCases(ctx context.Context, b *budget, query string, order []uuid.UUID, views map[uuid.UUID]*types.WikiIndex) ([]uuid.UUID, error) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Question: %s\n\nCases:\n", query)
	short := map[string]uuid.UUID{}
	for i, c := range order {
		id := fmt.Sprintf("c%d", i+1)
		short[id] = c
		code := c.String()
		if cs, err := s.cases.GetCase(ctx, c); err == nil {
			code = cs.Code
			if cs.Title != "" {
				code += " (" + cs.Title + ")"
			}
		}
		fmt.Fprintf(&sb, "[%s] %s — %s\n", id, code, overviewLine(views[c].Content))
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

// overviewLine is the summary line of the overview page, or the first
// index line.
func overviewLine(content string) string {
	var first string
	for _, l := range strings.Split(content, "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "[w") {
			continue
		}
		if first == "" {
			first = l
		}
		if strings.Contains(l, "tong-quan") {
			return textutil.Truncate(l, 400)
		}
	}
	return textutil.Truncate(first, 400)
}

type rawRef struct {
	Ref      string `json:"ref"`
	Footnote string `json:"footnote"`
	Reason   string `json:"reason"`
}

// fnHit is a footnote the LLM answered with at step 3.
type fnHit struct {
	page      types.WikiPage
	fn        types.WikiFootnote
	relevance float64
	reason    string
}

// searchCase runs steps 2–5 inside one case.
func (s *Service) searchCase(ctx context.Context, b *budget, req types.SearchRequest, caseID uuid.UUID, docs []types.Document,
	view *types.WikiIndex, resp *types.SearchResponse, mu *sync.Mutex) ([]types.SearchHit, error) {
	cfg := s.cfg.Search
	byID := map[uuid.UUID]types.Document{}
	for _, d := range docs {
		byID[d.ID] = d
	}

	// Step 2: read the wiki index (+ keyword hints).
	var sb strings.Builder
	fmt.Fprintf(&sb, "Question: %s\n\n%s\n", req.Query, view.Content)
	sb.WriteString(s.keywordHints(ctx, req, caseID, docs, view))
	var out struct {
		Wiki   []string `json:"wiki"`
		Raw    []rawRef `json:"raw"`
		Expand []string `json:"expand"`
	}
	system := fmt.Sprintf(promptWikiIndex, cfg.MaxWikiPages)
	if err := s.call(ctx, b, system, sb.String(), &out); err != nil {
		return nil, err
	}
	for hop := 0; len(out.Expand) > 0 && hop < cfg.MaxHops && s.wiki != nil; hop++ {
		var more strings.Builder
		for _, ref := range out.Expand {
			if txt, err := s.wiki.Expand(ctx, caseID, strings.TrimSpace(ref)); err == nil && txt != "" {
				more.WriteString(txt)
				more.WriteString("\n")
			}
		}
		if more.Len() == 0 {
			break
		}
		sb.WriteString("\nExpanded branches:\n" + more.String())
		out.Wiki, out.Raw, out.Expand = nil, nil, nil
		if err := s.call(ctx, b, system, sb.String(), &out); err != nil {
			return nil, err
		}
	}

	// Step 3: read the chosen wiki pages.
	var pages []types.WikiPage
	shortOf := map[string]string{} // slug → index id
	if s.wiki != nil {
		seen := map[string]bool{}
		for _, id := range out.Wiki {
			ref, ok := view.Refs[strings.TrimSpace(id)]
			if !ok || ref.PageID == nil || ref.Slug == "" || seen[ref.Slug] || len(pages) >= cfg.MaxWikiPages {
				continue
			}
			seen[ref.Slug] = true
			p, err := s.wiki.Page(ctx, caseID, ref.Slug)
			if err != nil {
				continue
			}
			pages = append(pages, *p)
			shortOf[p.Slug] = strings.TrimSpace(id)
		}
	}
	raws := out.Raw
	var fnHits []fnHit
	if len(pages) > 0 {
		hits, more, err := s.readWiki(ctx, b, req, pages, shortOf)
		if err != nil && !errors.Is(err, errBudget) {
			return nil, err
		}
		fnHits, raws = hits, append(raws, more...)
		mu.Lock()
		for _, p := range pages {
			resp.Trace.WikiPages = append(resp.Trace.WikiPages, p.Slug)
		}
		mu.Unlock()
	}

	// Step 4: read sources PageIndex-style.
	sels, kwDocs, err := s.rawSelections(ctx, b, req, caseID, view, pages, shortOf, byID, raws, resp, mu)
	if err != nil {
		return nil, err
	}
	hits, dropped, truncated, err := s.locate(ctx, b, req, sels)
	if err != nil {
		return nil, err
	}

	// Step 5: verify footnote hits against the current source lines.
	fh, fnDropped, stale := s.verifyFootnotes(ctx, caseID, fnHits, byID)
	hits = append(fh, hits...)
	mu.Lock()
	resp.Trace.DroppedHits += dropped + fnDropped
	resp.Trace.StaleFootnotes += stale
	resp.Trace.Truncated = resp.Trace.Truncated || truncated
	for _, sel := range sels {
		resp.Trace.SelectedDocs = append(resp.Trace.SelectedDocs, sel.doc.ID)
		resp.Trace.SelectedNodes[sel.doc.ID.String()] = sel.nodes
	}
	mu.Unlock()

	if len(kwDocs) > 0 {
		kw, err := s.keyword(ctx, req, kwDocs, req.TopK)
		if err != nil {
			return nil, err
		}
		hits = append(hits, kw...)
	}
	if len(hits) == 0 && len(sels) == 0 && len(fnHits) == 0 {
		// Nothing was selected: the wiki never excludes a file, so fall
		// back to full-text over the whole scope.
		mu.Lock()
		if resp.Trace.Fallback == "" {
			resp.Trace.Fallback = "nothing_selected"
		}
		mu.Unlock()
		kw, err := s.keyword(ctx, req, docs, req.TopK)
		s.logQuery(ctx, req, caseID, pages, len(kw))
		return kw, err
	}
	hits = dedupHits(hits)
	s.logQuery(ctx, req, caseID, pages, len(hits))
	return hits, nil
}

// logQuery records the search in the wiki log (best effort).
func (s *Service) logQuery(ctx context.Context, req types.SearchRequest, caseID uuid.UUID, pages []types.WikiPage, hits int) {
	if s.wiki == nil {
		return
	}
	slugs := make([]string, len(pages))
	for i, p := range pages {
		slugs[i] = p.Slug
	}
	if err := s.wiki.LogQuery(context.WithoutCancel(ctx), caseID, req.OwnerID, req.Query, slugs, hits); err != nil {
		s.log.Warn("wiki query log", "case", caseID, "err", err)
	}
}

// keywordHints renders cheap full-text matches as hints next to the index
// lines (§6.10 step 2); they never remove a line.
func (s *Service) keywordHints(ctx context.Context, req types.SearchRequest, caseID uuid.UUID, docs []types.Document, view *types.WikiIndex) string {
	docShort := map[uuid.UUID]string{}
	slugShort := map[string]string{}
	for id, ref := range view.Refs {
		if ref.DocumentID != nil && ref.NodeID == "" {
			if cur, ok := docShort[*ref.DocumentID]; !ok || len(id) < len(cur) {
				docShort[*ref.DocumentID] = id
			}
		}
		if ref.Slug != "" && ref.NodeID == "" {
			slugShort[ref.Slug] = id
		}
	}
	ids := make([]uuid.UUID, len(docs))
	for i, d := range docs {
		ids[i] = d.ID
	}
	lines := map[string]string{}
	if secs, err := s.st.Index.SearchSections(ctx, ids, req.Query, req.PageFrom, req.PageTo, 30); err == nil {
		pages := map[string]map[int]bool{}
		for _, h := range secs {
			id, ok := docShort[h.DocumentID]
			if !ok {
				continue
			}
			if pages[id] == nil {
				pages[id] = map[int]bool{}
			}
			for p := h.PageStart; p <= h.PageEnd && len(pages[id]) < 8; p++ {
				pages[id][p] = true
			}
		}
		for id, ps := range pages {
			var nums []int
			for p := range ps {
				nums = append(nums, p)
			}
			sort.Ints(nums)
			parts := make([]string, len(nums))
			for i, n := range nums {
				parts[i] = strconv.Itoa(n)
			}
			lines[id] = "(khớp: tr. " + strings.Join(parts, ", ") + ")"
		}
	}
	if s.wiki != nil {
		if hits, err := s.wiki.SearchPages(ctx, caseID, req.Query, 10); err == nil {
			for _, h := range hits {
				if id, ok := slugShort[h.Slug]; ok {
					if _, dup := lines[id]; !dup {
						lines[id] = "(khớp từ khoá)"
					}
				}
			}
		}
	}
	if len(lines) == 0 {
		return ""
	}
	keys := make([]string, 0, len(lines))
	for k := range lines {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString("\nKeyword hints:\n")
	for _, k := range keys {
		fmt.Fprintf(&sb, "[%s] %s\n", k, lines[k])
	}
	return sb.String()
}

// readWiki runs step 3: the LLM answers with footnotes or asks for sources.
func (s *Service) readWiki(ctx context.Context, b *budget, req types.SearchRequest, pages []types.WikiPage, shortOf map[string]string) ([]fnHit, []rawRef, error) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Question: %s\n\n", req.Query)
	for _, p := range pages {
		fmt.Fprintf(&sb, "<wiki_page id=%q kind=%q title=%q>\n%s\n", shortOf[p.Slug], p.Kind, p.Title, p.Content)
		// Pages written by the system already show their attributes in the
		// content; a hand-edited page may not.
		if len(p.Attributes) > 0 && p.LastEditSource == types.EditUser {
			sb.WriteString("Attributes:\n")
			names := make([]string, 0, len(p.Attributes))
			for k := range p.Attributes {
				names = append(names, k)
			}
			sort.Strings(names)
			for _, k := range names {
				a := p.Attributes[k]
				fmt.Fprintf(&sb, "- %s = %v%s", k, a.Value, footRefs(a.Footnotes))
				if a.Conflict {
					var alts []string
					for _, h := range a.History {
						alts = append(alts, fmt.Sprintf("%v%s", h.Value, footRefs(h.Footnotes)))
					}
					fmt.Fprintf(&sb, " (conflict: %s)", strings.Join(alts, " vs "))
				}
				sb.WriteString("\n")
			}
		}
		if len(p.Footnotes) > 0 {
			sb.WriteString("Footnotes:\n")
			for _, f := range p.Footnotes {
				mark := ""
				if f.Status == types.FootnoteStale {
					mark = " (stale)"
				}
				fmt.Fprintf(&sb, "[^%d] %s tr. %d L%d-%d: %q%s\n", f.N, f.FileName, f.PageNo, f.LineFrom, f.LineTo, f.Quote, mark)
			}
		}
		sb.WriteString("</wiki_page>\n")
	}
	var out struct {
		Hits []struct {
			Page      string  `json:"page"`
			Footnotes []int   `json:"footnotes"`
			Relevance float64 `json:"relevance"`
			Reason    string  `json:"reason"`
		} `json:"hits"`
		Raw []rawRef `json:"raw"`
	}
	if err := s.call(ctx, b, promptWikiRead, sb.String(), &out); err != nil {
		return nil, nil, err
	}
	byShort := map[string]types.WikiPage{}
	for _, p := range pages {
		byShort[shortOf[p.Slug]] = p
		byShort[p.Slug] = p
	}
	var hits []fnHit
	for _, h := range out.Hits {
		p, ok := byShort[strings.TrimSpace(h.Page)]
		if !ok && len(pages) == 1 {
			p, ok = pages[0], true
		}
		if !ok {
			continue
		}
		for _, n := range h.Footnotes {
			for _, f := range p.Footnotes {
				if f.N == n {
					hits = append(hits, fnHit{page: p, fn: f, relevance: clamp01(h.Relevance), reason: h.Reason})
				}
			}
		}
	}
	return hits, out.Raw, nil
}

func footRefs(ns []int) string {
	var sb strings.Builder
	for _, n := range ns {
		fmt.Fprintf(&sb, "[^%d]", n)
	}
	return sb.String()
}

// rawSelections resolves the source reads asked at steps 2–3 to pages of
// in-scope documents (§6.10 step 4). Documents without a tree go to keyword.
func (s *Service) rawSelections(ctx context.Context, b *budget, req types.SearchRequest, caseID uuid.UUID, view *types.WikiIndex,
	pages []types.WikiPage, shortOf map[string]string, byID map[uuid.UUID]types.Document, raws []rawRef,
	resp *types.SearchResponse, mu *sync.Mutex) ([]*docSel, []types.Document, error) {
	cfg := s.cfg.Search
	type target struct {
		starts []string
		pages  []int
		wiki   []string
	}
	targets := map[uuid.UUID]*target{}
	var docOrder []uuid.UUID
	add := func(doc uuid.UUID, start string, page int, slug string) {
		if _, ok := byID[doc]; !ok {
			return // outside the step-1 scope
		}
		t := targets[doc]
		if t == nil {
			if len(targets) >= cfg.MaxDocsSelected {
				return
			}
			t = &target{}
			targets[doc] = t
			docOrder = append(docOrder, doc)
		}
		if page > 0 {
			t.pages = append(t.pages, page)
		} else {
			t.starts = append(t.starts, start)
		}
		if slug != "" {
			t.wiki = append(t.wiki, slug)
		}
	}
	pageByShort := map[string]types.WikiPage{}
	for _, p := range pages {
		pageByShort[shortOf[p.Slug]] = p
	}
	var refsRead []string
	for _, r := range raws {
		switch {
		case r.Footnote != "":
			pid, nstr, ok := strings.Cut(strings.TrimSpace(r.Footnote), "#")
			n, err := strconv.Atoi(strings.TrimPrefix(nstr, "^"))
			p, found := pageByShort[pid]
			if !ok || err != nil || !found {
				continue
			}
			for _, f := range p.Footnotes {
				if f.N == n {
					add(f.DocumentID, "", f.PageNo, p.Slug)
					refsRead = append(refsRead, r.Footnote)
				}
			}
		case r.Ref != "":
			ref, ok := resolveRef(view, r.Ref)
			if !ok || ref.DocumentID == nil {
				continue
			}
			add(*ref.DocumentID, ref.NodeID, 0, ref.Slug)
			refsRead = append(refsRead, r.Ref)
		}
	}
	mu.Lock()
	resp.Trace.RawRefs = append(resp.Trace.RawRefs, refsRead...)
	mu.Unlock()

	sels := make([]*docSel, len(docOrder))
	var kwMu sync.Mutex
	var kwDocs []types.Document
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(max(1, cfg.ParallelDocs))
	for i, doc := range docOrder {
		i, d, t := i, byID[doc], targets[doc]
		g.Go(func() error {
			merged := &docSel{doc: d, paths: map[int][]string{}, wiki: t.wiki}
			seen := map[int]bool{}
			addPage := func(p int, path []string) {
				if !seen[p] && p >= 1 && (d.PageCount == 0 || p <= d.PageCount) {
					seen[p] = true
					merged.pages = append(merged.pages, p)
					merged.paths[p] = path
				}
			}
			for _, p := range t.pages {
				addPage(p, nil)
			}
			for _, start := range t.starts {
				sel, err := s.selectPages(gctx, b, req, d, start)
				if errors.Is(err, errNoTree) {
					kwMu.Lock()
					kwDocs = append(kwDocs, d)
					kwMu.Unlock()
					return nil
				}
				if errors.Is(err, errBudget) {
					break
				}
				if err != nil {
					return err
				}
				for _, p := range sel.pages {
					addPage(p, sel.paths[p])
				}
				merged.nodes = append(merged.nodes, sel.nodes...)
			}
			sort.Ints(merged.pages)
			merged.short = fmt.Sprintf("d%d", i+1)
			sels[i] = merged
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, nil, err
	}
	var out []*docSel
	for _, sel := range sels {
		if sel != nil && len(sel.pages) > 0 {
			out = append(out, sel)
		}
	}
	return out, kwDocs, nil
}

// verifyFootnotes turns footnote hits into located hits only when the quote
// still matches the current lines of the current generation (§6.10 step 5).
// Footnotes that no longer match are dropped and marked stale.
func (s *Service) verifyFootnotes(ctx context.Context, caseID uuid.UUID, hits []fnHit, byID map[uuid.UUID]types.Document) ([]types.SearchHit, int, int) {
	var out []types.SearchHit
	dropped, stale := 0, 0
	staleByPage := map[uuid.UUID][]int{}
	cache := map[string]map[int]postgres.PageLine{}
	for _, h := range hits {
		d, ok := byID[h.fn.DocumentID]
		if !ok {
			dropped++ // outside the scope of this request
			continue
		}
		if h.fn.Gen != d.Gen || h.fn.Status == types.FootnoteStale {
			dropped++
			stale++
			staleByPage[h.page.ID] = append(staleByPage[h.page.ID], h.fn.N)
			continue
		}
		if h.fn.PageNo < 1 {
			dropped++
			continue
		}
		key := fmt.Sprintf("%s:%d", d.ID, h.fn.PageNo)
		lines, ok := cache[key]
		if !ok {
			ls, err := s.st.Pages.Lines(ctx, d.ID, h.fn.PageNo, h.fn.PageNo)
			if err != nil {
				dropped++
				continue
			}
			lines = map[int]postgres.PageLine{}
			for _, l := range ls {
				lines[l.LineNo] = l
			}
			cache[key] = lines
		}
		var nums []int
		for n := h.fn.LineFrom; n <= h.fn.LineTo; n++ {
			nums = append(nums, n)
		}
		got, boxes, ok := verifyQuote(locHit{Lines: nums, Quote: h.fn.Quote}, lines, s.cfg.Search.QuoteMinSimilarity)
		if !ok {
			dropped++
			stale++
			staleByPage[h.page.ID] = append(staleByPage[h.page.ID], h.fn.N)
			continue
		}
		hit := hitFromLine(d, h.fn.PageNo, got, h.fn.Quote, boxes, h.relevance, "", nil)
		hit.Via, hit.WikiPages, hit.Reason = "wiki", []string{h.page.Slug}, h.reason
		out = append(out, hit)
	}
	if s.wiki != nil {
		for page, ns := range staleByPage {
			if err := s.wiki.MarkFootnotesStale(ctx, caseID, page, ns); err != nil {
				s.log.Warn("mark footnotes stale", "page", page, "err", err)
			}
		}
	}
	return out, dropped, stale
}

// resolveRef maps an index id to its ref; a branch opened by expand
// (w<n>.n<k> not in the index) resolves through its file entry.
func resolveRef(view *types.WikiIndex, id string) (types.WikiRef, bool) {
	id = strings.TrimSpace(id)
	if ref, ok := view.Refs[id]; ok {
		return ref, true
	}
	base, node, cut := strings.Cut(id, ".")
	if !cut || !strings.HasPrefix(node, "n") {
		return types.WikiRef{}, false
	}
	b, ok := view.Refs[base]
	if !ok || b.DocumentID == nil {
		return types.WikiRef{}, false
	}
	return types.WikiRef{DocumentID: b.DocumentID, NodeID: node, Slug: b.Slug}, true
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
