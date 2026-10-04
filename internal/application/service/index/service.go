// Package index is Module 2 (§6): sections, the vectorless case tree (files
// with their layout-built trees), and search — case scope and metadata filtering, Postgres
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
	// TreeLLM writes document cards; SearchLLM runs reasoning search.
	// Either may be nil (extractive card / keyword fallback).
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

// tree builds the document tree in code from the layout of the parsed
// pages (TurboOCR title/heading blocks, §6.5): groups of each page, then the
// pages joined in order. The only LLM call is the document card. The file
// is then appended to its case tree; other files of the case are not
// touched. The document is searchable once the tree is stored.
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
	results := map[int]*pageResult{}
	for _, p := range pages {
		pageCount = max(pageCount, p.PageNo)
		firstLines[p.PageNo] = firstLine(p.Markdown)
		results[p.PageNo] = draftResult(PageGroups(p, cfg.MinNodeTokens), cfg.SummaryWords)
	}
	root := BuildTree(pageCount, bookmarksOf(d.PDFInfo), results, firstLines, cfg.SummaryWords)
	assignByPage(root, secs)
	finish(root)
	card := s.card(ctx, d, root, pages, s.treeLLM != nil && s.cfg.Index.TreeLLMEnabled())
	root.title, root.summary = card.Title, card.Summary

	nodes := flatten(root, d.ID, d.Gen, secs)
	FillTreeTokens(nodes)
	if err := s.st.Index.ReplaceTree(ctx, d.ID, d.Gen, nodes); err != nil {
		return err
	}
	if err := s.st.Index.AppendToCaseTree(ctx, d.CaseID, d.ID); err != nil {
		return err
	}
	return s.docs.SetIndexResult(ctx, d.ID, d.Gen, card, nil)
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
