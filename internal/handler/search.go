package handler

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/thanhenti/bepaylot/internal/handler/dto"
	"github.com/thanhenti/bepaylot/internal/types"
)

// Search handles POST /v1/search.
//
// @Summary   Search cases (reasoning over tables of contents, keyword, or metadata only)
// @Description Scope: case_ids, or kb_ids (every case of those KBs; API only). mode=reasoning (default): an LLM reads the case table of contents, walks the file trees, only the pages of the chosen nodes are read and exact source lines are cited, every hit re-checked against the current lines; mode=keyword: Postgres full-text + trigram; mode=metadata: list documents matching the metadata filter. No embeddings are used.
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
	pages, err := h.Searcher.FindInDocument(ctx, u.ID, id, req.Query, req.Mode, req.PageFrom, req.PageTo)
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
// @Description format=text returns the tree as the LLM reads it (§6.5): whole when it fits search.tree_token_budget, deep levels collapsed otherwise; node_id limits it to one subtree.
// @Tags      Search
// @Produce   json
// @Param     id       path      string  true   "Document id"  format(uuid)
// @Param     format   query     string  false  "json (default) or text"
// @Param     node_id  query     string  false  "Only this subtree (format=text), e.g. n3"
// @Success   200      {object}  dto.TreeResponse
// @Failure   404      {object}  dto.ErrorResponse
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
	if string(c.Query("format")) == "text" {
		txt, err := h.Searcher.DocumentTreeText(ctx, u.ID, id, string(c.Query("node_id")))
		if err != nil {
			h.serviceError(c, err)
			return
		}
		c.JSON(consts.StatusOK, dto.TreeResponse{Nodes: []types.TreeNode{}, Text: txt})
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

// CaseTOC handles GET /v1/cases/{id}/toc.
//
// @Summary   The case table of contents: file cards and first branches of their trees
// @Description Built from stored cards and trees, without the LLM (§6.6 step 2). text is the rendering given to the LLM (and to kb_case_toc); documents holds the same data as JSON for the UI. metadata narrows the files; expand (comma-separated d<n>) shows those files with their whole tree.
// @Tags      Search
// @Produce   json
// @Param     id        path      string  true   "Case id"  format(uuid)
// @Param     metadata  query     string  false  "Metadata filter (JSON object)"
// @Param     expand    query     string  false  "File refs to show whole, e.g. d1,d3"
// @Success   200       {object}  types.CaseTOC
// @Failure   404       {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/cases/{id}/toc [get]
func (h *Handlers) CaseTOC(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var filter types.MetadataFilter
	if m := string(c.Query("metadata")); m != "" {
		if err := json.Unmarshal([]byte(m), &filter); err != nil {
			h.badRequest(c, "metadata must be a JSON object")
			return
		}
	}
	var expand []string
	if e := strings.TrimSpace(string(c.Query("expand"))); e != "" {
		expand = strings.Split(e, ",")
	}
	toc, err := h.Searcher.CaseTOC(ctx, u.ID, id, filter, expand)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, toc)
}

// LocateCitation handles GET /v1/citations.
//
// @Summary   Resolve a citation id (doc:<id>:p<page>:l<a>-<b>) to text and boxes
// @Tags      Search
// @Produce   json
// @Param     id   query     string  true  "Citation id: doc:<id>:p<page>, optionally :l<a>-<b> or :l<a>"
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
