package handler

import (
	"context"
	"errors"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/service/docmodel"
	"github.com/thanhenti/bepaylot/internal/handler/dto"
)

func (h *Handlers) splitReady(c *app.RequestContext) bool {
	if h.Split == nil {
		c.JSON(consts.StatusServiceUnavailable, dto.NewError("api_error", "classification is not configured"))
		return false
	}
	return true
}

func (h *Handlers) splitError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, docmodel.ErrNoLabels), errors.Is(err, docmodel.ErrBadSplit):
		h.unprocessable(c, err.Error())
	default:
		h.serviceError(c, err)
	}
}

// ClassifyCase handles POST /v1/cases/{id}/classify.
//
// @Summary      Propose how the files of a case split into documents (§6.9.4)
// @Description  Queues document:classify for the indexed files (all, or document_ids): one LLM call per file reading only titles and the cut hints found by code (mode titles, default), or also the first lines of every page (mode pages). The user's segments are kept.
// @Tags         Split
// @Accept       json
// @Produce      json
// @Param        id       path      string                   true  "Case id"  format(uuid)
// @Param        request  body      dto.ClassifyCaseRequest  false "Mode and files"
// @Success      202      {object}  dto.ClassifyCaseResponse
// @Failure      422      {object}  dto.ErrorResponse
// @Security     ApiKeyAuth
// @Router       /v1/cases/{id}/classify [post]
func (h *Handlers) ClassifyCase(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok || !h.splitReady(c) {
		return
	}
	caseID, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var req dto.ClassifyCaseRequest
	if len(c.Request.Body()) > 0 {
		if err := c.BindJSON(&req); err != nil {
			h.badRequest(c, "invalid JSON body: "+err.Error())
			return
		}
	}
	var docs []uuid.UUID
	for _, s := range req.DocumentIDs {
		id, err := uuid.Parse(s)
		if err != nil {
			h.unprocessable(c, "document_ids: "+err.Error())
			return
		}
		docs = append(docs, id)
	}
	if !h.withinBudget(ctx, c, u) {
		return
	}
	n, err := h.Split.Classify(ctx, u, caseID, docs, req.Mode)
	if err != nil {
		h.splitError(c, err)
		return
	}
	c.JSON(consts.StatusAccepted, dto.ClassifyCaseResponse{Queued: n})
}

// GetSplit handles GET /v1/cases/{id}/split.
//
// @Summary   The split of a case into documents and bundles (§6.9.7)
// @Tags      Split
// @Produce   json
// @Param     id   path      string  true  "Case id"  format(uuid)
// @Success   200  {object}  docmodel.SplitView
// @Security  ApiKeyAuth
// @Router    /v1/cases/{id}/split [get]
func (h *Handlers) GetSplit(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok || !h.splitReady(c) {
		return
	}
	caseID, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	v, err := h.Split.View(ctx, u.ID, caseID)
	if err != nil {
		h.splitError(c, err)
		return
	}
	c.JSON(consts.StatusOK, v)
}

// SaveSplit handles PUT /v1/cases/{id}/split.
//
// @Summary      Confirm the split of a case (§6.9.7)
// @Description  The bundles in order, each with its documents. Every page of each file in the request must be in exactly one document; the documents become the user's segments and only they become rows of sheets.
// @Tags         Split
// @Accept       json
// @Produce      json
// @Param        id       path      string               true  "Case id"  format(uuid)
// @Param        request  body      docmodel.SplitInput  true  "Bundles"
// @Success      200      {object}  docmodel.SplitView
// @Failure      422      {object}  dto.ErrorResponse
// @Security     ApiKeyAuth
// @Router       /v1/cases/{id}/split [put]
func (h *Handlers) SaveSplit(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok || !h.splitReady(c) {
		return
	}
	caseID, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var req docmodel.SplitInput
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	if err := h.Split.Save(ctx, u, caseID, req); err != nil {
		h.splitError(c, err)
		return
	}
	v, err := h.Split.View(ctx, u.ID, caseID)
	if err != nil {
		h.splitError(c, err)
		return
	}
	c.JSON(consts.StatusOK, v)
}
