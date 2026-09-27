package wiki

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/queue"
	"github.com/thanhenti/bepaylot/internal/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
	"github.com/thanhenti/bepaylot/internal/types/interfaces"
)

func notFound(err error) error {
	if errors.Is(err, postgres.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// ownedCase returns a live case the owner may read; any other case is 404.
func (s *Service) ownedCase(ctx context.Context, owner, caseID uuid.UUID) (types.Case, error) {
	c, err := s.cases.GetCaseOwned(ctx, owner, caseID)
	if err != nil {
		return c, ErrNotFound
	}
	return c, nil
}

// ---- WikiReader (search, agent) ----

// Page implements interfaces.WikiReader.
func (s *Service) Page(ctx context.Context, caseID uuid.UUID, slug string) (*types.WikiPage, error) {
	p, err := s.st.Wiki.PageBySlug(ctx, caseID, strings.TrimSpace(slug))
	if err != nil {
		return nil, notFound(err)
	}
	fns, err := s.st.Wiki.Footnotes(ctx, []uuid.UUID{p.ID})
	if err != nil {
		return nil, err
	}
	p.Footnotes = fns[p.ID]
	if p.LinksOut, err = s.st.Wiki.Links(ctx, caseID, &p.ID, "out", ""); err != nil {
		return nil, err
	}
	if p.LinksIn, err = s.st.Wiki.Links(ctx, caseID, &p.ID, "in", ""); err != nil {
		return nil, err
	}
	return &p, nil
}

// SearchPages implements interfaces.WikiReader.
func (s *Service) SearchPages(ctx context.Context, caseID uuid.UUID, query string, limit int) ([]interfaces.WikiSearchHit, error) {
	if strings.TrimSpace(query) == "" {
		return []interfaces.WikiSearchHit{}, nil
	}
	hits, err := s.st.Wiki.Search(ctx, caseID, query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]interfaces.WikiSearchHit, len(hits))
	for i, h := range hits {
		out[i] = interfaces.WikiSearchHit{Slug: h.Slug, Title: h.Title, Kind: h.Kind, Snippet: h.Snippet, Score: h.Score}
	}
	return out, nil
}

// Links implements interfaces.WikiReader: the links from (out), to (in) or
// both ways of one page.
func (s *Service) Links(ctx context.Context, caseID uuid.UUID, slug, relation, direction string) ([]types.WikiLink, error) {
	p, err := s.st.Wiki.PageBySlug(ctx, caseID, strings.TrimSpace(slug))
	if err != nil {
		return nil, notFound(err)
	}
	links, err := s.st.Wiki.Links(ctx, caseID, &p.ID, direction, relation)
	if err != nil {
		return nil, err
	}
	if links == nil {
		links = []types.WikiLink{}
	}
	return links, nil
}

// LogQuery appends a query line to the log: the question, the pages read
// and how many verified hits came back. No LLM call.
func (s *Service) LogQuery(ctx context.Context, caseID, owner uuid.UUID, question string, pages []string, hits int) error {
	_, err := s.st.Wiki.AppendLog(ctx, types.WikiLogEntry{CaseID: caseID, Op: types.WikiOpQuery, Ref: textutil.Truncate(textutil.CollapseSpace(question), 120),
		Pages: pages, Summary: fmt.Sprintf("đọc %d trang wiki, %d kết quả đã kiểm", len(pages), hits), Actor: owner.String()})
	return err
}

// MarkFootnotesStale implements interfaces.WikiReader.
func (s *Service) MarkFootnotesStale(ctx context.Context, caseID, page uuid.UUID, ns []int) error {
	return s.st.Wiki.SetFootnoteStatus(ctx, caseID, page, ns, types.FootnoteStale)
}

// ---- Module 3 API (§7, §10.4) ----

// TOC returns the navigation of a case wiki.
func (s *Service) TOC(ctx context.Context, owner, caseID uuid.UUID) (*types.WikiTOC, error) {
	c, err := s.ownedCase(ctx, owner, caseID)
	if err != nil {
		return nil, err
	}
	pages, err := s.st.Wiki.Pages(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	docs, err := s.st.Documents.ByCase(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	out := &types.WikiTOC{Case: c, Pages: []types.WikiPageRef{}, DocumentsTotal: len(docs)}
	has := map[uuid.UUID]bool{}
	for _, p := range pages {
		if p.DocumentID != nil {
			has[*p.DocumentID] = true
		}
		out.Pages = append(out.Pages, types.WikiPageRef{Slug: p.Slug, Title: p.Title, Kind: p.Kind, EntityType: p.EntityType, Summary: p.Summary,
			DocumentID: p.DocumentID, Proposed: p.ProposedContent != nil, UpdatedAt: p.UpdatedAt})
	}
	for _, d := range docs {
		if !has[d.ID] {
			out.Pending = append(out.Pending, types.WikiPending{DocumentID: d.ID, FileName: d.FileName, Status: d.Status, WikiStatus: d.WikiStatus})
		}
	}
	out.OpenLintIssues, err = s.st.Wiki.CountOpenIssues(ctx, c.ID)
	return out, err
}

// IndexText returns the index as given to the LLM, or an expanded entry.
func (s *Service) IndexText(ctx context.Context, owner, caseID uuid.UUID, expand string) (string, error) {
	c, err := s.ownedCase(ctx, owner, caseID)
	if err != nil {
		return "", err
	}
	if expand != "" {
		return s.Expand(ctx, c.ID, expand)
	}
	v, err := s.IndexView(ctx, c.ID, nil)
	if err != nil {
		return "", err
	}
	return v.Content, nil
}

// GetPage returns a page of an owned case.
func (s *Service) GetPage(ctx context.Context, owner, caseID uuid.UUID, slug string) (*types.WikiPage, error) {
	c, err := s.ownedCase(ctx, owner, caseID)
	if err != nil {
		return nil, err
	}
	return s.Page(ctx, c.ID, slug)
}

// EditRequest is a manual page edit (§7.4). Footnotes maps a footnote number
// to a citation id of the case; numbers used in content must be listed or
// already exist on the page.
type EditRequest struct {
	Title      *string                        `json:"title,omitempty"`
	Content    *string                        `json:"content,omitempty"`
	Attributes map[string]types.WikiAttribute `json:"attributes,omitempty"`
	Footnotes  map[string]string              `json:"footnotes,omitempty"`
}

// EditResult is the saved page and the footnotes that could not be verified.
type EditResult struct {
	Page    *types.WikiPage `json:"page"`
	Invalid []string        `json:"invalid_footnotes,omitempty"`
}

// EditPage saves a manual edit: citations must belong to the case and match
// the source lines; invalid ones are marked stale, or refused (422) with
// wiki.strict_citations. The page is then protected from ingest overwrites.
func (s *Service) EditPage(ctx context.Context, owner, caseID uuid.UUID, slug string, req EditRequest) (*EditResult, error) {
	c, err := s.ownedCase(ctx, owner, caseID)
	if err != nil {
		return nil, err
	}
	p, err := s.st.Wiki.PageBySlug(ctx, c.ID, slug)
	if err != nil {
		return nil, notFound(err)
	}
	cur, err := s.st.Wiki.Footnotes(ctx, []uuid.UUID{p.ID})
	if err != nil {
		return nil, err
	}
	fns := map[int]types.WikiFootnote{}
	for _, f := range cur[p.ID] {
		fns[f.N] = f
	}
	var invalid []string
	for k, cid := range req.Footnotes {
		n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(k), "^"))
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("%w: footnote key %q is not a number", ErrBadRequest, k)
		}
		f, err := s.resolveCitation(ctx, c.ID, cid)
		if err != nil {
			invalid = append(invalid, fmt.Sprintf("[^%d] %s: %v", n, cid, err))
			f = types.WikiFootnote{N: n, CitationID: cid, Status: types.FootnoteStale}
			if doc, page, lo, hi, ok := parseCitation(cid); ok {
				f.DocumentID, f.PageNo, f.LineFrom, f.LineTo = doc, page, max(lo, 0), max(hi, 0)
			}
		}
		f.N = n
		fns[n] = f
	}
	if req.Title != nil {
		p.Title = strings.TrimSpace(*req.Title)
	}
	if req.Content != nil {
		p.Content = *req.Content
	}
	if req.Attributes != nil {
		p.Attributes = req.Attributes
	}
	used := footnoteRefs(p.Content)
	for _, a := range p.Attributes {
		for _, n := range a.Footnotes {
			used[n] = true
		}
	}
	var keep []types.WikiFootnote
	for n := range used {
		f, ok := fns[n]
		if !ok {
			invalid = append(invalid, fmt.Sprintf("[^%d]: no citation given", n))
			continue
		}
		if f.DocumentID == uuid.Nil {
			continue // cannot store a footnote without a document
		}
		keep = append(keep, f)
	}
	if len(invalid) > 0 && s.cfg.Wiki.StrictCitations {
		return nil, fmt.Errorf("%w: invalid footnotes: %s", ErrBadRequest, strings.Join(invalid, "; "))
	}
	sort.Slice(keep, func(i, j int) bool { return keep[i].N < keep[j].N })
	p.Content, _ = sanitizeMermaid(p.Content)
	editor := owner
	err = s.st.Wiki.InTx(ctx, func(tx *postgres.WikiRepo) error {
		logID, err := tx.AppendLog(ctx, types.WikiLogEntry{CaseID: c.ID, Op: types.WikiOpEdit, Ref: p.Slug, Pages: []string{p.Slug},
			Summary: "sửa tay trang " + p.Title, Actor: "user:" + owner.String()})
		if err != nil {
			return err
		}
		saved, err := tx.SavePage(ctx, p, types.EditUser, &editor, &logID)
		if err != nil {
			return err
		}
		if err := tx.ReplaceFootnotes(ctx, saved.ID, keep); err != nil {
			return err
		}
		return s.relink(ctx, tx, c.ID, saved)
	})
	if err != nil {
		return nil, err
	}
	s.queueIndex(ctx, c.ID)
	page, err := s.Page(ctx, c.ID, p.Slug)
	return &EditResult{Page: page, Invalid: invalid}, err
}

// relink replaces a page's plain [[slug]] links from its content and keeps
// its typed links (written by ingest).
func (s *Service) relink(ctx context.Context, tx *postgres.WikiRepo, caseID uuid.UUID, p types.WikiPage) error {
	out, err := tx.Links(ctx, caseID, &p.ID, "out", "")
	if err != nil {
		return err
	}
	var rows []postgres.LinkRow
	for _, l := range out {
		if l.Relation == "" {
			continue
		}
		if to, err := tx.PageBySlug(ctx, caseID, l.To); err == nil {
			rows = append(rows, postgres.LinkRow{To: to.ID, Relation: l.Relation, Attributes: l.Attributes, FootnoteN: l.FootnoteN})
		}
	}
	for _, slug := range wikiLinks(p.Content) {
		if to, err := tx.PageBySlug(ctx, caseID, slug); err == nil {
			rows = append(rows, postgres.LinkRow{To: to.ID})
		}
	}
	return tx.ReplaceLinks(ctx, caseID, p.ID, rows)
}

// queueIndex enqueues an index rebuild (wiki:index) after a non-ingest change.
func (s *Service) queueIndex(ctx context.Context, caseID uuid.UUID) {
	if err := s.q.Enqueue(ctx, types.TaskWikiIndex, types.CaseTaskPayload{CaseID: caseID},
		queue.Opts{TaskID: fmt.Sprintf("wx:%s:%d", caseID, time.Now().UnixNano())}); err != nil {
		s.log.Warn("enqueue wiki:index", "case", caseID, "err", err)
	}
}

// resolveCitation turns a citation id of the case into a footnote carrying
// the current text of its lines.
func (s *Service) resolveCitation(ctx context.Context, caseID uuid.UUID, cid string) (types.WikiFootnote, error) {
	doc, page, lo, hi, ok := parseCitation(cid)
	if !ok {
		return types.WikiFootnote{}, errors.New("not a citation id")
	}
	d, err := s.docs.GetDocument(ctx, doc)
	if err != nil || d.CaseID != caseID {
		return types.WikiFootnote{}, errors.New("document is not in this case")
	}
	lines, err := s.st.Pages.Lines(ctx, d.ID, page, page)
	if err != nil {
		return types.WikiFootnote{}, err
	}
	var parts []string
	from, to := -1, -1
	for _, l := range lines {
		if (lo < 0 || (l.LineNo >= lo && l.LineNo <= hi)) && strings.TrimSpace(l.Text) != "" {
			parts = append(parts, l.Text)
			if from < 0 {
				from = l.LineNo
			}
			to = l.LineNo
		}
	}
	if len(parts) == 0 {
		return types.WikiFootnote{}, errors.New("no such lines")
	}
	return types.WikiFootnote{DocumentID: d.ID, Gen: d.Gen, PageNo: page, LineFrom: from, LineTo: to, Quote: textutil.Truncate(strings.Join(parts, " "), 500),
		CitationID: citationID(d.ID, page, from, to), Status: types.FootnoteValid, FileName: d.FileName}, nil
}

// Revisions lists a page's history.
func (s *Service) Revisions(ctx context.Context, owner, caseID uuid.UUID, slug string) ([]types.WikiRevision, error) {
	c, err := s.ownedCase(ctx, owner, caseID)
	if err != nil {
		return nil, err
	}
	p, err := s.st.Wiki.PageBySlug(ctx, c.ID, slug)
	if err != nil {
		return nil, notFound(err)
	}
	revs, err := s.st.Wiki.Revisions(ctx, p.ID)
	if revs == nil {
		revs = []types.WikiRevision{}
	}
	return revs, err
}

// Restore brings back an old revision as a manual edit.
func (s *Service) Restore(ctx context.Context, owner, caseID uuid.UUID, slug string, version int) (*types.WikiPage, error) {
	revs, err := s.Revisions(ctx, owner, caseID, slug)
	if err != nil {
		return nil, err
	}
	for _, r := range revs {
		if r.Version == version {
			title, content := r.Title, r.Content
			res, err := s.EditPage(ctx, owner, caseID, slug, EditRequest{Title: &title, Content: &content, Attributes: r.Attributes})
			if err != nil {
				return nil, err
			}
			return res.Page, nil
		}
	}
	return nil, ErrNotFound
}

// Proposal accepts or rejects what ingest proposed for a user-edited page.
func (s *Service) Proposal(ctx context.Context, owner, caseID uuid.UUID, slug string, accept bool) (*types.WikiPage, error) {
	c, err := s.ownedCase(ctx, owner, caseID)
	if err != nil {
		return nil, err
	}
	p, err := s.st.Wiki.PageBySlug(ctx, c.ID, slug)
	if err != nil {
		return nil, notFound(err)
	}
	if p.ProposedContent == nil {
		return nil, fmt.Errorf("%w: the page has no proposal", ErrBadRequest)
	}
	editor := owner
	err = s.st.Wiki.InTx(ctx, func(tx *postgres.WikiRepo) error {
		action := "bỏ"
		if accept {
			action = "chấp nhận"
		}
		logID, err := tx.AppendLog(ctx, types.WikiLogEntry{CaseID: c.ID, Op: types.WikiOpEdit, Ref: p.Slug, Pages: []string{p.Slug},
			Summary: action + " đề xuất từ ingest", Actor: "user:" + owner.String()})
		if err != nil {
			return err
		}
		if accept {
			p.Content = *p.ProposedContent
			if p.ProposedAttributes != nil {
				p.Attributes = p.ProposedAttributes
			}
			saved, err := tx.SavePage(ctx, p, types.EditUser, &editor, &logID)
			if err != nil {
				return err
			}
			if err := s.relink(ctx, tx, c.ID, saved); err != nil {
				return err
			}
		}
		return tx.SetProposal(ctx, c.ID, p.ID, nil, nil)
	})
	if err != nil {
		return nil, err
	}
	s.queueIndex(ctx, c.ID)
	return s.Page(ctx, c.ID, slug)
}

// GraphNode is a page in the link graph.
type GraphNode struct {
	Slug       string `json:"slug"`
	Title      string `json:"title"`
	Kind       string `json:"kind"`
	EntityType string `json:"entity_type,omitempty"`
}

// LinkGraph is the wiki as nodes (pages) and edges (links, §7.3).
type LinkGraph struct {
	Nodes []GraphNode      `json:"nodes"`
	Edges []types.WikiLink `json:"edges"`
}

// Graph returns the link graph, filtered by page kind, entity type and
// relation.
func (s *Service) Graph(ctx context.Context, owner, caseID uuid.UUID, kind, entityType, relation string) (*LinkGraph, error) {
	c, err := s.ownedCase(ctx, owner, caseID)
	if err != nil {
		return nil, err
	}
	pages, err := s.st.Wiki.Pages(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	links, err := s.st.Wiki.Links(ctx, c.ID, nil, "", relation)
	if err != nil {
		return nil, err
	}
	out := &LinkGraph{Nodes: []GraphNode{}, Edges: []types.WikiLink{}}
	in := map[string]bool{}
	for _, p := range pages {
		if (kind != "" && p.Kind != kind) || (entityType != "" && p.EntityType != entityType) {
			continue
		}
		in[p.Slug] = true
		out.Nodes = append(out.Nodes, GraphNode{Slug: p.Slug, Title: p.Title, Kind: p.Kind, EntityType: p.EntityType})
	}
	for _, l := range links {
		if in[l.From] && in[l.To] {
			out.Edges = append(out.Edges, l)
		}
	}
	return out, nil
}

// Search runs full-text over the wiki of an owned case.
func (s *Service) Search(ctx context.Context, owner, caseID uuid.UUID, q string) ([]interfaces.WikiSearchHit, error) {
	c, err := s.ownedCase(ctx, owner, caseID)
	if err != nil {
		return nil, err
	}
	return s.SearchPages(ctx, c.ID, q, 30)
}

// NoteRequest saves an answer as a note page: either text, or an assistant
// message of a session bound to the case.
type NoteRequest struct {
	Title     string `json:"title,omitempty"`
	Content   string `json:"content,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	MessageID string `json:"message_id,omitempty"`
}

var inlineCitation = regexp.MustCompile(`\[?(doc:[0-9a-fA-F-]{36}:p\d+(?::l\d+(?:-l?\d+)?)?)\]?`)

// CreateNote saves an answer into the wiki as a note page (§6.10, query
// operation of LLM Wiki). Inline citation ids become footnotes after being
// checked against the case and the source lines; sentences without a valid
// citation are dropped. The agent never writes to the wiki by itself.
func (s *Service) CreateNote(ctx context.Context, owner, caseID uuid.UUID, req NoteRequest) (*types.WikiPage, error) {
	c, err := s.ownedCase(ctx, owner, caseID)
	if err != nil {
		return nil, err
	}
	content := req.Content
	if req.SessionID != "" || req.MessageID != "" {
		content, err = s.messageText(ctx, owner, c.ID, req.SessionID, req.MessageID)
		if err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("%w: content (or session_id + message_id) is required", ErrBadRequest)
	}
	num := map[string]int{}
	var fns []types.WikiFootnote
	invalid := map[int]bool{}
	next := 1
	body := inlineCitation.ReplaceAllStringFunc(content, func(m string) string {
		cid := inlineCitation.FindStringSubmatch(m)[1]
		if n, ok := num[cid]; ok {
			return fmt.Sprintf("[^%d]", n)
		}
		n := next
		next++
		num[cid] = n
		f, err := s.resolveCitation(ctx, c.ID, cid)
		if err != nil {
			invalid[n] = true
			return fmt.Sprintf("[^%d]", n)
		}
		f.N = n
		fns = append(fns, f)
		return fmt.Sprintf("[^%d]", n)
	})
	body = keepCited(dropFootnotes(body, invalid))
	if strings.TrimSpace(body) == "" || len(fns) == 0 {
		return nil, fmt.Errorf("%w: the answer has no statement with a valid citation of this case", ErrBadRequest)
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "Ghi chú " + time.Now().Format("2006-01-02 15:04")
	}
	existing, err := s.st.Wiki.Pages(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	page := types.WikiPage{CaseID: c.ID, Kind: types.WikiKindNote, Title: title, Slug: uniqueSlug("ghi-chu/"+Slugify(title), existing),
		Summary: textutil.Truncate(textutil.CollapseSpace(footRefRe.ReplaceAllString(firstProse(body), "")), 200), Content: body}
	editor := owner
	err = s.st.Wiki.InTx(ctx, func(tx *postgres.WikiRepo) error {
		logID, err := tx.AppendLog(ctx, types.WikiLogEntry{CaseID: c.ID, Op: types.WikiOpNote, Ref: page.Slug, Pages: []string{page.Slug},
			Summary: fmt.Sprintf("lưu ghi chú \"%s\" (%d trích dẫn)", title, len(fns)), Actor: "user:" + owner.String()})
		if err != nil {
			return err
		}
		saved, err := tx.SavePage(ctx, page, types.EditUser, &editor, &logID)
		if err != nil {
			return err
		}
		page = saved
		if err := tx.ReplaceFootnotes(ctx, saved.ID, fns); err != nil {
			return err
		}
		return s.relink(ctx, tx, c.ID, saved)
	})
	if err != nil {
		return nil, err
	}
	s.queueIndex(ctx, c.ID)
	return s.Page(ctx, c.ID, page.Slug)
}

// keepCited keeps headings, table scaffolding and the sentences or rows that
// carry at least one footnote.
func keepCited(content string) string {
	var out []string
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		t := strings.TrimSpace(line)
		switch {
		case t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "```"):
			out = append(out, line)
		case strings.HasPrefix(t, "|"):
			isHeader := i+1 < len(lines) && strings.Contains(lines[i+1], "---")
			if isHeader || strings.Contains(t, "---") || footRefRe.MatchString(t) {
				out = append(out, line)
			}
		case strings.HasPrefix(t, "-") || strings.HasPrefix(t, "*") || orderedItem.MatchString(t):
			if footRefRe.MatchString(t) {
				out = append(out, line)
			}
		default:
			var kept []string
			for _, s := range splitSentences(line) {
				if footRefRe.MatchString(s) {
					kept = append(kept, s)
				}
			}
			if len(kept) > 0 {
				out = append(out, strings.Join(kept, " "))
			}
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func firstProse(content string) string {
	for _, l := range strings.Split(content, "\n") {
		t := strings.TrimSpace(l)
		if t != "" && !strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "|") && !strings.HasPrefix(t, "```") {
			return strings.TrimLeft(t, "-* ")
		}
	}
	return ""
}

// messageText returns the text of an assistant message of a session bound to
// the case.
func (s *Service) messageText(ctx context.Context, owner, caseID uuid.UUID, sessionID, messageID string) (string, error) {
	sid, err1 := uuid.Parse(sessionID)
	mid, err2 := uuid.Parse(messageID)
	if err1 != nil || err2 != nil {
		return "", fmt.Errorf("%w: session_id and message_id must both be uuids", ErrBadRequest)
	}
	sess, err := s.st.Sessions.Get(ctx, sid)
	if err != nil || sess.UserID != owner {
		return "", ErrNotFound
	}
	if sess.CaseID == nil || *sess.CaseID != caseID {
		return "", fmt.Errorf("%w: the session is not bound to this case", ErrBadRequest)
	}
	msgs, err := s.st.Messages.ListBySession(ctx, sid, 0, 10000)
	if err != nil {
		return "", err
	}
	for _, m := range msgs {
		if m.ID != mid {
			continue
		}
		if m.Role != "assistant" {
			return "", fmt.Errorf("%w: only assistant answers can be saved", ErrBadRequest)
		}
		var parts []string
		for _, b := range m.Blocks {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n\n"), nil
	}
	return "", ErrNotFound
}

// DeleteNote removes a note page; other pages belong to ingest.
func (s *Service) DeleteNote(ctx context.Context, owner, caseID uuid.UUID, slug string) error {
	c, err := s.ownedCase(ctx, owner, caseID)
	if err != nil {
		return err
	}
	p, err := s.st.Wiki.PageBySlug(ctx, c.ID, slug)
	if err != nil {
		return notFound(err)
	}
	if p.Kind != types.WikiKindNote {
		return fmt.Errorf("%w: only note pages can be deleted; other pages are maintained by ingest", ErrBadRequest)
	}
	err = s.st.Wiki.InTx(ctx, func(tx *postgres.WikiRepo) error {
		if _, err := tx.AppendLog(ctx, types.WikiLogEntry{CaseID: c.ID, Op: types.WikiOpNote, Ref: p.Slug, Pages: []string{p.Slug},
			Summary: "xoá ghi chú " + p.Title, Actor: "user:" + owner.String()}); err != nil {
			return err
		}
		return tx.DeletePage(ctx, c.ID, p.ID)
	})
	if err == nil {
		s.queueIndex(ctx, c.ID)
	}
	return err
}

// Log returns the log of an owned case, newest first.
func (s *Service) Log(ctx context.Context, owner, caseID uuid.UUID, f postgres.LogFilter) ([]types.WikiLogEntry, error) {
	c, err := s.ownedCase(ctx, owner, caseID)
	if err != nil {
		return nil, err
	}
	out, err := s.st.Wiki.Log(ctx, c.ID, f)
	if out == nil {
		out = []types.WikiLogEntry{}
	}
	return out, err
}

// Issues lists lint issues of an owned case.
func (s *Service) Issues(ctx context.Context, owner, caseID uuid.UUID, status, kind string) ([]types.WikiLintIssue, error) {
	c, err := s.ownedCase(ctx, owner, caseID)
	if err != nil {
		return nil, err
	}
	out, err := s.st.Wiki.Issues(ctx, c.ID, status, kind, 500)
	if out == nil {
		out = []types.WikiLintIssue{}
	}
	return out, err
}

// RunLint enqueues a lint of an owned case now.
func (s *Service) RunLint(ctx context.Context, owner, caseID uuid.UUID) error {
	c, err := s.ownedCase(ctx, owner, caseID)
	if err != nil {
		return err
	}
	return s.q.Enqueue(ctx, types.TaskWikiLint, types.CaseTaskPayload{CaseID: c.ID}, queue.Opts{TaskID: fmt.Sprintf("wl:%s:u%d", c.ID, time.Now().UnixNano())})
}

// SetIssue resolves (fixed) or dismisses a lint issue.
func (s *Service) SetIssue(ctx context.Context, owner, caseID uuid.UUID, id int64, status string) error {
	c, err := s.ownedCase(ctx, owner, caseID)
	if err != nil {
		return err
	}
	switch status {
	case "fixed", "dismissed", "open":
	default:
		return fmt.Errorf("%w: status must be fixed, dismissed or open", ErrBadRequest)
	}
	by := owner
	return notFound(s.st.Wiki.SetIssueStatus(ctx, c.ID, id, status, &by))
}

// Rebuild drops the generated wiki of a case and ingests every file again
// (after a schema change). Notes and user-edited pages stay; edited pages
// receive proposals.
func (s *Service) Rebuild(ctx context.Context, owner, caseID uuid.UUID) error {
	c, err := s.ownedCase(ctx, owner, caseID)
	if err != nil {
		return err
	}
	docs, err := s.st.Documents.ByCase(ctx, c.ID)
	if err != nil {
		return err
	}
	err = s.st.Wiki.InTx(ctx, func(tx *postgres.WikiRepo) error {
		if err := tx.DeleteCase(ctx, c.ID, true); err != nil {
			return err
		}
		if _, err := tx.AppendLog(ctx, types.WikiLogEntry{CaseID: c.ID, Op: types.WikiOpRebuild, Ref: c.Code,
			Summary: fmt.Sprintf("dựng lại wiki: %d tệp được ingest lại", len(docs)), Actor: "user:" + owner.String()}); err != nil {
			return err
		}
		v, err := tx.BumpVersion(ctx, c.ID)
		if err != nil {
			return err
		}
		c.WikiVersion = v
		if err := s.storeIndex(ctx, tx, c); err != nil {
			return err
		}
		return tx.SetCaseWiki(ctx, c.ID, types.WikiBuilding, 0)
	})
	if err != nil {
		return err
	}
	for _, d := range docs {
		if !types.Searchable(d.Status) {
			continue
		}
		_ = s.docs.SetWikiStatus(ctx, d.ID, d.Gen, types.StagePending, nil)
		if err := s.queueOp(ctx, c.ID, types.WikiOpIngestDoc, types.WikiOpPayload{DocumentID: d.ID, Gen: d.Gen, FileName: d.FileName}); err != nil {
			return err
		}
	}
	return nil
}

// Event is one realtime wiki event (§7.7).
type Event struct {
	ID    int64          `json:"id"`
	Event string         `json:"event"` // ingest_started | page_updated | ingest_done | lint_done
	Data  map[string]any `json:"data"`
}

// EventsSince turns new log lines (after id) and status changes into events.
func (s *Service) EventsSince(ctx context.Context, owner, caseID uuid.UUID, after int64, lastStatus string) ([]Event, int64, string, error) {
	c, err := s.ownedCase(ctx, owner, caseID)
	if err != nil {
		return nil, after, lastStatus, err
	}
	var out []Event
	if c.WikiStatus != lastStatus {
		if c.WikiStatus == types.WikiBuilding {
			out = append(out, Event{Event: "ingest_started", Data: map[string]any{"wiki_status": c.WikiStatus, "wiki_version": c.WikiVersion}})
		}
		lastStatus = c.WikiStatus
	}
	if after < 0 {
		id, err := s.st.Wiki.LastLogID(ctx, c.ID)
		return out, id, lastStatus, err
	}
	logs, err := s.st.Wiki.Log(ctx, c.ID, postgres.LogFilter{After: after, Limit: 200})
	if err != nil {
		return nil, after, lastStatus, err
	}
	for _, l := range logs {
		after = l.ID
		for _, p := range l.Pages {
			out = append(out, Event{ID: l.ID, Event: "page_updated", Data: map[string]any{"slug": p, "op": l.Op}})
		}
		switch l.Op {
		case types.WikiOpIngest, types.WikiOpRetract, types.WikiOpRebuild:
			out = append(out, Event{ID: l.ID, Event: "ingest_done", Data: map[string]any{"op": l.Op, "ref": l.Ref, "summary": l.Summary}})
		case types.WikiOpLint:
			out = append(out, Event{ID: l.ID, Event: "lint_done", Data: map[string]any{"summary": l.Summary}})
		}
	}
	return out, after, lastStatus, nil
}
