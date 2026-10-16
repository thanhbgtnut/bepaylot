package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/handler/dto"
	"github.com/thanhenti/bepaylot/internal/types"
)

// SaveChecks handles PUT /v1/kbs/{id}/checks.
//
// @Summary      Save the cross-check rules of a knowledge base (§6.9.9)
// @Description  The anchor type, the steps that link each document type to an anchor document and the rules; every formula is checked against the fields of the KB. Requires the prompt_editor or admin role.
// @Tags         Checks
// @Accept       json
// @Produce      json
// @Param        id       path      string             true  "Knowledge base id"  format(uuid)
// @Param        request  body      types.CheckConfig  true  "Rules"
// @Success      200      {object}  types.KnowledgeBase
// @Failure      422      {object}  dto.ErrorResponse
// @Security     ApiKeyAuth
// @Router       /v1/kbs/{id}/checks [put]
func (h *Handlers) SaveChecks(ctx context.Context, c *app.RequestContext) {
	u, ok := h.requirePrompt(c)
	if !ok || !h.sheetsReady(c) {
		return
	}
	kbID, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var req types.CheckConfig
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	kb, err := h.Sheets.SaveChecks(ctx, u, kbID, req)
	if err != nil {
		h.sheetError(c, err)
		return
	}
	c.JSON(consts.StatusOK, kb)
}

// CompileCheck handles POST /v1/kbs/{id}/checks/compile.
//
// @Summary      Turn a sentence into a check formula (one model call)
// @Description  kind=rule: a rule of a contract group; kind=link: a step telling whether a document of doc_type belongs to an anchor document. A sentence the model cannot turn into a valid formula comes back with ai=true. Requires the prompt_editor or admin role.
// @Tags         Checks
// @Accept       json
// @Produce      json
// @Param        id       path      string                   true  "Knowledge base id"  format(uuid)
// @Param        request  body      dto.CompileCheckRequest  true  "Sentence"
// @Success      200      {object}  sheets.CompileResult
// @Failure      402      {object}  dto.ErrorResponse
// @Security     ApiKeyAuth
// @Router       /v1/kbs/{id}/checks/compile [post]
func (h *Handlers) CompileCheck(ctx context.Context, c *app.RequestContext) {
	u, ok := h.requirePrompt(c)
	if !ok || !h.sheetsReady(c) || !h.withinBudget(ctx, c, u) {
		return
	}
	kbID, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var req dto.CompileCheckRequest
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	res, err := h.Sheets.Compile(ctx, u, kbID, req.Kind, req.DocType, req.Anchor, req.Text)
	if err != nil {
		h.sheetError(c, err)
		return
	}
	c.JSON(consts.StatusOK, res)
}

// CreateCheck handles POST /v1/cases/{id}/checks.
//
// @Summary      Cross-check the documents of a case (§6.9.9)
// @Description  Builds a sheet of every document type with fields (extracting only what is missing), groups the documents around the anchor documents and evaluates the rules. Poll GET /v1/checks/{id}.
// @Tags         Checks
// @Produce      json
// @Param        id   path      string  true  "Case id"  format(uuid)
// @Success      202  {object}  types.CheckRun
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
	r, err := h.Sheets.CreateCheck(ctx, u, caseID)
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
