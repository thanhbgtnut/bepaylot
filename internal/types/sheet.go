package types

import (
	"time"

	"github.com/google/uuid"
)

// User roles (§8.4).
const (
	UserRoleAdmin        = "admin"
	UserRolePromptEditor = "prompt_editor"
	UserRoleUser         = "user"
)

// CanEditPrompts reports whether the role may write custom prompts and
// templates (§8.4).
func CanEditPrompts(role string) bool { return role == UserRoleAdmin || role == UserRolePromptEditor }

// Extracted field statuses and sources (§6.9.3).
const (
	FieldProposed   = "proposed"
	FieldConfirmed  = "confirmed"
	FieldRejected   = "rejected"
	FieldSuperseded = "superseded"
	FieldStale      = "stale"

	FieldSourceAgent = "agent"
	FieldSourceUser  = "user"
)

// Field is one Extracted Field (§6.9.3) with its evidence.
type Field struct {
	ID           uuid.UUID  `json:"id"`
	CaseID       uuid.UUID  `json:"case_id"`
	DocumentID   uuid.UUID  `json:"document_id"`
	Key          string     `json:"key"`
	Ord          int        `json:"ord"`
	Value        any        `json:"value"`
	ValueType    string     `json:"value_type"`
	ValueText    string     `json:"value_text"`
	ValueMatched bool       `json:"value_matched"`
	Confidence   float64    `json:"confidence"`
	Status       string     `json:"status"`
	Source       string     `json:"source"`
	Supersedes   *uuid.UUID `json:"supersedes,omitempty"`
	Note         string     `json:"note,omitempty"`
	Evidence     []Evidence `json:"evidence"`
	CreatedAt    time.Time  `json:"created_at"`
}

// Evidence is a place in a source page that backs a field (§6.9.2).
type Evidence struct {
	DocumentID uuid.UUID `json:"document_id"`
	FileName   string    `json:"file_name,omitempty"`
	PageNo     int       `json:"page_no"`
	LineFrom   int       `json:"line_from,omitempty"`
	LineTo     int       `json:"line_to,omitempty"`
	BBox       []float64 `json:"bbox,omitempty"`
	Quote      string    `json:"quote"`
	CitationID string    `json:"citation_id"`
	Status     string    `json:"status"`
}

// FieldInput is a field written by the agent (kb_save_fields) or a user.
type FieldInput struct {
	DocumentID uuid.UUID
	Key        string
	Ord        int
	Value      any
	ValueType  string
	Confidence float64
	Citations  []string
	Note       string
}

// Prompt template kinds and statuses (§8.4).
const (
	TemplateChat  = "chat"
	TemplateSheet = "sheet"

	TemplateDraft     = "draft"
	TemplatePublished = "published"
	TemplateArchived  = "archived"
)

// SheetField is one row of a sheet template.
type SheetField struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	ValueType string `json:"value_type,omitempty"`
}

// PromptTemplate is a prompt preset (kind chat) or a sheet template (kind
// sheet). Body and Fields are those of the version asked for (the published
// one unless said otherwise).
type PromptTemplate struct {
	ID             uuid.UUID    `json:"id"`
	Kind           string       `json:"kind"`
	Slug           string       `json:"slug"`
	Name           string       `json:"name"`
	Description    string       `json:"description"`
	CaseType       string       `json:"case_type,omitempty"`
	Status         string       `json:"status"`
	CurrentVersion int          `json:"current_version"`
	LatestVersion  int          `json:"latest_version"`
	Version        int          `json:"version"`
	Body           string       `json:"body,omitempty"`
	Fields         []SheetField `json:"fields,omitempty"`
	CreatedBy      uuid.UUID    `json:"created_by"`
	CreatedByName  string       `json:"created_by_name,omitempty"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
}

// Sheet statuses (§6.9.6).
const (
	SheetPending = "pending"
	SheetRunning = "running"
	SheetDone    = "done"
	SheetFailed  = "failed"
)

// SheetRow is one row of a case sheet as captured when it was built.
type SheetRow struct {
	Key         string     `json:"key"`
	Label       string     `json:"label"`
	ValueType   string     `json:"value_type,omitempty"`
	FieldID     *uuid.UUID `json:"field_id,omitempty"`
	AIFieldID   *uuid.UUID `json:"ai_field_id,omitempty"`
	AIValueText string     `json:"ai_value_text"`
	Note        string     `json:"note,omitempty"`
}

// Sheet is a case sheet (§6.9.6).
type Sheet struct {
	ID              uuid.UUID  `json:"id"`
	CaseID          uuid.UUID  `json:"case_id"`
	TemplateID      uuid.UUID  `json:"template_id"`
	TemplateVersion int        `json:"template_version"`
	TemplateName    string     `json:"template_name,omitempty"`
	Name            string     `json:"name"`
	Status          string     `json:"status"`
	Filled          int        `json:"filled"`
	Total           int        `json:"total"`
	Rows            []SheetRow `json:"rows"`
	Error           string     `json:"error,omitempty"`
	CreatedBy       uuid.UUID  `json:"created_by"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// Correction is one AI value a user changed on a sheet (§6.9.6).
type Correction struct {
	SheetID         uuid.UUID
	TemplateID      uuid.UUID
	TemplateVersion int
	Key             string
	AIFieldID       *uuid.UUID
	UserFieldID     *uuid.UUID
	AIValueText     string
	UserValueText   string
	Origin          string // page | xlsx
	UserID          uuid.UUID
}

// CorrectionStat is the error rate of one field of a template version.
type CorrectionStat struct {
	Key      string   `json:"key"`
	Version  int      `json:"version"`
	Sheets   int      `json:"sheets"`
	Edited   int      `json:"edited"`
	Rate     float64  `json:"rate"`
	Examples []string `json:"examples,omitempty"`
}

// Usage kinds (§8.5).
const (
	UsageChat  = "chat"
	UsageSheet = "sheet"
	UsageParse = "parse"
)

// UsageEvent is the cost of one model call.
type UsageEvent struct {
	UserID    *uuid.UUID
	CaseID    *uuid.UUID
	Kind      string
	Model     string
	TokensIn  int
	TokensOut int
	Cost      float64
}

// UsageSummary is what the usage page shows (§7.8).
type UsageSummary struct {
	Currency  string             `json:"currency"`
	Month     UsagePeriod        `json:"month"`
	Today     UsagePeriod        `json:"today"`
	ByKind    map[string]float64 `json:"by_kind"`
	UpdatedAt time.Time          `json:"updated_at"`
}

// UsagePeriod is the spend of a period and its limit (0 = none).
type UsagePeriod struct {
	Spent    float64    `json:"spent"`
	Limit    float64    `json:"limit"`
	ResetsAt *time.Time `json:"resets_at,omitempty"`
}

// UserUsage is one row of the admin user list.
type UserUsage struct {
	User
	Spent float64 `json:"spent"`
	Keys  int     `json:"keys"`
}
