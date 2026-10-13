package dto

import (
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
