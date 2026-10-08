package handler

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/service/sheets"
	"github.com/thanhenti/bepaylot/internal/handler/dto"
)

// maxSheetUpload caps an uploaded .xlsx.
const maxSheetUpload = 10 << 20

func (h *Handlers) sheetsReady(c *app.RequestContext) bool {
	if h.Sheets == nil {
		c.JSON(consts.StatusServiceUnavailable, dto.NewError("api_error", "case sheets are not configured"))
		return false
	}
	return true
}

func (h *Handlers) sheetError(c *app.RequestContext, err error) {
	var split *sheets.SplitNotReviewedError
	switch {
	case errors.As(err, &split):
		c.JSON(consts.StatusConflict, dto.NewError("split_not_reviewed", err.Error()))
	case errors.Is(err, sheets.ErrNotFound):
		h.notFound(c, "sheet not found")
	case errors.Is(err, sheets.ErrNotSheet), errors.Is(err, sheets.ErrBadFile), errors.Is(err, sheets.ErrNoTables),
		errors.Is(err, sheets.ErrUnknownKey):
		h.unprocessable(c, err.Error())
	case errors.Is(err, sheets.ErrNotFinished):
		c.JSON(consts.StatusConflict, dto.NewError("conflict_error", err.Error()))
	default:
		h.serviceError(c, err)
	}
}

// CreateSheet handles POST /v1/cases/{id}/sheets.
//
// @Summary      Build a sheet of a case from a sheet template (§6.9.6)
// @Description  One sub-table per document type (tables = labels, omitted = all); rows are the reviewed documents of the case. Confirmed fields are reused; the missing ones are extracted by one agent turn per bundle (task case:sheet). Poll GET /v1/sheets/{id} for progress. 409 split_not_reviewed when a file's split is not reviewed.
// @Tags         Sheets
// @Accept       json
// @Produce      json
// @Param        id       path      string                  true  "Case id"  format(uuid)
// @Param        request  body      dto.CreateSheetRequest  true  "Template"
// @Success      202      {object}  types.Sheet
// @Failure      402      {object}  dto.ErrorResponse
// @Failure      409      {object}  dto.ErrorResponse
// @Failure      422      {object}  dto.ErrorResponse
// @Security     ApiKeyAuth
// @Router       /v1/cases/{id}/sheets [post]
func (h *Handlers) CreateSheet(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok || !h.sheetsReady(c) {
		return
	}
	caseID, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var req dto.CreateSheetRequest
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	tpl, err := uuid.Parse(req.TemplateID)
	if err != nil {
		h.unprocessable(c, "template_id is required")
		return
	}
	if !h.withinBudget(ctx, c, u) {
		return
	}
	sh, err := h.Sheets.Create(ctx, u, caseID, tpl, req.Tables)
	if err != nil {
		h.sheetError(c, err)
		return
	}
	c.JSON(consts.StatusAccepted, sh)
}

// ListSheets handles GET /v1/cases/{id}/sheets.
//
// @Summary   The sheets of a case, newest first
// @Tags      Sheets
// @Produce   json
// @Param     id   path      string  true  "Case id"  format(uuid)
// @Success   200  {object}  dto.SheetList
// @Security  ApiKeyAuth
// @Router    /v1/cases/{id}/sheets [get]
func (h *Handlers) ListSheets(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok || !h.sheetsReady(c) {
		return
	}
	caseID, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	list, err := h.Sheets.List(ctx, u.ID, caseID)
	if err != nil {
		h.sheetError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.SheetList{Data: list})
}

// GetSheet handles GET /v1/sheets/{id}.
//
// @Summary   A sheet: status, progress and its sub-tables; each cell's AI value, current value, confidence and evidence
// @Tags      Sheets
// @Produce   json
// @Param     id   path      string  true  "Sheet id"  format(uuid)
// @Success   200  {object}  sheets.View
// @Failure   404  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/sheets/{id} [get]
func (h *Handlers) GetSheet(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok || !h.sheetsReady(c) {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	v, err := h.Sheets.Get(ctx, u.ID, id)
	if err != nil {
		h.sheetError(c, err)
		return
	}
	c.JSON(consts.StatusOK, v)
}

// SaveSheetEdits handles POST /v1/sheets/{id}/edits.
//
// @Summary      Save the user's values (§6.9.6)
// @Description  Each value becomes a confirmed user field; a value different from the AI's is recorded as a correction.
// @Tags         Sheets
// @Accept       json
// @Produce      json
// @Param        id       path      string                true  "Sheet id"  format(uuid)
// @Param        request  body      dto.SheetEditsRequest  true  "Edits"
// @Success      200      {object}  sheets.View
// @Failure      409      {object}  dto.ErrorResponse
// @Failure      422      {object}  dto.ErrorResponse
// @Security     ApiKeyAuth
// @Router       /v1/sheets/{id}/edits [post]
func (h *Handlers) SaveSheetEdits(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok || !h.sheetsReady(c) {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var req dto.SheetEditsRequest
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	v, err := h.Sheets.SaveEdits(ctx, u, id, req.Edits)
	if err != nil {
		h.sheetError(c, err)
		return
	}
	c.JSON(consts.StatusOK, v)
}

// ImportSheet handles POST /v1/sheets/{id}/import.
//
// @Summary      Compare an edited .xlsx with the values it was downloaded with
// @Description  Nothing is written: the result lists edits, conflicts (changed on the web since download) and ignored cells; save with /edits.
// @Tags         Sheets
// @Accept       multipart/form-data
// @Produce      json
// @Param        id    path      string  true  "Sheet id"  format(uuid)
// @Param        file  formData  file    true  "The .xlsx downloaded from this sheet"
// @Success      200   {object}  sheets.ImportResult
// @Failure      422   {object}  dto.ErrorResponse
// @Security     ApiKeyAuth
// @Router       /v1/sheets/{id}/import [post]
func (h *Handlers) ImportSheet(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok || !h.sheetsReady(c) {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	fh, err := c.FormFile("file")
	if err != nil {
		h.badRequest(c, "file is required")
		return
	}
	if fh.Size > maxSheetUpload {
		h.unprocessable(c, "file is too large")
		return
	}
	f, err := fh.Open()
	if err != nil {
		h.badRequest(c, err.Error())
		return
	}
	defer f.Close()
	res, err := h.Sheets.Import(ctx, u.ID, id, f)
	if err != nil {
		h.sheetError(c, err)
		return
	}
	c.JSON(consts.StatusOK, res)
}

// DownloadSheet handles GET /v1/sheets/{id}/xlsx.
//
// @Summary   Download the sheet as .xlsx: one data-only sheet per sub-table, plus the hidden _bp
// @Tags      Sheets
// @Produce   application/vnd.openxmlformats-officedocument.spreadsheetml.sheet
// @Param     id      path   string  true   "Sheet id"  format(uuid)
// @Param     tables  query  string  false  "Labels of the sub-tables to include, comma separated (default all)"
// @Success   200  {file}    binary
// @Failure   404  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/sheets/{id}/xlsx [get]
func (h *Handlers) DownloadSheet(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok || !h.sheetsReady(c) {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var buf bytes.Buffer
	var tables []string
	for _, t := range strings.Split(string(c.Query("tables")), ",") {
		if t = strings.TrimSpace(t); t != "" {
			tables = append(tables, t)
		}
	}
	name, err := h.Sheets.XLSX(ctx, u.ID, id, tables, &buf)
	if err != nil {
		h.sheetError(c, err)
		return
	}
	c.Response.Header.Set("Content-Disposition", "attachment; filename=\"sheet.xlsx\"; filename*=UTF-8''"+url.PathEscape(name))
	c.Data(consts.StatusOK, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", buf.Bytes())
}
