// Package index is Module 2 (§6): sections, the vectorless document tree with
// LLM summaries, and search — metadata filtering, Postgres full-text and
// PageIndex-style reasoning over the tree. It uses no embeddings.
package index

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/google/uuid"

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
	Config    *config.Config
	Log       *slog.Logger
}

// Service implements Module 2.
type Service struct {
	st        *postgres.Store
	docs      interfaces.DocumentStore
	q         queue.Enqueuer
	treeLLM   interfaces.Completer
	searchLLM interfaces.Completer
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
		st: d.Store, docs: d.Docs, q: d.Queue, treeLLM: d.TreeLLM, searchLLM: d.SearchLLM, cfg: d.Config,
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

// tree builds the document tree, node summaries and the document card, then
// hands the document to Module 3 when graph extraction is enabled.
func (s *Service) tree(ctx context.Context, d types.Document) error {
	secs, err := s.st.Index.Sections(ctx, d.ID, d.Gen)
	if err != nil {
		return err
	}
	pages, err := s.docs.LoadPages(ctx, d.ID, d.Gen, 0, 0)
	if err != nil {
		return err
	}
	titles := map[int]string{}
	for _, p := range pages {
		titles[p.PageNo] = firstLine(p.Markdown)
	}
	bms := bookmarksOf(d.PDFInfo)
	root, origin := BuildSkeleton(d.PageCount, bms, secs, titles, SkeletonOptions{FlatMaxPages: s.cfg.Index.Tree.FlatMaxPages})
	useLLM := s.treeLLM != nil && s.cfg.Index.TreeLLMEnabled()
	if origin == "page" && d.PageCount > s.cfg.Index.Tree.FlatMaxPages && useLLM && d.PageCount <= 400 {
		if toc := s.proposeTOC(ctx, pages); len(toc) >= 2 {
			root.children = toc
			for _, c := range root.children {
				c.sections = nil
			}
			assignByPage(root, secs)
			root = finish(root, secs)
		}
	}
	root.title = firstNonEmpty(d.Title, strFrom(d.PDFInfo, "Title"), d.FileName)

	pageText := map[int]string{}
	for _, p := range pages {
		pageText[p.PageNo] = stripMD(p.Markdown)
	}
	s.summarize(ctx, root, secs, pageText, useLLM)
	card := s.card(ctx, d, root, pages, useLLM)
	root.title, root.summary = card.Title, card.Summary

	if err := s.st.Index.ReplaceTree(ctx, d.ID, d.Gen, flatten(root, d.ID, d.Gen, secs)); err != nil {
		return err
	}
	if err := s.docs.SetIndexResult(ctx, d.ID, d.Gen, card, nil); err != nil {
		return err
	}
	cur, err := s.docs.GetDocument(ctx, d.ID)
	if err == nil && cur.Status == types.DocEnriching {
		return s.q.Enqueue(ctx, types.TaskGraphExtract, types.DocTaskPayload{DocumentID: d.ID, KBID: d.KBID, Gen: d.Gen},
			queue.Opts{TaskID: fmt.Sprintf("gx:%s:%d", d.ID, d.Gen)})
	}
	return nil
}

func (s *Service) proposeTOC(ctx context.Context, pages []*types.ParsedPage) []*node {
	var sb strings.Builder
	for _, p := range pages {
		fmt.Fprintf(&sb, "[page %d] %s\n", p.PageNo, textutil.Truncate(textutil.CollapseSpace(stripMD(p.Markdown)), 160))
	}
	var out struct {
		TOC []TOCEntry `json:"toc"`
	}
	if err := s.treeLLM.CompleteJSON(ctx, promptTOC, sb.String(), &out); err != nil {
		s.log.Warn("toc proposal failed", "err", err)
		return nil
	}
	return FromTOC(out.TOC, len(pages))
}

// summarize fills node summaries bottom-up; leaves from their text, parents
// from their children. LLM calls are batched; failures fall back to
// extractive summaries.
func (s *Service) summarize(ctx context.Context, root *node, secs []types.Section, pageText map[int]string, useLLM bool) {
	words := s.cfg.Index.Tree.SummaryWords
	levels := [][]*node{}
	var walk func(n *node, depth int)
	walk = func(n *node, depth int) {
		if len(levels) <= depth {
			levels = append(levels, nil)
		}
		levels[depth] = append(levels[depth], n)
		for _, c := range n.children {
			walk(c, depth+1)
		}
	}
	walk(root, 0)
	for depth := len(levels) - 1; depth >= 1; depth-- {
		type item struct {
			n    *node
			text string
		}
		var items []item
		for _, n := range levels[depth] {
			text := nodeText(n, secs, 1500)
			if declaredRange(n.origin) && len(n.children) == 0 {
				// A page-range leaf reads its own pages: its sections may
				// start earlier and span several leaves.
				var parts []string
				for p := n.pageStart; p <= n.pageEnd && textutil.EstimateTokens(strings.Join(parts, "\n")) < 1500; p++ {
					parts = append(parts, pageText[p])
				}
				text = strings.Join(parts, "\n")
			}
			if len(n.children) > 0 {
				var parts []string
				for _, c := range n.children {
					parts = append(parts, fmt.Sprintf("- %s: %s", c.title, c.summary))
				}
				text = strings.Join(parts, "\n") + "\n" + textutil.Truncate(text, 1500)
			}
			items = append(items, item{n, text})
		}
		if !useLLM {
			for _, it := range items {
				it.n.summary = extractiveSummary(it.text, words)
			}
			continue
		}
		// Batch up to ~12k tokens per call.
		for start := 0; start < len(items); {
			end, budget := start, 0
			for end < len(items) && (end == start || budget+textutil.EstimateTokens(items[end].text) < 12000) {
				budget += textutil.EstimateTokens(items[end].text)
				end++
			}
			var sb strings.Builder
			for _, it := range items[start:end] {
				fmt.Fprintf(&sb, "<part id=%q title=%q pages=\"%d-%d\">\n%s\n</part>\n", it.n.shortID, it.n.title, it.n.pageStart, it.n.pageEnd, it.text)
			}
			var out struct {
				Summaries map[string]string `json:"summaries"`
			}
			err := s.treeLLM.CompleteJSON(ctx, fmt.Sprintf(promptSummarize, words), sb.String(), &out)
			for _, it := range items[start:end] {
				if sum := strings.TrimSpace(out.Summaries[it.n.shortID]); err == nil && sum != "" {
					it.n.summary = sum
				} else {
					it.n.summary = extractiveSummary(it.text, words)
				}
			}
			if err != nil {
				s.log.Warn("summaries failed, using extractive", "err", err)
			}
			start = end
		}
	}
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
		DocType string `json:"doc_type"`
		Summary string `json:"summary"`
	}
	if err := s.treeLLM.CompleteJSON(ctx, fmt.Sprintf(promptCard, s.cfg.Index.Tree.CardSummaryWords), sb.String(), &raw); err != nil {
		s.log.Warn("card failed, using fallback", "doc", d.ID, "err", err)
		return fallback
	}
	out.Title = firstNonEmpty(strings.TrimSpace(raw.Title), fallback.Title)
	out.DocType = strings.TrimSpace(raw.DocType)
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
