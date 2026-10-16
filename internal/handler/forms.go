package handler

import (
	"bytes"
	"context"
	"io"
	"net/url"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/handler/dto"
)

// ListExportTemplates handles GET /v1/kbs/{id}/export-templates.
//
// @Summary   The Excel forms of a knowledge base (§6.9.8)
// @Tags      Export templates
// @Produce   json
// @Param     id   path      string  true  "Knowledge base id"  format(uuid)
// @Success   200  {object}  dto.ExportTemplateList
// @Security  ApiKeyAuth
// @Router    /v1/kbs/{id}/export-templates [get]
func (h *Handlers) ListExportTemplates(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok || !h.sheetsReady(c) {
		return
	}
	kbID, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	list, err := h.Sheets.ListForms(ctx, u.ID, kbID)
	if err != nil {
		h.sheetError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.ExportTemplateList{Data: list})
}

// UploadExportTemplate handles POST /v1/kbs/{id}/export-templates.
//
// @Summary      Import an Excel form (§6.9.8)
// @Description  The code finds the table, its empty record rows, the total row and the placeholders; one model call proposes how each column is filled (fields, formulas). Review and save it with PUT /v1/export-templates/{id}. Requires the prompt_editor or admin role.
// @Tags         Export templates
// @Accept       multipart/form-data
// @Produce      json
// @Param        id    path      string  true  "Knowledge base id"  format(uuid)
// @Param        file  formData  file    true  "The .xlsx form"
// @Success      201   {object}  types.ExportTemplate
// @Failure      403   {object}  dto.ErrorResponse
// @Failure      422   {object}  dto.ErrorResponse
// @Security     ApiKeyAuth
// @Router       /v1/kbs/{id}/export-templates [post]
func (h *Handlers) UploadExportTemplate(ctx context.Context, c *app.RequestContext) {
	u, ok := h.requirePrompt(c)
	if !ok || !h.sheetsReady(c) || !h.withinBudget(ctx, c, u) {
		return
	}
	kbID, ok := h.uuidParam(c, "id")
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
	data, err := io.ReadAll(f)
	if err != nil {
		h.badRequest(c, err.Error())
		return
	}
	t, err := h.Sheets.UploadForm(ctx, u, kbID, fh.Filename, data)
	if err != nil {
		h.sheetError(c, err)
		return
	}
	c.JSON(consts.StatusCreated, t)
}

// GetExportTemplate handles GET /v1/export-templates/{id}.
//
// @Summary   An Excel form: its regions and mapping
// @Tags      Export templates
// @Produce   json
// @Param     id   path      string  true  "Export template id"  format(uuid)
// @Success   200  {object}  types.ExportTemplate
// @Failure   404  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/export-templates/{id} [get]
func (h *Handlers) GetExportTemplate(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok || !h.sheetsReady(c) {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	t, err := h.Sheets.GetForm(ctx, u.ID, id)
	if err != nil {
		h.sheetError(c, err)
		return
	}
	c.JSON(consts.StatusOK, t)
}

// SaveExportTemplate handles PUT /v1/export-templates/{id}.
//
// @Summary      Save the reviewed mapping of an Excel form
// @Description  Formulas use {r}, {first} and {last} for rows. New fields are added to their document types in the knowledge base. Requires the prompt_editor or admin role.
// @Tags         Export templates
// @Accept       json
// @Produce      json
// @Param        id       path      string                          true  "Export template id"  format(uuid)
// @Param        request  body      dto.SaveExportTemplateRequest  true  "Name and mapping"
// @Success      200      {object}  types.ExportTemplate
// @Failure      422      {object}  dto.ErrorResponse
// @Security     ApiKeyAuth
// @Router       /v1/export-templates/{id} [put]
func (h *Handlers) SaveExportTemplate(ctx context.Context, c *app.RequestContext) {
	u, ok := h.requirePrompt(c)
	if !ok || !h.sheetsReady(c) {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var req dto.SaveExportTemplateRequest
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	t, err := h.Sheets.SaveForm(ctx, u, id, req.Name, req.Mapping)
	if err != nil {
		h.sheetError(c, err)
		return
	}
	c.JSON(consts.StatusOK, t)
}

// DeleteExportTemplate handles DELETE /v1/export-templates/{id}.
//
// @Summary   Delete an Excel form; sheets built for it stay
// @Tags      Export templates
// @Produce   json
// @Param     id   path      string  true  "Export template id"  format(uuid)
// @Success   200  {object}  dto.OK
// @Security  ApiKeyAuth
// @Router    /v1/export-templates/{id} [delete]
func (h *Handlers) DeleteExportTemplate(ctx context.Context, c *app.RequestContext) {
	u, ok := h.requirePrompt(c)
	if !ok || !h.sheetsReady(c) {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	if err := h.Sheets.DeleteForm(ctx, u.ID, id); err != nil {
		h.sheetError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.OK{OK: true})
}

// DownloadSheetForm handles GET /v1/sheets/{id}/form.xlsx.
//
// @Summary   Download a sheet filled into an Excel form of its knowledge base (§6.9.8)
// @Tags      Export templates
// @Produce   application/vnd.openxmlformats-officedocument.spreadsheetml.sheet
// @Param     id        path   string  true   "Sheet id"  format(uuid)
// @Param     template  query  string  false  "Export template id (default: the one the sheet was built for)"  format(uuid)
// @Success   200  {file}    binary
// @Failure   404  {object}  dto.ErrorResponse
// @Failure   422  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/sheets/{id}/form.xlsx [get]
func (h *Handlers) DownloadSheetForm(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok || !h.sheetsReady(c) {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var formID *uuid.UUID
	if raw := string(c.Query("template")); raw != "" {
		fid, err := uuid.Parse(raw)
		if err != nil {
			h.badRequest(c, "invalid template")
			return
		}
		formID = &fid
	}
	var buf bytes.Buffer
	name, err := h.Sheets.FormXLSX(ctx, u.ID, id, formID, &buf)
	if err != nil {
		h.sheetError(c, err)
		return
	}
	c.Response.Header.Set("Content-Disposition", "attachment; filename=\"form.xlsx\"; filename*=UTF-8''"+url.PathEscape(name))
	c.Data(consts.StatusOK, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", buf.Bytes())
}
