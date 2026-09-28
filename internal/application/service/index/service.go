// Package index is Module 2 (§6): sections, the vectorless document tree with
// LLM summaries, and search — case scope and metadata filtering, Postgres
// full-text, then PageIndex-style reasoning over the case table of contents
// and the document trees, reading only the pages of the chosen nodes down to
// verified source lines (§6.6). It uses no embeddings and no wiki.
package index

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/config"
	"github.com/thanhenti/bepaylot/internal/queue"
	"github.com/thanhenti/bepaylot/internal/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
	"github.com/thanhenti/bepaylot/internal/types/interfaces"
)

// Deps are the service dependencies.
type Deps struct {
	Store *postgres.Store
	Docs  interfaces.DocumentStore
	Queue queue.Enqueuer
	// TreeLLM builds summaries and cards; SearchLLM runs reasoning search.
	// Either may be nil (extractive summaries / keyword fallback).
	TreeLLM   interfaces.Completer
	SearchLLM interfaces.Completer
	// Cases resolves search scopes and case codes.
	Cases  interfaces.CaseService
	Config *config.Config
	Log    *slog.Logger
}

// Service implements Module 2.
type Service struct {
	st        *postgres.Store
	docs      interfaces.DocumentStore
	q         queue.Enqueuer
	treeLLM   interfaces.Completer
	searchLLM interfaces.Completer
	cases     interfaces.CaseService
	cfg       *config.Config
	log       *slog.Logger
	cache     *ttlCache
}

// New builds the service.
func New(d Deps) *Service {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		st: d.Store, docs: d.Docs, q: d.Queue, treeLLM: d.TreeLLM, searchLLM: d.SearchLLM, cases: d.Cases, cfg: d.Config,
		log: log.With("module", "index"), cache: newTTLCache(d.Config.Search.CacheTTL, 2000),
	}
}

var (
	_ interfaces.SectionReader = (*Service)(nil)
	_ interfaces.Searcher      = (*Service)(nil)
)

// Handlers returns the task handlers owned by Module 2.
func (s *Service) Handlers() map[string]queue.Handler {
	return map[string]queue.Handler{
		types.TaskIndexBuild: s.handle(s.build),
		types.TaskIndexTree:  s.handle(s.tree),
	}
}

func (s *Service) handle(fn func(ctx context.Context, d types.Document) error) queue.Handler {
	return func(ctx context.Context, raw []byte) error {
		var p types.DocTaskPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return fmt.Errorf("%w: bad payload: %v", queue.ErrSkipRetry, err)
		}
		d, err := s.docs.GetDocument(ctx, p.DocumentID)
		if err != nil {
			return nil // gone
		}
		if d.Gen != p.Gen || d.Status == types.DocCancelled || d.Status == types.DocDeleting {
			return nil
		}
		err = fn(ctx, d)
		if err != nil && (errors.Is(err, queue.ErrSkipRetry) || queue.IsFinalAttempt(ctx)) {
			_ = s.docs.SetIndexResult(context.WithoutCancel(ctx), d.ID, d.Gen, interfaces.DocumentCard{}, err)
		}
		return err
	}
}

// Sections implements interfaces.SectionReader.
func (s *Service) Sections(ctx context.Context, doc uuid.UUID, gen int) ([]types.Section, error) {
	return s.st.Index.Sections(ctx, doc, gen)
}

// Tree implements interfaces.SectionReader.
func (s *Service) Tree(ctx context.Context, doc uuid.UUID, gen int) ([]types.TreeNode, error) {
	return s.st.Index.Tree(ctx, doc, gen)
}

// build creates the sections of a freshly assembled document.
func (s *Service) build(ctx context.Context, d types.Document) error {
	pages, err := s.docs.LoadPages(ctx, d.ID, d.Gen, 0, 0)
	if err != nil {
		return err
	}
	secs := BuildSections(d.ID, d.KBID, d.Gen, pages, s.cfg.Index.Section.MaxTokens)
	if err := s.st.Index.ReplaceSections(ctx, d.ID, d.Gen, secs); err != nil {
		return err
	}
	return s.q.Enqueue(ctx, types.TaskIndexTree, types.DocTaskPayload{DocumentID: d.ID, KBID: d.KBID, Gen: d.Gen, Interactive: d.Interactive},
		queue.Opts{TaskID: fmt.Sprintf("tree:%s:%d", d.ID, d.Gen), Interactive: d.Interactive})
}

// tree builds the document tree page by page (§6.5): layout groups of each
// page, one LLM call per page (in parallel) that decides which groups start
// a node and summarizes them, then the page results are joined in code. The
// document is searchable once the tree is stored.
func (s *Service) tree(ctx context.Context, d types.Document) error {
	secs, err := s.st.Index.Sections(ctx, d.ID, d.Gen)
	if err != nil {
		return err
	}
	pages, err := s.docs.LoadPages(ctx, d.ID, d.Gen, 0, 0)
	if err != nil {
		return err
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].PageNo < pages[j].PageNo })
	cfg := s.cfg.Index.Tree
	pageCount := d.PageCount
	firstLines := map[int]string{}
	groups := map[int][]layoutGroup{}
	results := map[int]*pageResult{}
	for _, p := range pages {
		pageCount = max(pageCount, p.PageNo)
		firstLines[p.PageNo] = firstLine(p.Markdown)
		groups[p.PageNo] = PageGroups(p, cfg.MinNodeTokens)
		results[p.PageNo] = draftResult(groups[p.PageNo], cfg.SummaryWords)
	}
	useLLM := s.treeLLM != nil && s.cfg.Index.TreeLLMEnabled()
	if useLLM {
		if err := s.readPages(ctx, pages, groups, results); err != nil {
			return err
		}
	}
	root := BuildTree(pageCount, bookmarksOf(d.PDFInfo), results, firstLines, cfg.SummaryWords)
	assignByPage(root, secs)
	finish(root)
	card := s.card(ctx, d, root, pages, useLLM)
	root.title, root.summary = card.Title, card.Summary

	nodes := flatten(root, d.ID, d.Gen, secs)
	FillTreeTokens(nodes)
	if err := s.st.Index.ReplaceTree(ctx, d.ID, d.Gen, nodes); err != nil {
		return err
	}
	return s.docs.SetIndexResult(ctx, d.ID, d.Gen, card, nil)
}

// readPages makes one LLM call per page with groups, index.tree.concurrency
// at a time. The context of a page comes from the layout drafts of its
// neighbours (open sections and last words of the previous page, headings
// and first words of the next), so pages do not wait for each other. A page
// whose call fails keeps its draft.
func (s *Service) readPages(ctx context.Context, pages []*types.ParsedPage, groups map[int][]layoutGroup, results map[int]*pageResult) error {
	cfg := s.cfg.Index.Tree
	open := openSections(pages, results)
	var mu sync.Mutex
	eg, ectx := errgroup.WithContext(ctx)
	eg.SetLimit(max(1, cfg.Concurrency))
	for i, p := range pages {
		gs := groups[p.PageNo]
		if len(gs) == 0 {
			continue
		}
		var prev, next *types.ParsedPage
		if i > 0 {
			prev = pages[i-1]
		}
		if i+1 < len(pages) {
			next = pages[i+1]
		}
		prompt := pagePrompt(p.PageNo, gs, prev, groups, open, next, cfg.PageTokens)
		eg.Go(func() error {
			var out pageReply
			if err := s.treeLLM.CompleteJSON(ectx, fmt.Sprintf(promptPageNodes, cfg.SummaryWords), prompt, &out); err != nil {
				s.log.Warn("page nodes failed, using layout draft", "page", p.PageNo, "err", err)
				return nil
			}
			r, ok := out.result(gs, cfg.SummaryWords)
			if !ok {
				s.log.Warn("page nodes invalid, using layout draft", "page", p.PageNo)
				return nil
			}
			mu.Lock()
			results[p.PageNo] = r
			mu.Unlock()
			return nil
		})
	}
	_ = eg.Wait()
	return ctx.Err()
}

// openSections is, per page, the path of headings still open at its end
// according to the layout drafts.
func openSections(pages []*types.ParsedPage, drafts map[int]*pageResult) map[int]string {
	out := map[int]string{}
	var stack []pageNode
	for _, p := range pages {
		for _, n := range drafts[p.PageNo].nodes {
			for len(stack) > 0 && stack[len(stack)-1].level >= n.level {
				stack = stack[:len(stack)-1]
			}
			stack = append(stack, n)
		}
		titles := make([]string, len(stack))
		for i, n := range stack {
			titles[i] = textutil.Truncate(n.title, 80)
		}
		out[p.PageNo] = strings.Join(titles, " > ")
	}
	return out
}

// pagePrompt renders one page for promptPageNodes; the page text is cut to
// budget tokens shared between its groups.
func pagePrompt(pageNo int, gs []layoutGroup, prev *types.ParsedPage, groups map[int][]layoutGroup, open map[int]string, next *types.ParsedPage, budget int) string {
	var sb strings.Builder
	if prev == nil {
		sb.WriteString("Previous page: none (first page)\n")
	} else {
		fmt.Fprintf(&sb, "Previous page %d. Open sections: %s\n", prev.PageNo, firstNonEmpty(open[prev.PageNo], "(none)"))
		fmt.Fprintf(&sb, "It ends: %q\n", tail(groupsText(groups[prev.PageNo]), 200))
	}
	share := max(80, budget/len(gs)) * 3 // tokens → runes
	fmt.Fprintf(&sb, "<page n=\"%d\">\n", pageNo)
	for _, g := range gs {
		body := textutil.Truncate(textutil.CollapseSpace(stripMD(g.body)), share)
		if g.heading == "" {
			fmt.Fprintf(&sb, "<g id=%q>%s</g>\n", g.id, body)
		} else {
			fmt.Fprintf(&sb, "<g id=%q heading=%q level=\"%d\">%s</g>\n", g.id, g.heading, g.level, body)
		}
	}
	sb.WriteString("</page>\n")
	if next == nil {
		sb.WriteString("Next page: none (last page)\n")
	} else {
		var hs []string
		for _, g := range groups[next.PageNo] {
			if g.heading != "" && len(hs) < 5 {
				hs = append(hs, textutil.Truncate(g.heading, 80))
			}
		}
		fmt.Fprintf(&sb, "Next page %d. Headings: %s\n", next.PageNo, firstNonEmpty(strings.Join(hs, "; "), "(none)"))
		fmt.Fprintf(&sb, "It starts: %q\n", textutil.Truncate(groupsText(groups[next.PageNo]), 200))
	}
	return sb.String()
}

func groupsText(gs []layoutGroup) string {
	var parts []string
	for _, g := range gs {
		parts = append(parts, g.text())
	}
	return textutil.CollapseSpace(stripMD(strings.Join(parts, " ")))
}

func tail(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "…" + string(r[len(r)-n:])
}

// pageReply is the answer to promptPageNodes.
type pageReply struct {
	Lead  string `json:"lead"`
	Nodes []struct {
		From    string `json:"from"`
		Title   string `json:"title"`
		Level   int    `json:"level"`
		Summary string `json:"summary"`
	} `json:"nodes"`
}

// result checks the reply against the page's groups: nodes must start at
// known groups in page order. Groups before the first node are the lead;
// each node covers the groups up to the next one.
func (r pageReply) result(gs []layoutGroup, words int) (*pageResult, bool) {
	idx := map[string]int{}
	for i, g := range gs {
		idx[g.id] = i
	}
	starts := make([]int, len(r.Nodes))
	for k, n := range r.Nodes {
		i, ok := idx[strings.TrimSpace(n.From)]
		if !ok || (k > 0 && i <= starts[k-1]) {
			return nil, false
		}
		starts[k] = i
	}
	span := func(from, to int) (int, string) {
		tok, parts := 0, []string{}
		for _, g := range gs[from:to] {
			tok += g.tokens
			parts = append(parts, g.text())
		}
		return tok, strings.Join(parts, "\n")
	}
	out := &pageResult{}
	first := len(gs)
	if len(starts) > 0 {
		first = starts[0]
	}
	if first > 0 {
		tok, text := span(0, first)
		out.leadTokens = tok
		out.leadSummary = firstNonEmpty(firstWords(r.Lead, words), extractiveSummary(text, words))
	}
	for k, n := range r.Nodes {
		to := len(gs)
		if k+1 < len(starts) {
			to = starts[k+1]
		}
		tok, text := span(starts[k], to)
		g := gs[starts[k]]
		title := firstNonEmpty(strings.TrimSpace(n.Title), g.heading, firstWords(text, 12))
		out.nodes = append(out.nodes, pageNode{
			title: textutil.Truncate(textutil.CollapseSpace(title), 120), level: min(max(n.Level, 1), 6), tokens: tok,
			summary: firstNonEmpty(firstWords(n.Summary, words), extractiveSummary(text, words)),
		})
	}
	return out, true
}

// card builds the document card from children summaries and metadata.
func (s *Service) card(ctx context.Context, d types.Document, root *node, pages []*types.ParsedPage, useLLM bool) interfaces.DocumentCard {
	fallback := interfaces.DocumentCard{
		Title:   firstNonEmpty(strFrom(d.PDFInfo, "Title"), firstTitle(pages), d.FileName),
		Summary: extractiveSummary(firstPageText(pages), s.cfg.Index.Tree.CardSummaryWords),
	}
	if !useLLM {
		return fallback
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "File: %s\nPages: %d\n", d.FileName, d.PageCount)
	if len(d.Metadata) > 0 {
		b, _ := json.Marshal(d.Metadata)
		fmt.Fprintf(&sb, "Metadata: %s\n", b)
	}
	if t := strFrom(d.PDFInfo, "Title"); t != "" {
		fmt.Fprintf(&sb, "PDF title: %s\n", t)
	}
	sb.WriteString("First page:\n" + textutil.Truncate(firstPageText(pages), 2500) + "\n\nParts:\n")
	for _, c := range root.children {
		fmt.Fprintf(&sb, "- %s (pages %d-%d): %s\n", c.title, c.pageStart, c.pageEnd, c.summary)
	}
	var out interfaces.DocumentCard
	var raw struct {
		Title   string `json:"title"`
		Summary string `json:"summary"`
	}
	if err := s.treeLLM.CompleteJSON(ctx, fmt.Sprintf(promptCard, s.cfg.Index.Tree.CardSummaryWords), sb.String(), &raw); err != nil {
		s.log.Warn("card failed, using fallback", "doc", d.ID, "err", err)
		return fallback
	}
	out.Title = firstNonEmpty(strings.TrimSpace(raw.Title), fallback.Title)
	out.Summary = firstNonEmpty(strings.TrimSpace(raw.Summary), fallback.Summary)
	return out
}

func bookmarksOf(info map[string]any) []types.PDFBookmark {
	raw, ok := info["bookmarks"]
	if !ok {
		return nil
	}
	b, _ := json.Marshal(raw)
	var out []types.PDFBookmark
	_ = json.Unmarshal(b, &out)
	return out
}

func strFrom(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func stripMD(s string) string {
	return strings.NewReplacer("#", "", "|", " ", "*", "", "> ", "", "---", "").Replace(s)
}

func firstLine(md string) string {
	for _, l := range strings.Split(md, "\n") {
		if t := strings.TrimSpace(stripMD(l)); t != "" && !strings.HasPrefix(t, "![") {
			return t
		}
	}
	return ""
}

func firstTitle(pages []*types.ParsedPage) string {
	for _, p := range pages {
		for _, b := range p.Blocks {
			if b.Type == types.BlockTitle && strings.TrimSpace(b.Text) != "" {
				return textutil.CollapseSpace(b.Text)
			}
		}
	}
	return ""
}

func firstPageText(pages []*types.ParsedPage) string {
	sort.Slice(pages, func(i, j int) bool { return pages[i].PageNo < pages[j].PageNo })
	for _, p := range pages {
		if strings.TrimSpace(p.Markdown) != "" {
			return stripMD(p.Markdown)
		}
	}
	return ""
}
