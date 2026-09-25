package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"
	hsse "github.com/hertz-contrib/sse"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/application/service/document"
	"github.com/thanhenti/bepaylot/internal/application/service/graph"
	"github.com/thanhenti/bepaylot/internal/application/service/index"
	"github.com/thanhenti/bepaylot/internal/application/service/wiki"
	"github.com/thanhenti/bepaylot/internal/handler/dto"
	"github.com/thanhenti/bepaylot/internal/middleware"
	"github.com/thanhenti/bepaylot/internal/types"
)

// serviceError maps module errors to HTTP statuses.
func (h *Handlers) serviceError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, document.ErrNotFound), errors.Is(err, index.ErrNotFound), errors.Is(err, graph.ErrNotFound),
		errors.Is(err, wiki.ErrNotFound), errors.Is(err, postgres.ErrNotFound):
		h.notFound(c, "not found")
	case errors.Is(err, document.ErrBadRequest), errors.Is(err, index.ErrBadRequest), errors.Is(err, graph.ErrBadRequest):
		c.JSON(consts.StatusUnprocessableEntity, dto.NewError("invalid_request_error", err.Error()))
	case errors.Is(err, context.DeadlineExceeded):
		c.JSON(consts.StatusGatewayTimeout, dto.NewError("timeout_error", err.Error()))
	default:
		h.serverError(c, err)
	}
}

func (h *Handlers) uuidParam(c *app.RequestContext, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		h.badRequest(c, "invalid "+name)
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handlers) user(c *app.RequestContext) (types.User, bool) {
	u, ok := middleware.UserFrom(c)
	if !ok {
		c.JSON(consts.StatusUnauthorized, dto.NewError("authentication_error", "unauthenticated"))
	}
	return u, ok
}

func intQuery(c *app.RequestContext, key string, def int) int {
	if v, err := strconv.Atoi(string(c.Query(key))); err == nil {
		return v
	}
	return def
}

// ---- knowledge bases ----

// CreateKB handles POST /v1/kbs.
//
// @Summary   Create a knowledge base
// @Tags      Knowledge bases
// @Accept    json
// @Produce   json
// @Param     request  body      dto.CreateKBRequest  true  "Knowledge base"
// @Success   201      {object}  types.KnowledgeBase
// @Failure   422      {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/kbs [post]
func (h *Handlers) CreateKB(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	var req dto.CreateKBRequest
	if err := json.Unmarshal(c.Request.Body(), &req); err != nil {
		h.badRequest(c, "invalid JSON: "+err.Error())
		return
	}
	kb, err := h.Docs.CreateKB(ctx, u.ID, req.Name, req.Description, req.Config, req.MetadataSchema, false)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusCreated, kb)
}

// ListKBs handles GET /v1/kbs.
//
// @Summary   List knowledge bases
// @Tags      Knowledge bases
// @Produce   json
// @Success   200  {object}  dto.KBList
// @Security  ApiKeyAuth
// @Router    /v1/kbs [get]
func (h *Handlers) ListKBs(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	kbs, err := h.Docs.ListKBs(ctx, u.ID)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	if kbs == nil {
		kbs = []types.KnowledgeBase{}
	}
	c.JSON(consts.StatusOK, dto.KBList{Data: kbs})
}

// GetKB handles GET /v1/kbs/{id}.
//
// @Summary   Get a knowledge base
// @Tags      Knowledge bases
// @Produce   json
// @Param     id   path      string  true  "Knowledge base id"  format(uuid)
// @Success   200  {object}  types.KnowledgeBase
// @Failure   404  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/kbs/{id} [get]
func (h *Handlers) GetKB(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	kb, err := h.Docs.GetKBOwned(ctx, u.ID, id)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, kb)
}

// UpdateKB handles PATCH /v1/kbs/{id}.
//
// @Summary   Update a knowledge base
// @Tags      Knowledge bases
// @Accept    json
// @Produce   json
// @Param     id       path      string               true  "Knowledge base id"  format(uuid)
// @Param     request  body      dto.UpdateKBRequest  true  "Fields to change"
// @Success   200      {object}  types.KnowledgeBase
// @Failure   404      {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/kbs/{id} [patch]
func (h *Handlers) UpdateKB(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var req dto.UpdateKBRequest
	if err := json.Unmarshal(c.Request.Body(), &req); err != nil {
		h.badRequest(c, "invalid JSON: "+err.Error())
		return
	}
	kb, err := h.Docs.UpdateKB(ctx, u.ID, id, postgres.KBPatch{Name: req.Name, Description: req.Description, Config: req.Config})
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, kb)
}

// DeleteKB handles DELETE /v1/kbs/{id}.
//
// @Summary   Delete a knowledge base and its documents
// @Tags      Knowledge bases
// @Produce   json
// @Param     id   path      string  true  "Knowledge base id"  format(uuid)
// @Success   200  {object}  dto.OK
// @Failure   404  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/kbs/{id} [delete]
func (h *Handlers) DeleteKB(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	if err := h.Docs.DeleteKB(ctx, u.ID, id); err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.OK{OK: true})
}

// GetMetadataSchema handles GET /v1/kbs/{id}/metadata-schema.
//
// @Summary   Get a knowledge base's metadata schema
// @Tags      Metadata
// @Produce   json
// @Param     id   path      string  true  "Knowledge base id"  format(uuid)
// @Success   200  {object}  dto.MetadataSchemaBody
// @Security  ApiKeyAuth
// @Router    /v1/kbs/{id}/metadata-schema [get]
func (h *Handlers) GetMetadataSchema(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	kb, err := h.Docs.GetKBOwned(ctx, u.ID, id)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.MetadataSchemaBody{Schema: kb.MetadataSchema})
}

// PutMetadataSchema handles PUT /v1/kbs/{id}/metadata-schema.
//
// @Summary   Replace a knowledge base's metadata schema
// @Tags      Metadata
// @Accept    json
// @Produce   json
// @Param     id       path      string                  true  "Knowledge base id"  format(uuid)
// @Param     request  body      dto.MetadataSchemaBody  true  "Schema (null clears it)"
// @Success   200      {object}  types.KnowledgeBase
// @Failure   422      {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/kbs/{id}/metadata-schema [put]
func (h *Handlers) PutMetadataSchema(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var req dto.MetadataSchemaBody
	if err := json.Unmarshal(c.Request.Body(), &req); err != nil {
		h.badRequest(c, "invalid JSON: "+err.Error())
		return
	}
	kb, err := h.Docs.UpdateKB(ctx, u.ID, id, postgres.KBPatch{MetadataSchema: &req.Schema})
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, kb)
}

// MetadataValues handles GET /v1/kbs/{id}/metadata/values.
//
// @Summary   Distinct values of a metadata key (e.g. every case code)
// @Tags      Metadata
// @Produce   json
// @Param     id      path      string  true   "Knowledge base id"  format(uuid)
// @Param     key     query     string  true   "Metadata key, e.g. ma_ho_so"
// @Param     prefix  query     string  false  "Only values starting with this (accent-insensitive)"
// @Param     limit   query     int     false  "Max values"  default(100)
// @Success   200     {object}  dto.MetadataValuesResponse
// @Security  ApiKeyAuth
// @Router    /v1/kbs/{id}/metadata/values [get]
func (h *Handlers) MetadataValues(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	key := string(c.Query("key"))
	vals, err := h.Docs.MetadataValues(ctx, u.ID, id, key, string(c.Query("prefix")), intQuery(c, "limit", 100))
	if err != nil {
		if strings.Contains(err.Error(), "invalid metadata key") {
			h.badRequest(c, err.Error())
			return
		}
		h.serviceError(c, err)
		return
	}
	out := dto.MetadataValuesResponse{Key: key, Values: []dto.MetadataValue{}}
	for _, v := range vals {
		out.Values = append(out.Values, dto.MetadataValue{Value: v.Value, Count: v.Count})
	}
	c.JSON(consts.StatusOK, out)
}

// ---- documents ----

// UploadDocuments handles POST /v1/kbs/{id}/documents.
//
// Multipart form: one or more "file" parts, optional "metadata" (JSON object
// applied to every file) and "files_metadata" (JSON object keyed by file name
// or 0-based index). Files stream to S3 as they arrive.
//
// @Summary   Upload one or more files (streamed) with optional metadata
// @Tags      Documents
// @Accept    mpfd
// @Produce   json
// @Param     id              path      string  true   "Knowledge base id"  format(uuid)
// @Param     file            formData  file    true   "File (repeat for several files)"
// @Param     metadata        formData  string  false  "JSON metadata for every file, e.g. {\"ma_ho_so\":\"HS-2026-000123\"}"
// @Param     files_metadata  formData  string  false  "JSON per-file metadata keyed by file name or index"
// @Param     interactive     query     bool    false  "Use the high-priority lanes (chat attachments)"
// @Success   202             {object}  document.UploadResult
// @Failure   400             {object}  dto.ErrorResponse
// @Failure   404             {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/kbs/{id}/documents [post]
func (h *Handlers) UploadDocuments(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	res, err := h.upload(ctx, c, u.ID, id, string(c.Query("interactive")) == "true" || string(c.Query("interactive")) == "1")
	if err != nil {
		if errors.Is(err, errMultipart) {
			h.badRequest(c, err.Error())
			return
		}
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusAccepted, res)
}

var errMultipart = errors.New("request must be multipart/form-data with at least one file part")

// upload streams the multipart body part by part into the document service.
func (h *Handlers) upload(ctx context.Context, c *app.RequestContext, owner, kb uuid.UUID, interactive bool) (*document.UploadResult, error) {
	_, params, err := mime.ParseMediaType(string(c.ContentType()))
	if err != nil || params["boundary"] == "" {
		return nil, errMultipart
	}
	up, err := h.Docs.BeginUpload(ctx, owner, kb, interactive)
	if err != nil {
		return nil, err
	}
	// With StreamBody the part bytes flow straight from the socket to S3;
	// small requests that Hertz already buffered are read from memory.
	var body io.Reader
	if c.Request.IsBodyStream() {
		body = c.Request.BodyStream()
	} else {
		body = bytes.NewReader(c.Request.Body())
	}
	mr := multipart.NewReader(body, params["boundary"])
	files := 0
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errMultipart, err)
		}
		switch {
		case part.FileName() != "":
			files++
			if err := up.AddFile(ctx, part.FileName(), part.Header.Get("Content-Type"), part); err != nil {
				return nil, err
			}
		case part.FormName() == "metadata":
			var m map[string]any
			if err := json.NewDecoder(io.LimitReader(part, 1<<20)).Decode(&m); err != nil {
				return nil, fmt.Errorf("%w: metadata must be a JSON object", document.ErrBadRequest)
			}
			up.SetSharedMetadata(m)
		case part.FormName() == "files_metadata":
			var m map[string]map[string]any
			if err := json.NewDecoder(io.LimitReader(part, 4<<20)).Decode(&m); err != nil {
				return nil, fmt.Errorf("%w: files_metadata must be a JSON object of objects", document.ErrBadRequest)
			}
			up.SetFilesMetadata(m)
		}
		part.Close()
	}
	if files == 0 {
		return nil, errMultipart
	}
	return up.Finish(ctx)
}

// ListDocuments handles GET /v1/kbs/{id}/documents.
//
// @Summary   List documents, filtered by status, batch, metadata or text
// @Tags      Documents
// @Produce   json
// @Param     id        path      string  true   "Knowledge base id"  format(uuid)
// @Param     status    query     string  false  "Comma-separated statuses"
// @Param     batch_id  query     string  false  "Upload batch id"
// @Param     metadata  query     string  false  "JSON metadata filter, e.g. {\"ma_ho_so\":\"HS-2026-000123\"}"
// @Param     q         query     string  false  "Full-text over file name, card and metadata values"
// @Param     limit     query     int     false  "Page size (max 500)"  default(100)
// @Param     before    query     string  false  "Cursor: created_at of the last item (RFC3339)"
// @Success   200       {object}  dto.DocumentList
// @Failure   422       {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/kbs/{id}/documents [get]
func (h *Handlers) ListDocuments(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	f := postgres.DocumentFilter{Query: string(c.Query("q")), Limit: intQuery(c, "limit", 100)}
	if s := string(c.Query("status")); s != "" {
		f.Statuses = strings.Split(s, ",")
	}
	if b := string(c.Query("batch_id")); b != "" {
		bid, err := uuid.Parse(b)
		if err != nil {
			h.badRequest(c, "invalid batch_id")
			return
		}
		f.BatchID = &bid
	}
	if m := string(c.Query("metadata")); m != "" {
		if err := json.Unmarshal([]byte(m), &f.Metadata); err != nil {
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
		f.Before = &t
	}
	docs, err := h.Docs.ListDocuments(ctx, u.ID, id, f)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	out := dto.DocumentList{Data: docs}
	if out.Data == nil {
		out.Data = []types.Document{}
	}
	if len(docs) > 0 && len(docs) >= max(1, min(f.Limit, 500)) {
		out.NextCursor = docs[len(docs)-1].CreatedAt.Format(time.RFC3339Nano)
	}
	c.JSON(consts.StatusOK, out)
}

// BulkUpdateMetadata handles POST /v1/kbs/{id}/documents/metadata/bulk-update.
//
// @Summary   Set or remove metadata keys on every matching document
// @Tags      Metadata
// @Accept    json
// @Produce   json
// @Param     id       path      string                   true  "Knowledge base id"  format(uuid)
// @Param     request  body      dto.BulkMetadataRequest  true  "Filter and changes"
// @Success   200      {object}  dto.BulkMetadataResponse
// @Failure   422      {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/kbs/{id}/documents/metadata/bulk-update [post]
func (h *Handlers) BulkUpdateMetadata(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var req dto.BulkMetadataRequest
	if err := json.Unmarshal(c.Request.Body(), &req); err != nil {
		h.badRequest(c, "invalid JSON: "+err.Error())
		return
	}
	f := postgres.DocumentFilter{Metadata: req.Filter.Metadata}
	for _, s := range req.Filter.DocumentIDs {
		did, err := uuid.Parse(s)
		if err != nil {
			h.badRequest(c, "invalid document id "+s)
			return
		}
		f.DocumentIDs = append(f.DocumentIDs, did)
	}
	if req.Filter.BatchID != "" {
		bid, err := uuid.Parse(req.Filter.BatchID)
		if err != nil {
			h.badRequest(c, "invalid batch_id")
			return
		}
		f.BatchID = &bid
	}
	n, err := h.Docs.BulkMetadata(ctx, u.ID, id, f, req.Set, req.Unset)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.BulkMetadataResponse{Updated: n})
}

// GetDocument handles GET /v1/documents/{id}.
//
// @Summary   Get a document with progress, failed pages and (optionally) spans
// @Tags      Documents
// @Produce   json
// @Param     id     path      string  true   "Document id"  format(uuid)
// @Param     spans  query     bool    false  "Include processing spans"
// @Success   200    {object}  document.DocumentDetail
// @Failure   404    {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/documents/{id} [get]
func (h *Handlers) GetDocument(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	d, err := h.Docs.Detail(ctx, u.ID, id, string(c.Query("spans")) == "true")
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, d)
}

// DocumentEvents handles GET /v1/documents/{id}/events (SSE).
//
// @Summary   Stream processing progress (Server-Sent Events)
// @Description Emits a "status" event whenever status, stage or page counters change, and ends when the document reaches a terminal state.
// @Tags      Documents
// @Produce   text/event-stream
// @Param     id   path  string  true  "Document id"  format(uuid)
// @Success   200  {string}  string  "event stream"
// @Failure   404  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/documents/{id}/events [get]
func (h *Handlers) DocumentEvents(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	if _, err := h.Docs.GetOwned(ctx, u.ID, id); err != nil {
		h.serviceError(c, err)
		return
	}
	stream := hsse.NewStream(c)
	last := ""
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	ping := time.Now()
	for {
		d, err := h.Docs.GetOwned(ctx, u.ID, id)
		if err != nil {
			_ = stream.Publish(&hsse.Event{Event: "gone", Data: []byte(`{}`)})
			return
		}
		ev := map[string]any{"status": d.Status, "parse_status": d.ParseStatus, "index_status": d.IndexStatus, "graph_status": d.GraphStatus,
			"page_count": d.PageCount, "pages_done": d.PagesDone, "pages_failed": d.PagesFailed, "progress": d.Progress(), "error": d.Error}
		b, _ := json.Marshal(ev)
		if string(b) != last {
			last = string(b)
			if err := stream.Publish(&hsse.Event{Event: "status", Data: b}); err != nil {
				return
			}
			ping = time.Now()
		} else if time.Since(ping) > 15*time.Second {
			if err := stream.Publish(&hsse.Event{Event: "ping", Data: []byte(`{}`)}); err != nil {
				return
			}
			ping = time.Now()
		}
		if types.Terminal(d.Status) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// CancelDocument handles POST /v1/documents/{id}/cancel.
//
// @Summary   Cancel processing (finished pages are kept)
// @Tags      Documents
// @Produce   json
// @Param     id   path      string  true  "Document id"  format(uuid)
// @Success   200  {object}  dto.OK
// @Failure   422  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/documents/{id}/cancel [post]
func (h *Handlers) CancelDocument(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	if err := h.Docs.Cancel(ctx, u.ID, id); err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.OK{OK: true})
}

// ReparseDocument handles POST /v1/documents/{id}/reparse.
//
// @Summary   Reparse the whole document (new generation) or selected pages
// @Tags      Documents
// @Accept    json
// @Produce   json
// @Param     id       path      string                   true   "Document id"  format(uuid)
// @Param     request  body      document.ReparseRequest  false  "pages and/or engine"
// @Success   202      {object}  types.Document
// @Failure   422      {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/documents/{id}/reparse [post]
func (h *Handlers) ReparseDocument(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var req document.ReparseRequest
	if len(c.Request.Body()) > 0 {
		if err := json.Unmarshal(c.Request.Body(), &req); err != nil {
			h.badRequest(c, "invalid JSON: "+err.Error())
			return
		}
	}
	d, err := h.Docs.Reparse(ctx, u.ID, id, req)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusAccepted, d)
}

// DeleteDocument handles DELETE /v1/documents/{id}.
//
// @Summary   Delete a document (async purge of rows and objects)
// @Tags      Documents
// @Produce   json
// @Param     id   path      string  true  "Document id"  format(uuid)
// @Success   200  {object}  dto.OK
// @Failure   404  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/documents/{id} [delete]
func (h *Handlers) DeleteDocument(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	if err := h.Docs.Delete(ctx, u.ID, id); err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.OK{OK: true})
}

// UpdateDocumentMetadata handles PATCH /v1/documents/{id}/metadata.
//
// @Summary   Merge or replace a document's metadata
// @Tags      Metadata
// @Accept    json
// @Produce   json
// @Param     id       path      string                     true  "Document id"  format(uuid)
// @Param     request  body      dto.MetadataUpdateRequest  true  "Metadata"
// @Success   200      {object}  types.Document
// @Failure   422      {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/documents/{id}/metadata [patch]
func (h *Handlers) UpdateDocumentMetadata(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var req dto.MetadataUpdateRequest
	if err := json.Unmarshal(c.Request.Body(), &req); err != nil {
		h.badRequest(c, "invalid JSON: "+err.Error())
		return
	}
	d, err := h.Docs.UpdateMetadata(ctx, u.ID, id, req.Metadata, req.Mode == "replace")
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, d)
}

// DocumentFile handles GET /v1/documents/{id}/file.
//
// @Summary   Download the original file
// @Tags      Documents
// @Produce   octet-stream
// @Param     id   path  string  true  "Document id"  format(uuid)
// @Success   200  {file}  file
// @Failure   404  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/documents/{id}/file [get]
func (h *Handlers) DocumentFile(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	d, rc, size, err := h.Docs.SourceFile(ctx, u.ID, id)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.Response.Header.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": d.FileName}))
	c.SetContentType(d.MimeType)
	c.SetBodyStream(rc, int(size))
}

// DocumentMarkdown handles GET /v1/documents/{id}/markdown.
//
// @Summary   Markdown of the document (optionally a page range)
// @Tags      Documents
// @Produce   json
// @Param     id     path      string  true   "Document id"  format(uuid)
// @Param     pages  query     string  false  "Page range, e.g. 1-5"
// @Success   200    {object}  dto.MarkdownResponse
// @Security  ApiKeyAuth
// @Router    /v1/documents/{id}/markdown [get]
func (h *Handlers) DocumentMarkdown(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	from, to := pageRange(string(c.Query("pages")))
	md, err := h.Docs.Markdown(ctx, u.ID, id, from, to)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.MarkdownResponse{Markdown: md})
}

func pageRange(s string) (int, int) {
	if s == "" {
		return 0, 0
	}
	a, b, found := strings.Cut(s, "-")
	from, _ := strconv.Atoi(strings.TrimSpace(a))
	to := from
	if found {
		to, _ = strconv.Atoi(strings.TrimSpace(b))
	}
	return from, to
}

// ListPages handles GET /v1/documents/{id}/pages.
//
// @Summary   List page rows (status, size, text source)
// @Tags      Pages
// @Produce   json
// @Param     id    path      string  true   "Document id"  format(uuid)
// @Param     from  query     int     false  "First page"
// @Param     to    query     int     false  "Last page"
// @Success   200   {object}  dto.PagesResponse
// @Security  ApiKeyAuth
// @Router    /v1/documents/{id}/pages [get]
func (h *Handlers) ListPages(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	pages, err := h.Docs.Pages(ctx, u.ID, id, intQuery(c, "from", 0), intQuery(c, "to", 0))
	if err != nil {
		h.serviceError(c, err)
		return
	}
	if pages == nil {
		pages = []types.DocumentPage{}
	}
	c.JSON(consts.StatusOK, dto.PagesResponse{Data: pages})
}

// GetPage handles GET /v1/documents/{id}/pages/{n}.
//
// @Summary   One page with markdown, blocks and lines (with boxes)
// @Tags      Pages
// @Produce   json
// @Param     id   path      string  true  "Document id"  format(uuid)
// @Param     n    path      int     true  "Page number (1-based)"
// @Success   200  {object}  document.PageView
// @Failure   404  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/documents/{id}/pages/{n} [get]
func (h *Handlers) GetPage(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	n, err := strconv.Atoi(c.Param("n"))
	if err != nil {
		h.badRequest(c, "invalid page number")
		return
	}
	p, err := h.Docs.Page(ctx, u.ID, id, n)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, p)
}

// PageImage handles GET /v1/documents/{id}/pages/{n}/image.
//
// @Summary   The rendered page image (redirect to a presigned URL, or streamed)
// @Tags      Pages
// @Produce   image/jpeg
// @Param     id   path  string  true  "Document id"  format(uuid)
// @Param     n    path  int     true  "Page number (1-based)"
// @Success   200  {file}  file
// @Success   302  {string}  string  "Redirect to the presigned S3 URL"
// @Failure   404  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/documents/{id}/pages/{n}/image [get]
func (h *Handlers) PageImage(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	n, err := strconv.Atoi(c.Param("n"))
	if err != nil {
		h.badRequest(c, "invalid page number")
		return
	}
	url, rc, size, err := h.Docs.PageImage(ctx, u.ID, id, n)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	if url != "" {
		c.Redirect(consts.StatusFound, []byte(url))
		return
	}
	c.SetContentType("image/jpeg")
	c.SetBodyStream(rc, int(size))
}

// LocateInDocument handles POST /v1/documents/{id}/locate.
//
// @Summary   Resolve a line, a markdown range or a text to page positions
// @Tags      Pages
// @Accept    json
// @Produce   json
// @Param     id       path      string                  true  "Document id"  format(uuid)
// @Param     request  body      document.LocateRequest  true  "line+page | md_start+md_end | text"
// @Success   200      {array}   document.Location
// @Failure   422      {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/documents/{id}/locate [post]
func (h *Handlers) LocateInDocument(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var req document.LocateRequest
	if err := json.Unmarshal(c.Request.Body(), &req); err != nil {
		h.badRequest(c, "invalid JSON: "+err.Error())
		return
	}
	locs, err := h.Docs.Locate(ctx, u.ID, id, req)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	if locs == nil {
		locs = []document.Location{}
	}
	c.JSON(consts.StatusOK, locs)
}

// ListEngines handles GET /v1/parser/engines.
//
// @Summary   Registered OCR engines and their health
// @Tags      Pages
// @Produce   json
// @Success   200  {object}  dto.EngineList
// @Security  ApiKeyAuth
// @Router    /v1/parser/engines [get]
func (h *Handlers) ListEngines(ctx context.Context, c *app.RequestContext) {
	out := dto.EngineList{Data: []dto.EngineInfo{}}
	if h.Engines != nil {
		for _, e := range h.Engines.List(ctx) {
			out.Data = append(out.Data, dto.EngineInfo{Name: e.Name, Default: e.Default, Available: e.Available, Error: e.Error})
		}
	}
	c.JSON(consts.StatusOK, out)
}
