package docmodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/config"
	"github.com/thanhenti/bepaylot/internal/llm"
	"github.com/thanhenti/bepaylot/internal/queue"
	"github.com/thanhenti/bepaylot/internal/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
	"github.com/thanhenti/bepaylot/internal/types/interfaces"
)

// Errors the HTTP layer maps to status codes.
var (
	ErrNoLabels   = errors.New("this case type has no classification labels")
	ErrBadSplit   = errors.New("invalid split")
	ErrCaseClosed = errors.New("case not found")
)

// SplitDeps are the collaborators of the split service.
type SplitDeps struct {
	Store  *postgres.Store
	Queue  queue.Enqueuer
	Cases  interfaces.CaseService
	LLM    interfaces.Completer
	Config *config.Config
	Log    *slog.Logger
}

// Split proposes and stores how the files of a case are split into
// documents (segments, §6.9.4) and grouped into bundles (§6.9.7): code finds
// cut hints, one LLM call per file reads only titles to label the segments,
// code groups bundles, and the user reviews.
type Split struct {
	st    *postgres.Store
	q     queue.Enqueuer
	cases interfaces.CaseService
	llm   interfaces.Completer
	cfg   *config.Config
	log   *slog.Logger
}

// NewSplit builds the service.
func NewSplit(d SplitDeps) *Split {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &Split{st: d.Store, q: d.Queue, cases: d.Cases, llm: d.LLM, cfg: d.Config, log: log}
}

// Handlers returns the task handlers.
func (s *Split) Handlers() map[string]queue.Handler {
	return map[string]queue.Handler{types.TaskDocClassify: s.handleClassify}
}

// Classify queues document:classify for the indexed files of a case (all
// when docs is empty). It returns how many were queued.
func (s *Split) Classify(ctx context.Context, user types.User, caseID uuid.UUID, docs []uuid.UUID, mode string) (int, error) {
	c, err := s.cases.GetCaseOwned(ctx, user.ID, caseID)
	if err != nil {
		return 0, err
	}
	if len(s.cases.CaseType(c.CaseType).Classification.Labels) == 0 {
		return 0, ErrNoLabels
	}
	if mode != "pages" {
		mode = "titles"
	}
	want := map[uuid.UUID]bool{}
	for _, d := range docs {
		want[d] = true
	}
	list, err := s.st.Segments.SplitDocs(ctx, caseID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, d := range list {
		if d.Seq == 0 || (len(want) > 0 && !want[d.ID]) {
			continue
		}
		if err := s.st.Segments.SetClassifyStatus(ctx, d.ID, types.ClassifyRunning); err != nil {
			return n, err
		}
		if err := s.q.Enqueue(ctx, types.TaskDocClassify, types.ClassifyTaskPayload{DocumentID: d.ID, Mode: mode, UserID: user.ID},
			queue.Opts{TaskID: fmt.Sprintf("classify:%s:%d", d.ID, time.Now().UnixNano()), Interactive: true}); err != nil {
			_ = s.st.Segments.SetClassifyStatus(ctx, d.ID, types.ClassifyFailed)
			return n, err
		}
		n++
	}
	return n, nil
}

// pageInfo is what classification sees of one page.
type pageInfo struct {
	titles []string
	mark   string
}

// cut hints (§6.9.4): a page number back to 1.
var pageOneRe = regexp.MustCompile(`(?i)^\s*(?:trang|page|tr\.)?\s*[-–(]?\s*1\s*(?:/|of|trên)\s*(\d+)\s*[-–)]?\s*$|^\s*[-–]\s*1\s*[-–]\s*$`)

// Marks finds the cut hints of a document by code: a page whose number goes
// back to 1, a blank page between pages with content, and a change of page
// size or orientation. Keys are page numbers.
func (s *Split) Marks(ctx context.Context, doc types.Document) (map[int]string, error) {
	pages, err := s.st.Pages.List(ctx, doc.ID, doc.Gen, 1, doc.PageCount)
	if err != nil {
		return nil, err
	}
	lines, err := s.st.Pages.Lines(ctx, doc.ID, 1, doc.PageCount)
	if err != nil {
		return nil, err
	}
	byPage := map[int][]string{}
	for _, l := range lines {
		byPage[l.PageNo] = append(byPage[l.PageNo], strings.TrimSpace(l.Text))
	}
	return marksOf(pages, byPage), nil
}

func marksOf(pages []types.DocumentPage, lines map[int][]string) map[int]string {
	out := map[int]string{}
	for i, p := range pages {
		if i == 0 {
			continue
		}
		var reason []string
		ls := lines[p.PageNo]
		// Page numbers sit in the first or last lines of a page.
		edge := ls
		if len(ls) > 4 {
			edge = append(append([]string{}, ls[:2]...), ls[len(ls)-2:]...)
		}
		for _, l := range edge {
			if pageOneRe.MatchString(l) {
				reason = append(reason, "trang về 1 ("+strings.TrimSpace(l)+")")
				break
			}
		}
		prev := pages[i-1]
		if prev.IsBlank && !p.IsBlank && i > 1 {
			reason = append(reason, "sau trang trắng")
		}
		if sizeChanged(prev, p) {
			reason = append(reason, "đổi khổ giấy")
		}
		if len(reason) > 0 {
			out[p.PageNo] = strings.Join(reason, ", ")
		}
	}
	return out
}

func sizeChanged(a, b types.DocumentPage) bool {
	w1, h1, w2, h2 := a.WidthPt, a.HeightPt, b.WidthPt, b.HeightPt
	if w1 <= 0 || h1 <= 0 || w2 <= 0 || h2 <= 0 {
		return false
	}
	if (w1 > h1) != (w2 > h2) {
		return true
	}
	return math.Abs(w1*h1-w2*h2)/(w1*h1) > 0.25
}

const promptClassify = `You split a scanned file into the documents it contains and label each one.
Allowed labels (name: title — description):
%s
- other: a page that is none of the above
Input: for each page, its titles as [p<page>] text, and the cut hints found by code as [p<page>] ✂ reason (the page number went back to 1, a blank separator page before it, the paper size changed). A document starts at a page whose title opens a document; a cut hint usually means a new document starts on that page.
Return JSON only: {"segments":[{"pages":"<first>-<last>","label":"<name>","confidence":0.0-1.0}]} covering the pages in order, without overlaps. Pages after a document's first page that have no title belong to that document.`

func (s *Split) handleClassify(ctx context.Context, raw []byte) error {
	var p types.ClassifyTaskPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("%w: %v", queue.ErrSkipRetry, err)
	}
	doc, err := s.st.Documents.Get(ctx, p.DocumentID)
	if errors.Is(err, postgres.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	fail := func(err error) error {
		if queue.IsFinalAttempt(ctx) || errors.Is(err, queue.ErrSkipRetry) {
			_ = s.st.Segments.SetClassifyStatus(context.WithoutCancel(ctx), doc.ID, types.ClassifyFailed)
		}
		return err
	}
	c, err := s.cases.GetCase(ctx, doc.CaseID)
	if err != nil {
		return fail(fmt.Errorf("%w: %v", queue.ErrSkipRetry, err))
	}
	rule := s.cases.CaseType(c.CaseType).Classification
	if len(rule.Labels) == 0 {
		return s.st.Segments.SetClassifyStatus(ctx, doc.ID, types.ClassifySkipped)
	}
	input, err := s.classifyInput(ctx, doc, p.Mode)
	if err != nil {
		return fail(err)
	}
	if strings.TrimSpace(input) == "" { // titles mode, no title anywhere: nothing to send
		return fail(s.st.Segments.SaveProposals(ctx, c.ID, doc.ID, doc.Gen, nil, p.Mode, ""))
	}
	var labels strings.Builder
	for _, l := range rule.Labels {
		fmt.Fprintf(&labels, "- %s: %s", l.Name, l.Title)
		if l.Description != "" {
			fmt.Fprintf(&labels, " — %s", l.Description)
		}
		labels.WriteString("\n")
	}
	user := p.UserID
	ctx = llm.WithCaller(ctx, llm.Caller{UserID: &user, CaseID: &c.ID, Kind: types.UsageParse})
	var out struct {
		Segments []struct {
			Pages      string  `json:"pages"`
			Label      string  `json:"label"`
			Confidence float64 `json:"confidence"`
		} `json:"segments"`
	}
	if err := s.llm.CompleteJSON(ctx, fmt.Sprintf(promptClassify, strings.TrimRight(labels.String(), "\n")), input, &out); err != nil {
		return fail(err)
	}
	min := rule.MinConfidence
	if min <= 0 {
		min = s.cfg.Classify.MinConfidence
	}
	var segs []types.Segment
	for _, o := range out.Segments {
		a, b, ok := parsePages(o.Pages, doc.PageCount)
		if !ok {
			continue
		}
		g := types.Segment{PageStart: a, PageEnd: b, Label: strings.TrimSpace(o.Label), Confidence: math.Max(0, math.Min(1, o.Confidence))}
		if !rule.HasLabel(g.Label) {
			g.ProposedLabel, g.Label = g.Label, types.LabelUnknown
		} else if g.Confidence < min && g.Label != types.LabelOther {
			g.ProposedLabel, g.Label = g.Label, types.LabelUnknown
		}
		segs = append(segs, g)
	}
	return fail(s.st.Segments.SaveProposals(ctx, c.ID, doc.ID, doc.Gen, dropOverlaps(segs), p.Mode, ""))
}

// classifyInput is what the LLM sees (§6.9.4): titles (title/heading
// elements, at most classify.titles_per_page per page) and cut hints; mode
// pages adds the first lines of every page. The first page and a page with a
// cut hint but no title show their first line, so the LLM can name what
// starts there.
func (s *Split) classifyInput(ctx context.Context, doc types.Document, mode string) (string, error) {
	blocks, err := s.st.Pages.Blocks(ctx, doc.ID, 1, doc.PageCount)
	if err != nil {
		return "", err
	}
	lines, err := s.st.Pages.Lines(ctx, doc.ID, 1, doc.PageCount)
	if err != nil {
		return "", err
	}
	first := map[int][]string{}
	for _, l := range lines {
		if t := strings.TrimSpace(l.Text); t != "" {
			first[l.PageNo] = append(first[l.PageNo], t)
		}
	}
	marks := map[int]string{}
	if s.cfg.Classify.PageMarks {
		if marks, err = s.Marks(ctx, doc); err != nil {
			return "", err
		}
	}
	info := map[int]*pageInfo{}
	get := func(p int) *pageInfo {
		if info[p] == nil {
			info[p] = &pageInfo{}
		}
		return info[p]
	}
	for _, b := range blocks {
		if b.IsFurniture || (b.Type != types.BlockTitle && b.Type != types.BlockHeading) {
			continue
		}
		pi := get(b.PageNo)
		if t := strings.TrimSpace(b.Text); t != "" && len(pi.titles) < s.cfg.Classify.TitlesPerPage {
			pi.titles = append(pi.titles, textutil.Truncate(t, 160))
		}
	}
	for p, m := range marks {
		get(p).mark = m
	}
	// The first page, and a page with a cut hint, open a document: without a
	// title element its first line stands for one.
	for p := range info {
		if pi := info[p]; pi.mark != "" && len(pi.titles) == 0 && len(first[p]) > 0 {
			pi.titles = []string{textutil.Truncate(first[p][0], 160)}
		}
	}
	if pi := get(1); len(pi.titles) == 0 && len(first[1]) > 0 {
		pi.titles = []string{textutil.Truncate(first[1][0], 160)}
	}
	pagesMode := mode == "pages"
	var b strings.Builder
	for p := 1; p <= doc.PageCount; p++ {
		pi := info[p]
		if pagesMode {
			n := s.cfg.Classify.LinesPerPage
			ls := first[p]
			if len(ls) > n {
				ls = ls[:n]
			}
			for _, l := range ls {
				fmt.Fprintf(&b, "[p%d] %s\n", p, textutil.Truncate(l, 160))
			}
		} else if pi != nil {
			for _, t := range pi.titles {
				fmt.Fprintf(&b, "[p%d] %s\n", p, t)
			}
		}
		if pi != nil && pi.mark != "" {
			fmt.Fprintf(&b, "[p%d] ✂ %s\n", p, pi.mark)
		}
	}
	if b.Len() == 0 {
		return "", nil
	}
	if pagesMode && b.Len()/3 > s.cfg.Classify.DocTokenBudget {
		return s.classifyInput(ctx, doc, "titles")
	}
	return fmt.Sprintf("File: %s (%d pages)\n%s", doc.FileName, doc.PageCount, b.String()), nil
}

func parsePages(v string, n int) (int, int, bool) {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "p"))
	a, b, found := strings.Cut(v, "-")
	x, err := strconv.Atoi(strings.TrimSpace(a))
	if err != nil {
		return 0, 0, false
	}
	y := x
	if found {
		if y, err = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(b), "p"))); err != nil {
			return 0, 0, false
		}
	}
	if x < 1 {
		x = 1
	}
	if y > n {
		y = n
	}
	return x, y, x <= y
}

// dropOverlaps keeps the more confident of overlapping segments (§6.9.4).
func dropOverlaps(in []types.Segment) []types.Segment {
	sort.SliceStable(in, func(i, j int) bool { return in[i].Confidence > in[j].Confidence })
	var out []types.Segment
	for _, g := range in {
		ok := true
		for _, k := range out {
			if g.PageStart <= k.PageEnd && g.PageEnd >= k.PageStart {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PageStart < out[j].PageStart })
	return out
}

// SplitFile is one file of the split screen (§7.9).
type SplitFile struct {
	DocumentID     uuid.UUID       `json:"document_id"`
	FileName       string          `json:"file_name"`
	PageCount      int             `json:"page_count"`
	Status         string          `json:"status"` // none | proposed | reviewed
	ClassifyStatus string          `json:"classify_status"`
	Indexed        bool            `json:"indexed"`
	Marks          map[int]string  `json:"marks"`
	Segments       []types.Segment `json:"segments"`
}

// SplitView is GET /cases/{id}/split: the files with their segments, and
// the bundles — reviewed ones, and code proposals for the rest.
type SplitView struct {
	Labels    []types.ClassLabel `json:"labels"`
	OpensWith []string           `json:"opens_with"`
	Files     []SplitFile        `json:"files"`
	Bundles   []string           `json:"bundles"`
	Reviewed  bool               `json:"reviewed"` // every indexed file is reviewed
}

// View returns the split of a case. Segments of files not yet reviewed carry
// the proposed bundle code; pages no segment covers come as unknown.
func (s *Split) View(ctx context.Context, owner, caseID uuid.UUID) (SplitView, error) {
	c, err := s.cases.GetCaseOwned(ctx, owner, caseID)
	if err != nil {
		return SplitView{}, err
	}
	ct := s.cases.CaseType(c.CaseType)
	v := SplitView{Labels: ct.Classification.Labels, OpensWith: ct.Bundles.OpensWith, Files: []SplitFile{}, Bundles: []string{}, Reviewed: true}
	if v.Labels == nil {
		v.Labels = []types.ClassLabel{}
	}
	docs, err := s.st.Segments.SplitDocs(ctx, caseID)
	if err != nil {
		return v, err
	}
	segs, err := s.st.Segments.ByCase(ctx, caseID)
	if err != nil {
		return v, err
	}
	byDoc := map[uuid.UUID][]types.Segment{}
	for _, g := range segs {
		byDoc[g.DocumentID] = append(byDoc[g.DocumentID], g)
	}
	for _, d := range docs {
		f := SplitFile{DocumentID: d.ID, FileName: d.FileName, PageCount: d.PageCount, ClassifyStatus: d.ClassifyStatus, Indexed: d.Seq > 0, Marks: map[int]string{}}
		f.Status = splitStatus(d, byDoc[d.ID])
		if f.Indexed {
			if doc, err := s.st.Documents.Get(ctx, d.ID); err == nil {
				if m, err := s.Marks(ctx, doc); err == nil {
					f.Marks = m
				}
			}
			f.Segments = fillGaps(d.ID, d.PageCount, byDoc[d.ID], f.Status == types.SplitReviewed)
			if f.Status != types.SplitReviewed {
				v.Reviewed = false
			}
		}
		if f.Segments == nil {
			f.Segments = []types.Segment{}
		}
		v.Files = append(v.Files, f)
	}
	v.Bundles = proposeBundles(v.Files, ct.Bundles.OpensWith)
	return v, nil
}

func splitStatus(d postgres.SplitDoc, segs []types.Segment) string {
	if d.ReviewedAt != nil {
		for _, g := range segs {
			if g.NeedsReview {
				return types.SplitProposed
			}
		}
		return types.SplitReviewed
	}
	if len(segs) > 0 {
		return types.SplitProposed
	}
	return types.SplitNone
}

// fillGaps returns the segments of a file covering every page: for a file
// not reviewed, the user's segments win over proposals and pages nobody
// labelled come as unknown.
func fillGaps(doc uuid.UUID, pages int, segs []types.Segment, reviewed bool) []types.Segment {
	var use []types.Segment
	for _, g := range segs {
		if reviewed && g.Source != types.SegmentUser {
			continue
		}
		use = append(use, g)
	}
	sort.Slice(use, func(i, j int) bool { return use[i].PageStart < use[j].PageStart })
	var out []types.Segment
	next := 1
	for _, g := range use {
		if g.PageStart < next {
			continue
		}
		if g.PageStart > next {
			out = append(out, types.Segment{DocumentID: doc, PageStart: next, PageEnd: g.PageStart - 1, Label: types.LabelUnknown, Source: types.SegmentPipeline})
		}
		out = append(out, g)
		next = g.PageEnd + 1
	}
	if next <= pages {
		out = append(out, types.Segment{DocumentID: doc, PageStart: next, PageEnd: pages, Label: types.LabelUnknown, Source: types.SegmentPipeline})
	}
	return out
}

// proposeBundles gives every segment a bundle code (§6.9.7, code only):
// reviewed segments keep theirs; for the others, a label in opensWith opens
// a new bundle and any other segment joins the open one. It returns the
// codes in order.
func proposeBundles(files []SplitFile, opensWith []string) []string {
	opens := map[string]bool{}
	for _, l := range opensWith {
		opens[l] = true
	}
	max, cur := 0, ""
	for _, f := range files {
		for _, g := range f.Segments {
			if n := bundleNo(g.BundleCode); n > max {
				max = n
			}
		}
	}
	seen := map[string]bool{}
	var codes []string
	for fi := range files {
		for si := range files[fi].Segments {
			g := &files[fi].Segments[si]
			if g.BundleCode != "" && files[fi].Status == types.SplitReviewed {
				cur = g.BundleCode
			} else {
				g.BundleCode = ""
				if cur == "" || opens[g.Label] {
					max++
					cur = postgres.BundleCode(max)
				}
				g.BundleCode = cur
			}
			if !seen[g.BundleCode] {
				seen[g.BundleCode] = true
				codes = append(codes, g.BundleCode)
			}
		}
	}
	sort.Strings(codes)
	return codes
}

func bundleNo(code string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(code, "B"))
	return n
}

// SplitInput is the body of PUT /cases/{id}/split: the bundles in order,
// each with its documents.
type SplitInput struct {
	Bundles []struct {
		Segments []struct {
			DocumentID uuid.UUID `json:"document_id"`
			PageStart  int       `json:"page_start"`
			PageEnd    int       `json:"page_end"`
			Label      string    `json:"label"`
		} `json:"segments"`
	} `json:"bundles"`
}

// Save stores a reviewed split (§6.9.7). Every page of every file in the
// request must be in exactly one segment, with a label of the case type.
func (s *Split) Save(ctx context.Context, user types.User, caseID uuid.UUID, in SplitInput) error {
	c, err := s.cases.GetCaseOwned(ctx, user.ID, caseID)
	if err != nil {
		return err
	}
	rule := s.cases.CaseType(c.CaseType).Classification
	if len(rule.Labels) == 0 {
		return ErrNoLabels
	}
	docs, err := s.st.Segments.SplitDocs(ctx, caseID)
	if err != nil {
		return err
	}
	pagesOf := map[uuid.UUID]int{}
	for _, d := range docs {
		if d.Seq > 0 {
			pagesOf[d.ID] = d.PageCount
		}
	}
	covered := map[uuid.UUID][]bool{}
	var bundles [][]postgres.SplitSegment
	for _, b := range in.Bundles {
		var list []postgres.SplitSegment
		for _, g := range b.Segments {
			n, ok := pagesOf[g.DocumentID]
			if !ok {
				return fmt.Errorf("%w: document %s is not an indexed file of this case", ErrBadSplit, g.DocumentID)
			}
			if g.Label == types.LabelUnknown || !rule.HasLabel(g.Label) {
				return fmt.Errorf("%w: label %q (choose a document type, or other)", ErrBadSplit, g.Label)
			}
			if g.PageStart < 1 || g.PageEnd > n || g.PageStart > g.PageEnd {
				return fmt.Errorf("%w: pages %d-%d of a %d-page file", ErrBadSplit, g.PageStart, g.PageEnd, n)
			}
			if covered[g.DocumentID] == nil {
				covered[g.DocumentID] = make([]bool, n+1)
			}
			for p := g.PageStart; p <= g.PageEnd; p++ {
				if covered[g.DocumentID][p] {
					return fmt.Errorf("%w: page %d is in two documents", ErrBadSplit, p)
				}
				covered[g.DocumentID][p] = true
			}
			list = append(list, postgres.SplitSegment{DocumentID: g.DocumentID, PageStart: g.PageStart, PageEnd: g.PageEnd, Label: g.Label})
		}
		if len(list) > 0 {
			bundles = append(bundles, list)
		}
	}
	if len(covered) == 0 {
		return fmt.Errorf("%w: no document", ErrBadSplit)
	}
	ids := make([]uuid.UUID, 0, len(covered))
	for id, pages := range covered {
		for p := 1; p < len(pages); p++ {
			if !pages[p] {
				return fmt.Errorf("%w: page %d of a file is in no document (label it, or other)", ErrBadSplit, p)
			}
		}
		ids = append(ids, id)
	}
	return s.st.Segments.SaveSplit(ctx, caseID, user.ID, ids, bundles)
}

// Unreviewed returns the indexed files of a case whose split is not reviewed
// (§6.9.6: sub-tables with a label need it).
func (s *Split) Unreviewed(ctx context.Context, caseID uuid.UUID) ([]string, error) {
	docs, err := s.st.Segments.SplitDocs(ctx, caseID)
	if err != nil {
		return nil, err
	}
	segs, err := s.st.Segments.ByCase(ctx, caseID)
	if err != nil {
		return nil, err
	}
	byDoc := map[uuid.UUID][]types.Segment{}
	for _, g := range segs {
		byDoc[g.DocumentID] = append(byDoc[g.DocumentID], g)
	}
	var out []string
	for _, d := range docs {
		if d.Seq > 0 && splitStatus(d, byDoc[d.ID]) != types.SplitReviewed {
			out = append(out, d.FileName)
		}
	}
	return out, nil
}
