// Package wiki is the wiki half of Module 3 (§7.5): entity pages generated
// from the graph with citations back to page lines, debounced per knowledge
// base, protected against overwriting user edits, and cross-linked.
package wiki

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/config"
	"github.com/thanhenti/bepaylot/internal/queue"
	"github.com/thanhenti/bepaylot/internal/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
	"github.com/thanhenti/bepaylot/internal/types/interfaces"
)

// Errors.
var (
	ErrNotFound = errors.New("not found")
	// ErrBusy means another worker holds the KB's wiki lock; the task retries.
	ErrBusy = errors.New("wiki ingest already running for this knowledge base")
)

// Service generates and serves wiki pages.
type Service struct {
	st  *postgres.Store
	q   queue.Enqueuer
	llm interfaces.Completer // nil → template pages
	cfg *config.Config
	log *slog.Logger
}

// New builds the service.
func New(st *postgres.Store, q queue.Enqueuer, llm interfaces.Completer, cfg *config.Config, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{st: st, q: q, llm: llm, cfg: cfg, log: log.With("module", "wiki")}
}

// Handlers returns the wiki task handlers.
func (s *Service) Handlers() map[string]queue.Handler {
	return map[string]queue.Handler{
		types.TaskWikiIngest:   s.kbHandler(s.Ingest),
		types.TaskWikiFinalize: s.kbHandler(s.Finalize),
	}
}

func (s *Service) kbHandler(fn func(ctx context.Context, kb uuid.UUID) error) queue.Handler {
	return func(ctx context.Context, raw []byte) error {
		var p types.KBTaskPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return fmt.Errorf("%w: %v", queue.ErrSkipRetry, err)
		}
		if _, err := s.st.KBs.Get(ctx, p.KBID); err != nil {
			return nil // KB deleted
		}
		return fn(ctx, p.KBID)
	}
}

// Ingest consumes the KB's pending wiki ops and (re)writes pages of the
// touched entities (§7.5).
func (s *Service) Ingest(ctx context.Context, kb uuid.UUID) error {
	ran, err := s.st.Graph.TryKBLock(ctx, kb, func(ctx context.Context) error {
		ops, err := s.st.Tasks.PeekOps(ctx, types.TaskWikiIngest, types.ScopeKnowledgeBase, kb.String(), 1000)
		if err != nil || len(ops) == 0 {
			return err
		}
		seen := map[uuid.UUID]bool{}
		var ids []uuid.UUID
		var opIDs []int64
		for _, op := range ops {
			opIDs = append(opIDs, op.ID)
			var pl struct {
				Entities []uuid.UUID `json:"entities"`
			}
			_ = json.Unmarshal(op.Payload, &pl)
			for _, id := range pl.Entities {
				if !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
		}
		pageTypes := s.cfg.Wiki.PageTypes
		if pageTypes == nil {
			pageTypes = []string{}
		}
		ents, err := s.st.Graph.WikiCandidates(ctx, kb, ids, s.cfg.Wiki.MinMentions, pageTypes)
		if err != nil {
			return err
		}
		for _, e := range ents {
			if err := s.writePage(ctx, kb, e); err != nil {
				_ = s.st.Tasks.IncrOpFail(ctx, opIDs)
				return err
			}
		}
		if err := s.st.Tasks.DeleteOps(ctx, opIDs); err != nil {
			return err
		}
		return s.q.Enqueue(ctx, types.TaskWikiFinalize, types.KBTaskPayload{KBID: kb},
			queue.Opts{TaskID: fmt.Sprintf("wikifin:%s:%d", kb, time.Now().Unix()/30), ProcessIn: 5 * time.Second})
	})
	if err != nil {
		return err
	}
	if !ran {
		return ErrBusy
	}
	return nil
}

type source struct {
	n        int
	citation string
	file     string
	page     int
	evidence string
}

const promptWiki = `You write a short wiki article about one entity, in the language of the sources (usually Vietnamese).
Rules:
- Use only the facts given (attributes, relations, source quotes). Never add outside knowledge.
- Every sentence ends with one or more footnote markers like [^1] referring to the numbered sources.
- Link related entities with [[slug]] using exactly the slugs given.
- Start with a one-paragraph overview, then short sections if useful. No title line (it is added separately).
Reply with JSON only: {"summary": "<one sentence>", "content": "<markdown body>"}`

// writePage generates or refreshes the page of one entity. Pages edited by a
// user are left untouched (the generator never overwrites human edits).
func (s *Service) writePage(ctx context.Context, kb uuid.UUID, e types.Entity) error {
	existing, err := s.st.Graph.WikiPageByEntity(ctx, e.ID)
	if err == nil && existing.LastEditSource == "user" {
		return nil
	}
	rels, err := s.st.Graph.Relations(ctx, e.ID)
	if err != nil {
		return err
	}
	var otherIDs []uuid.UUID
	for _, r := range rels {
		otherIDs = append(otherIDs, other(r, e.ID))
	}
	others, _ := s.st.Graph.EntitiesByID(ctx, otherIDs)
	byID := map[uuid.UUID]types.Entity{}
	for _, o := range others {
		byID[o.ID] = o
	}
	mentions, err := s.st.Graph.Mentions(ctx, &e.ID, nil, 20)
	if err != nil {
		return err
	}
	var sources []source
	for i, m := range mentions {
		src := source{n: i + 1, file: m.FileName, evidence: m.Evidence}
		if len(m.SourceSpans) > 0 {
			sp := m.SourceSpans[0]
			src.page = sp.Page
			src.citation = fmt.Sprintf("doc:%s:p%d:l%d-%d", m.DocumentID, sp.Page, sp.LineFrom, sp.LineTo)
		} else {
			src.citation = fmt.Sprintf("doc:%s", m.DocumentID)
		}
		sources = append(sources, src)
	}

	slug := s.slugFor(ctx, kb, e, existing)
	summary, body := s.template(e, rels, byID, sources)
	if s.llm != nil {
		if sum, content, err := s.generate(ctx, e, rels, byID, sources); err == nil {
			summary, body = sum, content+"\n\n"+footnotes(sources)
		} else {
			s.log.Warn("wiki generation failed, using template", "entity", e.ID, "err", err)
		}
	}
	var refs []string
	for _, src := range sources {
		refs = append(refs, src.citation)
	}
	id := e.ID
	_, err = s.st.Graph.SaveWikiPage(ctx, types.WikiPage{
		KBID: kb, EntityID: &id, Slug: slug, Title: e.Name, PageType: "entity", Summary: summary,
		Content: "# " + e.Name + "\n\n*" + e.Type + "*\n\n" + body, Aliases: e.Aliases, SourceRefs: refs, LastEditSource: "system",
	}, nil)
	return err
}

func other(r types.Relation, self uuid.UUID) uuid.UUID {
	if r.SourceID == self {
		return r.TargetID
	}
	return r.SourceID
}

func (s *Service) generate(ctx context.Context, e types.Entity, rels []types.Relation, byID map[uuid.UUID]types.Entity, sources []source) (string, string, error) {
	var sb strings.Builder
	attrs, _ := json.Marshal(e.Attributes)
	fmt.Fprintf(&sb, "Entity: %s (%s)\nAttributes: %s\n", e.Name, e.Type, attrs)
	if len(e.Aliases) > 0 {
		fmt.Fprintf(&sb, "Aliases: %s\n", strings.Join(e.Aliases, "; "))
	}
	sb.WriteString("Relations:\n")
	for _, r := range rels {
		o := byID[other(r, e.ID)]
		dir := "→"
		if r.TargetID == e.ID {
			dir = "←"
		}
		fmt.Fprintf(&sb, "- %s %s %s (%s) [[%s]]\n", r.Type, dir, o.Name, o.Type, Slugify(o.Name))
	}
	sb.WriteString("Sources:\n")
	for _, src := range sources {
		fmt.Fprintf(&sb, "[%d] %s (page %d): %q\n", src.n, src.file, src.page, src.evidence)
	}
	var out struct {
		Summary string `json:"summary"`
		Content string `json:"content"`
	}
	if err := s.llm.CompleteJSON(ctx, promptWiki, sb.String(), &out); err != nil {
		return "", "", err
	}
	if strings.TrimSpace(out.Content) == "" {
		return "", "", errors.New("empty wiki content")
	}
	return strings.TrimSpace(out.Summary), strings.TrimSpace(out.Content), nil
}

// template renders a deterministic page when no LLM is configured.
func (s *Service) template(e types.Entity, rels []types.Relation, byID map[uuid.UUID]types.Entity, sources []source) (string, string) {
	var sb strings.Builder
	keys := make([]string, 0, len(e.Attributes))
	for k := range e.Attributes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	cite := ""
	if len(sources) > 0 {
		cite = " [^1]"
	}
	if len(keys) > 0 {
		sb.WriteString("| Thuộc tính | Giá trị |\n|---|---|\n")
		for _, k := range keys {
			fmt.Fprintf(&sb, "| %s | %v%s |\n", k, e.Attributes[k], cite)
		}
		sb.WriteString("\n")
	}
	if len(rels) > 0 {
		sb.WriteString("## Quan hệ\n\n")
		for _, r := range rels {
			o := byID[other(r, e.ID)]
			dir := "→"
			if r.TargetID == e.ID {
				dir = "←"
			}
			fmt.Fprintf(&sb, "- %s %s [[%s]] (%s)\n", r.Type, dir, Slugify(o.Name), o.Name)
		}
		sb.WriteString("\n")
	}
	if len(sources) > 0 {
		sb.WriteString("## Trích dẫn\n\n")
		for _, src := range sources {
			fmt.Fprintf(&sb, "> %s [^%d]\n\n", src.evidence, src.n)
		}
		sb.WriteString(footnotes(sources))
	}
	summary := fmt.Sprintf("%s (%s)", e.Name, e.Type)
	return summary, sb.String()
}

func footnotes(sources []source) string {
	var sb strings.Builder
	for _, src := range sources {
		fmt.Fprintf(&sb, "[^%d]: %s — %s, trang %d\n", src.n, src.citation, src.file, src.page)
	}
	return sb.String()
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify turns a name into a URL slug (accent-free, dash separated).
func Slugify(name string) string {
	s := strings.Trim(slugRe.ReplaceAllString(textutil.Unaccent(name), "-"), "-")
	if s == "" {
		s = "entity"
	}
	return textutil.Truncate(s, 80)
}

func (s *Service) slugFor(ctx context.Context, kb uuid.UUID, e types.Entity, existing *types.WikiPage) string {
	if existing != nil {
		return existing.Slug
	}
	base := Slugify(e.Name)
	slug := base
	for i := 2; ; i++ {
		p, err := s.st.Graph.WikiPage(ctx, kb, slug)
		if err != nil || (p.EntityID != nil && *p.EntityID == e.ID) {
			return slug
		}
		slug = fmt.Sprintf("%s-%s", base, strings.ToLower(e.Type))
		if i > 2 {
			slug = fmt.Sprintf("%s-%d", base, i)
		}
	}
}

var linkRe = regexp.MustCompile(`\[\[([a-z0-9\-]+)\]\]`)

// Finalize recomputes links: out-links from [[slug]] markers (dead ones are
// turned into plain text), in-links from other pages, and the index page.
func (s *Service) Finalize(ctx context.Context, kb uuid.UUID) error {
	pages, err := s.st.Graph.WikiPages(ctx, kb)
	if err != nil {
		return err
	}
	exists := map[string]bool{}
	for _, p := range pages {
		exists[p.Slug] = true
	}
	in := map[string][]string{}
	type upd struct {
		p       types.WikiPage
		content string
		out     []string
	}
	var ups []upd
	for _, p := range pages {
		if p.PageType == "index" {
			continue
		}
		var out []string
		content := linkRe.ReplaceAllStringFunc(p.Content, func(m string) string {
			slug := linkRe.FindStringSubmatch(m)[1]
			if !exists[slug] || slug == p.Slug {
				return slug
			}
			out = append(out, slug)
			return m
		})
		out = uniq(out)
		for _, o := range out {
			in[o] = append(in[o], p.Slug)
		}
		ups = append(ups, upd{p, content, out})
	}
	for _, u := range ups {
		if err := s.st.Graph.SetWikiLinks(ctx, u.p.ID, u.content, uniq(in[u.p.Slug]), u.out); err != nil {
			return err
		}
	}
	// Index page grouped by entity type.
	var entIDs []uuid.UUID
	for _, p := range pages {
		if p.EntityID != nil {
			entIDs = append(entIDs, *p.EntityID)
		}
	}
	ents, err := s.st.Graph.EntitiesByID(ctx, entIDs)
	if err != nil {
		return err
	}
	typeOf := map[uuid.UUID]string{}
	for _, e := range ents {
		typeOf[e.ID] = e.Type
	}
	byType := map[string][]types.WikiPage{}
	var typesSeen []string
	for _, p := range pages {
		if p.PageType != "entity" || p.EntityID == nil {
			continue
		}
		t := typeOf[*p.EntityID]
		if _, ok := byType[t]; !ok {
			typesSeen = append(typesSeen, t)
		}
		byType[t] = append(byType[t], p)
	}
	sort.Strings(typesSeen)
	var sb strings.Builder
	sb.WriteString("# Mục lục\n\n")
	for _, t := range typesSeen {
		fmt.Fprintf(&sb, "## %s\n\n", t)
		for _, p := range byType[t] {
			fmt.Fprintf(&sb, "- [[%s]] — %s\n", p.Slug, p.Title)
		}
		sb.WriteString("\n")
	}
	if len(typesSeen) > 0 {
		_, err = s.st.Graph.SaveWikiPage(ctx, types.WikiPage{KBID: kb, Slug: "index", Title: "Mục lục", PageType: "index", Content: sb.String(), LastEditSource: "system"}, nil)
	}
	return err
}

func uniq(v []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range v {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// ---- read/edit API ----

// Pages lists a KB's pages.
func (s *Service) Pages(ctx context.Context, owner, kb uuid.UUID) ([]types.WikiPage, error) {
	if _, err := s.st.KBs.GetOwned(ctx, kb, owner); err != nil {
		return nil, ErrNotFound
	}
	return s.st.Graph.WikiPages(ctx, kb)
}

// Page returns one page.
func (s *Service) Page(ctx context.Context, owner, kb uuid.UUID, slug string) (*types.WikiPage, error) {
	if _, err := s.st.KBs.GetOwned(ctx, kb, owner); err != nil {
		return nil, ErrNotFound
	}
	p, err := s.st.Graph.WikiPage(ctx, kb, slug)
	if err != nil {
		return nil, ErrNotFound
	}
	return p, nil
}

// Edit stores a user edit; later generations leave the page alone.
func (s *Service) Edit(ctx context.Context, owner, kb uuid.UUID, slug, title, summary, content string) (*types.WikiPage, error) {
	p, err := s.Page(ctx, owner, kb, slug)
	if err != nil {
		return nil, err
	}
	if title != "" {
		p.Title = title
	}
	if summary != "" {
		p.Summary = summary
	}
	p.Content, p.LastEditSource = content, "user"
	saved, err := s.st.Graph.SaveWikiPage(ctx, *p, &owner)
	if err != nil {
		return nil, err
	}
	_ = s.q.Enqueue(ctx, types.TaskWikiFinalize, types.KBTaskPayload{KBID: kb}, queue.Opts{TaskID: fmt.Sprintf("wikifin:%s:%d", kb, time.Now().Unix()/30)})
	return &saved, nil
}

// Revisions lists a page's history.
func (s *Service) Revisions(ctx context.Context, owner, kb uuid.UUID, slug string) ([]postgres.WikiRevision, error) {
	p, err := s.Page(ctx, owner, kb, slug)
	if err != nil {
		return nil, err
	}
	return s.st.Graph.WikiRevisions(ctx, p.ID)
}
