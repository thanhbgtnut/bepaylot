package tools

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/types"
	"github.com/thanhenti/bepaylot/internal/types/interfaces"
)

// CaseScope is what a session may read: the caller and the one case bound to
// the session (§8.1). It comes from sessions.case_id on the server; no tool
// takes a case or KB argument, so the model cannot leave the case.
type CaseScope struct {
	Owner  uuid.UUID
	CaseID uuid.UUID
	KBID   uuid.UUID
}

type caseScopeKey struct{}

// WithCaseScope attaches the case scope for the document and wiki tools.
func WithCaseScope(ctx context.Context, s CaseScope) context.Context {
	return context.WithValue(ctx, caseScopeKey{}, s)
}

// errCaseGone is returned by every tool once the session's case is deleted.
var errCaseGone = errors.New("the case of this conversation no longer exists (case không còn tồn tại)")

func caseScope(ctx context.Context) (CaseScope, error) {
	s, ok := ctx.Value(caseScopeKey{}).(CaseScope)
	if !ok || s.Owner == uuid.Nil || s.CaseID == uuid.Nil {
		return s, errors.New("no case is bound to this conversation")
	}
	return s, nil
}

// alive turns a failure into errCaseGone when the case was deleted meanwhile.
func (s CaseScope) alive(ctx context.Context, searcher interfaces.Searcher, err error) error {
	if err != nil && !searcher.CaseAlive(ctx, s.Owner, s.CaseID) {
		return errCaseGone
	}
	return err
}

// checkDoc refuses a document outside the session's case with the same
// message as a missing document, so other cases' files are not revealed.
func (s CaseScope) checkDoc(ctx context.Context, searcher interfaces.Searcher, id uuid.UUID) error {
	ok, err := searcher.DocumentInCase(ctx, s.Owner, id, s.CaseID)
	if err != nil {
		return err
	}
	if !ok {
		if !searcher.CaseAlive(ctx, s.Owner, s.CaseID) {
			return errCaseGone
		}
		return fmt.Errorf("document %s not found", id)
	}
	return nil
}

// docArg parses and scope-checks a document id argument.
func docArg(ctx context.Context, searcher interfaces.Searcher, raw string) (CaseScope, uuid.UUID, error) {
	sc, err := caseScope(ctx)
	if err != nil {
		return sc, uuid.Nil, err
	}
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return sc, uuid.Nil, fmt.Errorf("document %s not found", strings.TrimSpace(raw))
	}
	return sc, id, sc.checkDoc(ctx, searcher, id)
}

// SetKnowledgeTools builds the case tools (§8.2): wiki_* over the case wiki
// and kb_* over the case documents. They are bound only in sessions with a
// case (EnableKnowledge). wiki may be nil (no wiki tools).
func (r *Registry) SetKnowledgeTools(searcher interfaces.Searcher, wiki interfaces.WikiReader) error {
	ts, err := knowledgeTools(searcher, wiki)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.knowledge = map[string]*Entry{}
	for _, t := range ts {
		e, err := newEntry(context.Background(), t)
		if err != nil {
			return err
		}
		r.knowledge[e.Name] = e
	}
	return nil
}

// EnableKnowledge binds the case tools for this turn (built-in, never
// deferred).
func (s *Session) EnableKnowledge() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for n, e := range s.knowledge {
		s.builtin[n] = e
		s.visible[n] = struct{}{}
	}
}

type kbSearchArgs struct {
	Query       string         `json:"query" jsonschema:"required" jsonschema_description:"The question or keywords to find in the case's files."`
	DocumentIDs []string       `json:"document_ids,omitempty" jsonschema_description:"Restrict to these document ids of the case."`
	Metadata    map[string]any `json:"metadata,omitempty" jsonschema_description:"Metadata filter on the case's files, e.g. {\"loai_giay_to\": \"HOP_DONG\"} or {\"ngay_nop\": {\"gte\": \"2026-01-01\"}}. Operators: eq, in, prefix, gte, gt, lte, lt, exists."`
	Mode        string         `json:"mode,omitempty" jsonschema:"enum=reasoning,enum=keyword" jsonschema_description:"reasoning (default: wiki index → wiki pages → source lines) or keyword (fast exact terms, codes, amounts)."`
	PageFrom    int            `json:"page_from,omitempty" jsonschema_description:"Only pages from this page number (inclusive)."`
	PageTo      int            `json:"page_to,omitempty" jsonschema_description:"Only pages up to this page number (inclusive)."`
	TopK        int            `json:"top_k,omitempty"`
}

type kbHit struct {
	CitationID string         `json:"citation_id"`
	File       string         `json:"file"`
	DocumentID string         `json:"document_id"`
	Page       int            `json:"page"`
	Quote      string         `json:"quote"`
	Section    string         `json:"section,omitempty"`
	Via        string         `json:"via,omitempty"`
	WikiPages  []string       `json:"wiki_pages,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Reason     string         `json:"reason,omitempty"`
}

type docBrief struct {
	DocumentID string         `json:"document_id"`
	File       string         `json:"file"`
	Title      string         `json:"title,omitempty"`
	Summary    string         `json:"summary,omitempty"`
	Pages      int            `json:"pages"`
	Status     string         `json:"status"`
	WikiStatus string         `json:"wiki_status,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

func toBriefs(ds []types.DocumentBrief) []docBrief {
	out := make([]docBrief, len(ds))
	for i, d := range ds {
		out[i] = docBrief{DocumentID: d.ID.String(), File: d.FileName, Title: d.Title, Summary: d.Summary, Pages: d.PageCount, Status: d.Status,
			WikiStatus: d.WikiStatus, Metadata: d.Metadata}
	}
	return out
}

func parseIDs(raw []string) ([]uuid.UUID, error) {
	var out []uuid.UUID
	for _, r := range raw {
		id, err := uuid.Parse(strings.TrimSpace(r))
		if err != nil {
			return nil, fmt.Errorf("document %s not found", r)
		}
		out = append(out, id)
	}
	return out, nil
}

func knowledgeTools(searcher interfaces.Searcher, wiki interfaces.WikiReader) ([]tool.InvokableTool, error) {
	var out []tool.InvokableTool
	add := func(t tool.InvokableTool, err error) error {
		if err != nil {
			return err
		}
		out = append(out, t)
		return nil
	}

	if wiki != nil {
		type indexArgs struct {
			Expand string `json:"expand,omitempty" jsonschema_description:"An index id to open further, e.g. w4 (a file's table of contents) or w4.n2 (a branch)."`
		}
		if err := add(utils.InferTool("wiki_index",
			"Read the index of this case's wiki: one line per wiki page, by category ([w<n>] title — summary): overview, one source page per file with its main branches ([w<n>.n<k>] title (pages)), entities by type, notes; plus files not in the wiki yet. Start here; then wiki_read a page by its id (w<n>) or read a branch with kb_read_pages.",
			func(ctx context.Context, a indexArgs) (map[string]any, error) {
				sc, err := caseScope(ctx)
				if err != nil {
					return nil, err
				}
				if a.Expand != "" {
					txt, err := wiki.Expand(ctx, sc.CaseID, strings.TrimSpace(a.Expand))
					if err != nil {
						return nil, sc.alive(ctx, searcher, err)
					}
					if txt == "" {
						txt = "(nothing to expand for " + a.Expand + ")"
					}
					return map[string]any{"expanded": txt}, nil
				}
				v, err := wiki.IndexView(ctx, sc.CaseID, nil)
				if err != nil {
					return nil, sc.alive(ctx, searcher, err)
				}
				// Ids are resolved by wiki_read / wiki_links; the index is not
				// repeated as an id → slug table (it is read on every query).
				return map[string]any{"index": v.Content}, nil
			})); err != nil {
			return nil, err
		}

		type readArgs struct {
			Page string `json:"page" jsonschema:"required" jsonschema_description:"Index id of the page (w5), or its slug (a [[slug]] link in a page)."`
		}
		if err := add(utils.InferTool("wiki_read",
			"Read a page of this case's wiki: markdown with footnotes [^n] (entity pages show their attributes, roles, relations and sources in it; ⚠ = sources disagree), the footnotes with their citation_id and quote, and the pages linking to it. The wiki is a finding aid, not evidence: cite the footnotes' citation_id (source lines) in answers, and read the source when a footnote is stale or a value is in conflict.",
			func(ctx context.Context, a readArgs) (map[string]any, error) {
				sc, err := caseScope(ctx)
				if err != nil {
					return nil, err
				}
				slug, err := pageSlug(ctx, wiki, sc.CaseID, a.Page)
				if err != nil {
					return nil, sc.alive(ctx, searcher, err)
				}
				p, err := wiki.Page(ctx, sc.CaseID, slug)
				if err != nil {
					if !searcher.CaseAlive(ctx, sc.Owner, sc.CaseID) {
						return nil, errCaseGone
					}
					return nil, fmt.Errorf("wiki page %q not found", a.Page)
				}
				// Compact: what the content already shows (attributes,
				// outgoing links, sources) is not repeated.
				type fn struct {
					N          int    `json:"n"`
					CitationID string `json:"citation_id"`
					Quote      string `json:"quote"`
					Stale      bool   `json:"stale,omitempty"`
				}
				fns := make([]fn, len(p.Footnotes))
				for i, f := range p.Footnotes {
					fns[i] = fn{f.N, f.CitationID, f.Quote, f.Status == types.FootnoteStale}
				}
				var inL []string
				for _, l := range p.LinksIn {
					s := l.From
					if l.Relation != "" {
						s += " (" + l.Relation + ")"
					}
					inL = append(inL, s)
				}
				res := map[string]any{"slug": p.Slug, "title": p.Title, "content": p.Content, "footnotes": fns}
				if len(inL) > 0 {
					res["linked_from"] = inL
				}
				if p.DocumentID != nil {
					res["document_id"] = p.DocumentID.String()
				}
				if p.LastEditSource == types.EditUser && len(p.Attributes) > 0 {
					res["attributes"] = p.Attributes // a hand edit may not show them
				}
				return res, nil
			})); err != nil {
			return nil, err
		}

		type searchArgs struct {
			Query string `json:"query" jsonschema:"required"`
		}
		if err := add(utils.InferTool("wiki_search",
			"Full-text search in this case's wiki pages (accent-insensitive). Returns slug, title and a matching snippet.",
			func(ctx context.Context, a searchArgs) (map[string]any, error) {
				sc, err := caseScope(ctx)
				if err != nil {
					return nil, err
				}
				hits, err := wiki.SearchPages(ctx, sc.CaseID, a.Query, 20)
				if err != nil {
					return nil, sc.alive(ctx, searcher, err)
				}
				return map[string]any{"pages": hits}, nil
			})); err != nil {
			return nil, err
		}

		type linksArgs struct {
			Page      string `json:"page" jsonschema:"required" jsonschema_description:"Index id of the page (w5), or its slug."`
			Relation  string `json:"relation,omitempty" jsonschema_description:"Only this relation of the wiki schema, e.g. chu_tai_khoan."`
			Direction string `json:"direction,omitempty" jsonschema:"enum=in,enum=out,enum=both" jsonschema_description:"out: links from the page, in: links to it, both (default)."`
		}
		if err := add(utils.InferTool("wiki_links",
			"List the wiki pages linked to or from a page of this case, with the relation type (e.g. account holder, representative) and the footnote proving it.",
			func(ctx context.Context, a linksArgs) (map[string]any, error) {
				sc, err := caseScope(ctx)
				if err != nil {
					return nil, err
				}
				dir := a.Direction
				if dir == "both" {
					dir = ""
				}
				slug, err := pageSlug(ctx, wiki, sc.CaseID, a.Page)
				if err != nil {
					return nil, sc.alive(ctx, searcher, err)
				}
				links, err := wiki.Links(ctx, sc.CaseID, slug, a.Relation, dir)
				if err != nil {
					if !searcher.CaseAlive(ctx, sc.Owner, sc.CaseID) {
						return nil, errCaseGone
					}
					return nil, fmt.Errorf("wiki page %q not found", a.Page)
				}
				return map[string]any{"links": links}, nil
			})); err != nil {
			return nil, err
		}
	}

	if err := add(utils.InferTool("kb_search",
		"Search the files of this case for passages that answer a question. mode=reasoning reads the case wiki index, then wiki pages, then the source pages, and returns quotes verified against the source lines with citation_id, file and page. mode=keyword is fast full-text for exact codes and amounts. metadata/document_ids/pages only narrow the search inside the case. Cite answers with the returned citation_id.",
		func(ctx context.Context, a kbSearchArgs) (map[string]any, error) {
			sc, err := caseScope(ctx)
			if err != nil {
				return nil, err
			}
			docs, err := parseIDs(a.DocumentIDs)
			if err != nil {
				return nil, err
			}
			resp, err := searcher.Search(ctx, types.SearchRequest{Query: a.Query, CaseIDs: []uuid.UUID{sc.CaseID}, DocumentIDs: docs,
				Metadata: types.MetadataFilter(a.Metadata), Mode: a.Mode, PageFrom: a.PageFrom, PageTo: a.PageTo, TopK: a.TopK, OwnerID: sc.Owner})
			if err != nil {
				return nil, sc.alive(ctx, searcher, err)
			}
			hits := make([]kbHit, len(resp.Hits))
			for i, h := range resp.Hits {
				hits[i] = kbHit{CitationID: h.CitationID, File: h.FileName, DocumentID: h.DocumentID.String(), Page: h.PageNo, Quote: h.Quote,
					Section: strings.Join(h.NodePath, " › "), Via: h.Via, WikiPages: h.WikiPages, Metadata: h.Metadata, Reason: h.Reason}
			}
			res := map[string]any{"hits": hits, "documents_considered": resp.Trace.CandidateDocs}
			if len(hits) == 0 {
				res["note"] = "No passage found. Try other wording, mode=keyword for exact codes, wiki_index to see what the case holds, or kb_read_pages."
			}
			return res, nil
		})); err != nil {
		return nil, err
	}

	type listArgs struct {
		Metadata map[string]any `json:"metadata,omitempty" jsonschema_description:"Metadata filter on the case's files, e.g. {\"loai_giay_to\": \"HOP_DONG\"}."`
		Status   []string       `json:"status,omitempty" jsonschema_description:"Only these statuses (queued, parsing, indexing, enriching, completed, partial, failed…)."`
		Limit    int            `json:"limit,omitempty"`
	}
	if err := add(utils.InferTool("kb_list_documents",
		"List the files of this case (optionally filtered by metadata or status) with their status, wiki status, page count and card summary.",
		func(ctx context.Context, a listArgs) (map[string]any, error) {
			sc, err := caseScope(ctx)
			if err != nil {
				return nil, err
			}
			ds, err := searcher.ListDocuments(ctx, sc.Owner, sc.CaseID, types.MetadataFilter(a.Metadata), a.Status, a.Limit)
			if err != nil {
				return nil, sc.alive(ctx, searcher, err)
			}
			return map[string]any{"documents": toBriefs(ds)}, nil
		})); err != nil {
		return nil, err
	}

	type valuesArgs struct {
		Key string `json:"key" jsonschema:"required" jsonschema_description:"Metadata key, e.g. loai_giay_to."`
	}
	if err := add(utils.InferTool("kb_metadata_values",
		"List the distinct values of a metadata key among the files of this case, with file counts.",
		func(ctx context.Context, a valuesArgs) (map[string]any, error) {
			sc, err := caseScope(ctx)
			if err != nil {
				return nil, err
			}
			vals, err := searcher.MetadataValues(ctx, sc.Owner, sc.CaseID, a.Key)
			if err != nil {
				return nil, sc.alive(ctx, searcher, err)
			}
			return map[string]any{"key": a.Key, "values": vals}, nil
		})); err != nil {
		return nil, err
	}

	type findArgs struct {
		DocumentID string `json:"document_id" jsonschema:"required"`
		Query      string `json:"query" jsonschema:"required"`
		PageFrom   int    `json:"page_from,omitempty" jsonschema_description:"Only pages from this page number (inclusive)."`
		PageTo     int    `json:"page_to,omitempty" jsonschema_description:"Only pages up to this page number (inclusive)."`
	}
	if err := add(utils.InferTool("kb_find_in_document",
		"Find where a term appears in one file of this case (accent-insensitive), grouped by page with line numbers. page_from/page_to are optional (default: every page).",
		func(ctx context.Context, a findArgs) (map[string]any, error) {
			sc, id, err := docArg(ctx, searcher, a.DocumentID)
			if err != nil {
				return nil, err
			}
			pages, err := searcher.FindInDocument(ctx, sc.Owner, id, a.Query, types.SearchKeyword, a.PageFrom, a.PageTo)
			if err != nil {
				return nil, err
			}
			return map[string]any{"pages": pages}, nil
		})); err != nil {
		return nil, err
	}

	type readArgs struct {
		DocumentID string `json:"document_id" jsonschema:"required"`
		PageFrom   int    `json:"page_from" jsonschema:"required"`
		PageTo     int    `json:"page_to,omitempty" jsonschema_description:"Inclusive; at most 10 pages per call."`
	}
	if err := add(utils.InferTool("kb_read_pages",
		"Read pages of a file of this case as numbered lines ([L<n>] text). Cite as doc:<document_id>:p<page>:l<a>-<b>.",
		func(ctx context.Context, a readArgs) (string, error) {
			sc, id, err := docArg(ctx, searcher, a.DocumentID)
			if err != nil {
				return "", err
			}
			return searcher.ReadPages(ctx, sc.Owner, id, a.PageFrom, max(a.PageTo, a.PageFrom))
		})); err != nil {
		return nil, err
	}

	type overviewArgs struct {
		DocumentID string `json:"document_id" jsonschema:"required"`
		PageFrom   int    `json:"page_from,omitempty" jsonschema_description:"First page (default 1)."`
		PageTo     int    `json:"page_to,omitempty" jsonschema_description:"Last page, inclusive (default: the last page)."`
	}
	if err := add(utils.InferTool("kb_page_overview",
		"Overview of a file of this case page by page: each page's title, the start of its text, line count and tree section. page_from/page_to are optional (default: every page).",
		func(ctx context.Context, a overviewArgs) (map[string]any, error) {
			sc, id, err := docArg(ctx, searcher, a.DocumentID)
			if err != nil {
				return nil, err
			}
			pages, err := searcher.PageOverview(ctx, sc.Owner, id, a.PageFrom, a.PageTo)
			if err != nil {
				return nil, err
			}
			return map[string]any{"pages": pages}, nil
		})); err != nil {
		return nil, err
	}

	type treeArgs struct {
		DocumentID string `json:"document_id" jsonschema:"required"`
		NodeID     string `json:"node_id,omitempty" jsonschema_description:"Open this node (e.g. n3); default: the first level."`
	}
	if err := add(utils.InferTool("kb_document_tree",
		"Browse a file's table of contents one level at a time (PageIndex): sections with page ranges and summaries; has_children tells which node_id to open next.",
		func(ctx context.Context, a treeArgs) (map[string]any, error) {
			sc, id, err := docArg(ctx, searcher, a.DocumentID)
			if err != nil {
				return nil, err
			}
			nodes, err := searcher.DocumentTree(ctx, sc.Owner, id)
			if err != nil {
				return nil, err
			}
			return treeLevel(nodes, strings.TrimSpace(a.NodeID))
		})); err != nil {
		return nil, err
	}

	type locateArgs struct {
		CitationID string `json:"citation_id,omitempty" jsonschema_description:"doc:<id>:p<page>:l<a>-<b>"`
		DocumentID string `json:"document_id,omitempty" jsonschema_description:"With text: the file to look in."`
		Text       string `json:"text,omitempty" jsonschema_description:"With document_id: the text to find."`
	}
	if err := add(utils.InferTool("kb_locate",
		"Resolve a citation id (or a text in a file of this case) to its exact text and position on the page.",
		func(ctx context.Context, a locateArgs) (map[string]any, error) {
			sc, err := caseScope(ctx)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(a.CitationID) == "" {
				if a.DocumentID == "" || strings.TrimSpace(a.Text) == "" {
					return nil, errors.New("give citation_id, or document_id and text")
				}
				_, id, err := docArg(ctx, searcher, a.DocumentID)
				if err != nil {
					return nil, err
				}
				pages, err := searcher.FindInDocument(ctx, sc.Owner, id, a.Text, types.SearchKeyword, 0, 0)
				if err != nil {
					return nil, err
				}
				return map[string]any{"pages": pages}, nil
			}
			hits, err := searcher.Locate(ctx, sc.Owner, a.CitationID)
			if err != nil {
				return nil, fmt.Errorf("citation %s not found", a.CitationID)
			}
			for _, h := range hits {
				if err := sc.checkDoc(ctx, searcher, h.DocumentID); err != nil {
					return nil, fmt.Errorf("citation %s not found", a.CitationID)
				}
			}
			return map[string]any{"locations": hits}, nil
		})); err != nil {
		return nil, err
	}
	return out, nil
}

// treeLevel returns the children of node (the root's when empty).
func treeLevel(nodes []types.TreeNode, node string) (map[string]any, error) {
	type n struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Pages       string `json:"pages"`
		Summary     string `json:"summary,omitempty"`
		HasChildren bool   `json:"has_children,omitempty"`
	}
	children := map[uuid.UUID]int{}
	var parent *types.TreeNode
	for i, x := range nodes {
		if x.ParentID != nil {
			children[*x.ParentID]++
		}
		if (node == "" && x.ParentID == nil) || (node != "" && x.ShortID == node) {
			parent = &nodes[i]
		}
	}
	if parent == nil {
		if node == "" {
			return map[string]any{"nodes": []n{}, "note": "the file has no table of contents yet"}, nil
		}
		return nil, fmt.Errorf("node %s not found", node)
	}
	var out []n
	for _, x := range nodes {
		if x.ParentID != nil && *x.ParentID == parent.ID {
			out = append(out, n{x.ShortID, x.Title, fmt.Sprintf("%d-%d", x.PageStart, x.PageEnd), x.Summary, children[x.ID] > 0})
		}
	}
	res := map[string]any{"node": map[string]any{"id": parent.ShortID, "title": parent.Title, "pages": fmt.Sprintf("%d-%d", parent.PageStart, parent.PageEnd),
		"summary": parent.Summary}, "children": out}
	return res, nil
}

var indexIDRe = regexp.MustCompile(`^w\d+(\.n\d+)?$`)

// pageSlug resolves an index id (w5) to its page slug; anything else is
// taken as a slug. A file line or a branch has no wiki page: its source is
// read with kb_read_pages.
func pageSlug(ctx context.Context, wiki interfaces.WikiReader, caseID uuid.UUID, ref string) (string, error) {
	ref = strings.Trim(strings.TrimSpace(ref), "[]")
	if !indexIDRe.MatchString(ref) {
		return ref, nil
	}
	v, err := wiki.IndexView(ctx, caseID, nil)
	if err != nil {
		return "", err
	}
	base, _, _ := strings.Cut(ref, ".")
	r, ok := v.Refs[ref]
	if !ok {
		r, ok = v.Refs[base]
	}
	switch {
	case !ok:
		return "", fmt.Errorf("%s is not in the wiki index", ref)
	case r.Slug != "" && ref == base:
		return r.Slug, nil
	case r.DocumentID != nil:
		return "", fmt.Errorf("%s is part of a file, not a wiki page: read it with kb_read_pages (document_id %s)", ref, r.DocumentID)
	}
	return "", fmt.Errorf("%s is not a wiki page", ref)
}
