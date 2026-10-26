package handler

import (
	"context"
	"errors"
	"strconv"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/service/docmodel/doctypes"
	"github.com/thanhenti/bepaylot/internal/handler/dto"
)

func (h *Handlers) docTypesReady(c *app.RequestContext) bool {
	if h.DocTypes == nil {
		c.JSON(consts.StatusServiceUnavailable, dto.NewError("api_error", "document types are not configured"))
		return false
	}
	return true
}

func (h *Handlers) docTypeError(c *app.RequestContext, err error) {
	var inUse *doctypes.InUseError
	switch {
	case errors.As(err, &inUse):
		body := dto.DocTypeInUse{ErrorResponse: dto.NewError("conflict_error", err.Error()), KBs: []dto.KBRef{}}
		for _, k := range inUse.KBs {
			body.KBs = append(body.KBs, dto.KBRef{ID: k.ID, Name: k.Name})
		}
		c.JSON(consts.StatusConflict, body)
	case errors.Is(err, doctypes.ErrNotFound):
		h.notFound(c, err.Error())
	case errors.Is(err, doctypes.ErrCurrent):
		c.JSON(consts.StatusConflict, dto.NewError("conflict_error", err.Error()))
	case errors.Is(err, doctypes.ErrBad), errors.Is(err, doctypes.ErrNoReviewed):
		h.unprocessable(c, err.Error())
	case errors.Is(err, doctypes.ErrNotReady):
		c.JSON(consts.StatusServiceUnavailable, dto.NewError("api_error", err.Error()))
	default:
		h.serviceError(c, err)
	}
}

// docTypeReq checks the user (an editor of prompts when write) and the
// service.
func (h *Handlers) docTypeReq(c *app.RequestContext, write bool) (uuid.UUID, bool) {
	if write {
		u, ok := h.requirePrompt(c)
		return u.ID, ok && h.docTypesReady(c)
	}
	u, ok := h.user(c)
	return u.ID, ok && h.docTypesReady(c)
}

func (h *Handlers) versionParam(c *app.RequestContext) (int, bool) {
	v, err := strconv.Atoi(c.Param("version"))
	if err != nil || v < 1 {
		h.badRequest(c, "invalid version")
		return 0, false
	}
	return v, true
}

// ListDocTypes handles GET /v1/doc-types.
//
// @Summary   The user's catalogue of document types, shared by every knowledge base of theirs (§6.9.10, U97)
// @Tags      DocTypes
// @Produce   json
// @Param     kb_id   query     string  false  "Only the types this knowledge base uses"  format(uuid)
// @Param     unused  query     bool    false  "Only the types no knowledge base uses"
// @Param     q       query     string  false  "Search in name and code"
// @Success   200     {object}  dto.DocTypeList
// @Security  ApiKeyAuth
// @Router    /v1/doc-types [get]
func (h *Handlers) ListDocTypes(ctx context.Context, c *app.RequestContext) {
	owner, ok := h.docTypeReq(c, false)
	if !ok {
		return
	}
	f := doctypes.ListFilter{Unused: c.Query("unused") == "true", Q: c.Query("q")}
	if q := c.Query("kb_id"); q != "" {
		id, err := uuid.Parse(q)
		if err != nil {
			h.badRequest(c, "kb_id must be a uuid")
			return
		}
		f.KB = &id
	}
	list, err := h.DocTypes.List(ctx, owner, f)
	if err != nil {
		h.docTypeError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.DocTypeList{Data: list})
}

// CreateDocType handles POST /v1/doc-types.
//
// @Summary      Add a document type to the catalogue
// @Description  The code is made from the name (U65), with _2, _3… when taken; version 1 of the AI instructions is the default one. kb_ids are knowledge bases that start using it. Requires the prompt_editor or admin role.
// @Tags         DocTypes
// @Accept       json
// @Produce      json
// @Param        request  body      doctypes.CreateInput  true  "New type"
// @Success      201      {object}  types.DocTypeView
// @Failure      422      {object}  dto.ErrorResponse
// @Security     ApiKeyAuth
// @Router       /v1/doc-types [post]
func (h *Handlers) CreateDocType(ctx context.Context, c *app.RequestContext) {
	owner, ok := h.docTypeReq(c, true)
	if !ok {
		return
	}
	var req doctypes.CreateInput
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	t, err := h.DocTypes.Create(ctx, owner, req)
	if err != nil {
		h.docTypeError(c, err)
		return
	}
	c.JSON(consts.StatusCreated, t)
}

// GetDocType handles GET /v1/doc-types/{code}.
//
// @Summary   One document type: fields, version in use, samples, accuracy, knowledge bases using it
// @Tags      DocTypes
// @Produce   json
// @Param     code  path      string  true  "Type code"
// @Success   200   {object}  types.DocTypeView
// @Failure   404   {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/doc-types/{code} [get]
func (h *Handlers) GetDocType(ctx context.Context, c *app.RequestContext) {
	owner, ok := h.docTypeReq(c, false)
	if !ok {
		return
	}
	t, err := h.DocTypes.Get(ctx, owner, c.Param("code"))
	if err != nil {
		h.docTypeError(c, err)
		return
	}
	c.JSON(consts.StatusOK, t)
}

// UpdateDocType handles PATCH /v1/doc-types/{code}.
//
// @Summary      Change the name, description or fields of a type
// @Description  A field keeps its key; a new field without one gets a key made from its name. The fields become those of the version in use and the columns of later sheets in every knowledge base using the type. Requires the prompt_editor or admin role.
// @Tags         DocTypes
// @Accept       json
// @Produce      json
// @Param        code     path      string          true  "Type code"
// @Param        request  body      doctypes.Patch  true  "Changes"
// @Success      200      {object}  types.DocTypeView
// @Failure      422      {object}  dto.ErrorResponse
// @Security     ApiKeyAuth
// @Router       /v1/doc-types/{code} [patch]
func (h *Handlers) UpdateDocType(ctx context.Context, c *app.RequestContext) {
	owner, ok := h.docTypeReq(c, true)
	if !ok {
		return
	}
	var req doctypes.Patch
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	t, err := h.DocTypes.Update(ctx, owner, c.Param("code"), req)
	if err != nil {
		h.docTypeError(c, err)
		return
	}
	c.JSON(consts.StatusOK, t)
}

// DeleteDocType handles DELETE /v1/doc-types/{code}.
//
// @Summary      Delete a type with its instructions and samples
// @Description  A type still used by knowledge bases answers 409 with them, unless detach=true removes it from them first. Requires the prompt_editor or admin role.
// @Tags         DocTypes
// @Param        code    path   string  true   "Type code"
// @Param        detach  query  bool    false  "Remove it from the knowledge bases using it"
// @Success      204
// @Failure      409  {object}  dto.DocTypeInUse
// @Security     ApiKeyAuth
// @Router       /v1/doc-types/{code} [delete]
func (h *Handlers) DeleteDocType(ctx context.Context, c *app.RequestContext) {
	owner, ok := h.docTypeReq(c, true)
	if !ok {
		return
	}
	if err := h.DocTypes.Delete(ctx, owner, c.Param("code"), c.Query("detach") == "true"); err != nil {
		h.docTypeError(c, err)
		return
	}
	c.Status(consts.StatusNoContent)
}

// CopyDocType handles POST /v1/doc-types/{code}/copy.
//
// @Summary   Copy a type: fields and the version in use of its instructions; samples are not copied
// @Tags      DocTypes
// @Produce   json
// @Param     code  path      string  true  "Type code"
// @Success   201   {object}  types.DocTypeView
// @Security  ApiKeyAuth
// @Router    /v1/doc-types/{code}/copy [post]
func (h *Handlers) CopyDocType(ctx context.Context, c *app.RequestContext) {
	owner, ok := h.docTypeReq(c, true)
	if !ok {
		return
	}
	t, err := h.DocTypes.Copy(ctx, owner, c.Param("code"), owner)
	if err != nil {
		h.docTypeError(c, err)
		return
	}
	c.JSON(consts.StatusCreated, t)
}

// SetDocTypeKBs handles PUT /v1/doc-types/{code}/kbs.
//
// @Summary   The knowledge bases that use a type (their pages are classified with it)
// @Tags      DocTypes
// @Accept    json
// @Param     code     path  string                 true  "Type code"
// @Param     request  body  dto.DocTypeKBsRequest  true  "Knowledge bases"
// @Success   204
// @Failure   422  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/doc-types/{code}/kbs [put]
func (h *Handlers) SetDocTypeKBs(ctx context.Context, c *app.RequestContext) {
	owner, ok := h.docTypeReq(c, true)
	if !ok {
		return
	}
	var req dto.DocTypeKBsRequest
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	if err := h.DocTypes.SetKBs(ctx, owner, c.Param("code"), req.KBIDs); err != nil {
		h.docTypeError(c, err)
		return
	}
	c.Status(consts.StatusNoContent)
}

// ListSamples handles GET /v1/doc-types/{code}/samples.
//
// @Summary   The samples of a type: status, reviewed, score of the version in use (no page content)
// @Tags      DocTypes
// @Produce   json
// @Param     code  path      string  true  "Type code"
// @Success   200   {object}  dto.SampleList
// @Security  ApiKeyAuth
// @Router    /v1/doc-types/{code}/samples [get]
func (h *Handlers) ListSamples(ctx context.Context, c *app.RequestContext) {
	owner, ok := h.docTypeReq(c, false)
	if !ok {
		return
	}
	list, err := h.DocTypes.Samples(ctx, owner, c.Param("code"))
	if err != nil {
		h.docTypeError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.SampleList{Data: list})
}

// UploadSamples handles POST /v1/doc-types/{code}/samples.
//
// @Summary      Upload sample documents of a type (PDF or images, field "file", several allowed)
// @Description  Each file is read once in the background (task doctype:sample): per page the content the AI read from the image and the recognised lines. Poll the list for status ready/failed. Requires the prompt_editor or admin role.
// @Tags         DocTypes
// @Accept       multipart/form-data
// @Produce      json
// @Param        code  path      string  true  "Type code"
// @Param        file  formData  file    true  "Sample files"
// @Success      202   {object}  dto.SampleUploaded
// @Failure      422   {object}  dto.ErrorResponse
// @Security     ApiKeyAuth
// @Router       /v1/doc-types/{code}/samples [post]
func (h *Handlers) UploadSamples(ctx context.Context, c *app.RequestContext) {
	owner, ok := h.docTypeReq(c, true)
	if !ok {
		return
	}
	form, err := c.MultipartForm()
	if err != nil || len(form.File["file"]) == 0 {
		h.badRequest(c, "file is required")
		return
	}
	out := dto.SampleUploaded{}
	for _, fh := range form.File["file"] {
		f, err := fh.Open()
		if err != nil {
			h.badRequest(c, err.Error())
			return
		}
		smp, err := h.DocTypes.Upload(ctx, owner, c.Param("code"), fh.Filename, f, owner)
		f.Close()
		if err != nil {
			h.docTypeError(c, err)
			return
		}
		out.Data = append(out.Data, smp)
	}
	c.JSON(consts.StatusAccepted, out)
}

func (h *Handlers) sampleParam(c *app.RequestContext) (uuid.UUID, bool) {
	return h.uuidParam(c, "id")
}

// GetSample handles GET /v1/doc-types/{code}/samples/{id}.
//
// @Summary   A sample: per page the content (Nội dung) and the lines (Các dòng); the runs of every version; the reviewed values
// @Tags      DocTypes
// @Produce   json
// @Param     code  path      string  true  "Type code"
// @Param     id    path      string  true  "Sample id"  format(uuid)
// @Success   200   {object}  doctypes.SampleDetail
// @Failure   404   {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/doc-types/{code}/samples/{id} [get]
func (h *Handlers) GetSample(ctx context.Context, c *app.RequestContext) {
	owner, ok := h.docTypeReq(c, false)
	if !ok {
		return
	}
	id, ok := h.sampleParam(c)
	if !ok {
		return
	}
	d, err := h.DocTypes.Sample(ctx, owner, c.Param("code"), id)
	if err != nil {
		h.docTypeError(c, err)
		return
	}
	c.JSON(consts.StatusOK, d)
}

// DeleteSample handles DELETE /v1/doc-types/{code}/samples/{id}.
//
// @Summary   Delete a sample, its files and runs
// @Tags      DocTypes
// @Param     code  path  string  true  "Type code"
// @Param     id    path  string  true  "Sample id"  format(uuid)
// @Success   204
// @Security  ApiKeyAuth
// @Router    /v1/doc-types/{code}/samples/{id} [delete]
func (h *Handlers) DeleteSample(ctx context.Context, c *app.RequestContext) {
	owner, ok := h.docTypeReq(c, true)
	if !ok {
		return
	}
	id, ok := h.sampleParam(c)
	if !ok {
		return
	}
	if err := h.DocTypes.DeleteSample(ctx, owner, c.Param("code"), id); err != nil {
		h.docTypeError(c, err)
		return
	}
	c.Status(consts.StatusNoContent)
}

// SamplePageImage handles GET /v1/doc-types/{code}/samples/{id}/pages/{n}/image.
//
// @Summary   The image of a page of a sample (JPEG)
// @Tags      DocTypes
// @Produce   jpeg
// @Param     code  path  string  true  "Type code"
// @Param     id    path  string  true  "Sample id"  format(uuid)
// @Param     n     path  int     true  "Page number"
// @Success   200
// @Failure   404  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/doc-types/{code}/samples/{id}/pages/{n}/image [get]
func (h *Handlers) SamplePageImage(ctx context.Context, c *app.RequestContext) {
	owner, ok := h.docTypeReq(c, false)
	if !ok {
		return
	}
	id, ok := h.sampleParam(c)
	if !ok {
		return
	}
	n, err := strconv.Atoi(c.Param("n"))
	if err != nil {
		h.badRequest(c, "invalid page number")
		return
	}
	rc, err := h.DocTypes.PageImage(ctx, owner, c.Param("code"), id, n)
	if err != nil {
		h.docTypeError(c, err)
		return
	}
	c.SetContentType("image/jpeg")
	c.Response.Header.Set("Cache-Control", "private, max-age=3600")
	c.SetBodyStream(rc, -1)
}

// SetSampleTruth handles PUT /v1/doc-types/{code}/samples/{id}/truth.
//
// @Summary   Save the right values of a sample (reviewed): one per field, "" when the document has none
// @Tags      DocTypes
// @Accept    json
// @Produce   json
// @Param     code     path      string            true  "Type code"
// @Param     id       path      string            true  "Sample id"  format(uuid)
// @Param     request  body      dto.TruthRequest  true  "Values"
// @Success   200      {object}  types.DocTypeSample
// @Security  ApiKeyAuth
// @Router    /v1/doc-types/{code}/samples/{id}/truth [put]
func (h *Handlers) SetSampleTruth(ctx context.Context, c *app.RequestContext) {
	owner, ok := h.docTypeReq(c, true)
	if !ok {
		return
	}
	id, ok := h.sampleParam(c)
	if !ok {
		return
	}
	var req dto.TruthRequest
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	smp, err := h.DocTypes.SetTruth(ctx, owner, c.Param("code"), id, req.Values, owner)
	if err != nil {
		h.docTypeError(c, err)
		return
	}
	c.JSON(consts.StatusOK, smp)
}

// RunSamples handles POST /v1/doc-types/{code}/samples/run.
//
// @Summary      Test a version of the instructions on samples
// @Description  One extraction turn of the agent per sample, as when a sheet is built; a sample used as an example is read without it. Runs in the request (not queued) and returns the finished job: status done, or failed with the reason in error. Each sample is bounded by doc_types.run_timeout. Billed as kind parse to the caller.
// @Tags         DocTypes
// @Accept       json
// @Produce      json
// @Param        code     path      string                 true  "Type code"
// @Param        request  body      dto.RunSamplesRequest  true  "Samples and version"
// @Success      200      {object}  types.DocTypeJob
// @Security     ApiKeyAuth
// @Router       /v1/doc-types/{code}/samples/run [post]
func (h *Handlers) RunSamples(ctx context.Context, c *app.RequestContext) {
	owner, ok := h.docTypeReq(c, true)
	if !ok {
		return
	}
	u, _ := h.user(c)
	if !h.withinBudget(ctx, c, u) {
		return
	}
	var req dto.RunSamplesRequest
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	j, err := h.DocTypes.Run(ctx, owner, c.Param("code"), req.SampleIDs, req.Version, owner)
	if err != nil {
		h.docTypeError(c, err)
		return
	}
	c.JSON(consts.StatusOK, j)
}

// ListPrompts handles GET /v1/doc-types/{code}/prompts.
//
// @Summary   The versions of the AI instructions of a type, newest first, with their accuracy
// @Tags      DocTypes
// @Produce   json
// @Param     code  path      string  true  "Type code"
// @Success   200   {object}  dto.PromptList
// @Security  ApiKeyAuth
// @Router    /v1/doc-types/{code}/prompts [get]
func (h *Handlers) ListPrompts(ctx context.Context, c *app.RequestContext) {
	owner, ok := h.docTypeReq(c, false)
	if !ok {
		return
	}
	list, err := h.DocTypes.Prompts(ctx, owner, c.Param("code"))
	if err != nil {
		h.docTypeError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.PromptList{Data: list})
}

// CreatePrompt handles POST /v1/doc-types/{code}/prompts.
//
// @Summary      Save a new version of the instructions (number = largest + 1)
// @Description  Fields left out are those of the version in use; examples must be reviewed samples. use=true makes it the version in use. Requires the prompt_editor or admin role.
// @Tags         DocTypes
// @Accept       json
// @Produce      json
// @Param        code     path      string                true  "Type code"
// @Param        request  body      doctypes.PromptInput  true  "Version"
// @Success      201      {object}  types.DocTypePrompt
// @Failure      422      {object}  dto.ErrorResponse
// @Security     ApiKeyAuth
// @Router       /v1/doc-types/{code}/prompts [post]
func (h *Handlers) CreatePrompt(ctx context.Context, c *app.RequestContext) {
	owner, ok := h.docTypeReq(c, true)
	if !ok {
		return
	}
	var req doctypes.PromptInput
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	p, err := h.DocTypes.AddPrompt(ctx, owner, c.Param("code"), req, owner)
	if err != nil {
		h.docTypeError(c, err)
		return
	}
	c.JSON(consts.StatusCreated, p)
}

// SetCurrentPrompt handles PUT /v1/doc-types/{code}/prompts/current.
//
// @Summary   Use another version of the instructions, from the next extraction on
// @Tags      DocTypes
// @Accept    json
// @Param     code     path  string              true  "Type code"
// @Param     request  body  dto.VersionRequest  true  "Version"
// @Success   204
// @Security  ApiKeyAuth
// @Router    /v1/doc-types/{code}/prompts/current [put]
func (h *Handlers) SetCurrentPrompt(ctx context.Context, c *app.RequestContext) {
	owner, ok := h.docTypeReq(c, true)
	if !ok {
		return
	}
	var req dto.VersionRequest
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	if err := h.DocTypes.SetCurrent(ctx, owner, c.Param("code"), req.Version); err != nil {
		h.docTypeError(c, err)
		return
	}
	c.Status(consts.StatusNoContent)
}

// DeletePrompt handles DELETE /v1/doc-types/{code}/prompts/{version}.
//
// @Summary   Delete a version of the instructions; the version in use answers 409
// @Tags      DocTypes
// @Param     code     path  string  true  "Type code"
// @Param     version  path  int     true  "Version"
// @Success   204
// @Failure   409  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/doc-types/{code}/prompts/{version} [delete]
func (h *Handlers) DeletePrompt(ctx context.Context, c *app.RequestContext) {
	owner, ok := h.docTypeReq(c, true)
	if !ok {
		return
	}
	v, ok := h.versionParam(c)
	if !ok {
		return
	}
	if err := h.DocTypes.DeletePrompt(ctx, owner, c.Param("code"), v); err != nil {
		h.docTypeError(c, err)
		return
	}
	c.Status(consts.StatusNoContent)
}

// PromptText handles GET /v1/doc-types/{code}/prompts/{version}/text.
//
// @Summary   The whole text a version sends the model, with the place of the document marked
// @Tags      DocTypes
// @Produce   json
// @Param     code     path      string  true  "Type code"
// @Param     version  path      int     true  "Version"
// @Success   200      {object}  dto.PromptText
// @Security  ApiKeyAuth
// @Router    /v1/doc-types/{code}/prompts/{version}/text [get]
func (h *Handlers) PromptText(ctx context.Context, c *app.RequestContext) {
	owner, ok := h.docTypeReq(c, false)
	if !ok {
		return
	}
	v, ok := h.versionParam(c)
	if !ok {
		return
	}
	text, err := h.DocTypes.PromptText(ctx, owner, c.Param("code"), v)
	if err != nil {
		h.docTypeError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.PromptText{Text: text})
}

// OptimizeDocType handles POST /v1/doc-types/{code}/optimize.
//
// @Summary      Ask the AI to improve a version (U98), in the background (task doctype:job)
// @Description  Needs a reviewed sample. The job's result is the proposal: rules per field, examples, line diff of the texts, accuracy before/after per field and the cells that got worse; or {none: true}. Apply it with POST …/prompts (use=true). Billed as kind parse to the caller.
// @Tags         DocTypes
// @Accept       json
// @Produce      json
// @Param        code     path      string              true  "Type code"
// @Param        request  body      dto.VersionRequest  true  "Version to improve (0 = in use)"
// @Success      202      {object}  types.DocTypeJob
// @Failure      422      {object}  dto.ErrorResponse
// @Security     ApiKeyAuth
// @Router       /v1/doc-types/{code}/optimize [post]
func (h *Handlers) OptimizeDocType(ctx context.Context, c *app.RequestContext) {
	owner, ok := h.docTypeReq(c, true)
	if !ok {
		return
	}
	u, _ := h.user(c)
	if !h.withinBudget(ctx, c, u) {
		return
	}
	var req dto.VersionRequest
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	j, err := h.DocTypes.Optimize(ctx, owner, c.Param("code"), req.Version, owner)
	if err != nil {
		h.docTypeError(c, err)
		return
	}
	c.JSON(consts.StatusAccepted, j)
}

// ListDocTypeJobs handles GET /v1/doc-types/{code}/jobs.
//
// @Summary   The running test runs and optimizations of a type, and the latest finished one of each kind
// @Tags      DocTypes
// @Produce   json
// @Param     code  path      string  true  "Type code"
// @Success   200   {object}  dto.JobList
// @Security  ApiKeyAuth
// @Router    /v1/doc-types/{code}/jobs [get]
func (h *Handlers) ListDocTypeJobs(ctx context.Context, c *app.RequestContext) {
	owner, ok := h.docTypeReq(c, false)
	if !ok {
		return
	}
	list, err := h.DocTypes.Jobs(ctx, owner, c.Param("code"))
	if err != nil {
		h.docTypeError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.JobList{Data: list})
}

// GetDocTypeJob handles GET /v1/doc-types/{code}/jobs/{id}.
//
// @Summary   A test run or optimization: status and, for an optimization, its proposal
// @Tags      DocTypes
// @Produce   json
// @Param     code  path      string  true  "Type code"
// @Param     id    path      string  true  "Job id"  format(uuid)
// @Success   200   {object}  types.DocTypeJob
// @Security  ApiKeyAuth
// @Router    /v1/doc-types/{code}/jobs/{id} [get]
func (h *Handlers) GetDocTypeJob(ctx context.Context, c *app.RequestContext) {
	owner, ok := h.docTypeReq(c, false)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	j, err := h.DocTypes.Job(ctx, owner, c.Param("code"), id)
	if err != nil {
		h.docTypeError(c, err)
		return
	}
	c.JSON(consts.StatusOK, j)
}

// DropDocTypeJob handles DELETE /v1/doc-types/{code}/jobs/{id}.
//
// @Summary   Drop the proposal of an optimization
// @Tags      DocTypes
// @Param     code  path  string  true  "Type code"
// @Param     id    path  string  true  "Job id"  format(uuid)
// @Success   204
// @Security  ApiKeyAuth
// @Router    /v1/doc-types/{code}/jobs/{id} [delete]
func (h *Handlers) DropDocTypeJob(ctx context.Context, c *app.RequestContext) {
	owner, ok := h.docTypeReq(c, true)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	if err := h.DocTypes.DropProposal(ctx, owner, c.Param("code"), id); err != nil {
		h.docTypeError(c, err)
		return
	}
	c.Status(consts.StatusNoContent)
}
