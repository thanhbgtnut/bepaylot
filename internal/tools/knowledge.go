package tools

import (
	"context"
	"errors"
	"fmt"
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

// WithCaseScope attaches the case scope for the document tools.
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

// SetKnowledgeTools builds the case tools (§8.2): kb_* over the documents of
// the case, read by walking tables of contents (case TOC → document tree →
// pages). They are bound only in sessions with a case (EnableKnowledge).
func (r *Registry) SetKnowledgeTools(searcher interfaces.Searcher) error {
	ts, err := knowledgeTools(searcher)
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
	Mode        string         `json:"mode,omitempty" jsonschema:"enum=reasoning,enum=keyword" jsonschema_description:"reasoning (default: the server walks the case TOC and the trees, reads the chosen pages and returns the lines) or keyword (fast exact terms, codes, amounts)."`
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
	Metadata   map[string]any `json:"metadata,omitempty"`
}

func toBriefs(ds []types.DocumentBrief) []docBrief {
	out := make([]docBrief, len(ds))
	for i, d := range ds {
		out[i] = docBrief{DocumentID: d.ID.String(), File: d.FileName, Title: d.Title, Summary: d.Summary, Pages: d.PageCount, Status: d.Status,
			Metadata: d.Metadata}
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

func knowledgeTools(searcher interfaces.Searcher) ([]tool.InvokableTool, error) {
	var out []tool.InvokableTool
	add := func(t tool.InvokableTool, err error) error {
		if err != nil {
			return err
		}
		out = append(out, t)
		return nil
	}

	type tocArgs struct {
		Metadata map[string]any `json:"metadata,omitempty" jsonschema_description:"Metadata filter on the case's files, e.g. {\"loai_giay_to\": \"HOP_DONG\"}."`
		Expand   []string       `json:"expand,omitempty" jsonschema_description:"File refs (d<n>) to show with their whole table of contents instead of the first branches."`
	}
	if err := add(utils.InferTool("kb_case_toc",
		"Start here. The table of contents of this case: one line per file ([d<n>] file (pages) {metadata} — summary) with the first branches of its table of contents. Pick the file (and branch) that likely holds the answer, open its tree with kb_document_tree, then read only the pages of the chosen node with kb_read_pages. Do not read whole files when a node fits.",
		func(ctx context.Context, a tocArgs) (map[string]any, error) {
			sc, err := caseScope(ctx)
			if err != nil {
				return nil, err
			}
			toc, err := searcher.CaseTOC(ctx, sc.Owner, sc.CaseID, types.MetadataFilter(a.Metadata), a.Expand)
			if err != nil {
				return nil, sc.alive(ctx, searcher, err)
			}
			refs := make(map[string]string, len(toc.Documents))
			for _, d := range toc.Documents {
				refs[d.Ref] = d.DocumentID.String()
			}
			res := map[string]any{"toc": toc.Text, "document_ids": refs}
			if len(toc.Pending) > 0 {
				pending := make([]map[string]string, len(toc.Pending))
				for i, p := range toc.Pending {
					pending[i] = map[string]string{"document_id": p.DocumentID.String(), "file": p.FileName, "status": p.Status}
				}
				res["not_indexed_yet"] = pending
			}
			return res, nil
		})); err != nil {
		return nil, err
	}

	if err := add(utils.InferTool("kb_search",
		"Let the server find passages that answer a question in this case's files: mode=reasoning walks the case table of contents and the file trees, reads only the chosen pages and returns quotes verified against the source lines with citation_id, file and page. mode=keyword is fast full-text for exact codes and amounts. metadata/document_ids/pages only narrow the search inside the case. Cite answers with the returned citation_id.",
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
					Section: strings.Join(h.NodePath, " › "), Via: h.Via, Metadata: h.Metadata, Reason: h.Reason}
			}
			res := map[string]any{"hits": hits, "documents_considered": resp.Trace.CandidateDocs}
			if len(hits) == 0 {
				res["note"] = "No passage found. Try other wording, mode=keyword for exact codes, or kb_case_toc and kb_document_tree to choose pages yourself."
			}
			return res, nil
		})); err != nil {
		return nil, err
	}

	type listArgs struct {
		Metadata map[string]any `json:"metadata,omitempty" jsonschema_description:"Metadata filter on the case's files, e.g. {\"loai_giay_to\": \"HOP_DONG\"}."`
		Status   []string       `json:"status,omitempty" jsonschema_description:"Only these statuses (queued, parsing, indexing, completed, partial, failed…)."`
		Limit    int            `json:"limit,omitempty"`
	}
	if err := add(utils.InferTool("kb_list_documents",
		"List the files of this case (optionally filtered by metadata or status) with their status, page count and card summary.",
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
		"Read pages of a file of this case as numbered lines ([L<n>] text), at most 10 pages per call. Read the page range of the node chosen on the table of contents, not whole files. Cite as doc:<document_id>:p<page>:l<a>-<b>.",
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
		NodeID     string `json:"node_id,omitempty" jsonschema_description:"Only the subtree of this node (e.g. n3), for a node marked (+k mục, expand n3)."`
	}
	if err := add(utils.InferTool("kb_document_tree",
		"The table of contents of a file of this case (PageIndex): one line per node, [n<k>] title (tr. pages) — summary, indented by level; the whole tree when it fits, otherwise deep levels are collapsed as (+k mục, expand n<k>) — call again with that node_id. Choose the most specific node, then kb_read_pages on its page range.",
		func(ctx context.Context, a treeArgs) (string, error) {
			sc, id, err := docArg(ctx, searcher, a.DocumentID)
			if err != nil {
				return "", err
			}
			return searcher.DocumentTreeText(ctx, sc.Owner, id, a.NodeID)
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
