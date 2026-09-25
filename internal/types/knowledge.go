package types

import (
	"time"

	"github.com/google/uuid"
)

// Document lifecycle states (documents.status).
const (
	DocQueued     = "queued"
	DocSplitting  = "splitting"
	DocParsing    = "parsing"
	DocAssembling = "assembling"
	DocIndexing   = "indexing"
	DocEnriching  = "enriching"
	DocCompleted  = "completed"
	DocPartial    = "partial"
	DocFailed     = "failed"
	DocCancelled  = "cancelled"
	DocDeleting   = "deleting"
)

// Stage states used by parse_status / index_status / graph_status.
const (
	StagePending    = "pending"
	StageProcessing = "processing"
	StageDone       = "done"
	StagePartial    = "partial"
	StageFailed     = "failed"
	StageSkipped    = "skipped"
)

// Page states (document_pages.status).
const (
	PagePending   = "pending"
	PageRendering = "rendering"
	PageRendered  = "rendered"
	PageOCR       = "ocr"
	PageDone      = "done"
	PageFailed    = "failed"
)

// Text sources for pages and lines (§5.8).
const (
	TextSourceOCR       = "ocr"
	TextSourceLayer     = "layer"
	TextSourceMerged    = "merged"
	TextSourceLayerOnly = "layer_only"
)

// Searchable reports whether documents in this state can be searched.
func Searchable(status string) bool {
	switch status {
	case DocCompleted, DocPartial, DocEnriching:
		return true
	}
	return false
}

// Terminal reports whether no more pipeline work will run for the status.
func Terminal(status string) bool {
	switch status {
	case DocCompleted, DocPartial, DocFailed, DocCancelled, DocDeleting:
		return true
	}
	return false
}

// KnowledgeBase groups documents that are searched together.
type KnowledgeBase struct {
	ID             uuid.UUID       `json:"id"`
	OwnerID        uuid.UUID       `json:"owner_id"`
	Name           string          `json:"name"`
	Description    string          `json:"description,omitempty"`
	Config         KBConfig        `json:"config"`
	MetadataSchema *MetadataSchema `json:"metadata_schema,omitempty"`
	GraphSchemaID  *uuid.UUID      `json:"graph_schema_id,omitempty"`
	IsTemporary    bool            `json:"is_temporary"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// KBConfig holds per-KB overrides of the global pipeline defaults.
type KBConfig struct {
	ParserEngine string `json:"parser_engine,omitempty"`
	GraphEnabled bool   `json:"graph_enabled,omitempty"`
	GraphSchema  string `json:"graph_schema,omitempty"`
}

// UploadBatch records one multi-file upload request.
type UploadBatch struct {
	ID        uuid.UUID      `json:"id"`
	KBID      uuid.UUID      `json:"kb_id"`
	CreatedBy uuid.UUID      `json:"created_by"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	FileCount int            `json:"file_count"`
	Accepted  int            `json:"accepted"`
	Rejected  int            `json:"rejected"`
	CreatedAt time.Time      `json:"created_at"`
}

// Document is one uploaded file and its pipeline state.
type Document struct {
	ID              uuid.UUID      `json:"id"`
	KBID            uuid.UUID      `json:"kb_id"`
	BatchID         *uuid.UUID     `json:"batch_id,omitempty"`
	FileName        string         `json:"file_name"`
	MimeType        string         `json:"mime_type"`
	SizeBytes       int64          `json:"size_bytes"`
	SHA256          string         `json:"-"`
	StorageKey      string         `json:"-"`
	PageCount       int            `json:"page_count"`
	Gen             int            `json:"gen"`
	Status          string         `json:"status"`
	ParseStatus     string         `json:"parse_status"`
	IndexStatus     string         `json:"index_status"`
	GraphStatus     string         `json:"graph_status"`
	PagesDone       int            `json:"pages_done"`
	PagesFailed     int            `json:"pages_failed"`
	PagesTextLayer  int            `json:"pages_text_layer"`
	PDFAPart        *int           `json:"pdfa_part,omitempty"`
	PDFAConformance string         `json:"pdfa_conformance,omitempty"`
	PDFInfo         map[string]any `json:"pdf_info"`
	Engine          string         `json:"engine,omitempty"`
	MarkdownKey     string         `json:"-"`
	Error           string         `json:"error,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	Title           string         `json:"title,omitempty"`
	DocType         string         `json:"doc_type,omitempty"`
	Summary         string         `json:"summary,omitempty"`
	CreatedBy       uuid.UUID      `json:"created_by"`
	Interactive     bool           `json:"-"` // uploaded from chat: uses the high-priority lanes
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

// Progress is PagesDone+PagesFailed over PageCount, in [0,1].
func (d *Document) Progress() float64 {
	if d.PageCount <= 0 {
		return 0
	}
	return float64(d.PagesDone+d.PagesFailed) / float64(d.PageCount)
}

// DocumentPage is one page of a document.
type DocumentPage struct {
	DocumentID   uuid.UUID  `json:"document_id"`
	PageNo       int        `json:"page_no"`
	Gen          int        `json:"gen"`
	Status       string     `json:"status"`
	Attempts     int        `json:"attempts"`
	Width        int        `json:"width"`
	Height       int        `json:"height"`
	DPI          float64    `json:"dpi"`
	WidthPt      float64    `json:"width_pt"`
	HeightPt     float64    `json:"height_pt"`
	Rotation     int        `json:"rotation"`
	ImageKey     string     `json:"-"`
	RawKey       string     `json:"-"`
	TextLayerKey string     `json:"-"`
	TextSource   string     `json:"text_source"`
	TextQuality  float64    `json:"text_quality"`
	RenderMs     int        `json:"render_ms"`
	OCRMs        int        `json:"ocr_ms"`
	Engine       string     `json:"engine,omitempty"`
	Markdown     string     `json:"markdown"`
	TextPlain    string     `json:"text_plain"`
	DocMdOffset  int        `json:"doc_md_offset"`
	IsBlank      bool       `json:"is_blank"`
	Error        string     `json:"error,omitempty"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

// PageStatusCounts summarises a document's pages by state.
type PageStatusCounts struct {
	Pending   int `json:"pending"`
	Rendering int `json:"rendering"`
	Rendered  int `json:"rendered"`
	OCR       int `json:"o_c_r"`
	Done      int `json:"done"`
	Failed    int `json:"failed"`
}

// Section is a leaf content unit of the document tree (§6.3).
type Section struct {
	ID          uuid.UUID    `json:"id"`
	DocumentID  uuid.UUID    `json:"document_id"`
	KBID        uuid.UUID    `json:"kb_id"`
	Gen         int          `json:"gen"`
	Seq         int          `json:"seq"`
	Kind        string       `json:"kind"` // text | table | figure
	Content     string       `json:"content"`
	HeadingPath []string     `json:"heading_path"`
	PageStart   int          `json:"page_start"`
	PageEnd     int          `json:"page_end"`
	LineFrom    int          `json:"line_from"`
	LineTo      int          `json:"line_to"`
	DocMdStart  int          `json:"doc_md_start"`
	DocMdEnd    int          `json:"doc_md_end"`
	SourceSpans []SourceSpan `json:"source_spans"`
	TokenCount  int          `json:"token_count"`
}

// SourceSpan points back to lines on one page.
type SourceSpan struct {
	Page     int   `json:"page"`
	LineFrom int   `json:"line_from"`
	LineTo   int   `json:"line_to"`
	BlockNos []int `json:"block_nos,omitempty"`
	BBox     BBox  `json:"bbox"`
}

// TreeNode is one node of the vectorless document tree (§6.4). The root node
// (ParentID == nil) is the document card.
type TreeNode struct {
	ID         uuid.UUID   `json:"id"`
	DocumentID uuid.UUID   `json:"document_id"`
	Gen        int         `json:"gen"`
	ParentID   *uuid.UUID  `json:"parent_id,omitempty"`
	ShortID    string      `json:"short_id"`
	Ord        int         `json:"ord"`
	Level      int         `json:"level"`
	Title      string      `json:"title,omitempty"`
	Origin     string      `json:"origin"` // root | bookmark | heading | llm | page
	PageStart  int         `json:"page_start"`
	PageEnd    int         `json:"page_end"`
	Summary    string      `json:"summary,omitempty"`
	SectionIDs []uuid.UUID `json:"section_ids"`
	TokenCount int         `json:"token_count"`
}

// ProcessingSpan records one pipeline stage execution for observability.
type ProcessingSpan struct {
	ID         int64      `json:"id"`
	DocumentID uuid.UUID  `json:"document_id"`
	Gen        int        `json:"gen"`
	Stage      string     `json:"stage"`
	Ref        string     `json:"ref"`
	Status     string     `json:"status"`
	Error      string     `json:"error,omitempty"`
	StartedAt  time.Time  `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}
