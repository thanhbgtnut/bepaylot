package types

import "github.com/google/uuid"

// Search modes (§6.5).
const (
	SearchReasoning = "reasoning"
	SearchKeyword   = "keyword"
	SearchMetadata  = "metadata"
)

// SearchRequest is the input of POST /v1/search and the kb_search tool.
type SearchRequest struct {
	Query       string         `json:"query"`
	KBIDs       []uuid.UUID    `json:"kb_ids"`
	DocumentIDs []uuid.UUID    `json:"document_ids,omitempty"`
	Metadata    MetadataFilter `json:"metadata,omitempty"`
	PageFrom    int            `json:"page_from,omitempty"`
	PageTo      int            `json:"page_to,omitempty"`
	Mode        string         `json:"mode,omitempty"`
	TopK        int            `json:"top_k,omitempty"`
	// OwnerID scopes the search to KBs the caller owns. Set by the server.
	OwnerID uuid.UUID `json:"-"`
}

// SearchHit is one located answer passage.
type SearchHit struct {
	DocumentID uuid.UUID      `json:"document_id"`
	FileName   string         `json:"file_name"`
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
	BBoxes     []BBox         `json:"bboxes"`
}

// SearchTrace explains how a reasoning search reached its hits.
type SearchTrace struct {
	Mode          string              `json:"mode"`
	CandidateDocs int                 `json:"candidate_docs"`
	SelectedDocs  []uuid.UUID         `json:"selected_docs"`
	SelectedNodes map[string][]string `json:"selected_nodes,omitempty"`
	LLMCalls      int                 `json:"llm_calls"`
	DroppedHits   int                 `json:"dropped_hits"`
	Fallback      string              `json:"fallback,omitempty"`
	Truncated     bool                `json:"truncated,omitempty"`
	Cached        bool                `json:"cached,omitempty"`
	ElapsedMs     int64               `json:"elapsed_ms"`
}

// SearchResponse bundles hits and trace.
type SearchResponse struct {
	Hits      []SearchHit     `json:"hits"`
	Documents []DocumentBrief `json:"documents,omitempty"`
	Trace     SearchTrace     `json:"trace"`
}

// DocumentBrief is a compact document listing entry.
type DocumentBrief struct {
	ID        uuid.UUID      `json:"id"`
	KBID      uuid.UUID      `json:"kb_id"`
	FileName  string         `json:"file_name"`
	Status    string         `json:"status"`
	PageCount int            `json:"page_count"`
	Title     string         `json:"title,omitempty"`
	DocType   string         `json:"doc_type,omitempty"`
	Summary   string         `json:"summary,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
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
