package handler

import (
	"context"
	"encoding/json"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/thanhenti/bepaylot/internal/handler/dto"
	"github.com/thanhenti/bepaylot/internal/types"
)

// Search handles POST /v1/search.
//
// @Summary   Search documents (reasoning over the document tree, keyword, or metadata only)
// @Description mode=reasoning (default): an LLM picks documents, walks their table of contents and cites exact lines; mode=keyword: Postgres full-text + trigram; mode=metadata: list documents matching the metadata filter. No embeddings are used.
// @Tags      Search
// @Accept    json
// @Produce   json
// @Param     request  body      types.SearchRequest  true  "Query, scope and filters"
// @Success   200      {object}  types.SearchResponse
// @Failure   422      {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/search [post]
func (h *Handlers) Search(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	var req types.SearchRequest
	if err := json.Unmarshal(c.Request.Body(), &req); err != nil {
		h.badRequest(c, "invalid JSON: "+err.Error())
		return
	}
	req.OwnerID = u.ID
	resp, err := h.Searcher.Search(ctx, req)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, resp)
}

// SearchInDocument handles POST /v1/documents/{id}/search.
//
// @Summary   Find a term in one document, grouped by page
// @Tags      Search
// @Accept    json
// @Produce   json
// @Param     id       path      string                     true  "Document id"  format(uuid)
// @Param     request  body      dto.DocumentSearchRequest  true  "Query"
// @Success   200      {object}  dto.DocumentSearchResponse
// @Failure   422      {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/documents/{id}/search [post]
func (h *Handlers) SearchInDocument(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var req dto.DocumentSearchRequest
	if err := json.Unmarshal(c.Request.Body(), &req); err != nil {
		h.badRequest(c, "invalid JSON: "+err.Error())
		return
	}
	pages, err := h.Searcher.FindInDocument(ctx, u.ID, id, req.Query, req.Mode)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	if pages == nil {
		pages = []types.PageSearchHit{}
	}
	c.JSON(consts.StatusOK, dto.DocumentSearchResponse{Pages: pages})
}

// DocumentTree handles GET /v1/documents/{id}/tree.
//
// @Summary   The document's table of contents (vectorless tree) with summaries
// @Tags      Search
// @Produce   json
// @Param     id   path      string  true  "Document id"  format(uuid)
// @Success   200  {object}  dto.TreeResponse
// @Failure   404  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/documents/{id}/tree [get]
func (h *Handlers) DocumentTree(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	nodes, err := h.Searcher.DocumentTree(ctx, u.ID, id)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	if nodes == nil {
		nodes = []types.TreeNode{}
	}
	c.JSON(consts.StatusOK, dto.TreeResponse{Nodes: nodes})
}

// LocateCitation handles GET /v1/citations.
//
// @Summary   Resolve a citation id (doc:<id>:p<page>:l<a>-<b>) to text and boxes
// @Tags      Search
// @Produce   json
// @Param     id   query     string  true  "Citation id"
// @Success   200  {object}  dto.LocationsResponse
// @Failure   404  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/citations [get]
func (h *Handlers) LocateCitation(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	hits, err := h.Searcher.Locate(ctx, u.ID, string(c.Query("id")))
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.LocationsResponse{Hits: hits})
}
