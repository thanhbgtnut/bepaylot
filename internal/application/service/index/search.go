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

// Search implements interfaces.Searcher (§6.6).
func (s *Service) Search(ctx context.Context, req types.SearchRequest) (*types.SearchResponse, error) {
	start := time.Now()
	if req.OwnerID == uuid.Nil {
		return nil, fmt.Errorf("%w: owner required", ErrBadRequest)
	}
	if len(req.CaseIDs) == 0 && len(req.KBIDs) == 0 {
		return nil, fmt.Errorf("%w: case_ids or kb_ids is required", ErrBadRequest)
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

	// Step 1: scope by SQL — case, metadata, status (§6.6).
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
	codes := s.caseCodes(ctx, cands)
	key := s.cacheKey(mode, req, cands)
	if v, ok := s.cache.get(key); ok {
		out := *(v.(*types.SearchResponse))
		out.Trace.Cached = true
		return &out, nil
	}

	if mode == types.SearchReasoning && s.searchLLM == nil {
		mode, resp.Trace.Fallback = types.SearchKeyword, "no_llm_configured"
	}
	if mode == types.SearchReasoning {
		err = s.reasoning(ctx, req, cands, resp)
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
	for i := range resp.Hits {
		resp.Hits[i].CaseCode = codes[resp.Hits[i].CaseID]
	}
	resp.Trace.ElapsedMs = time.Since(start).Milliseconds()
	s.cache.put(key, resp)
	return resp, nil
}

// scope turns the request into a document filter restricted to searchable
// documents of cases (or KBs) the caller owns, with metadata normalized by
// the schema of the case type (single case) or the KB (single KB). Document
// ids and metadata only narrow the scope, never widen it.
func (s *Service) scope(ctx context.Context, req types.SearchRequest) (postgres.DocumentFilter, error) {
	f := postgres.DocumentFilter{
		OwnerID: req.OwnerID, KBIDs: req.KBIDs, CaseIDs: req.CaseIDs, DocumentIDs: req.DocumentIDs,
		Statuses: []string{types.DocCompleted, types.DocPartial},
	}
	for _, id := range req.CaseIDs {
		c, err := s.cases.GetCaseOwned(ctx, req.OwnerID, id)
		if err != nil {
			return f, ErrNotFound
		}
		if len(req.CaseIDs) == 1 {
			f.Schema = s.caseSchema(ctx, c)
		}
	}
	for _, id := range req.KBIDs {
		kb, err := s.st.KBs.GetOwned(ctx, id, req.OwnerID)
		if err != nil {
			return f, ErrNotFound
		}
		if len(req.KBIDs) == 1 && len(req.CaseIDs) == 0 {
			f.Schema = kb.MetadataSchema
		}
	}
	f.Metadata = metadata.NormalizeFilter(f.Schema, req.Metadata)
	// Validate the filter early so bad keys/operators are a 422.
	if _, err := s.st.Documents.List(ctx, postgres.DocumentFilter{OwnerID: req.OwnerID, KBIDs: []uuid.UUID{uuid.Nil}, Metadata: f.Metadata, Schema: f.Schema, Limit: 1}); err != nil &&
		strings.Contains(err.Error(), "metadata filter") {
		return f, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	return f, nil
}

// caseCodes maps the cases of docs to their codes.
func (s *Service) caseCodes(ctx context.Context, docs []types.Document) map[uuid.UUID]string {
	out := map[uuid.UUID]string{}
	for _, d := range docs {
		if _, ok := out[d.CaseID]; ok {
			continue
		}
		out[d.CaseID] = ""
		if c, err := s.cases.GetCase(ctx, d.CaseID); err == nil {
			out[d.CaseID] = c.Code
		}
	}
	return out
}

// cacheKey covers the request, the owner and the generation of every
// document in scope: scopes always name their cases, so two cases never
// share a cached result (§6.6).
func (s *Service) cacheKey(mode string, req types.SearchRequest, docs []types.Document) string {
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
		out[i] = types.DocumentBrief{ID: d.ID, KBID: d.KBID, CaseID: d.CaseID, FileName: d.FileName, Status: d.Status,
			PageCount: d.PageCount, Title: d.Title, Summary: d.Summary, Metadata: d.Metadata}
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
	inRange := func(p int) bool {
		return (req.PageFrom <= 0 || p >= req.PageFrom) && (req.PageTo <= 0 || p <= req.PageTo)
	}
	for _, sh := range secHits {
		d := byID[sh.DocumentID]
		// A section may span pages outside the requested range.
		from, to := sh.PageStart, sh.PageEnd
		if req.PageFrom > 0 {
			from = max(from, req.PageFrom)
		}
		if req.PageTo > 0 {
			to = min(to, req.PageTo)
		}
		lines, _ := s.st.Pages.SearchLines(ctx, d.ID, d.Gen, req.Query, from, to, 3)
		if len(lines) > 0 {
			for _, l := range lines {
				add(hitFromLine(d, l.PageNo, []int{l.LineNo}, l.Text, []types.BBox{l.BBox}, l.Score, sh.Snippet, sh.HeadingPath))
			}
			continue
		}
		for _, sp := range sh.SourceSpans {
			if inRange(sp.Page) {
				add(hitFromLine(d, sp.Page, []int{max(sp.LineFrom, 0)}, textutil.Truncate(stripMD(sh.Content), 200), []types.BBox{sp.BBox}, sh.Score, sh.Snippet, sh.HeadingPath))
				break
			}
		}
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
		DocumentID: d.ID, FileName: d.FileName, CaseID: d.CaseID, Metadata: d.Metadata, PageNo: page, Lines: lines, Via: "keyword",
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

// The line range may be written as one line ("l9" = "l9-9") or with the "l"
// repeated ("l8-l10"): models often do either when citing.
var citationRe = regexp.MustCompile(`^doc:([0-9a-fA-F-]{36}):p(\d+)(?::l(\d+)(?:-l?(\d+))?)?$`)

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
		hi = lo
		if m[4] != "" {
			hi, _ = strconv.Atoi(m[4])
		}
	}
	return id, page, lo, hi, nil
}

// ---- reasoning mode ----

// budget caps the LLM calls of one request and counts their input tokens.
type budget struct {
	used, max, tokens atomic.Int64
}

func (s *Service) call(ctx context.Context, b *budget, system, user string, out any) error {
	if b.used.Add(1) > b.max.Load() {
		return errBudget
	}
	b.tokens.Add(int64(textutil.EstimateTokens(system) + textutil.EstimateTokens(user)))
	return s.searchLLM.CompleteJSON(ctx, system, user, out)
}

// docSel is what step 3 reads of one document: pages of the chosen nodes.
type docSel struct {
	doc   types.Document
	short string
	pages []int
	nodes []string
	paths map[int][]string // page → node path
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
// exact lines, then verifies each quote against the stored lines (§6.6
// steps 3–5).
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
			hit.Reason, hit.Via = h.Reason, "tree"
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
// which drops hallucinated answers (§6.6 step 5).
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

// ---- other Searcher methods ----

// FindInDocument searches one document, grouped by page, optionally within
// pages from..to (§6.8).
func (s *Service) FindInDocument(ctx context.Context, owner, docID uuid.UUID, query, mode string, from, to int) ([]types.PageSearchHit, error) {
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
		// Steps 2–5 on the tree of this file only.
		resp, err := s.Search(ctx, types.SearchRequest{Query: query, CaseIDs: []uuid.UUID{d.CaseID}, DocumentIDs: []uuid.UUID{d.ID}, Mode: mode,
			PageFrom: from, PageTo: to, OwnerID: owner, TopK: 20})
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
		lines, err := s.st.Pages.SearchLines(ctx, d.ID, d.Gen, query, from, to, 500)
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

// PageOverview describes pages from..to of a document (every page when they
// are 0): layout title, the start of the text and the tree section holding
// the page. Previews get shorter as the range grows, to keep long files
// readable in one call.
func (s *Service) PageOverview(ctx context.Context, owner, docID uuid.UUID, from, to int) ([]types.PageOverview, error) {
	d, err := s.st.Documents.GetOwned(ctx, docID, owner)
	if err != nil {
		return nil, ErrNotFound
	}
	from = max(from, 1)
	if to <= 0 || to > d.PageCount {
		to = d.PageCount
	}
	if to < from {
		return []types.PageOverview{}, nil
	}
	pages, err := s.docs.LoadPages(ctx, d.ID, d.Gen, from, to)
	if err != nil {
		return nil, err
	}
	var tree *treeView
	if nodes, err := s.st.Index.Tree(ctx, d.ID, d.Gen); err == nil && len(nodes) > 0 {
		tree = newTreeView(nodes)
	}
	preview := 240
	switch n := to - from + 1; {
	case n > 200:
		preview = 60
	case n > 50:
		preview = 120
	}
	out := make([]types.PageOverview, 0, len(pages))
	for _, p := range sortedPages(pages) {
		o := types.PageOverview{PageNo: p.PageNo, Lines: len(p.Lines), Blank: p.IsBlank,
			Title: pageTitleText(p), Preview: textutil.Truncate(textutil.CollapseSpace(stripMD(p.Markdown)), preview)}
		if tree != nil {
			if path := tree.pathForPage(p.PageNo); len(path) > 1 {
				o.Section = strings.Join(path[1:], " › ") // without the document title
			}
		}
		out = append(out, o)
	}
	return out, nil
}

// pageTitleText returns the first title or heading block of a page.
func pageTitleText(p *types.ParsedPage) string {
	for _, b := range p.Blocks {
		if (b.Type == types.BlockTitle || b.Type == types.BlockHeading) && !b.IsFurniture {
			if t := textutil.CollapseSpace(strings.TrimLeft(strings.TrimSpace(b.Text), "# ")); t != "" {
				return textutil.Truncate(t, 160)
			}
		}
	}
	return ""
}

func sortedPages(pages []*types.ParsedPage) []*types.ParsedPage {
	out := append([]*types.ParsedPage(nil), pages...)
	sort.Slice(out, func(i, j int) bool { return out[i].PageNo < out[j].PageNo })
	return out
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

// ListDocuments lists the documents of one case by metadata filter.
func (s *Service) ListDocuments(ctx context.Context, owner, caseID uuid.UUID, filter types.MetadataFilter, statuses []string, limit int) ([]types.DocumentBrief, error) {
	c, err := s.cases.GetCaseOwned(ctx, owner, caseID)
	if err != nil {
		return nil, ErrNotFound
	}
	schema := s.caseSchema(ctx, c)
	docs, err := s.st.Documents.List(ctx, postgres.DocumentFilter{
		OwnerID: owner, CaseIDs: []uuid.UUID{c.ID}, Metadata: metadata.NormalizeFilter(schema, filter), Schema: schema, Statuses: statuses, Limit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	refs, err := s.caseRefs(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	out := briefs(docs)
	for i := range out {
		if n, ok := refs[out[i].ID]; ok {
			out[i].Ref = fmt.Sprintf("d%d", n)
		}
	}
	// Case tree order; files without a tree yet last, newest first as listed.
	sort.SliceStable(out, func(i, j int) bool {
		a, aok := refs[out[i].ID]
		b, bok := refs[out[j].ID]
		return aok && (!bok || a < b)
	})
	return out, nil
}

func (s *Service) caseSchema(ctx context.Context, c types.Case) *types.MetadataSchema {
	if sc := s.cases.CaseType(c.CaseType).MetadataSchema; sc != nil {
		return sc
	}
	if kb, err := s.st.KBs.Get(ctx, c.KBID); err == nil {
		return kb.MetadataSchema
	}
	return nil
}

// MetadataValues lists distinct values of a metadata key over the documents
// of one case.
func (s *Service) MetadataValues(ctx context.Context, owner, caseID uuid.UUID, key string) ([]interfaces.MetadataValue, error) {
	c, err := s.cases.GetCaseOwned(ctx, owner, caseID)
	if err != nil {
		return nil, ErrNotFound
	}
	vals, err := s.st.Documents.MetadataValues(ctx, c.KBID, &c.ID, key, "", 200)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	out := make([]interfaces.MetadataValue, len(vals))
	for i, v := range vals {
		out[i] = interfaces.MetadataValue{Value: v.Value, Count: v.Count}
	}
	return out, nil
}

// DocumentInCase reports whether a live document the owner can see belongs
// to the case (the agent's per-document tools check it, §8.1).
func (s *Service) DocumentInCase(ctx context.Context, owner, docID, caseID uuid.UUID) (bool, error) {
	_, err := s.st.Documents.GetInCase(ctx, docID, caseID, owner)
	if errors.Is(err, postgres.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// CaseAlive implements interfaces.Searcher.
func (s *Service) CaseAlive(ctx context.Context, owner, caseID uuid.UUID) bool {
	_, err := s.cases.GetCaseOwned(ctx, owner, caseID)
	return err == nil
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
