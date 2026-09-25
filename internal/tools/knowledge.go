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

// KBScope is what a session may search: the caller, the session's knowledge
// bases and an optional pinned metadata filter (§8.1).
type KBScope struct {
	Owner  uuid.UUID
	KBIDs  []uuid.UUID
	Filter types.MetadataFilter
}

type kbScopeKey struct{}

// WithKBScope attaches the knowledge scope for the knowledge tools.
func WithKBScope(ctx context.Context, s KBScope) context.Context {
	return context.WithValue(ctx, kbScopeKey{}, s)
}

func kbScope(ctx context.Context) (KBScope, error) {
	s, ok := ctx.Value(kbScopeKey{}).(KBScope)
	if !ok || s.Owner == uuid.Nil || len(s.KBIDs) == 0 {
		return s, errors.New("no knowledge base is attached to this conversation")
	}
	return s, nil
}

// restrict keeps only requested KBs that are in scope (all when none given).
func (s KBScope) restrict(ids []string) ([]uuid.UUID, error) {
	if len(ids) == 0 {
		return s.KBIDs, nil
	}
	allowed := map[uuid.UUID]bool{}
	for _, id := range s.KBIDs {
		allowed[id] = true
	}
	var out []uuid.UUID
	for _, raw := range ids {
		id, err := uuid.Parse(strings.TrimSpace(raw))
		if err != nil || !allowed[id] {
			return nil, fmt.Errorf("knowledge base %q is not attached to this conversation", raw)
		}
		out = append(out, id)
	}
	return out, nil
}

// merge ANDs the pinned filter with a requested one; pinned keys win.
func (s KBScope) merge(f map[string]any) types.MetadataFilter {
	out := types.MetadataFilter{}
	for k, v := range f {
		out[k] = v
	}
	for k, v := range s.Filter {
		out[k] = v
	}
	return out
}

// SetKnowledgeTools builds the knowledge-base tools (§8.1). They are bound
// only in sessions that call EnableKnowledge. graph may be nil.
func (r *Registry) SetKnowledgeTools(searcher interfaces.Searcher, graph interfaces.GraphQuerier) error {
	ts, err := knowledgeTools(searcher, graph)
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

// EnableKnowledge binds the knowledge tools for this turn.
func (s *Session) EnableKnowledge() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for n, e := range s.knowledge {
		s.builtin[n] = e
		s.visible[n] = struct{}{}
	}
}

type kbSearchArgs struct {
	Query       string         `json:"query" jsonschema:"required" jsonschema_description:"The question or keywords to find in the documents."`
	KBIDs       []string       `json:"kb_ids,omitempty" jsonschema_description:"Restrict to these knowledge base ids (default: all attached)."`
	DocumentIDs []string       `json:"document_ids,omitempty" jsonschema_description:"Restrict to these document ids."`
	Metadata    map[string]any `json:"metadata,omitempty" jsonschema_description:"Metadata filter, e.g. {\"ma_ho_so\": \"HS-2026-000123\"} or {\"ngay_nop\": {\"gte\": \"2026-01-01\"}}. Operators: eq, in, prefix, gte, gt, lte, lt, exists."`
	Mode        string         `json:"mode,omitempty" jsonschema:"enum=reasoning,enum=keyword" jsonschema_description:"reasoning (default, reads the documents) or keyword (fast exact terms, codes, amounts)."`
	PageFrom    int            `json:"page_from,omitempty"`
	PageTo      int            `json:"page_to,omitempty"`
	TopK        int            `json:"top_k,omitempty"`
}

type kbHit struct {
	CitationID string         `json:"citation_id"`
	File       string         `json:"file"`
	Page       int            `json:"page"`
	Quote      string         `json:"quote"`
	Section    string         `json:"section,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Reason     string         `json:"reason,omitempty"`
}

type docBrief struct {
	DocumentID string         `json:"document_id"`
	File       string         `json:"file"`
	Title      string         `json:"title,omitempty"`
	Type       string         `json:"type,omitempty"`
	Pages      int            `json:"pages"`
	Status     string         `json:"status"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

func toBriefs(ds []types.DocumentBrief) []docBrief {
	out := make([]docBrief, len(ds))
	for i, d := range ds {
		out[i] = docBrief{DocumentID: d.ID.String(), File: d.FileName, Title: d.Title, Type: d.DocType, Pages: d.PageCount, Status: d.Status, Metadata: d.Metadata}
	}
	return out
}

func parseIDs(raw []string) ([]uuid.UUID, error) {
	var out []uuid.UUID
	for _, r := range raw {
		id, err := uuid.Parse(strings.TrimSpace(r))
		if err != nil {
			return nil, fmt.Errorf("invalid id %q", r)
		}
		out = append(out, id)
	}
	return out, nil
}

func knowledgeTools(searcher interfaces.Searcher, graph interfaces.GraphQuerier) ([]tool.InvokableTool, error) {
	var out []tool.InvokableTool
	add := func(t tool.InvokableTool, err error) error {
		if err != nil {
			return err
		}
		out = append(out, t)
		return nil
	}

	if err := add(utils.InferTool("kb_search",
		"Search the attached knowledge bases for passages that answer a question. Returns quotes with citation_id, file and page. Use the metadata filter when the user names a record (e.g. a case code). Cite answers with the returned citation_id.",
		func(ctx context.Context, a kbSearchArgs) (map[string]any, error) {
			sc, err := kbScope(ctx)
			if err != nil {
				return nil, err
			}
			kbs, err := sc.restrict(a.KBIDs)
			if err != nil {
				return nil, err
			}
			docs, err := parseIDs(a.DocumentIDs)
			if err != nil {
				return nil, err
			}
			resp, err := searcher.Search(ctx, types.SearchRequest{Query: a.Query, KBIDs: kbs, DocumentIDs: docs, Metadata: sc.merge(a.Metadata),
				Mode: a.Mode, PageFrom: a.PageFrom, PageTo: a.PageTo, TopK: a.TopK, OwnerID: sc.Owner})
			if err != nil {
				return nil, err
			}
			hits := make([]kbHit, len(resp.Hits))
			for i, h := range resp.Hits {
				hits[i] = kbHit{CitationID: h.CitationID, File: h.FileName, Page: h.PageNo, Quote: h.Quote, Section: strings.Join(h.NodePath, " › "), Metadata: h.Metadata, Reason: h.Reason}
			}
			res := map[string]any{"hits": hits, "documents_considered": resp.Trace.CandidateDocs}
			if len(hits) == 0 {
				res["note"] = "No passage found. Try other wording, mode=keyword for exact codes, or check the metadata filter."
			}
			return res, nil
		})); err != nil {
		return nil, err
	}

	type listArgs struct {
		KBID     string         `json:"kb_id,omitempty" jsonschema_description:"Knowledge base id (default: the first attached)."`
		Metadata map[string]any `json:"metadata,omitempty" jsonschema_description:"Metadata filter, e.g. {\"ma_ho_so\": \"HS-2026-000123\"}."`
		Limit    int            `json:"limit,omitempty"`
	}
	if err := add(utils.InferTool("kb_list_documents",
		"List the documents of a knowledge base, optionally filtered by metadata (e.g. every file of one case code), with their status and page count.",
		func(ctx context.Context, a listArgs) (map[string]any, error) {
			sc, err := kbScope(ctx)
			if err != nil {
				return nil, err
			}
			kbs, err := sc.restrict(nonEmpty(a.KBID))
			if err != nil {
				return nil, err
			}
			ds, err := searcher.ListDocuments(ctx, sc.Owner, kbs[0], sc.merge(a.Metadata), a.Limit)
			if err != nil {
				return nil, err
			}
			return map[string]any{"documents": toBriefs(ds)}, nil
		})); err != nil {
		return nil, err
	}

	type valuesArgs struct {
		KBID string `json:"kb_id,omitempty"`
		Key  string `json:"key" jsonschema:"required" jsonschema_description:"Metadata key, e.g. ma_ho_so."`
	}
	if err := add(utils.InferTool("kb_metadata_values",
		"List the distinct values of a metadata key in a knowledge base with document counts (e.g. which case codes exist).",
		func(ctx context.Context, a valuesArgs) (map[string]any, error) {
			sc, err := kbScope(ctx)
			if err != nil {
				return nil, err
			}
			kbs, err := sc.restrict(nonEmpty(a.KBID))
			if err != nil {
				return nil, err
			}
			vals, err := searcher.MetadataValues(ctx, sc.Owner, kbs[0], a.Key)
			if err != nil {
				return nil, err
			}
			return map[string]any{"key": a.Key, "values": vals}, nil
		})); err != nil {
		return nil, err
	}

	type findArgs struct {
		DocumentID string `json:"document_id" jsonschema:"required"`
		Query      string `json:"query" jsonschema:"required"`
	}
	if err := add(utils.InferTool("kb_find_in_document",
		"Find where a term appears in one document (accent-insensitive), grouped by page with line numbers.",
		func(ctx context.Context, a findArgs) (map[string]any, error) {
			sc, err := kbScope(ctx)
			if err != nil {
				return nil, err
			}
			id, err := uuid.Parse(a.DocumentID)
			if err != nil {
				return nil, fmt.Errorf("invalid document_id")
			}
			pages, err := searcher.FindInDocument(ctx, sc.Owner, id, a.Query, types.SearchKeyword)
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
		"Read document pages as numbered lines ([L<n>] text). Cite as doc:<document_id>:p<page>:l<a>-<b>.",
		func(ctx context.Context, a readArgs) (string, error) {
			sc, err := kbScope(ctx)
			if err != nil {
				return "", err
			}
			id, err := uuid.Parse(a.DocumentID)
			if err != nil {
				return "", fmt.Errorf("invalid document_id")
			}
			return searcher.ReadPages(ctx, sc.Owner, id, a.PageFrom, max(a.PageTo, a.PageFrom))
		})); err != nil {
		return nil, err
	}

	type treeArgs struct {
		DocumentID string `json:"document_id" jsonschema:"required"`
	}
	if err := add(utils.InferTool("kb_document_tree",
		"Show a document's table of contents: sections with page ranges and summaries.",
		func(ctx context.Context, a treeArgs) (map[string]any, error) {
			sc, err := kbScope(ctx)
			if err != nil {
				return nil, err
			}
			id, err := uuid.Parse(a.DocumentID)
			if err != nil {
				return nil, fmt.Errorf("invalid document_id")
			}
			nodes, err := searcher.DocumentTree(ctx, sc.Owner, id)
			if err != nil {
				return nil, err
			}
			type n struct {
				ID      string `json:"id"`
				Level   int    `json:"level"`
				Title   string `json:"title"`
				Pages   string `json:"pages"`
				Summary string `json:"summary,omitempty"`
			}
			out := make([]n, len(nodes))
			for i, x := range nodes {
				out[i] = n{x.ShortID, x.Level, x.Title, fmt.Sprintf("%d-%d", x.PageStart, x.PageEnd), x.Summary}
			}
			return map[string]any{"nodes": out}, nil
		})); err != nil {
		return nil, err
	}

	type locateArgs struct {
		CitationID string `json:"citation_id" jsonschema:"required"`
	}
	if err := add(utils.InferTool("kb_locate",
		"Resolve a citation id to its exact text and position on the page.",
		func(ctx context.Context, a locateArgs) (map[string]any, error) {
			sc, err := kbScope(ctx)
			if err != nil {
				return nil, err
			}
			hits, err := searcher.Locate(ctx, sc.Owner, a.CitationID)
			if err != nil {
				return nil, err
			}
			return map[string]any{"locations": hits}, nil
		})); err != nil {
		return nil, err
	}

	if graph == nil {
		return out, nil
	}
	type entArgs struct {
		Query string `json:"query" jsonschema:"required"`
		Type  string `json:"type,omitempty"`
		KBID  string `json:"kb_id,omitempty"`
	}
	if err := add(utils.InferTool("graph_search_entities",
		"Find entities (people, organizations, codes...) extracted from the knowledge base, with attributes and wiki slug.",
		func(ctx context.Context, a entArgs) (map[string]any, error) {
			sc, err := kbScope(ctx)
			if err != nil {
				return nil, err
			}
			kbs, err := sc.restrict(nonEmpty(a.KBID))
			if err != nil {
				return nil, err
			}
			var all []types.Entity
			for _, kb := range kbs {
				es, err := graph.SearchEntities(ctx, sc.Owner, kb, a.Query, a.Type, 20)
				if err != nil {
					return nil, err
				}
				all = append(all, es...)
			}
			return map[string]any{"entities": all}, nil
		})); err != nil {
		return nil, err
	}
	type nbArgs struct {
		EntityID      string   `json:"entity_id" jsonschema:"required"`
		RelationTypes []string `json:"relation_types,omitempty"`
		Depth         int      `json:"depth,omitempty" jsonschema_description:"1-3, default 1."`
	}
	if err := add(utils.InferTool("graph_neighbors",
		"Show the entities related to an entity and how (relation types), up to 3 hops.",
		func(ctx context.Context, a nbArgs) (*types.Subgraph, error) {
			sc, err := kbScope(ctx)
			if err != nil {
				return nil, err
			}
			id, err := uuid.Parse(a.EntityID)
			if err != nil {
				return nil, fmt.Errorf("invalid entity_id")
			}
			return graph.Neighbors(ctx, sc.Owner, id, a.RelationTypes, max(1, a.Depth))
		})); err != nil {
		return nil, err
	}
	type wikiArgs struct {
		Slug string `json:"slug" jsonschema:"required"`
		KBID string `json:"kb_id,omitempty"`
	}
	if err := add(utils.InferTool("wiki_read",
		"Read an entity's wiki page (markdown with footnote citations).",
		func(ctx context.Context, a wikiArgs) (*types.WikiPage, error) {
			sc, err := kbScope(ctx)
			if err != nil {
				return nil, err
			}
			kbs, err := sc.restrict(nonEmpty(a.KBID))
			if err != nil {
				return nil, err
			}
			var lastErr error
			for _, kb := range kbs {
				p, err := graph.WikiPage(ctx, sc.Owner, kb, a.Slug)
				if err == nil {
					return p, nil
				}
				lastErr = err
			}
			return nil, lastErr
		})); err != nil {
		return nil, err
	}
	return out, nil
}

func nonEmpty(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return []string{s}
}
