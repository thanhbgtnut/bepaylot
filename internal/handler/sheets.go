package handler

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

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
	switch {
	case errors.Is(err, sheets.ErrNotFound):
		h.notFound(c, "sheet not found")
	case errors.Is(err, sheets.ErrBadFile), errors.Is(err, sheets.ErrNoTables),
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
// @Summary      Build a sheet of a case from the document types of its knowledge base (§6.9.6)
// @Description  One sub-table per document type that has fields (tables = codes, omitted = all); one row per document of that type in the indexed files, filled with the fields extracted while the files were processed. Files processed before a field was added are extracted first (task case:sheet). Poll GET /v1/sheets/{id} for progress.
// @Tags         Sheets
// @Accept       json
// @Produce      json
// @Param        id       path      string                  true  "Case id"  format(uuid)
// @Param        request  body      dto.CreateSheetRequest  false  "Document types"
// @Success      202      {object}  types.Sheet
// @Failure      402      {object}  dto.ErrorResponse
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
	if len(c.Request.Body()) > 0 {
		if err := c.BindJSON(&req); err != nil {
			h.badRequest(c, "invalid JSON body: "+err.Error())
			return
		}
	}
	sh, err := h.Sheets.Create(ctx, u, caseID, req.Tables)
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

// DeleteSheet handles DELETE /v1/sheets/{id}.
//
// @Summary      Delete a sheet
// @Description  Removes the sheet and its corrections; the fields extracted from the files stay, so a new sheet shows the same values.
// @Tags         Sheets
// @Produce      json
// @Param        id   path      string  true  "Sheet id"  format(uuid)
// @Success      200  {object}  dto.OK
// @Failure      404  {object}  dto.ErrorResponse
// @Security     ApiKeyAuth
// @Router       /v1/sheets/{id} [delete]
func (h *Handlers) DeleteSheet(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok || !h.sheetsReady(c) {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	if err := h.Sheets.Delete(ctx, u.ID, id); err != nil {
		h.sheetError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.OK{OK: true})
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
