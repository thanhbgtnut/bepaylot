package handler

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/handler/dto"
	"github.com/thanhenti/bepaylot/internal/types"
)

// SearchEntities handles GET /v1/kbs/{id}/graph/entities.
//
// @Summary   Find graph entities by name/alias/attribute (accent-insensitive)
// @Tags      Graph
// @Produce   json
// @Param     id     path      string  true   "Knowledge base id"  format(uuid)
// @Param     q      query     string  false  "Search text"
// @Param     type   query     string  false  "Entity type"
// @Param     limit  query     int     false  "Max results"  default(20)
// @Success   200    {object}  dto.EntityList
// @Security  ApiKeyAuth
// @Router    /v1/kbs/{id}/graph/entities [get]
func (h *Handlers) SearchEntities(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	ents, err := h.Graph.SearchEntities(ctx, u.ID, id, string(c.Query("q")), string(c.Query("type")), intQuery(c, "limit", 20))
	if err != nil {
		h.serviceError(c, err)
		return
	}
	if ents == nil {
		ents = []types.Entity{}
	}
	c.JSON(consts.StatusOK, dto.EntityList{Data: ents})
}

// GetEntity handles GET /v1/graph/entities/{id}.
//
// @Summary   An entity with relations and evidence (with page positions)
// @Tags      Graph
// @Produce   json
// @Param     id   path      string  true  "Entity id"  format(uuid)
// @Success   200  {object}  graph.EntityDetail
// @Failure   404  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/graph/entities/{id} [get]
func (h *Handlers) GetEntity(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	e, err := h.Graph.Entity(ctx, u.ID, id)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, e)
}

// EntityNeighbors handles GET /v1/graph/entities/{id}/neighbors.
//
// @Summary   Entities within N hops (≤3)
// @Tags      Graph
// @Produce   json
// @Param     id              path      string  true   "Entity id"  format(uuid)
// @Param     depth           query     int     false  "Hops (1-3)"  default(1)
// @Param     relation_types  query     string  false  "Comma-separated relation types"
// @Success   200             {object}  types.Subgraph
// @Failure   404             {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/graph/entities/{id}/neighbors [get]
func (h *Handlers) EntityNeighbors(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var rels []string
	if s := string(c.Query("relation_types")); s != "" {
		rels = strings.Split(s, ",")
	}
	g, err := h.Graph.Neighbors(ctx, u.ID, id, rels, intQuery(c, "depth", 1))
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, g)
}

// GraphPath handles GET /v1/kbs/{id}/graph/path.
//
// @Summary   Shortest relation path between two entities (≤4 hops)
// @Tags      Graph
// @Produce   json
// @Param     id         path      string  true   "Knowledge base id"  format(uuid)
// @Param     from       query     string  true   "Entity id"  format(uuid)
// @Param     to         query     string  true   "Entity id"  format(uuid)
// @Param     max_depth  query     int     false  "Max hops"  default(4)
// @Success   200        {object}  types.Subgraph
// @Failure   404        {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/kbs/{id}/graph/path [get]
func (h *Handlers) GraphPath(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	from, err1 := uuid.Parse(string(c.Query("from")))
	to, err2 := uuid.Parse(string(c.Query("to")))
	if err1 != nil || err2 != nil {
		h.badRequest(c, "from and to must be entity ids")
		return
	}
	g, err := h.Graph.Path(ctx, u.ID, from, to, intQuery(c, "max_depth", 4))
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, g)
}

// RebuildGraph handles POST /v1/kbs/{id}/graph/rebuild.
//
// @Summary   Re-extract the graph of every document (after a schema change)
// @Tags      Graph
// @Produce   json
// @Param     id   path      string  true  "Knowledge base id"  format(uuid)
// @Success   202  {object}  dto.RebuildResponse
// @Security  ApiKeyAuth
// @Router    /v1/kbs/{id}/graph/rebuild [post]
func (h *Handlers) RebuildGraph(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	n, err := h.Graph.Rebuild(ctx, u.ID, id)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusAccepted, dto.RebuildResponse{Queued: n})
}

// ListWikiPages handles GET /v1/kbs/{id}/wiki/pages.
//
// @Summary   List wiki pages
// @Tags      Wiki
// @Produce   json
// @Param     id   path      string  true  "Knowledge base id"  format(uuid)
// @Success   200  {object}  dto.WikiPageList
// @Security  ApiKeyAuth
// @Router    /v1/kbs/{id}/wiki/pages [get]
func (h *Handlers) ListWikiPages(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	pages, err := h.Wiki.Pages(ctx, u.ID, id)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	if pages == nil {
		pages = []types.WikiPage{}
	}
	c.JSON(consts.StatusOK, dto.WikiPageList{Data: pages})
}

// GetWikiPage handles GET /v1/kbs/{id}/wiki/pages/{slug}.
//
// @Summary   Read a wiki page
// @Tags      Wiki
// @Produce   json
// @Param     id    path      string  true  "Knowledge base id"  format(uuid)
// @Param     slug  path      string  true  "Page slug"
// @Success   200   {object}  types.WikiPage
// @Failure   404   {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/kbs/{id}/wiki/pages/{slug} [get]
func (h *Handlers) GetWikiPage(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	p, err := h.Wiki.Page(ctx, u.ID, id, c.Param("slug"))
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, p)
}

// EditWikiPage handles PUT /v1/kbs/{id}/wiki/pages/{slug}.
//
// @Summary   Edit a wiki page by hand (later generations will not overwrite it)
// @Tags      Wiki
// @Accept    json
// @Produce   json
// @Param     id       path      string               true  "Knowledge base id"  format(uuid)
// @Param     slug     path      string               true  "Page slug"
// @Param     request  body      dto.WikiEditRequest  true  "New content"
// @Success   200      {object}  types.WikiPage
// @Failure   404      {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/kbs/{id}/wiki/pages/{slug} [put]
func (h *Handlers) EditWikiPage(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	var req dto.WikiEditRequest
	if err := json.Unmarshal(c.Request.Body(), &req); err != nil || strings.TrimSpace(req.Content) == "" {
		h.badRequest(c, "content is required")
		return
	}
	p, err := h.Wiki.Edit(ctx, u.ID, id, c.Param("slug"), req.Title, req.Summary, req.Content)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, p)
}

// WikiRevisions handles GET /v1/kbs/{id}/wiki/pages/{slug}/revisions.
//
// @Summary   A wiki page's revision history
// @Tags      Wiki
// @Produce   json
// @Param     id    path      string  true  "Knowledge base id"  format(uuid)
// @Param     slug  path      string  true  "Page slug"
// @Success   200   {array}   postgres.WikiRevision
// @Failure   404   {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/kbs/{id}/wiki/pages/{slug}/revisions [get]
func (h *Handlers) WikiRevisions(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := h.uuidParam(c, "id")
	if !ok {
		return
	}
	revs, err := h.Wiki.Revisions(ctx, u.ID, id, c.Param("slug"))
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, revs)
}

// ListSchemas handles GET /v1/graph/schemas.
//
// @Summary   List graph schemas (latest version of each)
// @Tags      Graph schemas
// @Produce   json
// @Success   200  {object}  dto.SchemaList
// @Security  ApiKeyAuth
// @Router    /v1/graph/schemas [get]
func (h *Handlers) ListSchemas(ctx context.Context, c *app.RequestContext) {
	ss, err := h.Graph.Schemas(ctx, "")
	if err != nil {
		h.serviceError(c, err)
		return
	}
	if ss == nil {
		ss = []types.GraphSchema{}
	}
	c.JSON(consts.StatusOK, dto.SchemaList{Data: ss})
}

// CreateSchema handles POST /v1/graph/schemas.
//
// @Summary   Create a graph schema version (validated)
// @Tags      Graph schemas
// @Accept    json
// @Produce   json
// @Param     request  body      types.GraphSchema  true  "Schema (version 0 = next version)"
// @Success   201      {object}  types.GraphSchema
// @Failure   422      {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/graph/schemas [post]
func (h *Handlers) CreateSchema(ctx context.Context, c *app.RequestContext) {
	var sc types.GraphSchema
	if err := json.Unmarshal(c.Request.Body(), &sc); err != nil {
		h.badRequest(c, "invalid JSON: "+err.Error())
		return
	}
	out, err := h.Graph.CreateSchema(ctx, sc)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusCreated, out)
}

// GetSchemaVersions handles GET /v1/graph/schemas/{name}.
//
// @Summary   Every version of a graph schema
// @Tags      Graph schemas
// @Produce   json
// @Param     name  path      string  true  "Schema name"
// @Success   200   {object}  dto.SchemaList
// @Security  ApiKeyAuth
// @Router    /v1/graph/schemas/{name} [get]
func (h *Handlers) GetSchemaVersions(ctx context.Context, c *app.RequestContext) {
	ss, err := h.Graph.Schemas(ctx, c.Param("name"))
	if err != nil {
		h.serviceError(c, err)
		return
	}
	if len(ss) == 0 {
		h.notFound(c, "schema not found")
		return
	}
	c.JSON(consts.StatusOK, dto.SchemaList{Data: ss})
}

// TestSchema handles POST /v1/graph/schemas/{name}/test.
//
// @Summary   Dry-run extraction of a text with a schema (nothing stored)
// @Tags      Graph schemas
// @Accept    json
// @Produce   json
// @Param     name     path      string                 true  "Schema name"
// @Param     request  body      dto.SchemaTestRequest  true  "Text"
// @Success   200      {object}  graph.TestResult
// @Failure   422      {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/graph/schemas/{name}/test [post]
func (h *Handlers) TestSchema(ctx context.Context, c *app.RequestContext) {
	var req dto.SchemaTestRequest
	if err := json.Unmarshal(c.Request.Body(), &req); err != nil || strings.TrimSpace(req.Text) == "" {
		h.badRequest(c, "text is required")
		return
	}
	res, err := h.Graph.TestSchema(ctx, c.Param("name"), req.Text)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, res)
}
