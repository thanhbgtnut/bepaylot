package index

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/application/service/metadata"
	"github.com/thanhenti/bepaylot/internal/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
	"github.com/thanhenti/bepaylot/internal/types/interfaces"
)

// Search errors.
var (
	ErrBadRequest = errors.New("bad request")
	ErrNotFound   = errors.New("not found")
	errBudget     = errors.New("llm call budget exhausted")
	errNoTree     = errors.New("document has no tree yet")
)

// Search implements interfaces.Searcher (§6.5).
func (s *Service) Search(ctx context.Context, req types.SearchRequest) (*types.SearchResponse, error) {
	start := time.Now()
	if req.OwnerID == uuid.Nil {
		return nil, fmt.Errorf("%w: owner required", ErrBadRequest)
	}
	if len(req.KBIDs) == 0 && len(req.DocumentIDs) == 0 {
		return nil, fmt.Errorf("%w: kb_ids or document_ids is required", ErrBadRequest)
	}
	mode := req.Mode
	if mode == "" {
		mode = s.cfg.Search.DefaultMode
	}
	switch mode {
	case types.SearchReasoning, types.SearchKeyword, types.SearchMetadata:
	default:
		return nil, fmt.Errorf("%w: unknown mode %q", ErrBadRequest, mode)
	}
	if mode != types.SearchMetadata && strings.TrimSpace(req.Query) == "" {
		return nil, fmt.Errorf("%w: query is required", ErrBadRequest)
	}
	if req.TopK <= 0 || req.TopK > 50 {
		req.TopK = 10
	}
	if s.cfg.Search.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.cfg.Search.Timeout)
		defer cancel()
	}

	filter, err := s.scope(ctx, req)
	if err != nil {
		return nil, err
	}
	resp := &types.SearchResponse{Trace: types.SearchTrace{Mode: mode}}
	if mode == types.SearchMetadata {
		filter.Limit = max(req.TopK, 100)
		docs, err := s.st.Documents.List(ctx, filter)
		if err != nil {
			return nil, err
		}
		resp.Documents = briefs(docs)
		resp.Trace.CandidateDocs = len(docs)
		resp.Trace.ElapsedMs = time.Since(start).Milliseconds()
		return resp, nil
	}

	filter.Limit = 1000
	cands, err := s.st.Documents.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	resp.Trace.CandidateDocs = len(cands)
	if len(cands) == 0 {
		resp.Hits = []types.SearchHit{}
		resp.Trace.ElapsedMs = time.Since(start).Milliseconds()
		return resp, nil
	}
	key := cacheKey(mode, req, cands)
	if v, ok := s.cache.get(key); ok {
		out := *(v.(*types.SearchResponse))
		out.Trace.Cached = true
		return &out, nil
	}

	if mode == types.SearchReasoning && s.searchLLM == nil {
		mode, resp.Trace.Fallback = types.SearchKeyword, "no_llm_configured"
	}
	if mode == types.SearchReasoning {
		err = s.reasoning(ctx, req, filter, cands, resp)
		if err != nil {
			s.log.Warn("reasoning search failed, falling back to keyword", "err", err)
			resp.Hits = nil
			resp.Trace.Fallback = "llm_error: " + textutil.Truncate(err.Error(), 200)
			mode = types.SearchKeyword
		}
	}
	if mode == types.SearchKeyword {
		hits, err := s.keyword(ctx, req, cands, req.TopK)
		if err != nil {
			return nil, err
		}
		resp.Hits = hits
	}
	if resp.Hits == nil {
		resp.Hits = []types.SearchHit{}
	}
	resp.Trace.ElapsedMs = time.Since(start).Milliseconds()
	s.cache.put(key, resp)
	return resp, nil
}

// scope turns the request into a document filter restricted to searchable
// documents the caller owns, with metadata normalized by the KB schema.
func (s *Service) scope(ctx context.Context, req types.SearchRequest) (postgres.DocumentFilter, error) {
	f := postgres.DocumentFilter{
		OwnerID: req.OwnerID, KBIDs: req.KBIDs, DocumentIDs: req.DocumentIDs,
		Statuses: []string{types.DocCompleted, types.DocPartial, types.DocEnriching},
	}
	if len(req.KBIDs) == 1 {
		kb, err := s.st.KBs.GetOwned(ctx, req.KBIDs[0], req.OwnerID)
		if err != nil {
			return f, ErrNotFound
		}
		f.Schema = kb.MetadataSchema
	}
	f.Metadata = metadata.NormalizeFilter(f.Schema, req.Metadata)
	// Validate the filter early so bad keys/operators are a 400.
	if _, err := s.st.Documents.List(ctx, postgres.DocumentFilter{OwnerID: req.OwnerID, KBIDs: []uuid.UUID{uuid.Nil}, Metadata: f.Metadata, Limit: 1}); err != nil &&
		strings.Contains(err.Error(), "metadata filter") {
		return f, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	return f, nil
}

func cacheKey(mode string, req types.SearchRequest, docs []types.Document) string {
	h := sha1.New()
	b, _ := json.Marshal(struct {
		M string
		R types.SearchRequest
		O uuid.UUID
	}{mode, req, req.OwnerID})
	h.Write(b)
	for _, d := range docs {
		fmt.Fprintf(h, "%s:%d;", d.ID, d.Gen)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func briefs(docs []types.Document) []types.DocumentBrief {
	out := make([]types.DocumentBrief, len(docs))
	for i, d := range docs {
		out[i] = types.DocumentBrief{ID: d.ID, KBID: d.KBID, FileName: d.FileName, Status: d.Status, PageCount: d.PageCount,
			Title: d.Title, DocType: d.DocType, Summary: d.Summary, Metadata: d.Metadata}
	}
	return out
}

// ---- keyword mode ----

func (s *Service) keyword(ctx context.Context, req types.SearchRequest, docs []types.Document, topK int) ([]types.SearchHit, error) {
	byID := map[uuid.UUID]types.Document{}
	ids := make([]uuid.UUID, len(docs))
	for i, d := range docs {
		byID[d.ID], ids[i] = d, d.ID
	}
	secHits, err := s.st.Index.SearchSections(ctx, ids, req.Query, req.PageFrom, req.PageTo, topK*3)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []types.SearchHit
	add := func(h types.SearchHit) {
		k := fmt.Sprintf("%s:%d:%v", h.DocumentID, h.PageNo, h.Lines)
		if seen[k] || len(out) >= topK {
			return
		}
		seen[k] = true
		out = append(out, h)
	}
	for _, sh := range secHits {
		d := byID[sh.DocumentID]
		lines, _ := s.st.Pages.SearchLines(ctx, d.ID, d.Gen, req.Query, sh.PageStart, sh.PageEnd, 3)
		if len(lines) > 0 {
			for _, l := range lines {
				add(hitFromLine(d, l.PageNo, []int{l.LineNo}, l.Text, []types.BBox{l.BBox}, l.Score, sh.Snippet, sh.HeadingPath))
			}
			continue
		}
		sp := sh.SourceSpans[0]
		add(hitFromLine(d, sp.Page, []int{max(sp.LineFrom, 0)}, textutil.Truncate(stripMD(sh.Content), 200), []types.BBox{sp.BBox}, sh.Score, sh.Snippet, sh.HeadingPath))
	}
	if len(out) == 0 {
		// Sections may miss short codes; fall back to line trigram search.
		for _, d := range docs {
			lines, err := s.st.Pages.SearchLines(ctx, d.ID, d.Gen, req.Query, req.PageFrom, req.PageTo, topK)
			if err != nil {
				return nil, err
			}
			for _, l := range lines {
				if l.Score >= 0.5 {
					add(hitFromLine(d, l.PageNo, []int{l.LineNo}, l.Text, []types.BBox{l.BBox}, l.Score, "", nil))
				}
			}
			if len(out) >= topK {
				break
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Relevance > out[j].Relevance })
	return out, nil
}

func hitFromLine(d types.Document, page int, lines []int, quote string, boxes []types.BBox, score float64, snippet string, path []string) types.SearchHit {
	return types.SearchHit{
		DocumentID: d.ID, FileName: d.FileName, Metadata: d.Metadata, PageNo: page, Lines: lines,
		Quote: quote, Snippet: snippet, Relevance: score, NodePath: path,
		CitationID: CitationID(d.ID, page, lines), BBoxes: boxes,
	}
}

// CitationID formats doc:<uuid>:p<page>:l<a>-<b>.
func CitationID(doc uuid.UUID, page int, lines []int) string {
	if len(lines) == 0 {
		return fmt.Sprintf("doc:%s:p%d", doc, page)
	}
	lo, hi := lines[0], lines[0]
	for _, l := range lines {
		lo, hi = min(lo, l), max(hi, l)
	}
	return fmt.Sprintf("doc:%s:p%d:l%d-%d", doc, page, lo, hi)
}

var citationRe = regexp.MustCompile(`^doc:([0-9a-fA-F-]{36}):p(\d+)(?::l(\d+)-(\d+))?$`)

// ParseCitation is the inverse of CitationID.
func ParseCitation(c string) (uuid.UUID, int, int, int, error) {
	m := citationRe.FindStringSubmatch(strings.TrimSpace(c))
	if m == nil {
		return uuid.Nil, 0, 0, 0, fmt.Errorf("%w: bad citation id %q", ErrBadRequest, c)
	}
	id, err := uuid.Parse(m[1])
	if err != nil {
		return uuid.Nil, 0, 0, 0, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	page, _ := strconv.Atoi(m[2])
	lo, hi := -1, -1
	if m[3] != "" {
		lo, _ = strconv.Atoi(m[3])
		hi, _ = strconv.Atoi(m[4])
	}
	return id, page, lo, hi, nil
}

// ---- reasoning mode ----

type budget struct {
	used, max atomic.Int64
}

func (s *Service) call(ctx context.Context, b *budget, system, user string, out any) error {
	if b.used.Add(1) > b.max.Load() {
		return errBudget
	}
	return s.searchLLM.CompleteJSON(ctx, system, user, out)
}

type docSel struct {
	doc   types.Document
	short string
	pages []int
	nodes []string
	paths map[int][]string // page → node path
}

func (s *Service) reasoning(ctx context.Context, req types.SearchRequest, filter postgres.DocumentFilter, cands []types.Document, resp *types.SearchResponse) error {
	cfg := s.cfg.Search
	b := &budget{}
	b.max.Store(int64(cfg.MaxLLMCalls))
	defer func() { resp.Trace.LLMCalls = int(min(b.used.Load(), b.max.Load())) }()

	// Step 2: choose documents.
	selected := cands
	if len(cands) > cfg.MaxDocsDirect {
		ranked, err := s.st.Documents.RankByText(ctx, filter, req.Query, cfg.DocCandidates)
		if err != nil {
			return err
		}
		byID := map[uuid.UUID]types.Document{}
		for _, d := range cands {
			byID[d.ID] = d
		}
		var pool []types.Document
		for _, id := range ranked {
			if d, ok := byID[id]; ok {
				pool = append(pool, d)
			}
		}
		selected, err = s.selectDocs(ctx, b, req.Query, pool, cfg.MaxDocsSelected)
		if err != nil {
			return err
		}
		if len(selected) == 0 {
			resp.Trace.Fallback = "no_document_selected"
			hits, err := s.keyword(ctx, req, pool[:min(len(pool), 10)], req.TopK)
			resp.Hits = hits
			return err
		}
	}
	for _, d := range selected {
		resp.Trace.SelectedDocs = append(resp.Trace.SelectedDocs, d.ID)
	}

	// Step 3: navigate each document's tree.
	sels := make([]*docSel, len(selected))
	var mu sync.Mutex
	var kwDocs []types.Document
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(max(1, cfg.ParallelDocs))
	for i, d := range selected {
		i, d := i, d
		g.Go(func() error {
			sel, err := s.selectPages(gctx, b, req, d)
			if errors.Is(err, errNoTree) {
				mu.Lock()
				kwDocs = append(kwDocs, d)
				mu.Unlock()
				return nil
			}
			if err != nil {
				return err
			}
			sel.short = fmt.Sprintf("d%d", i+1)
			sels[i] = sel
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	resp.Trace.SelectedNodes = map[string][]string{}
	var withPages []*docSel
	for _, sel := range sels {
		if sel != nil && len(sel.pages) > 0 {
			withPages = append(withPages, sel)
			resp.Trace.SelectedNodes[sel.doc.ID.String()] = sel.nodes
		}
	}

	// Steps 4–5: read pages, locate lines, verify quotes.
	hits, dropped, truncated, err := s.locate(ctx, b, req, withPages)
	if err != nil {
		return err
	}
	resp.Trace.DroppedHits = dropped
	resp.Trace.Truncated = truncated
	if len(kwDocs) > 0 {
		kw, err := s.keyword(ctx, req, kwDocs, req.TopK)
		if err != nil {
			return err
		}
		hits = append(hits, kw...)
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Relevance > hits[j].Relevance })
	if len(hits) > req.TopK {
		hits = hits[:req.TopK]
	}
	resp.Hits = hits
	return nil
}

func (s *Service) selectDocs(ctx context.Context, b *budget, query string, pool []types.Document, maxSel int) ([]types.Document, error) {
	if len(pool) == 0 {
		return nil, nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Question: %s\n\nDocuments:\n", query)
	short := map[string]types.Document{}
	for i, d := range pool {
		id := fmt.Sprintf("d%d", i+1)
		short[id] = d
		meta, _ := json.Marshal(d.Metadata)
		fmt.Fprintf(&sb, "- id=%s file=%q pages=%d metadata=%s\n  title: %s\n  type: %s\n  summary: %s\n",
			id, d.FileName, d.PageCount, meta, d.Title, d.DocType, textutil.Truncate(d.Summary, 600))
	}
	var out struct {
		Select []struct {
			Doc string `json:"doc"`
		} `json:"select"`
	}
	if err := s.call(ctx, b, fmt.Sprintf(promptSelectDocs, maxSel), sb.String(), &out); err != nil {
		return nil, err
	}
	var sel []types.Document
	seen := map[string]bool{}
	for _, x := range out.Select {
		if d, ok := short[strings.TrimSpace(x.Doc)]; ok && !seen[x.Doc] && len(sel) < maxSel {
			seen[x.Doc] = true
			sel = append(sel, d)
		}
	}
	return sel, nil
}

// selectPages walks the tree with the LLM (§6.5 step 3). Small documents skip
// the walk and read every page.
func (s *Service) selectPages(ctx context.Context, b *budget, req types.SearchRequest, d types.Document) (*docSel, error) {
	cfg := s.cfg.Search
	nodes, err := s.st.Index.Tree(ctx, d.ID, d.Gen)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, errNoTree
	}
	t := newTreeView(nodes)
	sel := &docSel{doc: d, paths: map[int][]string{}}
	inRange := func(p int) bool {
		return (req.PageFrom <= 0 || p >= req.PageFrom) && (req.PageTo <= 0 || p <= req.PageTo)
	}
	if t.root.TokenCount <= cfg.FullDocTokenBudget {
		for p := 1; p <= d.PageCount; p++ {
			if inRange(p) {
				sel.pages = append(sel.pages, p)
				sel.paths[p] = t.pathForPage(p)
			}
		}
		sel.nodes = []string{t.root.ShortID}
		return sel, nil
	}

	shown := t.initialView(cfg.TreeTokenBudget)
	var picked []string
	for hop := 0; hop <= cfg.MaxHops; hop++ {
		var sb strings.Builder
		fmt.Fprintf(&sb, "Question: %s\n\nDocument: %s — %s\n%s\n\nTable of contents:\n", req.Query, d.FileName, t.root.Title, textutil.Truncate(t.root.Summary, 600))
		t.render(&sb, shown)
		var out struct {
			Select []struct {
				NodeID string `json:"node_id"`
			} `json:"select"`
			Expand     []string `json:"expand"`
			Answerable *bool    `json:"answerable"`
		}
		if err := s.call(ctx, b, promptSelectNodes, sb.String(), &out); err != nil {
			return nil, err
		}
		for _, x := range out.Select {
			if _, ok := t.byShort[x.NodeID]; ok {
				picked = append(picked, x.NodeID)
			}
		}
		if out.Answerable != nil && !*out.Answerable && len(picked) == 0 {
			return sel, nil
		}
		grew := false
		for _, id := range out.Expand {
			if n, ok := t.byShort[id]; ok && len(t.children[n.ID]) > 0 && !shown[id+"+open"] {
				shown[id+"+open"] = true
				for _, c := range t.children[n.ID] {
					shown[c.ShortID] = true
				}
				grew = true
			}
		}
		if !grew || hop == cfg.MaxHops {
			break
		}
	}
	// Selected nodes → pages, within the page budget.
	budgetTokens := cfg.PageTokenBudget * 2
	used := 0
	seen := map[int]bool{}
	for _, id := range picked {
		n := t.byShort[id]
		perPage := n.TokenCount / max(1, n.PageEnd-n.PageStart+1)
		for p := n.PageStart; p <= n.PageEnd; p++ {
			if seen[p] || !inRange(p) {
				continue
			}
			if used+perPage > budgetTokens && len(sel.pages) > 0 {
				break
			}
			seen[p] = true
			used += perPage
			sel.pages = append(sel.pages, p)
			sel.paths[p] = t.path(n)
		}
		sel.nodes = append(sel.nodes, id)
	}
	sort.Ints(sel.pages)
	return sel, nil
}

type locHit struct {
	Doc       string  `json:"doc"`
	Page      int     `json:"page"`
	Lines     []int   `json:"lines"`
	Quote     string  `json:"quote"`
	Relevance float64 `json:"relevance"`
	Reason    string  `json:"reason"`
}

// locate reads the selected pages as numbered lines and asks the LLM for the
// exact lines, then verifies each quote against the stored lines (§6.5 4–5).
func (s *Service) locate(ctx context.Context, b *budget, req types.SearchRequest, sels []*docSel) ([]types.SearchHit, int, bool, error) {
	cfg := s.cfg.Search
	type pageText struct {
		sel   *docSel
		page  int
		text  string
		lines map[int]postgres.PageLine
		tok   int
	}
	var pts []pageText
	for _, sel := range sels {
		lines, err := s.st.Pages.Lines(ctx, sel.doc.ID, sel.pages[0], sel.pages[len(sel.pages)-1])
		if err != nil {
			return nil, 0, false, err
		}
		want := map[int]bool{}
		for _, p := range sel.pages {
			want[p] = true
		}
		byPage := map[int][]postgres.PageLine{}
		for _, l := range lines {
			if want[l.PageNo] && strings.TrimSpace(l.Text) != "" {
				byPage[l.PageNo] = append(byPage[l.PageNo], l)
			}
		}
		for _, p := range sel.pages {
			ls := byPage[p]
			if len(ls) == 0 {
				continue
			}
			var sb strings.Builder
			fmt.Fprintf(&sb, "<page n=\"%d\" doc=%q file=%q>\n", p, sel.short, sel.doc.FileName)
			m := map[int]postgres.PageLine{}
			for _, l := range ls {
				mark := ""
				if l.LowConfidence && l.TextSource == types.TextSourceOCR {
					mark = " (?)"
				}
				fmt.Fprintf(&sb, "[L%d] %s%s\n", l.LineNo, l.Text, mark)
				m[l.LineNo] = l
			}
			sb.WriteString("</page>\n")
			pts = append(pts, pageText{sel: sel, page: p, text: sb.String(), lines: m, tok: textutil.EstimateTokens(sb.String())})
		}
	}
	// Batch pages into calls under the page token budget.
	var batches [][]pageText
	for i := 0; i < len(pts); {
		j, used := i, 0
		for j < len(pts) && (j == i || used+pts[j].tok <= cfg.PageTokenBudget) {
			used += pts[j].tok
			j++
		}
		batches = append(batches, pts[i:j])
		i = j
	}
	truncated := false
	if rest := int(b.max.Load() - b.used.Load()); len(batches) > rest {
		batches = batches[:max(0, rest)]
		truncated = true
	}
	results := make([][]locHit, len(batches))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(max(1, cfg.ParallelDocs))
	for i, batch := range batches {
		i, batch := i, batch
		g.Go(func() error {
			var sb strings.Builder
			fmt.Fprintf(&sb, "Question: %s\n\n", req.Query)
			for _, pt := range batch {
				sb.WriteString(pt.text)
			}
			var out struct {
				Hits []locHit `json:"hits"`
			}
			if err := s.call(gctx, b, fmt.Sprintf(promptLocate, req.TopK), sb.String(), &out); err != nil {
				if errors.Is(err, errBudget) {
					return nil
				}
				return err
			}
			results[i] = out.Hits
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, 0, truncated, err
	}

	index := map[string]pageText{}
	for _, pt := range pts {
		index[fmt.Sprintf("%s:%d", pt.sel.short, pt.page)] = pt
	}
	dropped := 0
	var out []types.SearchHit
	seen := map[string]bool{}
	for _, rs := range results {
		for _, h := range rs {
			pt, ok := index[fmt.Sprintf("%s:%d", strings.TrimSpace(h.Doc), h.Page)]
			if !ok && len(sels) == 1 {
				pt, ok = index[fmt.Sprintf("%s:%d", sels[0].short, h.Page)]
			}
			if !ok {
				dropped++
				continue
			}
			lines, boxes, ok := verifyQuote(h, pt.lines, cfg.QuoteMinSimilarity)
			if !ok {
				dropped++
				continue
			}
			hit := hitFromLine(pt.sel.doc, pt.page, lines, h.Quote, boxes, clamp01(h.Relevance), "", pt.sel.paths[pt.page])
			hit.Reason = h.Reason
			if seen[hit.CitationID] {
				continue
			}
			seen[hit.CitationID] = true
			out = append(out, hit)
		}
	}
	return out, dropped, truncated, nil
}

// verifyQuote accepts a hit only when its quote appears in the cited lines
// (or, if the line numbers are off, in neighbouring lines of the same page),
// which drops hallucinated answers (§6.5 step 5).
func verifyQuote(h locHit, lines map[int]postgres.PageLine, minSim float64) ([]int, []types.BBox, bool) {
	q := textutil.Normalize(strings.TrimSuffix(h.Quote, " (?)"))
	if q == "" {
		return nil, nil, false
	}
	check := func(nums []int) bool {
		var parts []string
		for _, n := range nums {
			if l, ok := lines[n]; ok {
				parts = append(parts, l.Text)
			}
		}
		if len(parts) == 0 {
			return false
		}
		joined := textutil.Normalize(strings.Join(parts, " "))
		return strings.Contains(joined, q) || strings.Contains(q, joined) && len(joined) > len(q)/2 ||
			textutil.Similarity(q, joined) >= minSim
	}
	nums := append([]int(nil), h.Lines...)
	sort.Ints(nums)
	if !check(nums) {
		// Search the page for the line window that contains the quote.
		var all []int
		for n := range lines {
			all = append(all, n)
		}
		sort.Ints(all)
		nums = nil
		for w := 1; w <= 4 && nums == nil; w++ {
			for i := 0; i+w <= len(all); i++ {
				if check(all[i : i+w]) {
					nums = append([]int(nil), all[i:i+w]...)
					break
				}
			}
		}
		if nums == nil {
			return nil, nil, false
		}
	}
	boxes := make([]types.BBox, 0, len(nums))
	for _, n := range nums {
		boxes = append(boxes, lines[n].BBox)
	}
	return nums, boxes, true
}

func clamp01(v float64) float64 {
	if v <= 0 {
		return 0.5
	}
	return min(v, 1)
}

// ---- tree view helpers ----

type treeView struct {
	root     types.TreeNode
	byShort  map[string]types.TreeNode
	byID     map[uuid.UUID]types.TreeNode
	children map[uuid.UUID][]types.TreeNode
}

func newTreeView(nodes []types.TreeNode) *treeView {
	t := &treeView{byShort: map[string]types.TreeNode{}, byID: map[uuid.UUID]types.TreeNode{}, children: map[uuid.UUID][]types.TreeNode{}}
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

// initialView shows levels breadth-first while the rendering fits the budget.
func (t *treeView) initialView(budget int) map[string]bool {
	shown := map[string]bool{}
	level := t.children[t.root.ID]
	used := 0
	for len(level) > 0 {
		cost := 0
		for _, n := range level {
			cost += textutil.EstimateTokens(n.Title+n.Summary) + 8
		}
		if used > 0 && used+cost > budget {
			break
		}
		used += cost
		var next []types.TreeNode
		for _, n := range level {
			shown[n.ShortID] = true
			next = append(next, t.children[n.ID]...)
		}
		level = next
	}
	return shown
}

func (t *treeView) render(sb *strings.Builder, shown map[string]bool) {
	var walk func(parent uuid.UUID, depth int)
	walk = func(parent uuid.UUID, depth int) {
		for _, n := range t.children[parent] {
			if !shown[n.ShortID] {
				continue
			}
			more := ""
			if len(t.children[n.ID]) > 0 && !anyShown(t.children[n.ID], shown) {
				more = " +"
			}
			fmt.Fprintf(sb, "%s[%s] %s (tr. %d–%d)%s — %s\n", strings.Repeat("  ", depth), n.ShortID, n.Title, n.PageStart, n.PageEnd, more, textutil.Truncate(n.Summary, 300))
			walk(n.ID, depth+1)
		}
	}
	walk(t.root.ID, 0)
}

func anyShown(ns []types.TreeNode, shown map[string]bool) bool {
	for _, n := range ns {
		if shown[n.ShortID] {
			return true
		}
	}
	return false
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

// ---- other Searcher methods ----

// FindInDocument searches one document, grouped by page (§6.7).
func (s *Service) FindInDocument(ctx context.Context, owner, docID uuid.UUID, query, mode string) ([]types.PageSearchHit, error) {
	d, err := s.st.Documents.GetOwned(ctx, docID, owner)
	if err != nil {
		return nil, ErrNotFound
	}
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("%w: query is required", ErrBadRequest)
	}
	byPage := map[int]*types.PageSearchHit{}
	add := func(page, line int, text string, box types.BBox, score float64) {
		ph := byPage[page]
		if ph == nil {
			ph = &types.PageSearchHit{PageNo: page}
			byPage[page] = ph
		}
		ph.Hits = append(ph.Hits, types.LineMatch{LineNo: line, Snippet: text, BBox: box, Score: score})
		ph.Score = max(ph.Score, score)
	}
	if mode == types.SearchReasoning && s.searchLLM != nil {
		resp, err := s.Search(ctx, types.SearchRequest{Query: query, DocumentIDs: []uuid.UUID{d.ID}, Mode: mode, OwnerID: owner, TopK: 20})
		if err != nil {
			return nil, err
		}
		for _, h := range resp.Hits {
			for i, l := range h.Lines {
				box := types.BBox{}
				if i < len(h.BBoxes) {
					box = h.BBoxes[i]
				}
				add(h.PageNo, l, h.Quote, box, h.Relevance)
			}
		}
	} else {
		lines, err := s.st.Pages.SearchLines(ctx, d.ID, d.Gen, query, 0, 0, 500)
		if err != nil {
			return nil, err
		}
		for _, l := range lines {
			if l.Score >= 0.4 {
				add(l.PageNo, l.LineNo, l.Text, l.BBox, l.Score)
			}
		}
	}
	out := make([]types.PageSearchHit, 0, len(byPage))
	for _, ph := range byPage {
		sort.Slice(ph.Hits, func(i, j int) bool { return ph.Hits[i].LineNo < ph.Hits[j].LineNo })
		out = append(out, *ph)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PageNo < out[j].PageNo })
	return out, nil
}

// DocumentTree returns the stored tree (root first).
func (s *Service) DocumentTree(ctx context.Context, owner, docID uuid.UUID) ([]types.TreeNode, error) {
	d, err := s.st.Documents.GetOwned(ctx, docID, owner)
	if err != nil {
		return nil, ErrNotFound
	}
	return s.st.Index.Tree(ctx, d.ID, d.Gen)
}

// ReadPages returns pages as numbered lines (at most 10 pages), the format
// the agent cites from.
func (s *Service) ReadPages(ctx context.Context, owner, docID uuid.UUID, from, to int) (string, error) {
	d, err := s.st.Documents.GetOwned(ctx, docID, owner)
	if err != nil {
		return "", ErrNotFound
	}
	if from < 1 {
		from = 1
	}
	if to < from {
		to = from
	}
	if to-from >= 10 {
		to = from + 9
	}
	lines, err := s.st.Pages.Lines(ctx, d.ID, from, to)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	cur := -1
	for _, l := range lines {
		if l.PageNo != cur {
			if cur >= 0 {
				sb.WriteString("</page>\n")
			}
			cur = l.PageNo
			fmt.Fprintf(&sb, "<page n=\"%d\" doc=\"%s\">\n", cur, d.ID)
		}
		if strings.TrimSpace(l.Text) != "" {
			fmt.Fprintf(&sb, "[L%d] %s\n", l.LineNo, l.Text)
		}
	}
	if cur >= 0 {
		sb.WriteString("</page>\n")
	}
	return sb.String(), nil
}

// ListDocuments lists documents of a KB by metadata filter.
func (s *Service) ListDocuments(ctx context.Context, owner, kb uuid.UUID, filter types.MetadataFilter, limit int) ([]types.DocumentBrief, error) {
	k, err := s.st.KBs.GetOwned(ctx, kb, owner)
	if err != nil {
		return nil, ErrNotFound
	}
	docs, err := s.st.Documents.List(ctx, postgres.DocumentFilter{
		OwnerID: owner, KBIDs: []uuid.UUID{kb}, Metadata: metadata.NormalizeFilter(k.MetadataSchema, filter), Schema: k.MetadataSchema, Limit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	return briefs(docs), nil
}

// MetadataValues lists distinct values of a metadata key.
func (s *Service) MetadataValues(ctx context.Context, owner, kb uuid.UUID, key string) ([]interfaces.MetadataValue, error) {
	if _, err := s.st.KBs.GetOwned(ctx, kb, owner); err != nil {
		return nil, ErrNotFound
	}
	vals, err := s.st.Documents.MetadataValues(ctx, kb, key, "", 200)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	out := make([]interfaces.MetadataValue, len(vals))
	for i, v := range vals {
		out[i] = interfaces.MetadataValue{Value: v.Value, Count: v.Count}
	}
	return out, nil
}

// Locate resolves a citation id to lines and boxes.
func (s *Service) Locate(ctx context.Context, owner uuid.UUID, citation string) ([]types.SearchHit, error) {
	id, page, lo, hi, err := ParseCitation(citation)
	if err != nil {
		return nil, err
	}
	d, err := s.st.Documents.GetOwned(ctx, id, owner)
	if err != nil {
		return nil, ErrNotFound
	}
	lines, err := s.st.Pages.Lines(ctx, d.ID, page, page)
	if err != nil {
		return nil, err
	}
	var nums []int
	var boxes []types.BBox
	var texts []string
	for _, l := range lines {
		if lo < 0 || (l.LineNo >= lo && l.LineNo <= hi) {
			nums = append(nums, l.LineNo)
			boxes = append(boxes, l.BBox)
			texts = append(texts, l.Text)
		}
	}
	if len(nums) == 0 {
		return nil, ErrNotFound
	}
	return []types.SearchHit{hitFromLine(d, page, nums, strings.Join(texts, "\n"), boxes, 1, "", nil)}, nil
}
