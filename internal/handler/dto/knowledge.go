package dto

import (
	"encoding/json"

	"github.com/thanhenti/bepaylot/internal/types"
)

// CreateKBRequest is the body of POST /v1/kbs.
type CreateKBRequest struct {
	Name           string                `json:"name"`
	Description    string                `json:"description,omitempty"`
	Config         types.KBConfig        `json:"config"`
	MetadataSchema *types.MetadataSchema `json:"metadata_schema,omitempty"`
}

// UpdateKBRequest is the body of PATCH /v1/kbs/{id}.
type UpdateKBRequest struct {
	Name        *string         `json:"name,omitempty"`
	Description *string         `json:"description,omitempty"`
	Config      *types.KBConfig `json:"config,omitempty"`
}

// KBList is the response of GET /v1/kbs.
type KBList struct {
	Data []types.KnowledgeBase `json:"data"`
}

// DocumentList is the response of GET /v1/kbs/{id}/documents.
type DocumentList struct {
	Data       []types.Document `json:"data"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

// MetadataUpdateRequest is the body of PATCH /v1/documents/{id}/metadata.
type MetadataUpdateRequest struct {
	Metadata map[string]any `json:"metadata"`
	// Mode is merge (default; null values delete keys) or replace.
	Mode string `json:"mode,omitempty"`
}

// BulkMetadataRequest is the body of POST /v1/kbs/{id}/documents/metadata/bulk-update.
type BulkMetadataRequest struct {
	Filter struct {
		Metadata    types.MetadataFilter `json:"metadata,omitempty"`
		DocumentIDs []string             `json:"document_ids,omitempty"`
		BatchID     string               `json:"batch_id,omitempty"`
	} `json:"filter"`
	Set   map[string]any `json:"set,omitempty"`
	Unset []string       `json:"unset,omitempty"`
}

// BulkMetadataResponse reports how many documents changed.
type BulkMetadataResponse struct {
	Updated int64 `json:"updated"`
}

// MetadataValuesResponse lists distinct values of a key.
type MetadataValuesResponse struct {
	Key    string          `json:"key"`
	Values []MetadataValue `json:"values"`
}

// MetadataValue is one value and its document count.
type MetadataValue struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// MetadataSchemaBody wraps a KB metadata schema (null clears it).
type MetadataSchemaBody struct {
	Schema *types.MetadataSchema `json:"schema"`
}

// PagesResponse lists page rows.
type PagesResponse struct {
	Data []types.DocumentPage `json:"data"`
}

// MarkdownResponse holds markdown text.
type MarkdownResponse struct {
	Markdown string `json:"markdown"`
}

// DocumentSearchRequest is the body of POST /v1/documents/{id}/search.
type DocumentSearchRequest struct {
	Query string `json:"query"`
	Mode  string `json:"mode,omitempty"` // keyword (default) | reasoning
}

// DocumentSearchResponse groups matches by page.
type DocumentSearchResponse struct {
	Pages []types.PageSearchHit `json:"pages"`
}

// TreeResponse is a document tree, root first.
type TreeResponse struct {
	Nodes []types.TreeNode `json:"nodes"`
}

// LocationsResponse lists resolved positions.
type LocationsResponse struct {
	Hits []types.SearchHit `json:"hits"`
}

// EngineList lists OCR engines.
type EngineList struct {
	Data []EngineInfo `json:"data"`
}

// EngineInfo describes an OCR engine.
type EngineInfo struct {
	Name      string `json:"name"`
	Default   bool   `json:"default"`
	Available bool   `json:"available"`
	Error     string `json:"error,omitempty"`
}

// EntityList lists graph entities.
type EntityList struct {
	Data []types.Entity `json:"data"`
}

// WikiPageList lists wiki pages.
type WikiPageList struct {
	Data []types.WikiPage `json:"data"`
}

// WikiEditRequest is the body of PUT /v1/kbs/{id}/wiki/pages/{slug}.
type WikiEditRequest struct {
	Title   string `json:"title,omitempty"`
	Summary string `json:"summary,omitempty"`
	Content string `json:"content"`
}

// SchemaList lists graph schemas.
type SchemaList struct {
	Data []types.GraphSchema `json:"data"`
}

// SchemaTestRequest is the body of POST /v1/graph/schemas/{name}/test.
type SchemaTestRequest struct {
	Text string `json:"text"`
}

// RebuildResponse reports how many documents were queued.
type RebuildResponse struct {
	Queued int `json:"queued"`
}

// QueueStat is one queue's depth.
type QueueStat struct {
	Name      string `json:"name"`
	Pool      string `json:"pool"`
	Weight    int    `json:"weight"`
	Size      int    `json:"size"`
	Pending   int    `json:"pending"`
	Active    int    `json:"active"`
	Scheduled int    `json:"scheduled"`
	Retry     int    `json:"retry"`
	Archived  int    `json:"archived"`
	Error     string `json:"error,omitempty"`
}

// QueueStats lists every queue.
type QueueStats struct {
	Data []QueueStat `json:"data"`
}

// DeadLetter is an archived failed task.
type DeadLetter struct {
	ID        int64           `json:"id"`
	TaskType  string          `json:"task_type"`
	Queue     string          `json:"queue"`
	Scope     string          `json:"scope"`
	ScopeID   string          `json:"scope_id"`
	Payload   json.RawMessage `json:"payload" swaggertype:"object"`
	LastError string          `json:"last_error"`
	FailCount int             `json:"fail_count"`
	FailedAt  string          `json:"failed_at"`
}

// DeadLetterList lists dead letters.
type DeadLetterList struct {
	Data []DeadLetter `json:"data"`
}

// OK is a generic success body.
type OK struct {
	OK bool `json:"ok"`
}
