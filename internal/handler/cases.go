package handler

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/application/service/cases"
	"github.com/thanhenti/bepaylot/internal/handler/dto"
	"github.com/thanhenti/bepaylot/internal/types"
)

// ListCaseTypes handles GET /v1/case-types.
//
// @Summary   List the case types loaded from configs/case_types
// @Tags      Cases
// @Produce   json
// @Success   200  {object}  dto.CaseTypeList
// @Security  ApiKeyAuth
// @Router    /v1/case-types [get]
func (h *Handlers) ListCaseTypes(ctx context.Context, c *app.RequestContext) {
	if _, ok := h.user(c); !ok {
		return
	}
	c.JSON(consts.StatusOK, dto.CaseTypeList{Data: h.Cases.Types()})
}

// CreateCase handles POST /v1/kbs/{id}/cases.
//
// @Summary   Create a case (a file set identified by a business code)
// @Description The code is normalized and checked by the case type (e.g. " rt112233" → "RT112233"). An existing code answers 409 with the existing case.
// @Tags      Cases
// @Accept    json
// @Produce   json
// @Param     id       path      string                true  "Knowledge base id"  format(uuid)
// @Param     request  body      cases.CreateRequest   true  "Code, case type, title, case metadata"
// @Success   201      {object}  types.Case
// @Failure   409      {object}  dto.CaseConflict
// @Failure   422      {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/kbs/{id}/cases [post]
func (h *Handlers) CreateCase(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	kb, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var req cases.CreateRequest
	if err := json.Unmarshal(c.Request.Body(), &req); err != nil {
		h.badRequest(c, "invalid JSON: "+err.Error())
		return
	}
	cs, err := h.Cases.Create(ctx, u.ID, kb, req)
	if errors.Is(err, cases.ErrConflict) {
		c.JSON(consts.StatusConflict, dto.CaseConflict{ErrorResponse: dto.NewError("conflict_error", "case code already exists"), Case: cs})
		return
	}
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusCreated, cs)
}

// ListCases handles GET /v1/kbs/{id}/cases.
//
// @Summary   List cases with per-status document counts
// @Tags      Cases
// @Produce   json
// @Param     id         path      string  true   "Knowledge base id"  format(uuid)
// @Param     q          query     string  false  "Part of the code or title"
// @Param     case_type  query     string  false  "Case type"
// @Param     status     query     string  false  "open | closed"
// @Param     metadata   query     string  false  "JSON filter on case metadata"
// @Param     limit      query     int     false  "Page size (max 500)"  default(100)
// @Param     before     query     string  false  "Cursor: created_at of the last item (RFC3339)"
// @Success   200        {object}  dto.CaseList
// @Failure   422        {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/kbs/{id}/cases [get]
func (h *Handlers) ListCases(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	kb, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	req := cases.ListRequest{Query: string(c.Query("q")), CaseType: string(c.Query("case_type")), Status: string(c.Query("status")), Limit: intQuery(c, "limit", 100)}
	if m := string(c.Query("metadata")); m != "" {
		if err := json.Unmarshal([]byte(m), &req.Metadata); err != nil {
			h.badRequest(c, "metadata must be a JSON object")
			return
		}
	}
	if b := string(c.Query("before")); b != "" {
		t, err := time.Parse(time.RFC3339Nano, b)
		if err != nil {
			h.badRequest(c, "before must be RFC3339")
			return
		}
		req.Before = &t
	}
	out, err := h.Cases.List(ctx, u.ID, kb, req)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	res := dto.CaseList{Data: out}
	if res.Data == nil {
		res.Data = []types.Case{}
	}
	if len(out) > 0 && len(out) >= max(1, min(req.Limit, 500)) {
		res.NextCursor = out[len(out)-1].CreatedAt.Format(time.RFC3339Nano)
	}
	c.JSON(consts.StatusOK, res)
}

// GetCaseByCode handles GET /v1/kbs/{id}/cases/by-code/{code}.
//
// @Summary   Find a case by code (normalized by its case type first)
// @Tags      Cases
// @Produce   json
// @Param     id    path      string  true  "Knowledge base id"  format(uuid)
// @Param     code  path      string  true  "Case code"
// @Success   200   {object}  types.Case
// @Failure   404   {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/kbs/{id}/cases/by-code/{code} [get]
func (h *Handlers) GetCaseByCode(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	kb, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	cs, err := h.Cases.ByCode(ctx, u.ID, kb, c.Param("code"))
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, cs)
}

// GetCase handles GET /v1/cases/{id}.
//
// @Summary   Case detail: document counts by status
// @Tags      Cases
// @Produce   json
// @Param     id   path      string  true  "Case id"  format(uuid)
// @Success   200  {object}  types.Case
// @Failure   404  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/cases/{id} [get]
func (h *Handlers) GetCase(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	cs, err := h.Cases.Detail(ctx, u.ID, id)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, cs)
}

// UpdateCase handles PATCH /v1/cases/{id}.
//
// @Summary   Change a case's title, status (open/closed) or metadata
// @Description code, case_type and kb_id never change (422).
// @Tags      Cases
// @Accept    json
// @Produce   json
// @Param     id       path      string               true  "Case id"  format(uuid)
// @Param     request  body      cases.UpdateRequest  true  "Fields to change"
// @Success   200      {object}  types.Case
// @Failure   422      {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/cases/{id} [patch]
func (h *Handlers) UpdateCase(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var req cases.UpdateRequest
	if err := json.Unmarshal(c.Request.Body(), &req); err != nil {
		h.badRequest(c, "invalid JSON: "+err.Error())
		return
	}
	cs, err := h.Cases.Update(ctx, u.ID, id, req)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, cs)
}

// DeleteCase handles DELETE /v1/cases/{id}.
//
// @Summary   Delete a case and its documents (async)
// @Tags      Cases
// @Produce   json
// @Param     id   path      string  true  "Case id"  format(uuid)
// @Success   200  {object}  dto.OK
// @Failure   404  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/cases/{id} [delete]
func (h *Handlers) DeleteCase(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	if err := h.Cases.Delete(ctx, u.ID, id); err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.OK{OK: true})
}

// ListCaseDocuments handles GET /v1/cases/{id}/documents.
//
// @Summary   List the documents of a case
// @Tags      Cases
// @Produce   json
// @Param     id        path      string  true   "Case id"  format(uuid)
// @Param     status    query     string  false  "Comma-separated statuses"
// @Param     batch_id  query     string  false  "Upload batch id"
// @Param     metadata  query     string  false  "JSON metadata filter, e.g. {\"loai_giay_to\":\"GCN_HKD\"}"
// @Param     q         query     string  false  "Full-text over file name, card and metadata values"
// @Param     limit     query     int     false  "Page size (max 500)"  default(100)
// @Param     before    query     string  false  "Cursor: created_at of the last item (RFC3339)"
// @Success   200       {object}  dto.DocumentList
// @Failure   422       {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/cases/{id}/documents [get]
func (h *Handlers) ListCaseDocuments(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	f, ok := h.documentFilter(c)
	if !ok {
		return
	}
	docs, err := h.Docs.ListCaseDocuments(ctx, u.ID, id, f)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	h.writeDocumentList(c, docs, f.Limit)
}

// UploadCaseDocuments handles POST /v1/cases/{id}/documents.
//
// @Summary   Upload one or more files (streamed) into a case
// @Description Same fields as POST /v1/kbs/{id}/documents, without case_code.
// @Tags      Cases
// @Accept    mpfd
// @Produce   json
// @Param     id              path      string  true   "Case id"  format(uuid)
// @Param     file            formData  file    true   "File (repeat for several files)"
// @Param     metadata        formData  string  false  "JSON metadata for every file"
// @Param     files_metadata  formData  string  false  "JSON per-file metadata keyed by file name or index"
// @Param     callback_url    formData  string  false  "Optional URL that receives a POST (JSON) when each document finishes"
// @Param     interactive     query     bool    false  "Use the high-priority lanes (chat attachments)"
// @Success   202             {object}  document.UploadResult
// @Failure   404             {object}  dto.ErrorResponse
// @Failure   409             {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/cases/{id}/documents [post]
func (h *Handlers) UploadCaseDocuments(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	cs, err := h.Cases.GetCaseOwned(ctx, u.ID, id)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	interactive := string(c.Query("interactive")) == "true" || string(c.Query("interactive")) == "1"
	res, err := h.upload(ctx, c, u.ID, cs.KBID, &cs.ID, interactive)
	if err != nil {
		h.uploadError(c, err)
		return
	}
	c.JSON(consts.StatusAccepted, res)
}

// SearchCase handles POST /v1/cases/{id}/search.
//
// @Summary   Search one case (case TOC → document trees → pages → source lines)
// @Tags      Cases
// @Accept    json
// @Produce   json
// @Param     id       path      string               true  "Case id"  format(uuid)
// @Param     request  body      types.SearchRequest  true  "Query and filters (case_ids/kb_ids are ignored)"
// @Success   200      {object}  types.SearchResponse
// @Failure   422      {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/cases/{id}/search [post]
func (h *Handlers) SearchCase(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var req types.SearchRequest
	if err := json.Unmarshal(c.Request.Body(), &req); err != nil {
		h.badRequest(c, "invalid JSON: "+err.Error())
		return
	}
	req.CaseIDs, req.KBIDs, req.OwnerID = []uuid.UUID{id}, nil, u.ID
	resp, err := h.Searcher.Search(ctx, req)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, resp)
}

// documentFilter parses the shared document list query parameters.
func (h *Handlers) documentFilter(c *app.RequestContext) (postgres.DocumentFilter, bool) {
	f := postgres.DocumentFilter{Query: string(c.Query("q")), Limit: intQuery(c, "limit", 100)}
	if s := string(c.Query("status")); s != "" {
		f.Statuses = strings.Split(s, ",")
	}
	if b := string(c.Query("batch_id")); b != "" {
		bid, err := uuid.Parse(b)
		if err != nil {
			h.badRequest(c, "invalid batch_id")
			return f, false
		}
		f.BatchID = &bid
	}
	if b := string(c.Query("case_id")); b != "" {
		cid, err := uuid.Parse(b)
		if err != nil {
			h.badRequest(c, "invalid case_id")
			return f, false
		}
		f.CaseIDs = []uuid.UUID{cid}
	}
	if m := string(c.Query("metadata")); m != "" {
		if err := json.Unmarshal([]byte(m), &f.Metadata); err != nil {
			h.badRequest(c, "metadata must be a JSON object")
			return f, false
		}
	}
	if b := string(c.Query("before")); b != "" {
		t, err := time.Parse(time.RFC3339Nano, b)
		if err != nil {
			h.badRequest(c, "before must be RFC3339")
			return f, false
		}
		f.Before = &t
	}
	return f, true
}

func (h *Handlers) writeDocumentList(c *app.RequestContext, docs []types.Document, limit int) {
	out := dto.DocumentList{Data: docs}
	if out.Data == nil {
		out.Data = []types.Document{}
	}
	if len(docs) > 0 && len(docs) >= max(1, min(limit, 500)) {
		out.NextCursor = docs[len(docs)-1].CreatedAt.Format(time.RFC3339Nano)
	}
	c.JSON(consts.StatusOK, out)
}
