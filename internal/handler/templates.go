package handler

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/handler/dto"
	"github.com/thanhenti/bepaylot/internal/llm"
	"github.com/thanhenti/bepaylot/internal/types"
)

// role is the user's effective role (§8.4).
func (h *Handlers) role(u types.User) string { return dto.EffectiveRole(u, h.adminEmail(u)) }

// canPrompt reports whether the user may write prompts and templates.
func (h *Handlers) canPrompt(u types.User) bool { return types.CanEditPrompts(h.role(u)) }

func (h *Handlers) forbidPrompt(c *app.RequestContext) {
	c.JSON(consts.StatusForbidden, dto.NewError("permission_error", "setting a prompt requires the prompt_editor role (quyền Được đặt prompt)"))
}

// requirePrompt answers 403 unless the user may edit prompts.
func (h *Handlers) requirePrompt(c *app.RequestContext) (types.User, bool) {
	u, ok := h.user(c)
	if !ok {
		return u, false
	}
	if !h.canPrompt(u) {
		h.forbidPrompt(c)
		return u, false
	}
	return u, true
}

// withinBudget answers 402 when the user has spent the monthly limit (§8.5).
func (h *Handlers) withinBudget(ctx context.Context, c *app.RequestContext, u types.User) bool {
	if h.Usage == nil {
		return true
	}
	if err := h.Usage.Check(ctx, u.ID); err != nil {
		c.JSON(consts.StatusPaymentRequired, dto.NewError("budget_exceeded", err.Error()))
		return false
	}
	return true
}

// chatTemplate resolves a template_id for a session: "" = none; anything
// else must be a published chat template (422).
func (h *Handlers) chatTemplate(ctx context.Context, c *app.RequestContext, raw string) (*uuid.UUID, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, true
	}
	id, err := uuid.Parse(raw)
	if err == nil {
		var t types.PromptTemplate
		if t, err = h.Store.Templates.Get(ctx, id, 0); err == nil && t.Kind == types.TemplateChat && t.Status == types.TemplatePublished {
			return &id, true
		}
	}
	c.JSON(consts.StatusUnprocessableEntity, dto.NewError("invalid_request_error", "template_id is not a published chat template"))
	return nil, false
}

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,62}$`)

// ListTemplates handles GET /v1/templates.
//
// @Summary      Prompt templates (§8.4)
// @Description  Published templates of a kind (chat or sheet), usable for a case type. body and fields are returned to prompt editors only; they also see drafts with drafts=1.
// @Tags         Templates
// @Produce      json
// @Param        kind       query     string  false  "chat | sheet"
// @Param        case_type  query     string  false  "Only templates usable for this case type"
// @Param        drafts     query     bool    false  "Include drafts (prompt editors)"
// @Success      200        {object}  dto.TemplateList
// @Security     ApiKeyAuth
// @Router       /v1/templates [get]
func (h *Handlers) ListTemplates(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	editor := h.canPrompt(u)
	list, err := h.Store.Templates.List(ctx, string(c.Query("kind")), string(c.Query("case_type")), editor && c.Query("drafts") != "")
	if err != nil {
		h.serverError(c, err)
		return
	}
	if !editor {
		for i := range list {
			list[i].Body = ""
			if list[i].Kind == types.TemplateChat {
				list[i].Fields = nil
			}
		}
	}
	c.JSON(consts.StatusOK, dto.TemplateList{Data: list})
}

// GetTemplate handles GET /v1/templates/{id}.
//
// @Summary   One template at a version (prompt editors)
// @Tags      Templates
// @Produce   json
// @Param     id       path      string  true   "Template id"  format(uuid)
// @Param     version  query     int     false  "Version (default: published, else latest)"
// @Success   200      {object}  types.PromptTemplate
// @Failure   403      {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/templates/{id} [get]
func (h *Handlers) GetTemplate(ctx context.Context, c *app.RequestContext) {
	if _, ok := h.requirePrompt(c); !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	t, err := h.Store.Templates.Get(ctx, id, intQuery(c, "version", 0))
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, t)
}

// CreateTemplate handles POST /v1/templates.
//
// @Summary   Create a template (draft, version 1)
// @Tags      Templates
// @Accept    json
// @Produce   json
// @Param     request  body      dto.TemplateRequest  true  "Template"
// @Success   201      {object}  types.PromptTemplate
// @Failure   403      {object}  dto.ErrorResponse
// @Failure   409      {object}  dto.ErrorResponse
// @Failure   422      {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/templates [post]
func (h *Handlers) CreateTemplate(ctx context.Context, c *app.RequestContext) {
	u, ok := h.requirePrompt(c)
	if !ok {
		return
	}
	var req dto.TemplateRequest
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	if req.Kind != types.TemplateChat && req.Kind != types.TemplateSheet {
		h.unprocessable(c, "kind must be chat or sheet")
		return
	}
	if !slugRe.MatchString(req.Slug) || strings.TrimSpace(req.Name) == "" {
		h.unprocessable(c, "slug (a-z, 0-9, _ -) and name are required")
		return
	}
	tables := req.SheetTables()
	if msg := h.checkTemplate(req.Kind, req.Body, req.CaseType, tables); msg != "" {
		h.unprocessable(c, msg)
		return
	}
	t, err := h.Store.Templates.Create(ctx, postgres.TemplateDraft{Kind: req.Kind, Slug: req.Slug, Name: req.Name, Description: req.Description,
		CaseType: req.CaseType, Body: req.Body, Tables: tables, By: u.ID})
	if errors.Is(err, postgres.ErrSlugTaken) {
		c.JSON(consts.StatusConflict, dto.NewError("conflict_error", err.Error()))
		return
	}
	if err != nil {
		h.serverError(c, err)
		return
	}
	c.JSON(consts.StatusCreated, t)
}

// AddTemplateVersion handles POST /v1/templates/{id}/versions.
//
// @Summary   Save a new (draft) version; the published one keeps running
// @Tags      Templates
// @Accept    json
// @Produce   json
// @Param     id       path      string               true  "Template id"  format(uuid)
// @Param     request  body      dto.TemplateRequest  true  "Body and fields (name/description optional)"
// @Success   201      {object}  types.PromptTemplate
// @Failure   403      {object}  dto.ErrorResponse
// @Failure   422      {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/templates/{id}/versions [post]
func (h *Handlers) AddTemplateVersion(ctx context.Context, c *app.RequestContext) {
	u, ok := h.requirePrompt(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var req dto.TemplateRequest
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	cur, err := h.Store.Templates.Get(ctx, id, 0)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	tables := req.SheetTables()
	if msg := h.checkTemplate(cur.Kind, req.Body, cur.CaseType, tables); msg != "" {
		h.unprocessable(c, msg)
		return
	}
	t, err := h.Store.Templates.AddVersion(ctx, id, postgres.TemplateDraft{Name: req.Name, Description: req.Description,
		Body: req.Body, Tables: tables, By: u.ID})
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusCreated, t)
}

// PublishTemplate handles POST /v1/templates/{id}/publish.
//
// @Summary   Publish a version
// @Tags      Templates
// @Accept    json
// @Produce   json
// @Param     id       path      string                  true  "Template id"  format(uuid)
// @Param     request  body      dto.PublishTemplateRequest  true  "Version"
// @Success   200      {object}  types.PromptTemplate
// @Failure   403      {object}  dto.ErrorResponse
// @Failure   404      {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/templates/{id}/publish [post]
func (h *Handlers) PublishTemplate(ctx context.Context, c *app.RequestContext) {
	if _, ok := h.requirePrompt(c); !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var req dto.PublishTemplateRequest
	if err := c.BindJSON(&req); err != nil || req.Version <= 0 {
		h.badRequest(c, "version is required")
		return
	}
	t, err := h.Store.Templates.Publish(ctx, id, req.Version)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, t)
}

// TemplateCorrections handles GET /v1/templates/{id}/corrections.
//
// @Summary   AI error rate per field of a sheet template (§6.9.6)
// @Tags      Templates
// @Produce   json
// @Param     id   path      string  true  "Template id"  format(uuid)
// @Success   200  {object}  dto.CorrectionStats
// @Failure   403  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/templates/{id}/corrections [get]
func (h *Handlers) TemplateCorrections(ctx context.Context, c *app.RequestContext) {
	if _, ok := h.requirePrompt(c); !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	stats, err := h.Store.Sheets.CorrectionStats(ctx, id)
	if err != nil {
		h.serverError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.CorrectionStats{Data: stats})
}

// checkTemplate validates a template version: a sheet template needs at
// least one sub-table, each with fields (unique snake_case keys) and a label
// of the case type's classification, or "" for one row per file (§6.9.6).
func (h *Handlers) checkTemplate(kind, body, caseType string, tables []types.SheetTable) string {
	if strings.TrimSpace(body) == "" {
		return "body is required"
	}
	if kind != types.TemplateSheet {
		return ""
	}
	if len(tables) == 0 {
		return "a sheet template needs tables (or fields)"
	}
	var rule types.ClassificationRule
	if h.Cases != nil && caseType != "" {
		rule = h.Cases.CaseType(caseType).Classification
	}
	labels := map[string]bool{}
	for _, t := range tables {
		if labels[t.Label] {
			return fmt.Sprintf("table %q appears twice", t.Label)
		}
		labels[t.Label] = true
		if t.Label != "" {
			if caseType == "" {
				return "a table with a document type (label) needs the template's case_type"
			}
			if t.Label == types.LabelOther || t.Label == types.LabelUnknown || !rule.HasLabel(t.Label) {
				return fmt.Sprintf("table label %q is not a classification label of case type %s", t.Label, caseType)
			}
		}
		if len(t.Fields) == 0 {
			return "each table needs fields"
		}
		seen := map[string]bool{}
		for _, f := range t.Fields {
			if !slugRe.MatchString(f.Key) || strings.TrimSpace(f.Label) == "" || seen[f.Key] {
				return "each field needs a unique snake_case key and a label"
			}
			seen[f.Key] = true
		}
	}
	return ""
}

func (h *Handlers) unprocessable(c *app.RequestContext, msg string) {
	c.JSON(consts.StatusUnprocessableEntity, dto.NewError("invalid_request_error", msg))
}

// ---- usage, users and the shared key (U46) ----

// MyUsage handles GET /v1/me/usage.
//
// @Summary   My cost this month and today (§7.8)
// @Tags      Usage
// @Produce   json
// @Success   200  {object}  types.UsageSummary
// @Security  ApiKeyAuth
// @Router    /v1/me/usage [get]
func (h *Handlers) MyUsage(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok || !h.usageReady(c) {
		return
	}
	sum, err := h.Usage.Summary(ctx, &u.ID)
	if err != nil {
		h.serverError(c, err)
		return
	}
	c.JSON(consts.StatusOK, sum)
}

// AdminUsage handles GET /v1/admin/usage.
//
// @Summary   Cost of the whole system this month and today
// @Tags      Admin
// @Produce   json
// @Success   200  {object}  types.UsageSummary
// @Failure   403  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/admin/usage [get]
func (h *Handlers) AdminUsage(ctx context.Context, c *app.RequestContext) {
	if !h.isAdmin(c) || !h.usageReady(c) {
		return
	}
	sum, err := h.Usage.Summary(ctx, nil)
	if err != nil {
		h.serverError(c, err)
		return
	}
	c.JSON(consts.StatusOK, sum)
}

func (h *Handlers) usageReady(c *app.RequestContext) bool {
	if h.Usage == nil {
		c.JSON(consts.StatusServiceUnavailable, dto.NewError("api_error", "usage metering is not configured"))
		return false
	}
	return true
}

// AdminListUsers handles GET /v1/admin/users.
//
// @Summary   Users with their role, cost this month, limit and keys
// @Tags      Admin
// @Produce   json
// @Success   200  {object}  dto.AdminUserList
// @Failure   403  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/admin/users [get]
func (h *Handlers) AdminListUsers(ctx context.Context, c *app.RequestContext) {
	if !h.isAdmin(c) || !h.usageReady(c) {
		return
	}
	list, err := h.Store.Users.ListWithUsage(ctx, h.Usage.MonthStart(timeNow()))
	if err != nil {
		h.serverError(c, err)
		return
	}
	out := dto.AdminUserList{Currency: h.Usage.Currency(), Data: make([]dto.AdminUser, 0, len(list))}
	for _, u := range list {
		out.Data = append(out.Data, dto.AdminUser{UserView: dto.UserViewFrom(u.User, h.adminEmail(u.User)), IsActive: u.IsActive, Spent: u.Spent, Keys: u.Keys})
	}
	c.JSON(consts.StatusOK, out)
}

// AdminUpdateUser handles PATCH /v1/admin/users/{id}.
//
// @Summary      Change a user's role, monthly limit or active flag
// @Description  monthly_limit < 0 removes the limit.
// @Tags         Admin
// @Accept       json
// @Produce      json
// @Param        id       path      string                     true  "User id"  format(uuid)
// @Param        request  body      dto.AdminUpdateUserRequest  true  "Changes"
// @Success      200      {object}  dto.UserView
// @Failure      403      {object}  dto.ErrorResponse
// @Failure      422      {object}  dto.ErrorResponse
// @Security     ApiKeyAuth
// @Router       /v1/admin/users/{id} [patch]
func (h *Handlers) AdminUpdateUser(ctx context.Context, c *app.RequestContext) {
	if !h.isAdmin(c) {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var req dto.AdminUpdateUserRequest
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	if req.Role != nil && *req.Role != types.UserRoleAdmin && *req.Role != types.UserRolePromptEditor && *req.Role != types.UserRoleUser {
		h.unprocessable(c, "role must be admin, prompt_editor or user")
		return
	}
	u, err := h.Store.Users.Update(ctx, id, postgres.UserUpdate{Role: req.Role, MonthlyLimit: req.MonthlyLimit, IsActive: req.IsActive})
	if err != nil {
		h.serviceError(c, err)
		return
	}
	if h.Usage != nil {
		h.Usage.Forget(id)
	}
	c.JSON(consts.StatusOK, dto.UserViewFrom(u, h.adminEmail(u)))
}

// AdminIssueAPIKey handles POST /v1/admin/users/{id}/api-keys.
//
// @Summary   Issue an API key to a user; the plaintext is returned only here
// @Tags      Admin
// @Accept    json
// @Produce   json
// @Param     id       path      string                   true   "User id"  format(uuid)
// @Param     request  body      dto.CreateAPIKeyRequest  false  "Label"
// @Success   201      {object}  dto.CreatedAPIKey
// @Failure   403      {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/admin/users/{id}/api-keys [post]
func (h *Handlers) AdminIssueAPIKey(ctx context.Context, c *app.RequestContext) {
	if !h.isAdmin(c) {
		return
	}
	admin, _ := h.user(c)
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	if _, err := h.Store.Users.Get(ctx, id); err != nil {
		h.serviceError(c, err)
		return
	}
	var req dto.CreateAPIKeyRequest
	if len(c.Request.Body()) > 0 {
		if err := c.BindJSON(&req); err != nil {
			h.badRequest(c, "invalid JSON body: "+err.Error())
			return
		}
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "web + script"
	}
	plaintext, key, err := h.Store.APIKeys.IssueBy(ctx, id, name, &admin.ID)
	if err != nil {
		h.serverError(c, err)
		return
	}
	key.IssuedBy = admin.Email
	c.JSON(consts.StatusCreated, dto.CreatedAPIKey{Key: dto.APIKeyViewFrom(key), APIKey: plaintext})
}

// secretLLMKey names the shared LLM key set from Settings (§8.5).
const secretLLMKey = "llm.api_key"

// AdminGetLLM handles GET /v1/admin/llm.
//
// @Summary   The shared LLM key (masked) and the models it serves
// @Tags      Admin
// @Produce   json
// @Success   200  {object}  dto.LLMKeyView
// @Failure   403  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/admin/llm [get]
func (h *Handlers) AdminGetLLM(_ context.Context, c *app.RequestContext) {
	if !h.isAdmin(c) {
		return
	}
	c.JSON(consts.StatusOK, h.llmKeyView())
}

// AdminSetLLM handles PUT /v1/admin/llm.
//
// @Summary      Replace the shared LLM key
// @Description  Stored in the database and applied at once to every provider that used the shared key; an empty key goes back to the one in .env.
// @Tags         Admin
// @Accept       json
// @Produce      json
// @Param        request  body      dto.SetLLMKeyRequest  true  "Key"
// @Success      200      {object}  dto.LLMKeyView
// @Failure      403      {object}  dto.ErrorResponse
// @Security     ApiKeyAuth
// @Router       /v1/admin/llm [put]
func (h *Handlers) AdminSetLLM(ctx context.Context, c *app.RequestContext) {
	if !h.isAdmin(c) {
		return
	}
	var req dto.SetLLMKeyRequest
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	key := strings.TrimSpace(req.APIKey)
	if err := h.Store.Tokens.SetSecret(ctx, secretLLMKey, key); err != nil {
		h.serverError(c, err)
		return
	}
	h.Registry.SetSharedKey(key)
	c.JSON(consts.StatusOK, h.llmKeyView())
}

func (h *Handlers) llmKeyView() dto.LLMKeyView {
	v := dto.LLMKeyView{KeyInfo: h.Registry.SharedKeyInfo()}
	seen := map[string]bool{}
	add := func(m string) {
		if m = strings.TrimSpace(m); m != "" && !seen[m] {
			seen[m] = true
			v.Models = append(v.Models, m)
		}
	}
	add(h.LLM.DefaultModel)
	add(h.LLM.SummaryModel)
	if h.Config != nil {
		add(h.Config.Index.Tree.Model)
		add(h.Config.Search.Model)
		add(h.Config.Parser.Engines.VLM.Model)
	}
	return v
}

// LoadSharedLLMKey applies a key saved from Settings at startup.
func LoadSharedLLMKey(ctx context.Context, st *postgres.Store, reg *llm.Registry) {
	if key := st.Tokens.GetSecret(ctx, secretLLMKey); key != "" {
		reg.SetSharedKey(key)
	}
}

// timeNow is time.Now, replaceable in tests.
var timeNow = time.Now
