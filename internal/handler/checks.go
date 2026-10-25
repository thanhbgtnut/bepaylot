package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/handler/dto"
	"github.com/thanhenti/bepaylot/internal/types"
)

// ListRulesets handles GET /v1/check-rulesets.
//
// @Summary   The user's cross-check rule sets, shared by every knowledge base of theirs (§6.9.9, U105)
// @Tags      Checks
// @Produce   json
// @Param     kb_id  query     string  false  "Only the sets this knowledge base uses"  format(uuid)
// @Success   200    {object}  dto.RulesetList
// @Security  ApiKeyAuth
// @Router    /v1/check-rulesets [get]
func (h *Handlers) ListRulesets(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok || !h.sheetsReady(c) {
		return
	}
	var kb *uuid.UUID
	if q := c.Query("kb_id"); q != "" {
		id, err := uuid.Parse(q)
		if err != nil {
			h.badRequest(c, "kb_id must be a uuid")
			return
		}
		kb = &id
	}
	list, err := h.Sheets.ListRulesets(ctx, u.ID, kb)
	if err != nil {
		h.sheetError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.RulesetList{Data: list})
}

// GetRuleset handles GET /v1/check-rulesets/{id}.
//
// @Summary   One rule set: anchor type, how documents find their contract, rules
// @Tags      Checks
// @Produce   json
// @Param     id   path      string  true  "Rule set id"  format(uuid)
// @Success   200  {object}  types.CheckRuleset
// @Failure   404  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/check-rulesets/{id} [get]
func (h *Handlers) GetRuleset(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok || !h.sheetsReady(c) {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	rs, err := h.Sheets.GetRuleset(ctx, u.ID, id)
	if err != nil {
		h.sheetError(c, err)
		return
	}
	c.JSON(consts.StatusOK, rs)
}

// CreateRuleset handles POST /v1/check-rulesets.
//
// @Summary      Create a rule set (U105)
// @Description  Each rule compares a field of document A with a field of document B ({a, op, b}; same kind of field) or is a sentence the AI judges ({text}). The formula of a field comparison is built by the server. kb_ids must be knowledge bases of the user. Requires the prompt_editor or admin role.
// @Tags         Checks
// @Accept       json
// @Produce      json
// @Param        request  body      types.CheckRuleset  true  "Rule set"
// @Success      201      {object}  types.CheckRuleset
// @Failure      422      {object}  dto.ErrorResponse
// @Security     ApiKeyAuth
// @Router       /v1/check-rulesets [post]
func (h *Handlers) CreateRuleset(ctx context.Context, c *app.RequestContext) {
	u, ok := h.requirePrompt(c)
	if !ok || !h.sheetsReady(c) {
		return
	}
	var req types.CheckRuleset
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	req.ID = uuid.Nil
	rs, err := h.Sheets.SaveRuleset(ctx, u, req)
	if err != nil {
		h.sheetError(c, err)
		return
	}
	c.JSON(consts.StatusCreated, rs)
}

// UpdateRuleset handles PUT /v1/check-rulesets/{id}.
//
// @Summary      Replace a rule set (name, knowledge bases, anchor, links, rules)
// @Description  Every field comparison and formula is checked against the document types of the knowledge bases that use the set. Requires the prompt_editor or admin role.
// @Tags         Checks
// @Accept       json
// @Produce      json
// @Param        id       path      string              true  "Rule set id"  format(uuid)
// @Param        request  body      types.CheckRuleset  true  "Rule set"
// @Success      200      {object}  types.CheckRuleset
// @Failure      422      {object}  dto.ErrorResponse
// @Security     ApiKeyAuth
// @Router       /v1/check-rulesets/{id} [put]
func (h *Handlers) UpdateRuleset(ctx context.Context, c *app.RequestContext) {
	u, ok := h.requirePrompt(c)
	if !ok || !h.sheetsReady(c) {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var req types.CheckRuleset
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	req.ID = id
	rs, err := h.Sheets.SaveRuleset(ctx, u, req)
	if err != nil {
		h.sheetError(c, err)
		return
	}
	c.JSON(consts.StatusOK, rs)
}

// DeleteRuleset handles DELETE /v1/check-rulesets/{id}.
//
// @Summary   Delete a rule set; earlier runs keep their result
// @Tags      Checks
// @Param     id   path  string  true  "Rule set id"  format(uuid)
// @Success   204
// @Failure   404  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/check-rulesets/{id} [delete]
func (h *Handlers) DeleteRuleset(ctx context.Context, c *app.RequestContext) {
	u, ok := h.requirePrompt(c)
	if !ok || !h.sheetsReady(c) {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	if err := h.Sheets.DeleteRuleset(ctx, u.ID, id); err != nil {
		h.sheetError(c, err)
		return
	}
	c.Status(consts.StatusNoContent)
}

// CopyRuleset handles POST /v1/check-rulesets/{id}/copy.
//
// @Summary   Duplicate a rule set (used by no knowledge base yet)
// @Tags      Checks
// @Produce   json
// @Param     id   path      string  true  "Rule set id"  format(uuid)
// @Success   201  {object}  types.CheckRuleset
// @Security  ApiKeyAuth
// @Router    /v1/check-rulesets/{id}/copy [post]
func (h *Handlers) CopyRuleset(ctx context.Context, c *app.RequestContext) {
	u, ok := h.requirePrompt(c)
	if !ok || !h.sheetsReady(c) {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	rs, err := h.Sheets.CopyRuleset(ctx, u, id)
	if err != nil {
		h.sheetError(c, err)
		return
	}
	c.JSON(consts.StatusCreated, rs)
}

// CompileRuleset handles POST /v1/check-rulesets/{id}/compile.
//
// @Summary      Turn a sentence into a check formula (one model call)
// @Description  kind=link: a step telling whether a document of doc_type belongs to an anchor document; kind=rule: a rule of a contract group. A sentence the model cannot turn into a valid formula comes back with ai=true. Requires the prompt_editor or admin role.
// @Tags         Checks
// @Accept       json
// @Produce      json
// @Param        id       path      string                   true  "Rule set id"  format(uuid)
// @Param        request  body      dto.CompileCheckRequest  true  "Sentence"
// @Success      200      {object}  sheets.CompileResult
// @Failure      402      {object}  dto.ErrorResponse
// @Security     ApiKeyAuth
// @Router       /v1/check-rulesets/{id}/compile [post]
func (h *Handlers) CompileRuleset(ctx context.Context, c *app.RequestContext) {
	u, ok := h.requirePrompt(c)
	if !ok || !h.sheetsReady(c) || !h.withinBudget(ctx, c, u) {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var req dto.CompileCheckRequest
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	res, err := h.Sheets.Compile(ctx, u, id, req.Kind, req.DocType, req.Anchor, req.Text)
	if err != nil {
		h.sheetError(c, err)
		return
	}
	c.JSON(consts.StatusOK, res)
}

// CreateCheck handles POST /v1/cases/{id}/checks.
//
// @Summary      Cross-check the documents of a case by one rule set (§6.9.9, U105)
// @Description  Builds a sheet of the document types the set compares (extracting only what is missing), groups the documents around the anchor documents and evaluates the rules. The set must be used by the case's knowledge base. Poll GET /v1/checks/{id}.
// @Tags         Checks
// @Accept       json
// @Produce      json
// @Param        id       path      string                  true  "Case id"  format(uuid)
// @Param        request  body      dto.CreateCheckRequest  true  "Rule set"
// @Success      202      {object}  types.CheckRun
// @Failure      422  {object}  dto.ErrorResponse
// @Security     ApiKeyAuth
// @Router       /v1/cases/{id}/checks [post]
func (h *Handlers) CreateCheck(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok || !h.sheetsReady(c) {
		return
	}
	caseID, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var req dto.CreateCheckRequest
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	r, err := h.Sheets.CreateCheck(ctx, u, caseID, req.RulesetID)
	if err != nil {
		h.sheetError(c, err)
		return
	}
	c.JSON(consts.StatusAccepted, r)
}

// ListChecks handles GET /v1/cases/{id}/checks.
//
// @Summary   The cross-check runs of a case, newest first
// @Tags      Checks
// @Produce   json
// @Param     id   path      string  true  "Case id"  format(uuid)
// @Success   200  {object}  dto.CheckRunList
// @Security  ApiKeyAuth
// @Router    /v1/cases/{id}/checks [get]
func (h *Handlers) ListChecks(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok || !h.sheetsReady(c) {
		return
	}
	caseID, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	list, err := h.Sheets.ListChecks(ctx, u.ID, caseID)
	if err != nil {
		h.sheetError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.CheckRunList{Data: list})
}

// GetCheck handles GET /v1/checks/{id}.
//
// @Summary   A cross-check run: groups, unlinked documents, outcome of each rule
// @Tags      Checks
// @Produce   json
// @Param     id   path      string  true  "Check run id"  format(uuid)
// @Success   200  {object}  types.CheckRun
// @Failure   404  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/checks/{id} [get]
func (h *Handlers) GetCheck(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok || !h.sheetsReady(c) {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	r, err := h.Sheets.GetCheck(ctx, u.ID, id)
	if err != nil {
		h.sheetError(c, err)
		return
	}
	c.JSON(consts.StatusOK, r)
}

// RerunCheck handles POST /v1/checks/{id}/rerun.
//
// @Summary   Evaluate a run again on the current values and rules
// @Tags      Checks
// @Produce   json
// @Param     id   path      string  true  "Check run id"  format(uuid)
// @Success   200  {object}  types.CheckRun
// @Security  ApiKeyAuth
// @Router    /v1/checks/{id}/rerun [post]
func (h *Handlers) RerunCheck(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok || !h.sheetsReady(c) {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	r, err := h.Sheets.RerunCheck(ctx, u, id)
	if err != nil {
		h.sheetError(c, err)
		return
	}
	c.JSON(consts.StatusOK, r)
}

// SetCheckLink handles PUT /v1/checks/{id}/links.
//
// @Summary      Choose the anchor document of a document, then evaluate again
// @Description  anchor_segment_id null = belongs to none; auto=true = let the steps decide again. The choice applies to every later run of the case.
// @Tags         Checks
// @Accept       json
// @Produce      json
// @Param        id       path      string                   true  "Check run id"  format(uuid)
// @Param        request  body      dto.SetCheckLinkRequest  true  "Choice"
// @Success      200      {object}  types.CheckRun
// @Security     ApiKeyAuth
// @Router       /v1/checks/{id}/links [put]
func (h *Handlers) SetCheckLink(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok || !h.sheetsReady(c) {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var req dto.SetCheckLinkRequest
	if err := c.BindJSON(&req); err != nil || req.SegmentID == uuid.Nil {
		h.badRequest(c, "segment_id is required")
		return
	}
	r, err := h.Sheets.SetCheckLink(ctx, u, id, req.SegmentID, req.AnchorSegmentID, req.Auto)
	if err != nil {
		h.sheetError(c, err)
		return
	}
	c.JSON(consts.StatusOK, r)
}

// DeleteCheck handles DELETE /v1/checks/{id}.
//
// @Summary   Delete a cross-check run (its sheet stays)
// @Tags      Checks
// @Produce   json
// @Param     id   path      string  true  "Check run id"  format(uuid)
// @Success   200  {object}  dto.OK
// @Security  ApiKeyAuth
// @Router    /v1/checks/{id} [delete]
func (h *Handlers) DeleteCheck(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok || !h.sheetsReady(c) {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	if err := h.Sheets.DeleteCheck(ctx, u.ID, id); err != nil {
		h.sheetError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.OK{OK: true})
}
