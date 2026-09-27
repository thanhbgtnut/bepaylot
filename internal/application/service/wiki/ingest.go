package wiki

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
)

var errCallBudget = errors.New("wiki.ingest.max_llm_calls reached")

// run tracks one ingest: its LLM call budget and whether it was cut short.
type run struct {
	calls, max atomic.Int64
	partial    atomic.Bool
}

func (s *Service) call(ctx context.Context, r *run, system, user string, out any) error {
	if r.calls.Add(1) > r.max.Load() {
		r.partial.Store(true)
		return errCallBudget
	}
	return s.llm.CompleteJSON(ctx, system, user, out)
}

// ---- extraction (the only LLM step) ----

type xValue struct {
	Value any    `json:"value"`
	At    string `json:"at"`
}

type xEntity struct {
	ID         string            `json:"id"`
	Type       string            `json:"type"`
	Name       string            `json:"name"`
	Aliases    []string          `json:"aliases"`
	Role       string            `json:"role"`
	At         string            `json:"at"`
	Attributes map[string]xValue `json:"attributes"`
}

type xRelation struct {
	From       string         `json:"from"`
	To         string         `json:"to"`
	Type       string         `json:"type"`
	At         string         `json:"at"`
	Attributes map[string]any `json:"attributes"`
}

type extraction struct {
	Entities  []xEntity   `json:"entities"`
	Relations []xRelation `json:"relations"`
}

// loc is a line range of the file that was checked to hold a value.
type loc struct{ page, from, to int }

type foundValue struct {
	value any
	at    loc
}

// fileEntity is one entity of the file after verification and merging.
type fileEntity struct {
	def     *types.WikiEntityType
	name    string
	aliases []string
	roles   []string // what it is in the file ("bên giao thầu"), cited at its name
	at      *loc
	attrs   map[string]foundValue
	order   []string
}

func (e *fileEntity) values() map[string]any {
	out := map[string]any{}
	for k, v := range e.attrs {
		out[k] = v.value
	}
	return out
}

func (e *fileEntity) merge(o *fileEntity) {
	if e.at == nil {
		e.at = o.at
	}
	for _, a := range append([]string{o.name}, o.aliases...) {
		if a != e.name && !contains(e.aliases, a) {
			e.aliases = append(e.aliases, a)
		}
	}
	for _, r := range o.roles {
		if !containsNorm(e.roles, r) {
			e.roles = append(e.roles, r)
		}
	}
	for _, k := range o.order {
		if _, ok := e.attrs[k]; !ok {
			e.attrs[k] = o.attrs[k]
			e.order = append(e.order, k)
		}
	}
}

type fileRelation struct {
	from, to int
	def      *types.WikiRelationType
	at       *loc
	attrs    map[string]any
}

// written is one page an ingest saves.
type written struct {
	page      types.WikiPage
	footnotes []types.WikiFootnote
	links     []linkSpec
	conflicts []string // attribute names that now disagree
	proposal  bool     // user-edited: stored as proposed_content
	isNew     bool
}

type linkSpec struct {
	to         string
	relation   string
	attributes map[string]any
	footnote   *int
}

// ingestOp handles an ingest op: a new generation is folded into the wiki;
// the same generation again (page reparse) re-checks its footnotes.
func (s *Service) ingestOp(ctx context.Context, c types.Case, p types.WikiOpPayload) error {
	d, err := s.docs.GetDocument(ctx, p.DocumentID)
	if err != nil || d.CaseID != c.ID || d.Gen != p.Gen || !types.Searchable(d.Status) {
		return nil // gone, superseded or not searchable: a later op covers it
	}
	if !s.cases.WikiEnabled(c) {
		return s.docs.SetWikiStatus(ctx, d.ID, d.Gen, types.StageSkipped, nil)
	}
	fns, err := s.st.Wiki.CaseFootnotes(ctx, c.ID, &d.ID, d.Gen)
	if err != nil {
		return err
	}
	if len(fns) > 0 {
		return s.recheck(ctx, c, d, fns)
	}
	return s.ingestDoc(ctx, c, d)
}

// extractOn reports whether ingest calls the LLM at all.
func (s *Service) extractOn() bool { return s.llm != nil && s.cfg.Wiki.ExtractEnabled() }

// ingestDoc folds one file into the case wiki (§6.8).
func (s *Service) ingestDoc(ctx context.Context, c types.Case, d types.Document) error {
	return s.ingest(ctx, c, d, s.extractOn())
}

// ingestTemplate folds a file in without the LLM (source page and overview
// only): the fallback when extraction keeps failing.
func (s *Service) ingestTemplate(ctx context.Context, c types.Case, doc uuid.UUID, gen int) error {
	d, err := s.docs.GetDocument(ctx, doc)
	if err != nil || d.CaseID != c.ID || d.Gen != gen {
		return fmt.Errorf("document %s is gone or superseded", doc)
	}
	return s.ingest(ctx, c, d, false)
}

// ingest runs the steps of §6.8: (1) the source page from the index tree,
// (2) one extraction call per file or part, checked line by line, (3)
// entities resolved to pages by code, (4) entity pages and the overview
// rendered by code, (5) one transaction with the log line and the index.
func (s *Service) ingest(ctx context.Context, c types.Case, d types.Document, extract bool) error {
	_ = s.docs.SetWikiStatus(ctx, d.ID, d.Gen, types.StageProcessing, nil)
	schema := s.schemaFor(ctx, c)
	pages, err := s.docs.LoadPages(ctx, d.ID, d.Gen, 0, 0)
	if err != nil {
		return err
	}
	tree, _ := s.secs.Tree(ctx, d.ID, d.Gen)
	lines := linesOf(pages)
	r := &run{}
	r.max.Store(int64(max(s.cfg.Wiki.Ingest.MaxLLMCalls, 1)))

	var ents []*fileEntity
	var rels []fileRelation
	if extract && len(schema.EntityTypes) > 0 {
		if ents, rels, err = s.extract(ctx, r, schema, d, pages, tree, lines); err != nil {
			return err
		}
	}
	existing, err := s.st.Wiki.Pages(ctx, c.ID)
	if err != nil {
		return err
	}
	outs, err := s.compose(ctx, c, schema, d, tree, lines, ents, rels, existing)
	if err != nil {
		return err
	}
	summary, err := s.commit(ctx, c, d, outs, r)
	if err != nil {
		return err
	}
	status := types.StageDone
	if r.partial.Load() {
		status = types.StagePartial
		_, _ = s.st.Wiki.AddIssue(ctx, types.WikiLintIssue{CaseID: c.ID, Kind: types.LintGap,
			Detail: map[string]any{"reason": "ingest hit wiki.ingest.max_llm_calls or max_entities", "document_id": d.ID, "file_name": d.FileName}},
			fmt.Sprintf("gap:partial:%s:%d", d.ID, d.Gen))
	}
	s.log.Info("wiki ingest", "case", c.ID, "doc", d.ID, "summary", summary, "llm_calls", min(r.calls.Load(), r.max.Load()))
	return s.docs.SetWikiStatus(ctx, d.ID, d.Gen, status, nil)
}

// extract asks the LLM for the entities and relations of the file (one
// call; one per part of a big file) and keeps only values found on the
// lines they cite. Entities met in several parts are merged.
func (s *Service) extract(ctx context.Context, r *run, schema types.WikiSchema, d types.Document, pages []*types.ParsedPage,
	tree []types.TreeNode, lines lineTexts) ([]*fileEntity, []fileRelation, error) {
	maxE := max(s.cfg.Wiki.Ingest.MaxEntities, 1)
	system := fmt.Sprintf(promptExtract, firstNonEmpty(schema.Language, "vi"), schema.Conventions, entityTypesText(schema), relationsText(schema), maxE)
	card := fmt.Sprintf("File: %s (%d pages)\nTitle: %s\n\n", d.FileName, d.PageCount, d.Title)
	chunks := s.chunks(pages, tree)
	outs := make([]*extraction, len(chunks))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(max(1, s.cfg.Wiki.Ingest.Parallel))
	for i, ch := range chunks {
		i, ch := i, ch
		g.Go(func() error {
			var out extraction
			if err := s.call(gctx, r, system, card+numberedText(pages, ch[0], ch[1]), &out); err != nil {
				if errors.Is(err, errCallBudget) {
					return nil
				}
				return err
			}
			outs[i] = &out
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, nil, err
	}

	var ents []*fileEntity
	byKey := map[string]int{}
	var rels []fileRelation
	seenRel := map[string]bool{}
	for _, out := range outs {
		if out == nil {
			continue
		}
		local := map[string]int{}
		for _, x := range out.Entities {
			e := verifyEntity(schema, x, lines)
			if e == nil {
				continue
			}
			nameKey := e.def.Name + "|n:" + NormName(e.name)
			idKey := ""
			if k := IdentityKey(e.def, e.values()); k != "" {
				idKey = e.def.Name + "|id:" + k
			}
			i, ok := byKey[idKey]
			if !ok || idKey == "" {
				i, ok = byKey[nameKey]
			}
			if ok {
				ents[i].merge(e)
			} else {
				if len(ents) >= maxE {
					r.partial.Store(true)
					continue
				}
				i = len(ents)
				ents = append(ents, e)
			}
			byKey[nameKey] = i
			if idKey != "" {
				byKey[idKey] = i
			}
			if id := strings.TrimSpace(x.ID); id != "" {
				local[id] = i
			}
		}
		for _, x := range out.Relations {
			from, okF := local[strings.TrimSpace(x.From)]
			to, okT := local[strings.TrimSpace(x.To)]
			rd := schema.Relation(strings.TrimSpace(x.Type))
			if !okF || !okT || from == to || rd == nil || !rd.From.Has(ents[from].def.Name) || !rd.To.Has(ents[to].def.Name) {
				continue
			}
			k := fmt.Sprintf("%d|%d|%s", from, to, rd.Name)
			if seenRel[k] {
				continue
			}
			seenRel[k] = true
			rel := fileRelation{from: from, to: to, def: rd, attrs: x.Attributes}
			if l, ok := lineAt(lines, x.At); ok {
				rel.at = &l
			}
			rels = append(rels, rel)
		}
	}
	return ents, rels, nil
}

// chunks splits a file for extraction: the whole file when it fits
// wiki.ingest.doc_token_budget, else the top branches of its tree (page
// groups without a tree), each cut further when still too big.
func (s *Service) chunks(pages []*types.ParsedPage, tree []types.TreeNode) [][2]int {
	budget := s.cfg.Wiki.Ingest.DocTokenBudget
	if textutil.EstimateTokens(numberedText(pages, 0, 0)) <= budget {
		return [][2]int{{0, 0}}
	}
	var ranges [][2]int
	var root uuid.UUID
	for _, n := range tree {
		if n.ParentID == nil {
			root = n.ID
		}
	}
	for _, n := range tree {
		if n.ParentID != nil && *n.ParentID == root {
			ranges = append(ranges, [2]int{n.PageStart, n.PageEnd})
		}
	}
	if len(ranges) == 0 {
		for p := 1; p <= len(pages); p += 5 {
			ranges = append(ranges, [2]int{p, min(p+4, len(pages))})
		}
	}
	var chunks [][2]int
	for _, rg := range ranges {
		for start := rg[0]; start <= rg[1]; {
			end := start
			for end < rg[1] && textutil.EstimateTokens(numberedText(pages, start, end+1)) <= budget {
				end++
			}
			chunks = append(chunks, [2]int{start, end})
			start = end + 1
		}
	}
	return chunks
}

// verifyEntity keeps an extracted entity when its schema type exists and
// its name or at least one attribute is found on the cited lines.
func verifyEntity(schema types.WikiSchema, x xEntity, lines lineTexts) *fileEntity {
	def := schema.EntityType(strings.TrimSpace(x.Type))
	name := strings.TrimSpace(x.Name)
	if def == nil || name == "" {
		return nil
	}
	e := &fileEntity{def: def, name: name, attrs: map[string]foundValue{}}
	if l, ok := locate(lines, x.At, name); ok {
		e.at = &l
	}
	// The role is a reading of the file, not a value on a line: it is kept
	// only with a verified name, and cited there.
	if r := textutil.Truncate(textutil.CollapseSpace(x.Role), 60); r != "" && e.at != nil {
		e.roles = []string{r}
	}
	for _, a := range x.Aliases {
		if a = strings.TrimSpace(a); a != "" && a != name && !contains(e.aliases, a) {
			e.aliases = append(e.aliases, a)
		}
	}
	keys := make([]string, 0, len(x.Attributes))
	for k := range x.Attributes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := x.Attributes[k]
		ad := def.Attribute(k)
		value := v.Value
		if err := checkAttr(ad, value); err != nil {
			// "0101-234-567" as written, "0101234567" by the schema pattern.
			compact := separators.Replace(valueText(value))
			if ad == nil || ad.Pattern == "" || checkAttr(ad, compact) != nil {
				continue
			}
			value = compact
		}
		var l loc
		var ok bool
		if ad.Type == "bool" {
			l, ok = lineAt(lines, v.At)
		} else {
			l, ok = locate(lines, v.At, valueText(v.Value))
		}
		if ok {
			e.attrs[k] = foundValue{value: value, at: l}
			e.order = append(e.order, k)
		}
	}
	if e.at == nil && len(e.attrs) == 0 {
		return nil // nothing of it is on the page: a guess
	}
	return e
}

var atRe = regexp.MustCompile(`(?i)p\s*(\d+)\s*[:.,]?\s*L\s*(\d+)(?:\s*[-–,]\s*L?\s*(\d+))?`)

// lineAt parses "p<page>:L<a>[-<b>]" and checks the lines exist.
func lineAt(lines lineTexts, at string) (loc, bool) {
	m := atRe.FindStringSubmatch(at)
	if m == nil {
		return loc{}, false
	}
	var l loc
	l.page, _ = strconv.Atoi(m[1])
	l.from, _ = strconv.Atoi(m[2])
	l.to = l.from
	if m[3] != "" {
		l.to, _ = strconv.Atoi(m[3])
	}
	if l.to < l.from || l.to-l.from > 6 {
		l.to = l.from
	}
	page := lines[l.page]
	if page == nil {
		return loc{}, false
	}
	for n := l.from; n <= l.to; n++ {
		if _, ok := page[n]; ok {
			return l, true
		}
	}
	return loc{}, false
}

// locate finds the line of the cited page that holds value: the cited
// lines, the lines around them, then the whole page. A value that is not on
// the page is refused (a hallucinated or misplaced citation).
func locate(lines lineTexts, at, value string) (loc, bool) {
	if strings.TrimSpace(value) == "" {
		return loc{}, false
	}
	m := atRe.FindStringSubmatch(at)
	if m == nil {
		return loc{}, false
	}
	pageNo, _ := strconv.Atoi(m[1])
	from, _ := strconv.Atoi(m[2])
	to := from
	if m[3] != "" {
		to, _ = strconv.Atoi(m[3])
	}
	page := lines[pageNo]
	if page == nil {
		return loc{}, false
	}
	var nums []int
	for n := range page {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	// One line near the citation, nearest first.
	best, bestDist := -1, math.MaxInt
	for _, n := range nums {
		if hasValue(page[n], value) {
			d := 0
			if n < from {
				d = from - n
			} else if n > to {
				d = n - to
			}
			if d < bestDist {
				best, bestDist = n, d
			}
		}
	}
	if best >= 0 {
		return loc{page: pageNo, from: best, to: best}, true
	}
	// A value broken over two consecutive lines.
	for i := 0; i+1 < len(nums); i++ {
		if hasValue(page[nums[i]]+" "+page[nums[i+1]], value) {
			return loc{page: pageNo, from: nums[i], to: nums[i+1]}, true
		}
	}
	return loc{}, false
}

var (
	nonDigit   = regexp.MustCompile(`\D+`)
	separators = strings.NewReplacer(" ", "", ".", "", "-", "", "/", "")
)

// hasValue reports whether text holds value, accents and spacing aside;
// numbers also match on their digits (0101-234-567 = 0101234567).
func hasValue(text, value string) bool {
	t, v := textutil.Normalize(text), textutil.Normalize(value)
	if v == "" {
		return false
	}
	if strings.Contains(t, v) {
		return true
	}
	dv := nonDigit.ReplaceAllString(value, "")
	return len(dv) >= 4 && len(dv)*2 >= len([]rune(strings.TrimSpace(value))) && strings.Contains(nonDigit.ReplaceAllString(text, ""), dv)
}

// valueText renders an extracted value the way a line would show it.
func valueText(v any) string {
	switch x := v.(type) {
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case nil:
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

// ---- composing the pages (code only) ----

// entityWork is one entity page an ingest updates.
type entityWork struct {
	page      types.WikiPage
	def       *types.WikiEntityType
	fns       []types.WikiFootnote // current footnotes plus the new ones
	maxN      int
	links     []linkSpec // typed links out: kept plus new
	conflicts []string
}

// cite returns the footnote number of a line range of d, adding it when new.
func (w *entityWork) cite(d types.Document, lines lineTexts, l loc) int {
	for _, f := range w.fns {
		if f.DocumentID == d.ID && f.Gen == d.Gen && f.PageNo == l.page && f.LineFrom == l.from && f.LineTo == l.to {
			return f.N
		}
	}
	w.maxN++
	w.fns = append(w.fns, types.WikiFootnote{N: w.maxN, DocumentID: d.ID, Gen: d.Gen, PageNo: l.page, LineFrom: l.from, LineTo: l.to,
		Quote: quoteOf(lines, l), CitationID: citationID(d.ID, l.page, l.from, l.to), Status: types.FootnoteValid, FileName: d.FileName})
	return w.maxN
}

func (w *entityWork) citationOf(ns []int) string {
	for _, n := range ns {
		for _, f := range w.fns {
			if f.N == n {
				return f.CitationID
			}
		}
	}
	return ""
}

func (w *entityWork) addLink(l linkSpec) {
	for i, x := range w.links {
		if x.to == l.to && x.relation == l.relation {
			if x.footnote == nil {
				w.links[i].footnote = l.footnote
			}
			return
		}
	}
	w.links = append(w.links, l)
}

func quoteOf(lines lineTexts, l loc) string {
	var parts []string
	for n := l.from; n <= l.to; n++ {
		if t, ok := lines[l.page][n]; ok {
			parts = append(parts, strings.TrimSpace(t))
		}
	}
	return strings.Join(parts, " ")
}

// compose builds every page the ingest writes: the source page of the file,
// the entity pages it touches and the overview.
func (s *Service) compose(ctx context.Context, c types.Case, schema types.WikiSchema, d types.Document, tree []types.TreeNode,
	lines lineTexts, ents []*fileEntity, rels []fileRelation, existing []types.WikiPage) ([]written, error) {
	all := append([]types.WikiPage{}, existing...)
	src := sourcePageOf(c, d, all)
	if src.ID == uuid.Nil {
		all = append(all, src)
	}
	var works []*entityWork
	workOf := make([]*entityWork, len(ents))
	for i, e := range ents {
		w, isNew, err := s.resolveEntity(ctx, c, e, works, &all)
		if err != nil {
			return nil, err
		}
		if isNew {
			works = append(works, w)
		}
		workOf[i] = w
		foldEntity(w, e, d, lines)
	}
	for _, rl := range rels {
		from, to := workOf[rl.from], workOf[rl.to]
		if from == to {
			continue
		}
		var fn *int
		if rl.at != nil {
			n := from.cite(d, lines, *rl.at)
			fn = &n
		}
		from.addLink(linkSpec{to: to.page.Slug, relation: rl.def.Name, attributes: rl.attrs, footnote: fn})
	}

	srcOf := sourcesByDoc(all)
	srcOf[d.ID] = src
	outs := []written{s.sourceWritten(src, d, tree, works)}
	for _, w := range works {
		p := w.page
		p.Content, p.Summary = renderEntity(p, w.def, w.fns, w.links, srcOf)
		wr := written{page: p, footnotes: w.fns, conflicts: w.conflicts, isNew: p.ID == uuid.Nil,
			proposal: p.ID != uuid.Nil && p.LastEditSource == types.EditUser}
		wr.links = append(append(wr.links, w.links...), sourceLinks(w.fns, srcOf)...)
		outs = append(outs, wr)
	}
	outs = append(outs, overviewWritten(c, schema, pagesAfter(all, outs)))
	return outs, nil
}

// resolveEntity finds the page of an entity by code (§6.8): the normalized
// identity first, then a trigram match on the name. Never an embedding. It
// reports isNew when the returned work is not in works yet.
func (s *Service) resolveEntity(ctx context.Context, c types.Case, e *fileEntity, works []*entityWork, all *[]types.WikiPage) (*entityWork, bool, error) {
	key := IdentityKey(e.def, e.values())
	name := NormName(e.name)
	for _, w := range works {
		if w.def.Name != e.def.Name {
			continue
		}
		if (key != "" && w.page.IdentityKey == key) || ((key == "" || w.page.IdentityKey == "") && NormName(w.page.Title) == name) {
			return w, false, nil
		}
	}
	var page *types.WikiPage
	if key != "" {
		if p, err := s.st.Wiki.PageByIdentity(ctx, c.ID, e.def.Name, key); err == nil {
			page = &p
		} else if !errors.Is(err, postgres.ErrNotFound) {
			return nil, false, err
		}
	}
	if page == nil {
		ps, err := s.st.Wiki.SimilarEntities(ctx, c.ID, e.def.Name, name, s.cfg.Wiki.Ingest.NameSimilarity, 3)
		if err != nil {
			return nil, false, err
		}
		for _, p := range ps {
			if key == "" || p.IdentityKey == "" || p.IdentityKey == key {
				p := p
				page = &p
				break
			}
		}
	}
	if page == nil {
		p := types.WikiPage{CaseID: c.ID, Kind: types.WikiKindEntity, EntityType: e.def.Name, IdentityKey: key, Title: e.name,
			Slug: uniqueSlug(entityPrefix(e.def.Name)+"/"+Slugify(e.name), *all)}
		*all = append(*all, p)
		return &entityWork{page: p, def: e.def}, true, nil
	}
	for _, w := range works {
		if w.page.ID == page.ID {
			return w, false, nil
		}
	}
	w := &entityWork{page: *page, def: e.def}
	fns, err := s.st.Wiki.Footnotes(ctx, []uuid.UUID{page.ID})
	if err != nil {
		return nil, false, err
	}
	w.fns = fns[page.ID]
	for _, f := range w.fns {
		w.maxN = max(w.maxN, f.N)
	}
	links, err := s.st.Wiki.Links(ctx, c.ID, &page.ID, "out", "")
	if err != nil {
		return nil, false, err
	}
	for _, l := range links {
		if l.Relation != "" {
			w.links = append(w.links, linkSpec{to: l.To, relation: l.Relation, attributes: l.Attributes, footnote: l.FootnoteN})
		}
	}
	if key != "" && w.page.IdentityKey == "" {
		w.page.IdentityKey = key
	}
	return w, true, nil
}

// foldEntity adds what the file says about an entity to its page: aliases,
// a footnote for where it is named, and attributes. Two sources that
// disagree are both kept and flagged; nothing is decided (§6.8).
func foldEntity(w *entityWork, e *fileEntity, d types.Document, lines lineTexts) {
	for _, a := range append([]string{e.name}, e.aliases...) {
		if a != w.page.Title && !contains(w.page.Aliases, a) {
			w.page.Aliases = append(w.page.Aliases, a)
		}
	}
	attrs := map[string]types.WikiAttribute{}
	for k, v := range w.page.Attributes {
		attrs[k] = v
	}
	if e.at != nil {
		n := w.cite(d, lines, *e.at)
		for _, r := range e.roles {
			addRole(attrs, r, n)
		}
	}
	for _, k := range e.order {
		fv := e.attrs[k]
		n := w.cite(d, lines, fv.at)
		if mergeAttr(attrs, k, fv.value, []int{n}, w.citationOf) {
			w.conflicts = append(w.conflicts, k)
		}
	}
	w.page.Attributes = attrs
	if key := IdentityKey(w.def, attrValues(attrs)); key != "" && w.page.IdentityKey == "" {
		w.page.IdentityKey = key
	}
}

// roleAttr holds the roles of an entity: one history entry per role with
// its footnotes. Roles accumulate across files; they never conflict.
const roleAttr = "vai_tro"

func addRole(attrs map[string]types.WikiAttribute, role string, n int) {
	a := attrs[roleAttr]
	found := false
	for i, h := range a.History {
		if NormName(valueText(h.Value)) == NormName(role) {
			a.History[i].Footnotes = mergeInts(h.Footnotes, []int{n})
			found = true
		}
	}
	if !found {
		a.History = append(a.History, types.WikiAttrValue{Value: role, Footnotes: []int{n}})
	}
	attrs[roleAttr] = rolesValue(a.History)
}

// rolesValue rebuilds the role attribute from its entries.
func rolesValue(hist []types.WikiAttrValue) types.WikiAttribute {
	var vals []string
	var fns []int
	for _, h := range hist {
		vals = append(vals, valueText(h.Value))
		fns = mergeInts(fns, h.Footnotes)
	}
	return types.WikiAttribute{Value: strings.Join(vals, "; "), Footnotes: fns, History: hist}
}

func containsNorm(xs []string, s string) bool {
	for _, x := range xs {
		if NormName(x) == NormName(s) {
			return true
		}
	}
	return false
}

// mergeAttr adds a sourced value to an attribute and reports a new conflict.
func mergeAttr(attrs map[string]types.WikiAttribute, name string, value any, fns []int, cite func([]int) string) bool {
	prev, had := attrs[name]
	switch {
	case !had:
		attrs[name] = types.WikiAttribute{Value: value, Footnotes: fns}
		return false
	case attrValueKey(prev.Value) == attrValueKey(value):
		prev.Footnotes = mergeInts(prev.Footnotes, fns)
		for i, h := range prev.History {
			if attrValueKey(h.Value) == attrValueKey(value) {
				prev.History[i].Footnotes = mergeInts(h.Footnotes, fns)
			}
		}
		attrs[name] = prev
		return false
	}
	for i, h := range prev.History {
		if attrValueKey(h.Value) == attrValueKey(value) {
			prev.History[i].Footnotes = mergeInts(h.Footnotes, fns)
			attrs[name] = prev
			return false
		}
	}
	hist := prev.History
	if len(hist) == 0 {
		hist = []types.WikiAttrValue{{Value: prev.Value, Footnotes: prev.Footnotes, CitationID: cite(prev.Footnotes)}}
	}
	hist = append(hist, types.WikiAttrValue{Value: value, Footnotes: fns, CitationID: cite(fns)})
	attrs[name] = types.WikiAttribute{Value: prev.Value, Footnotes: prev.Footnotes, Conflict: true, History: hist}
	return !prev.Conflict
}

// sourcePageOf returns the source page of the file, new when it has none.
func sourcePageOf(c types.Case, d types.Document, all []types.WikiPage) types.WikiPage {
	for _, p := range all {
		if p.Kind == types.WikiKindSource && p.DocumentID != nil && *p.DocumentID == d.ID {
			return p
		}
	}
	doc := d.ID
	return types.WikiPage{CaseID: c.ID, Kind: types.WikiKindSource, DocumentID: &doc,
		Slug: uniqueSlug("nguon/"+Slugify(strings.TrimSuffix(d.FileName, filepath.Ext(d.FileName))), all)}
}

// sourceWritten renders the source page from the file card and its tree.
func (s *Service) sourceWritten(src types.WikiPage, d types.Document, tree []types.TreeNode, works []*entityWork) written {
	src.Title = firstNonEmpty(d.Title, d.FileName)
	src.Summary = textutil.Truncate(textutil.CollapseSpace(firstNonEmpty(d.Summary, d.Title, d.FileName)), 300)
	var slugs []string
	for _, w := range works {
		slugs = append(slugs, w.page.Slug)
	}
	src.Content = renderSource(d, tree, works)
	w := written{page: src, isNew: src.ID == uuid.Nil, proposal: src.ID != uuid.Nil && src.LastEditSource == types.EditUser}
	for _, slug := range slugs {
		w.links = append(w.links, linkSpec{to: slug})
	}
	return w
}

// overviewWritten renders the overview of the case from its pages.
func overviewWritten(c types.Case, schema types.WikiSchema, pages []types.WikiPage) written {
	ov := types.WikiPage{CaseID: c.ID, Kind: types.WikiKindOverview, Slug: "tong-quan", Title: "Tổng quan hồ sơ " + c.Code}
	if cur := bySlugKind(pages, types.WikiKindOverview); cur != nil {
		ov.ID, ov.Version, ov.LastEditSource, ov.Slug = cur.ID, cur.Version, cur.LastEditSource, cur.Slug
	}
	ov.Content, ov.Summary = renderOverview(c, schema, pages)
	w := written{page: ov, isNew: ov.ID == uuid.Nil, proposal: ov.ID != uuid.Nil && ov.LastEditSource == types.EditUser}
	for _, slug := range wikiLinks(ov.Content) {
		w.links = append(w.links, linkSpec{to: slug})
	}
	return w
}

// sourcesByDoc maps documents to their source pages.
func sourcesByDoc(pages []types.WikiPage) map[uuid.UUID]types.WikiPage {
	out := map[uuid.UUID]types.WikiPage{}
	for _, p := range pages {
		if p.Kind == types.WikiKindSource && p.DocumentID != nil {
			out[*p.DocumentID] = p
		}
	}
	return out
}

// sourceLinks links an entity page to the source pages of the files it is
// cited from.
func sourceLinks(fns []types.WikiFootnote, srcOf map[uuid.UUID]types.WikiPage) []linkSpec {
	var out []linkSpec
	seen := map[string]bool{}
	for _, f := range fns {
		if p, ok := srcOf[f.DocumentID]; ok && !seen[p.Slug] {
			seen[p.Slug] = true
			out = append(out, linkSpec{to: p.Slug})
		}
	}
	return out
}

// pagesAfter is the wiki as it will be after the ingest is saved.
func pagesAfter(all []types.WikiPage, outs []written) []types.WikiPage {
	bySlug := map[string]int{}
	res := append([]types.WikiPage{}, all...)
	for i, p := range res {
		bySlug[p.Slug] = i
	}
	for _, w := range outs {
		if i, ok := bySlug[w.page.Slug]; ok {
			res[i] = w.page
		} else {
			bySlug[w.page.Slug] = len(res)
			res = append(res, w.page)
		}
	}
	return res
}

// commit writes an ingest in one transaction (§6.8): pages, revisions,
// footnotes, links, the log line, a new version and the index.
func (s *Service) commit(ctx context.Context, c types.Case, d types.Document, outs []written, r *run) (string, error) {
	created, updated := 0, 0
	var slugs []string
	for _, w := range outs {
		slugs = append(slugs, w.page.Slug)
		if w.isNew {
			created++
		} else {
			updated++
		}
	}
	summary := fmt.Sprintf("tạo %d trang, cập nhật %d trang", created, updated)
	var conflicts [][2]string
	err := s.st.Wiki.InTx(ctx, func(tx *postgres.WikiRepo) error {
		logID, err := tx.AppendLog(ctx, types.WikiLogEntry{CaseID: c.ID, Op: types.WikiOpIngest, Ref: d.FileName, DocumentID: &d.ID,
			Pages: slugs, Summary: summary, LLMCalls: int(min(r.calls.Load(), r.max.Load()))})
		if err != nil {
			return err
		}
		ids := map[string]uuid.UUID{}
		for i := range outs {
			w := &outs[i]
			if w.proposal {
				content := w.page.Content
				if err := tx.SetProposal(ctx, c.ID, w.page.ID, &content, w.page.Attributes); err != nil {
					return err
				}
				cur, err := tx.Footnotes(ctx, []uuid.UUID{w.page.ID})
				if err != nil {
					return err
				}
				if err := tx.ReplaceFootnotes(ctx, w.page.ID, mergeFootnotes(cur[w.page.ID], w.footnotes)); err != nil {
					return err
				}
				ids[w.page.Slug] = w.page.ID
				continue
			}
			saved, err := tx.SavePage(ctx, w.page, types.EditSystem, nil, &logID)
			if err != nil {
				return fmt.Errorf("save %s: %w", w.page.Slug, err)
			}
			w.page = saved
			ids[saved.Slug] = saved.ID
			if err := tx.ReplaceFootnotes(ctx, saved.ID, w.footnotes); err != nil {
				return err
			}
			for _, a := range w.conflicts {
				conflicts = append(conflicts, [2]string{saved.ID.String(), a})
			}
		}
		for _, w := range outs {
			if w.proposal {
				continue
			}
			if err := tx.ReplaceLinks(ctx, c.ID, w.page.ID, linkRows(ctx, tx, c.ID, ids, w.links)); err != nil {
				return err
			}
		}
		v, err := tx.BumpVersion(ctx, c.ID)
		if err != nil {
			return err
		}
		c.WikiVersion = v
		if err := s.storeIndex(ctx, tx, c); err != nil {
			return err
		}
		covered, err := tx.CoveredDocs(ctx, c.ID)
		if err != nil {
			return err
		}
		return tx.SetCaseWiki(ctx, c.ID, types.WikiBuilding, covered)
	})
	if err != nil {
		return "", err
	}
	for _, cf := range conflicts {
		pid := uuid.MustParse(cf[0])
		_, _ = s.st.Wiki.AddIssue(ctx, types.WikiLintIssue{CaseID: c.ID, Kind: types.LintContradiction, PageIDs: []uuid.UUID{pid},
			Detail: map[string]any{"attribute": cf[1], "document_id": d.ID, "file_name": d.FileName}}, "contradiction:"+cf[0]+":"+cf[1])
	}
	return summary, nil
}

// linkRows resolves link slugs to page ids (pages saved in this
// transaction first); links to missing pages are dropped.
func linkRows(ctx context.Context, tx *postgres.WikiRepo, caseID uuid.UUID, ids map[string]uuid.UUID, links []linkSpec) []postgres.LinkRow {
	var rows []postgres.LinkRow
	seen := map[string]bool{}
	for _, l := range links {
		if seen[l.to+"|"+l.relation] {
			continue
		}
		seen[l.to+"|"+l.relation] = true
		to, ok := ids[l.to]
		if !ok {
			p, err := tx.PageBySlug(ctx, caseID, l.to)
			if err != nil {
				continue
			}
			to = p.ID
		}
		rows = append(rows, postgres.LinkRow{To: to, Relation: l.relation, Attributes: l.attributes, FootnoteN: l.footnote})
	}
	return rows
}

// ---- helpers ----

// numberedText renders pages [from, to] (0 = all) as [p<page>:L<line>] lines.
func numberedText(pages []*types.ParsedPage, from, to int) string {
	var sb strings.Builder
	for _, p := range pages {
		if (from > 0 && p.PageNo < from) || (to > 0 && p.PageNo > to) {
			continue
		}
		fmt.Fprintf(&sb, "<page n=\"%d\">\n", p.PageNo)
		for _, l := range p.Lines {
			if t := strings.TrimSpace(l.Text); t != "" {
				mark := ""
				if l.LowConfidence && l.TextSource == types.TextSourceOCR {
					mark = " (?)"
				}
				fmt.Fprintf(&sb, "[p%d:L%d] %s%s\n", p.PageNo, l.LineNo, t, mark)
			}
		}
		sb.WriteString("</page>\n")
	}
	return sb.String()
}

func entityTypesText(s types.WikiSchema) string {
	var sb strings.Builder
	for _, e := range s.EntityTypes {
		fmt.Fprintf(&sb, "- %s (%s): identity [%s]; attributes: %s\n", e.Name, e.Title, strings.Join(e.Identity, ", "), attrsText(e.Attributes))
	}
	if sb.Len() == 0 {
		return "(none)"
	}
	return sb.String()
}

func attrsText(as []types.AttributeDef) string {
	parts := make([]string, len(as))
	for i, a := range as {
		t := a.Type
		if t == "" {
			t = "string"
		}
		parts[i] = a.Name + ":" + t
		if a.Required {
			parts[i] += "*"
		}
	}
	return strings.Join(parts, ", ")
}

func relationsText(s types.WikiSchema) string {
	var sb strings.Builder
	for _, r := range s.Relations {
		fmt.Fprintf(&sb, "- %s: %s → %s", r.Name, strings.Join(r.From, "|"), strings.Join(r.To, "|"))
		if len(r.Attributes) > 0 {
			fmt.Fprintf(&sb, " (%s)", attrsText(r.Attributes))
		}
		sb.WriteString("\n")
	}
	if sb.Len() == 0 {
		return "(none)"
	}
	return sb.String()
}

func bySlugKind(ps []types.WikiPage, kind string) *types.WikiPage {
	for i := range ps {
		if ps[i].Kind == kind && ps[i].ID != uuid.Nil {
			return &ps[i]
		}
	}
	return nil
}

func uniqueSlug(base string, existing []types.WikiPage) string {
	taken := map[string]bool{}
	for _, p := range existing {
		taken[p.Slug] = true
	}
	slug := base
	for i := 2; taken[slug]; i++ {
		slug = fmt.Sprintf("%s-%d", base, i)
	}
	return slug
}

func mergeFootnotes(a, b []types.WikiFootnote) []types.WikiFootnote {
	seen := map[int]bool{}
	var out []types.WikiFootnote
	for _, f := range append(append([]types.WikiFootnote{}, a...), b...) {
		if !seen[f.N] {
			seen[f.N] = true
			out = append(out, f)
		}
	}
	return out
}

func mergeInts(a, b []int) []int {
	seen := map[int]bool{}
	var out []int
	for _, n := range append(append([]int{}, a...), b...) {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out
}

func attrValues(m map[string]types.WikiAttribute) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v.Value
	}
	return out
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
