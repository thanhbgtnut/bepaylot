package dto

import (
	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/service/sheets"
	"github.com/thanhenti/bepaylot/internal/llm"
	"github.com/thanhenti/bepaylot/internal/types"
)

// TemplateList is the body of GET /v1/templates.
type TemplateList struct {
	Data []types.PromptTemplate `json:"data"`
}

// TemplateRequest creates a chat template or a new version of one (§8.4).
type TemplateRequest struct {
	Kind        string `json:"kind,omitempty" example:"chat"`
	Slug        string `json:"slug,omitempty" example:"tham_dinh"`
	Name        string `json:"name,omitempty" example:"Thẩm định phương án vay"`
	Description string `json:"description,omitempty"`
	CaseType    string `json:"case_type,omitempty" example:"tin_dung_dn"`
	Body        string `json:"body"`
}

// PublishTemplateRequest is the body of POST /v1/templates/{id}/publish.
type PublishTemplateRequest struct {
	Version int `json:"version" example:"2"`
}

// CreateSheetRequest is the body of POST /v1/cases/{id}/sheets.
type CreateSheetRequest struct {
	// Tables are the codes of the document types to build; omitted = every
	// document type of the KB that has fields.
	Tables []string `json:"tables,omitempty" example:"hop_dong,hoa_don"`
	// ExportTemplateID builds the sheet for an Excel form of the KB (§6.9.8):
	// its document types, named after it; Tables is then ignored.
	ExportTemplateID *uuid.UUID `json:"export_template_id,omitempty"`
}

// ExportTemplateList is the body of GET /v1/kbs/{id}/export-templates.
type ExportTemplateList struct {
	Data []types.ExportTemplate `json:"data"`
}

// SaveExportTemplateRequest is the body of PUT /v1/export-templates/{id}.
type SaveExportTemplateRequest struct {
	Name    string              `json:"name"`
	Mapping types.ExportMapping `json:"mapping"`
}

// ClassifyCaseRequest is the body of POST /v1/cases/{id}/classify.
type ClassifyCaseRequest struct {
	Mode        string   `json:"mode,omitempty" example:"titles"` // titles | pages
	DocumentIDs []string `json:"document_ids,omitempty"`
}

// ClassifyCaseResponse says how many files were queued.
type ClassifyCaseResponse struct {
	Queued int `json:"queued"`
}

// SheetList is the body of GET /v1/cases/{id}/sheets.
type SheetList struct {
	Data []types.Sheet `json:"data"`
}

// AdminUser is one row of GET /v1/admin/users.
type AdminUser struct {
	UserView
	IsActive bool    `json:"is_active"`
	Spent    float64 `json:"spent"`
	Keys     int     `json:"keys"`
}

// AdminUserList is the body of GET /v1/admin/users.
type AdminUserList struct {
	Currency string      `json:"currency" example:"VND"`
	Data     []AdminUser `json:"data"`
}

// AdminUpdateUserRequest is the body of PATCH /v1/admin/users/{id}.
type AdminUpdateUserRequest struct {
	Role         *string  `json:"role,omitempty" example:"prompt_editor"`
	MonthlyLimit *float64 `json:"monthly_limit,omitempty" example:"500000"`
	IsActive     *bool    `json:"is_active,omitempty"`
}

// LLMKeyView is the shared LLM key, masked, and the models it serves.
type LLMKeyView struct {
	llm.KeyInfo
	Models []string `json:"models"`
}

// SetLLMKeyRequest is the body of PUT /v1/admin/llm.
type SetLLMKeyRequest struct {
	APIKey string `json:"api_key"`
}

// SheetEditsRequest is the body of POST /v1/sheets/{id}/edits.
type SheetEditsRequest struct {
	Edits []sheets.EditInput `json:"edits"`
}

// CompileCheckRequest is the body of POST /v1/kbs/{id}/checks/compile.
type CompileCheckRequest struct {
	Kind    string `json:"kind" example:"rule"` // rule | link
	DocType string `json:"doc_type,omitempty" example:"hoa_don"`
	Anchor  string `json:"anchor" example:"hop_dong"`
	Text    string `json:"text" example:"Tổng giá trị hoá đơn không vượt giá trị hợp đồng"`
}

// CheckRunList is the body of GET /v1/cases/{id}/checks.
type CheckRunList struct {
	Data []types.CheckRun `json:"data"`
}

// SetCheckLinkRequest is the body of PUT /v1/checks/{id}/links.
type SetCheckLinkRequest struct {
	SegmentID       uuid.UUID  `json:"segment_id"`
	AnchorSegmentID *uuid.UUID `json:"anchor_segment_id"`
	Auto            bool       `json:"auto,omitempty"`
}
