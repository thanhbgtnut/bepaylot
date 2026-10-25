package types

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// CatalogDocType is one document type of its owner's catalogue (§6.9.10,
// U97), shared by every knowledge base of that owner; a KB lists the codes it
// uses. PromptVersion is the version of the AI instructions in use.
type CatalogDocType struct {
	DocType
	PromptVersion int       `json:"prompt_version"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// DocTypePrompt is one version of the AI instructions of a document type
// (U94, U98): the general instruction, the fields to read (the columns of a
// sheet built with it, U103), per-field rules and reviewed samples used as
// examples. Versions are never edited: saving makes a new one.
type DocTypePrompt struct {
	Code        string       `json:"code"`
	Version     int          `json:"version"`
	Instruction string       `json:"instruction"`
	Fields      []SheetField `json:"fields"`
	Rules       []string     `json:"rules"`
	Shots       []PromptShot `json:"shots"`
	Note        string       `json:"note"`
	CreatedBy   *uuid.UUID   `json:"created_by,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
}

// PromptShot is a reviewed sample used as an example, and why.
type PromptShot struct {
	SampleID uuid.UUID `json:"sample_id"`
	Why      string    `json:"why,omitempty"`
}

// Sample statuses.
const (
	SampleProcessing = "processing"
	SampleReady      = "ready"
	SampleFailed     = "failed"
)

// DocTypeSample is a sample document of a type, read once by docpipe: per
// page the content the VLM read (the main source) and the lines TurboOCR read
// (for checking digits), U102. Truth holds the values a user reviewed.
type DocTypeSample struct {
	ID         uuid.UUID         `json:"id"`
	Code       string            `json:"code"`
	FileName   string            `json:"file_name"`
	StorageKey string            `json:"-"`
	MimeType   string            `json:"mime_type"`
	Status     string            `json:"status"`
	Error      string            `json:"error,omitempty"`
	Pages      []SamplePage      `json:"pages"`
	Truth      map[string]string `json:"truth,omitempty"`
	ReviewedAt *time.Time        `json:"reviewed_at,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
}

// SamplePage is one page of a sample.
type SamplePage struct {
	N        int          `json:"n"`
	Width    float64      `json:"width,omitempty"`
	Height   float64      `json:"height,omitempty"`
	Markdown string       `json:"markdown"`
	Lines    []SampleLine `json:"lines"`
}

// SampleLine is one TurboOCR line of a sample page.
type SampleLine struct {
	N    int       `json:"n"`
	Text string    `json:"text"`
	Conf float64   `json:"conf"`
	BBox []float64 `json:"bbox,omitempty"`
}

// SampleRun is what one version of the instructions read from a sample.
type SampleRun struct {
	SampleID uuid.UUID           `json:"sample_id"`
	Version  int                 `json:"version"`
	Output   map[string]RunValue `json:"output"`
	RanAt    time.Time           `json:"ran_at"`
}

// RunValue is one field read in a test run, with where it was read.
type RunValue struct {
	Value string `json:"value"`
	Page  int    `json:"page,omitempty"`
	Line  int    `json:"line,omitempty"`
}

// DefaultInstruction is the general instruction of version 1 of a type.
func DefaultInstruction(title string) string {
	return "Bạn đọc một " + strings.ToLower(title) + " và lấy ra các thông tin bên dưới.\n" +
		"Dựa vào phần Nội dung; chữ số nào không rõ thì kiểm lại ở phần Các dòng.\n" +
		"Chỉ ghi đúng giá trị có trên tài liệu. Thông tin nào không thấy thì để trống, không đoán."
}

// DocTypeView is a type of the catalogue as listed (§10.9): the knowledge
// bases using it, its samples and the accuracy of the instructions in use on
// the reviewed ones (nil when none was reviewed and run).
type DocTypeView struct {
	CatalogDocType
	KBIDs    []uuid.UUID `json:"kb_ids"`
	Samples  int         `json:"samples"`
	Reviewed int         `json:"reviewed"`
	Accuracy *float64    `json:"accuracy"`
}

// Guide is what a version of the instructions adds to an extraction turn
// (U98): the general instruction, the per-field rules and reviewed samples
// as examples.
type Guide struct {
	Instruction string
	Rules       []string
	Shots       []GuideShot
}

// GuideShot is one example: the content of a reviewed sample and its values.
type GuideShot struct {
	SampleID uuid.UUID
	Name     string
	Content  string
	Truth    map[string]string
}

// Job kinds and statuses of doc_type_jobs.
const (
	JobRun      = "run"
	JobOptimize = "optimize"

	JobRunning = "running"
	JobDone    = "done"
	JobFailed  = "failed"
)

// DocTypeJob is a test run or an optimization of a type, done in the
// background; Result is the proposal of an optimization.
type DocTypeJob struct {
	ID         uuid.UUID         `json:"id"`
	Code       string            `json:"code"`
	Kind       string            `json:"kind"`
	Status     string            `json:"status"`
	Version    int               `json:"version"`
	SampleIDs  []uuid.UUID       `json:"sample_ids,omitempty"`
	Result     *OptimizeProposal `json:"result,omitempty"`
	Error      string            `json:"error,omitempty"`
	CreatedBy  uuid.UUID         `json:"created_by"`
	CreatedAt  time.Time         `json:"created_at"`
	FinishedAt *time.Time        `json:"finished_at,omitempty"`
}

// OptimizeProposal is what an optimization proposes (U98), not saved as a
// version until applied; None means no clear repeated error was found.
type OptimizeProposal struct {
	None        bool              `json:"none,omitempty"`
	Version     int               `json:"version"`
	Instruction string            `json:"instruction"`
	Fields      []SheetField      `json:"fields"`
	Rules       []string          `json:"rules"`
	Shots       []PromptShot      `json:"shots"`
	Patterns    []OptimizePattern `json:"patterns"`
	Diff        []DiffLine        `json:"diff"`
	Before      *float64          `json:"before"`
	After       *float64          `json:"after"`
	ByField     []FieldScore      `json:"by_field"`
	Worse       []WorseCell       `json:"worse"`
}

// OptimizePattern is a repeated error of a field and the rule that fixes it.
type OptimizePattern struct {
	Field   string      `json:"field"`
	Finding string      `json:"finding"`
	Rule    string      `json:"rule"`
	Samples []uuid.UUID `json:"samples"`
}

// DiffLine is one line of the line diff of two prompts: op "=", "-" or "+";
// A and B are the line numbers in the old and new text; Why is "f:<field>"
// or "s:<sample id>" for an added line made for that field or example.
type DiffLine struct {
	Op  string `json:"op"`
	A   int    `json:"a,omitempty"`
	B   int    `json:"b,omitempty"`
	T   string `json:"t"`
	Why string `json:"why,omitempty"`
}

// FieldScore is the accuracy of one field before and after.
type FieldScore struct {
	Key    string   `json:"key"`
	Before *float64 `json:"before"`
	After  *float64 `json:"after"`
}

// WorseCell is a (sample, field) that was right before and is wrong after.
type WorseCell struct {
	SampleID uuid.UUID `json:"sample_id"`
	Key      string    `json:"key"`
	Value    string    `json:"value"`
	Truth    string    `json:"truth"`
}
