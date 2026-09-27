package types

import "github.com/google/uuid"

// Search modes (§6.5).
const (
	SearchReasoning = "reasoning"
	SearchKeyword   = "keyword"
	SearchMetadata  = "metadata"
)

// SearchRequest is the input of POST /v1/search and the kb_search tool. The
// scope is CaseIDs, or KBIDs (every case of those KBs; API only, never the
// agent). DocumentIDs and Metadata only narrow the scope (§6.10 step 1).
type SearchRequest struct {
	Query       string         `json:"query"`
	CaseIDs     []uuid.UUID    `json:"case_ids,omitempty"`
	KBIDs       []uuid.UUID    `json:"kb_ids,omitempty"`
	DocumentIDs []uuid.UUID    `json:"document_ids,omitempty"`
	Metadata    MetadataFilter `json:"metadata,omitempty"`
	PageFrom    int            `json:"page_from,omitempty"`
	PageTo      int            `json:"page_to,omitempty"`
	Mode        string         `json:"mode,omitempty"`
	TopK        int            `json:"top_k,omitempty"`
	// OwnerID scopes the search to KBs the caller owns. Set by the server.
	OwnerID uuid.UUID `json:"-"`
	// SkipWiki searches the source documents only (tree + pages).
	SkipWiki bool `json:"-"`
}

// SearchHit is one located answer passage.
type SearchHit struct {
	DocumentID uuid.UUID      `json:"document_id"`
	FileName   string         `json:"file_name"`
	CaseID     uuid.UUID      `json:"case_id"`
	CaseCode   string         `json:"case_code,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	PageNo     int            `json:"page_no"`
	Lines      []int          `json:"lines"`
	NodeID     string         `json:"node_id,omitempty"`
	NodePath   []string       `json:"node_path,omitempty"`
	Quote      string         `json:"quote"`
	Snippet    string         `json:"snippet,omitempty"`
	Relevance  float64        `json:"relevance"`
	Reason     string         `json:"reason,omitempty"`
	CitationID string         `json:"citation_id"`
	// Via is "wiki" when the hit came from a wiki footnote, "raw" when it was
	// read from the source pages, "keyword" for full-text hits.
	Via       string   `json:"via,omitempty"`
	WikiPages []string `json:"wiki_pages,omitempty"`
	BBoxes    []BBox   `json:"bboxes"`
}

// SearchTrace explains how a reasoning search reached its hits.
type SearchTrace struct {
	Mode           string              `json:"mode"`
	CandidateDocs  int                 `json:"candidate_docs"`
	Cases          []uuid.UUID         `json:"cases,omitempty"`
	WikiVersions   map[string]int      `json:"wiki_versions,omitempty"`
	WikiPages      []string            `json:"wiki_pages,omitempty"`
	RawRefs        []string            `json:"raw_refs,omitempty"`
	StaleFootnotes int                 `json:"stale_footnotes,omitempty"`
	SelectedDocs   []uuid.UUID         `json:"selected_docs"`
	SelectedNodes  map[string][]string `json:"selected_nodes,omitempty"`
	LLMCalls       int                 `json:"llm_calls"`
	DroppedHits    int                 `json:"dropped_hits"`
	Fallback       string              `json:"fallback,omitempty"`
	Truncated      bool                `json:"truncated,omitempty"`
	Cached         bool                `json:"cached,omitempty"`
	ElapsedMs      int64               `json:"elapsed_ms"`
}

// SearchResponse bundles hits and trace.
type SearchResponse struct {
	Hits      []SearchHit     `json:"hits"`
	Documents []DocumentBrief `json:"documents,omitempty"`
	Trace     SearchTrace     `json:"trace"`
}

// DocumentBrief is a compact document listing entry.
type DocumentBrief struct {
	ID         uuid.UUID      `json:"id"`
	KBID       uuid.UUID      `json:"kb_id"`
	CaseID     uuid.UUID      `json:"case_id"`
	FileName   string         `json:"file_name"`
	Status     string         `json:"status"`
	WikiStatus string         `json:"wiki_status,omitempty"`
	PageCount  int            `json:"page_count"`
	Title      string         `json:"title,omitempty"`
	Summary    string         `json:"summary,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// PageOverview is a glimpse of one page used to decide which pages to read
// (§6.7): its first title, the start of its text and its place in the tree.
// It describes the content only; what kind of document a page belongs to is
// left to the reader.
type PageOverview struct {
	PageNo  int    `json:"page_no"`
	Title   string `json:"title,omitempty"`
	Preview string `json:"preview"`
	Lines   int    `json:"lines"`
	Section string `json:"section,omitempty"`
	Blank   bool   `json:"blank,omitempty"`
}

// PageSearchHit groups keyword matches of one page (§6.7).
type PageSearchHit struct {
	PageNo int         `json:"page_no"`
	Score  float64     `json:"score"`
	Hits   []LineMatch `json:"hits"`
}

// LineMatch is one matching line with its location.
type LineMatch struct {
	LineNo  int     `json:"line_no"`
	Snippet string  `json:"snippet"`
	BBox    BBox    `json:"bbox"`
	Score   float64 `json:"score,omitempty"`
}
