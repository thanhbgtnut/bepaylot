package types

import (
	"fmt"
	"regexp"
	"strings"
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
	DocCompleted  = "completed"
	DocPartial    = "partial"
	DocFailed     = "failed"
	DocCancelled  = "cancelled"
	DocDeleting   = "deleting"
)

// Stage states used by parse_status / index_status.
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

// Searchable reports whether documents in this state can be searched.
func Searchable(status string) bool {
	switch status {
	case DocCompleted, DocPartial:
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
	// IsTemporary marks the chat-attachment KBs of spec 0.4; none are
	// created since cases replaced them (§8.2).
	IsTemporary bool      `json:"is_temporary"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// KBConfig holds per-KB overrides of the global pipeline defaults.
type KBConfig struct {
	ParserEngine string `json:"parser_engine,omitempty"`
	// DocTypes are the document types of the KB (code, title, description)
	// and the fields to extract from a document of each type. Pages of its
	// files are classified into them from their image and they replace the
	// classification labels of the case type (§6.9.4).
	DocTypes []DocType `json:"doc_types,omitempty"`
}

// DocType is one document type of a KB with the fields the pipeline extracts
// from every document of that type (§6.9.3); they are the columns of its
// sheet (§6.9.6).
type DocType struct {
	ClassLabel
	Fields []SheetField `json:"fields,omitempty"`
}

// Labels returns the document types of the KB as classification labels.
func (c KBConfig) Labels() []ClassLabel {
	out := make([]ClassLabel, 0, len(c.DocTypes))
	for _, t := range c.DocTypes {
		out = append(out, t.ClassLabel)
	}
	return out
}

// SheetTables returns the document types that have fields, as sheet tables.
func (c KBConfig) SheetTables() []SheetTable {
	var out []SheetTable
	for _, t := range c.DocTypes {
		if len(t.Fields) > 0 {
			out = append(out, SheetTable{Label: t.Name, Title: t.Title, Description: t.Description, Fields: t.Fields})
		}
	}
	return out
}

var fieldKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,63}$`)

// ValidateDocTypes checks the codes of the document types and, per type, that
// every field has a label and a unique snake_case key.
func ValidateDocTypes(list []DocType) error {
	labels := make([]ClassLabel, 0, len(list))
	for _, t := range list {
		labels = append(labels, t.ClassLabel)
		seen := map[string]bool{}
		for _, f := range t.Fields {
			if !fieldKeyRe.MatchString(f.Key) || strings.TrimSpace(f.Label) == "" || seen[f.Key] {
				return fmt.Errorf("%s: each field needs a label and a unique snake_case key (got %q)", t.Name, f.Key)
			}
			seen[f.Key] = true
		}
	}
	return ValidateLabels(labels)
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
	CaseID          uuid.UUID      `json:"case_id"`
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
	Summary         string         `json:"summary,omitempty"`
	// CallbackURL, when set, receives a POST once the document reaches a
	// terminal status (§4.7).
	CallbackURL string `json:"callback_url,omitempty"`
	// CallbackRun counts page reparses of the current generation, so each
	// run that finishes gets its own delivery.
	CallbackRun int       `json:"-"`
	CreatedBy   uuid.UUID `json:"created_by"`
	Interactive bool      `json:"-"` // uploaded from chat: uses the high-priority lanes
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
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
	DocumentID   uuid.UUID `json:"document_id"`
	PageNo       int       `json:"page_no"`
	Gen          int       `json:"gen"`
	Status       string    `json:"status"`
	Attempts     int       `json:"attempts"`
	Width        int       `json:"width"`
	Height       int       `json:"height"`
	DPI          float64   `json:"dpi"`
	WidthPt      float64   `json:"width_pt"`
	HeightPt     float64   `json:"height_pt"`
	Rotation     int       `json:"rotation"`
	ImageKey     string    `json:"-"`
	RawKey       string    `json:"-"`
	TextLayerKey string    `json:"-"`
	TextSource   string    `json:"text_source"`
	TextQuality  float64   `json:"text_quality"`
	RenderMs     int       `json:"render_ms"`
	OCRMs        int       `json:"ocr_ms"`
	Engine       string    `json:"engine,omitempty"`
	Markdown     string    `json:"markdown"`
	TextPlain    string    `json:"text_plain"`
	DocMdOffset  int       `json:"doc_md_offset"`
	IsBlank      bool      `json:"is_blank"`
	// DocType is the document type the Decision API read from the page image
	// (a doc_types code of the KB, §6.9.4); "" = not classified.
	DocType           string     `json:"doc_type,omitempty"`
	DocTypeConfidence float64    `json:"doc_type_confidence,omitempty"`
	Error             string     `json:"error,omitempty"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	FinishedAt        *time.Time `json:"finished_at,omitempty"`
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
	ID         uuid.UUID  `json:"id"`
	DocumentID uuid.UUID  `json:"document_id"`
	Gen        int        `json:"gen"`
	ParentID   *uuid.UUID `json:"parent_id,omitempty"`
	ShortID    string     `json:"short_id"`
	Ord        int        `json:"ord"`
	Level      int        `json:"level"`
	Title      string     `json:"title,omitempty"`
	Origin     string     `json:"origin"` // root | doctype | bookmark | heading | llm | page
	// DocType is the document type of an origin=doctype node: the pages of
	// one document inside the file (§6.5).
	DocType    string      `json:"doc_type,omitempty"`
	PageStart  int         `json:"page_start"`
	PageEnd    int         `json:"page_end"`
	Summary    string      `json:"summary,omitempty"`
	SectionIDs []uuid.UUID `json:"section_ids"`
	TokenCount int         `json:"token_count"`
	// TreeTokens estimates the subtree rendered as a table of contents
	// (§6.5 step 7); 0 on trees stored before it was recorded.
	TreeTokens int `json:"tree_tokens"`
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
