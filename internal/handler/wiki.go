package handler

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"
	hsse "github.com/hertz-contrib/sse"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/application/service/wiki"
	"github.com/thanhenti/bepaylot/internal/handler/dto"
	"github.com/thanhenti/bepaylot/internal/types"
)

// caseParam parses the case id and the caller.
func (h *Handlers) caseParam(c *app.RequestContext) (types.User, uuid.UUID, bool) {
	u, ok := h.user(c)
	if !ok {
		return u, uuid.Nil, false
	}
	id, ok := h.uuidParam(c, "id")
	return u, id, ok
}

// GetWiki handles GET /v1/cases/{id}/wiki.
//
// @Summary   Case wiki navigation: pages by kind, files not in the wiki yet, wiki state
// @Tags      Wiki
// @Produce   json
// @Param     id   path      string  true  "Case id"  format(uuid)
// @Success   200  {object}  types.WikiTOC
// @Failure   404  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/cases/{id}/wiki [get]
func (h *Handlers) GetWiki(ctx context.Context, c *app.RequestContext) {
	u, id, ok := h.caseParam(c)
	if !ok {
		return
	}
	toc, err := h.Wiki.TOC(ctx, u.ID, id)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, toc)
}

// GetWikiIndex handles GET /v1/cases/{id}/wiki/index.
//
// @Summary   The wiki index exactly as given to the LLM (index.md of LLM Wiki)
// @Tags      Wiki
// @Produce   json
// @Param     id      path      string  true   "Case id"  format(uuid)
// @Param     expand  query     string  false  "Index id to expand, e.g. w4 or w4.n2"
// @Success   200     {object}  dto.WikiIndexResponse
// @Failure   404     {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/cases/{id}/wiki/index [get]
func (h *Handlers) GetWikiIndex(ctx context.Context, c *app.RequestContext) {
	u, id, ok := h.caseParam(c)
	if !ok {
		return
	}
	txt, err := h.Wiki.IndexText(ctx, u.ID, id, string(c.Query("expand")))
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.WikiIndexResponse{Content: txt})
}

// splitPagePath separates a slug (which contains "/") from a trailing action.
func splitPagePath(p string) (slug, action string, version int) {
	p = strings.Trim(p, "/")
	if i := strings.LastIndex(p, "/revisions/"); i > 0 && strings.HasSuffix(p, "/restore") {
		v, err := strconv.Atoi(strings.TrimSuffix(p[i+len("/revisions/"):], "/restore"))
		if err == nil {
			return p[:i], "restore", v
		}
	}
	for _, a := range []string{"revisions", "proposal"} {
		if strings.HasSuffix(p, "/"+a) {
			return strings.TrimSuffix(p, "/"+a), a, 0
		}
	}
	return p, "", 0
}

// GetWikiPage handles GET /v1/cases/{id}/wiki/pages/{slug}.
//
// @Summary   Read a wiki page, or its history with …/{slug}/revisions
// @Description Returns content, attributes, footnotes (n → citation_id, quote, file, page, status), links in and out and the ingest proposal if any. The slug contains "/", e.g. nguon/hop-dong-15-2026. A slug of another case is 404.
// @Tags      Wiki
// @Produce   json
// @Param     id    path      string  true  "Case id"  format(uuid)
// @Param     slug  path      string  true  "Page slug (optionally followed by /revisions)"
// @Success   200   {object}  types.WikiPage
// @Failure   404   {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/cases/{id}/wiki/pages/{slug} [get]
func (h *Handlers) GetWikiPage(ctx context.Context, c *app.RequestContext) {
	u, id, ok := h.caseParam(c)
	if !ok {
		return
	}
	slug, action, _ := splitPagePath(c.Param("slug"))
	switch action {
	case "":
		p, err := h.Wiki.GetPage(ctx, u.ID, id, slug)
		if err != nil {
			h.serviceError(c, err)
			return
		}
		c.JSON(consts.StatusOK, p)
	case "revisions":
		revs, err := h.Wiki.Revisions(ctx, u.ID, id, slug)
		if err != nil {
			h.serviceError(c, err)
			return
		}
		c.JSON(consts.StatusOK, dto.WikiRevisionList{Data: revs})
	default:
		h.notFound(c, "not found")
	}
}

// EditWikiPage handles PUT /v1/cases/{id}/wiki/pages/{slug}.
//
// @Summary   Edit a wiki page by hand (title, content, attributes, footnotes)
// @Description footnotes maps a footnote number to a citation id of this case (doc:<id>:p<page>:l<a>-<b>); each is checked against the source lines. Invalid footnotes are marked stale (or refused with 422 when wiki.strict_citations). An edited page is not overwritten by ingest: later ingests leave a proposal.
// @Tags      Wiki
// @Accept    json
// @Produce   json
// @Param     id       path      string            true  "Case id"  format(uuid)
// @Param     slug     path      string            true  "Page slug"
// @Param     request  body      wiki.EditRequest  true  "Changes"
// @Success   200      {object}  wiki.EditResult
// @Failure   422      {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/cases/{id}/wiki/pages/{slug} [put]
func (h *Handlers) EditWikiPage(ctx context.Context, c *app.RequestContext) {
	u, id, ok := h.caseParam(c)
	if !ok {
		return
	}
	slug, action, _ := splitPagePath(c.Param("slug"))
	if action != "" {
		h.notFound(c, "not found")
		return
	}
	var req wiki.EditRequest
	if err := json.Unmarshal(c.Request.Body(), &req); err != nil {
		h.badRequest(c, "invalid JSON: "+err.Error())
		return
	}
	res, err := h.Wiki.EditPage(ctx, u.ID, id, slug, req)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, res)
}

// PostWikiPage handles POST /v1/cases/{id}/wiki/pages/{slug}/proposal and
// POST /v1/cases/{id}/wiki/pages/{slug}/revisions/{v}/restore.
//
// @Summary   Accept/reject an ingest proposal (…/{slug}/proposal) or restore a revision (…/{slug}/revisions/{v}/restore)
// @Tags      Wiki
// @Accept    json
// @Produce   json
// @Param     id       path      string                    true   "Case id"  format(uuid)
// @Param     slug     path      string                    true   "Page slug followed by /proposal or /revisions/{v}/restore"
// @Param     request  body      dto.WikiProposalRequest   false  "For /proposal: {\"action\": \"accept\" | \"reject\"}"
// @Success   200      {object}  types.WikiPage
// @Failure   422      {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/cases/{id}/wiki/pages/{slug} [post]
func (h *Handlers) PostWikiPage(ctx context.Context, c *app.RequestContext) {
	u, id, ok := h.caseParam(c)
	if !ok {
		return
	}
	slug, action, version := splitPagePath(c.Param("slug"))
	var (
		p   *types.WikiPage
		err error
	)
	switch action {
	case "proposal":
		var req dto.WikiProposalRequest
		if err := json.Unmarshal(c.Request.Body(), &req); err != nil {
			h.badRequest(c, "invalid JSON: "+err.Error())
			return
		}
		if req.Action != "accept" && req.Action != "reject" {
			h.badRequest(c, "action must be accept or reject")
			return
		}
		p, err = h.Wiki.Proposal(ctx, u.ID, id, slug, req.Action == "accept")
	case "restore":
		p, err = h.Wiki.Restore(ctx, u.ID, id, slug, version)
	default:
		h.notFound(c, "not found")
		return
	}
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, p)
}

// DeleteWikiPage handles DELETE /v1/cases/{id}/wiki/pages/{slug}.
//
// @Summary   Delete a note page (other pages are maintained by ingest)
// @Tags      Wiki
// @Produce   json
// @Param     id    path      string  true  "Case id"  format(uuid)
// @Param     slug  path      string  true  "Note slug"
// @Success   200   {object}  dto.OK
// @Failure   422   {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/cases/{id}/wiki/pages/{slug} [delete]
func (h *Handlers) DeleteWikiPage(ctx context.Context, c *app.RequestContext) {
	u, id, ok := h.caseParam(c)
	if !ok {
		return
	}
	slug, action, _ := splitPagePath(c.Param("slug"))
	if action != "" {
		h.notFound(c, "not found")
		return
	}
	if err := h.Wiki.DeleteNote(ctx, u.ID, id, slug); err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.OK{OK: true})
}

// WikiLinks handles GET /v1/cases/{id}/wiki/links.
//
// @Summary   The link graph of the wiki (nodes = pages, edges = links)
// @Tags      Wiki
// @Produce   json
// @Param     id           path      string  true   "Case id"  format(uuid)
// @Param     kind         query     string  false  "overview | source | entity | topic | note"
// @Param     entity_type  query     string  false  "Entity type of the schema"
// @Param     relation     query     string  false  "Only this relation"
// @Success   200          {object}  wiki.LinkGraph
// @Failure   404          {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/cases/{id}/wiki/links [get]
func (h *Handlers) WikiLinks(ctx context.Context, c *app.RequestContext) {
	u, id, ok := h.caseParam(c)
	if !ok {
		return
	}
	g, err := h.Wiki.Graph(ctx, u.ID, id, string(c.Query("kind")), string(c.Query("entity_type")), string(c.Query("relation")))
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, g)
}

// SearchWiki handles GET /v1/cases/{id}/wiki/search.
//
// @Summary   Full-text search in the wiki of a case
// @Tags      Wiki
// @Produce   json
// @Param     id   path      string  true  "Case id"  format(uuid)
// @Param     q    query     string  true  "Query"
// @Success   200  {object}  dto.WikiSearchResponse
// @Security  ApiKeyAuth
// @Router    /v1/cases/{id}/wiki/search [get]
func (h *Handlers) SearchWiki(ctx context.Context, c *app.RequestContext) {
	u, id, ok := h.caseParam(c)
	if !ok {
		return
	}
	hits, err := h.Wiki.Search(ctx, u.ID, id, string(c.Query("q")))
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.WikiSearchResponse{Data: hits})
}

// CreateWikiNote handles POST /v1/cases/{id}/wiki/notes.
//
// @Summary   Save an answer as a note page ("Lưu vào wiki")
// @Description Body {title, content} or {session_id, message_id} (an assistant answer of a session bound to this case). Inline citation ids become footnotes after being checked; sentences without a valid citation are dropped.
// @Tags      Wiki
// @Accept    json
// @Produce   json
// @Param     id       path      string            true  "Case id"  format(uuid)
// @Param     request  body      wiki.NoteRequest  true  "Note"
// @Success   201      {object}  types.WikiPage
// @Failure   422      {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/cases/{id}/wiki/notes [post]
func (h *Handlers) CreateWikiNote(ctx context.Context, c *app.RequestContext) {
	u, id, ok := h.caseParam(c)
	if !ok {
		return
	}
	var req wiki.NoteRequest
	if err := json.Unmarshal(c.Request.Body(), &req); err != nil {
		h.badRequest(c, "invalid JSON: "+err.Error())
		return
	}
	p, err := h.Wiki.CreateNote(ctx, u.ID, id, req)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusCreated, p)
}

// WikiLog handles GET /v1/cases/{id}/wiki/log.
//
// @Summary   The wiki log (log.md of LLM Wiki), newest first
// @Tags      Wiki
// @Produce   json
// @Param     id           path      string  true   "Case id"  format(uuid)
// @Param     op           query     string  false  "ingest | retract | lint | edit | note | rebuild | query"
// @Param     document_id  query     string  false  "Only lines about this file"  format(uuid)
// @Param     before       query     string  false  "Cursor: at of the last line (RFC3339)"
// @Param     limit        query     int     false  "Page size (max 500)"  default(100)
// @Success   200          {object}  dto.WikiLogList
// @Security  ApiKeyAuth
// @Router    /v1/cases/{id}/wiki/log [get]
func (h *Handlers) WikiLog(ctx context.Context, c *app.RequestContext) {
	u, id, ok := h.caseParam(c)
	if !ok {
		return
	}
	f := postgres.LogFilter{Op: string(c.Query("op")), Limit: intQuery(c, "limit", 100)}
	if raw := string(c.Query("document_id")); raw != "" {
		d, err := uuid.Parse(raw)
		if err != nil {
			h.badRequest(c, "invalid document_id")
			return
		}
		f.DocumentID = &d
	}
	if raw := string(c.Query("before")); raw != "" {
		t, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			h.badRequest(c, "before must be RFC3339")
			return
		}
		f.Before = &t
	}
	logs, err := h.Wiki.Log(ctx, u.ID, id, f)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.WikiLogList{Data: logs})
}

// ListWikiLint handles GET /v1/cases/{id}/wiki/lint.
//
// @Summary   Lint issues of the wiki
// @Tags      Wiki
// @Produce   json
// @Param     id      path      string  true   "Case id"  format(uuid)
// @Param     status  query     string  false  "open | fixed | dismissed"
// @Param     kind    query     string  false  "stale | contradiction | orphan | missing_link | gap | index_drift"
// @Success   200     {object}  dto.WikiLintList
// @Security  ApiKeyAuth
// @Router    /v1/cases/{id}/wiki/lint [get]
func (h *Handlers) ListWikiLint(ctx context.Context, c *app.RequestContext) {
	u, id, ok := h.caseParam(c)
	if !ok {
		return
	}
	issues, err := h.Wiki.Issues(ctx, u.ID, id, string(c.Query("status")), string(c.Query("kind")))
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.WikiLintList{Data: issues})
}

// RunWikiLint handles POST /v1/cases/{id}/wiki/lint.
//
// @Summary   Run the wiki lint now (async)
// @Tags      Wiki
// @Produce   json
// @Param     id   path      string  true  "Case id"  format(uuid)
// @Success   202  {object}  dto.OK
// @Security  ApiKeyAuth
// @Router    /v1/cases/{id}/wiki/lint [post]
func (h *Handlers) RunWikiLint(ctx context.Context, c *app.RequestContext) {
	u, id, ok := h.caseParam(c)
	if !ok {
		return
	}
	if err := h.Wiki.RunLint(ctx, u.ID, id); err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusAccepted, dto.OK{OK: true})
}

// PatchWikiLint handles PATCH /v1/cases/{id}/wiki/lint/{issue_id}.
//
// @Summary   Mark a lint issue fixed or dismissed
// @Tags      Wiki
// @Accept    json
// @Produce   json
// @Param     id        path      string             true  "Case id"  format(uuid)
// @Param     issue_id  path      int                true  "Issue id"
// @Param     request   body      dto.WikiLintPatch  true  "New status"
// @Success   200       {object}  dto.OK
// @Failure   404       {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/cases/{id}/wiki/lint/{issue_id} [patch]
func (h *Handlers) PatchWikiLint(ctx context.Context, c *app.RequestContext) {
	u, id, ok := h.caseParam(c)
	if !ok {
		return
	}
	issue, err := strconv.ParseInt(c.Param("issue_id"), 10, 64)
	if err != nil {
		h.badRequest(c, "invalid issue_id")
		return
	}
	var req dto.WikiLintPatch
	if err := json.Unmarshal(c.Request.Body(), &req); err != nil {
		h.badRequest(c, "invalid JSON: "+err.Error())
		return
	}
	if err := h.Wiki.SetIssue(ctx, u.ID, id, issue, req.Status); err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.OK{OK: true})
}

// RebuildWiki handles POST /v1/cases/{id}/wiki/rebuild.
//
// @Summary   Rebuild the wiki: drop generated pages and ingest every file again
// @Description Use after a schema change. Note pages and hand-edited pages are kept; edited pages receive proposals.
// @Tags      Wiki
// @Produce   json
// @Param     id   path      string  true  "Case id"  format(uuid)
// @Success   202  {object}  dto.OK
// @Security  ApiKeyAuth
// @Router    /v1/cases/{id}/wiki/rebuild [post]
func (h *Handlers) RebuildWiki(ctx context.Context, c *app.RequestContext) {
	u, id, ok := h.caseParam(c)
	if !ok {
		return
	}
	if err := h.Wiki.Rebuild(ctx, u.ID, id); err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusAccepted, dto.OK{OK: true})
}

// ExportWiki handles GET /v1/cases/{id}/wiki/export.
//
// @Summary   Export the wiki: zip of markdown pages or one static HTML file
// @Tags      Wiki
// @Produce   application/zip
// @Produce   text/html
// @Param     id      path      string  true   "Case id"  format(uuid)
// @Param     format  query     string  false  "md (default) | html"
// @Success   200     {file}    file
// @Failure   404     {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/cases/{id}/wiki/export [get]
func (h *Handlers) ExportWiki(ctx context.Context, c *app.RequestContext) {
	u, id, ok := h.caseParam(c)
	if !ok {
		return
	}
	name, ctype, data, err := h.Wiki.Export(ctx, u.ID, id, string(c.Query("format")))
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
	c.Data(consts.StatusOK, ctype, data)
}

// WikiEvents handles GET /v1/cases/{id}/wiki/events (SSE).
//
// @Summary   Stream wiki events (ingest_started, page_updated, ingest_done, lint_done)
// @Tags      Wiki
// @Produce   text/event-stream
// @Param     id   path  string  true  "Case id"  format(uuid)
// @Success   200  {string}  string  "event stream"
// @Failure   404  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/cases/{id}/wiki/events [get]
func (h *Handlers) WikiEvents(ctx context.Context, c *app.RequestContext) {
	u, id, ok := h.caseParam(c)
	if !ok {
		return
	}
	_, after, status, err := h.Wiki.EventsSince(ctx, u.ID, id, -1, "")
	if err != nil {
		h.serviceError(c, err)
		return
	}
	stream := hsse.NewStream(c)
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	ping := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		var evs []wiki.Event
		evs, after, status, err = h.Wiki.EventsSince(ctx, u.ID, id, after, status)
		if err != nil {
			_ = stream.Publish(&hsse.Event{Event: "gone", Data: []byte(`{}`)})
			return
		}
		for _, e := range evs {
			b, _ := json.Marshal(e.Data)
			if err := stream.Publish(&hsse.Event{ID: strconv.FormatInt(e.ID, 10), Event: e.Event, Data: b}); err != nil {
				return
			}
			ping = time.Now()
		}
		if time.Since(ping) > 15*time.Second {
			if err := stream.Publish(&hsse.Event{Event: "ping", Data: []byte(`{}`)}); err != nil {
				return
			}
			ping = time.Now()
		}
	}
}

// ListWikiSchemas handles GET /v1/wiki/schemas.
//
// @Summary   List wiki schemas (latest version of each)
// @Tags      Wiki schemas
// @Produce   json
// @Success   200  {object}  dto.SchemaList
// @Security  ApiKeyAuth
// @Router    /v1/wiki/schemas [get]
func (h *Handlers) ListWikiSchemas(ctx context.Context, c *app.RequestContext) {
	if _, ok := h.user(c); !ok {
		return
	}
	out, err := h.Wiki.Schemas(ctx, "")
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.SchemaList{Data: out})
}

// CreateWikiSchema handles POST /v1/wiki/schemas.
//
// @Summary   Create a new wiki schema version (validated)
// @Description Existing cases keep their pages until POST /v1/cases/{id}/wiki/rebuild.
// @Tags      Wiki schemas
// @Accept    json
// @Produce   json
// @Param     request  body      types.WikiSchema  true  "Schema"
// @Success   201      {object}  types.WikiSchema
// @Failure   422      {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/wiki/schemas [post]
func (h *Handlers) CreateWikiSchema(ctx context.Context, c *app.RequestContext) {
	if _, ok := h.user(c); !ok {
		return
	}
	var sc types.WikiSchema
	if err := json.Unmarshal(c.Request.Body(), &sc); err != nil {
		h.badRequest(c, "invalid JSON: "+err.Error())
		return
	}
	out, err := h.Wiki.CreateSchema(ctx, sc)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusCreated, out)
}

// GetWikiSchema handles GET /v1/wiki/schemas/{name}.
//
// @Summary   Every version of a wiki schema
// @Tags      Wiki schemas
// @Produce   json
// @Param     name  path      string  true  "Schema name"
// @Success   200   {object}  dto.SchemaList
// @Failure   404   {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/wiki/schemas/{name} [get]
func (h *Handlers) GetWikiSchema(ctx context.Context, c *app.RequestContext) {
	if _, ok := h.user(c); !ok {
		return
	}
	out, err := h.Wiki.Schemas(ctx, c.Param("name"))
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.SchemaList{Data: out})
}

// TestWikiSchema handles POST /v1/wiki/schemas/{name}/test.
//
// @Summary   Dry-run the extraction of a schema on a text or a document
// @Description Returns the entities (with identity keys and the lines of each attribute) and relations an ingest would keep after checking them against the source lines, and the LLM calls used. Nothing is stored.
// @Tags      Wiki schemas
// @Accept    json
// @Produce   json
// @Param     name     path      string                 true  "Schema name"
// @Param     request  body      dto.SchemaTestRequest  true  "Text or document id"
// @Success   200      {object}  wiki.TestResult
// @Failure   422      {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/wiki/schemas/{name}/test [post]
func (h *Handlers) TestWikiSchema(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	var req dto.SchemaTestRequest
	if err := json.Unmarshal(c.Request.Body(), &req); err != nil {
		h.badRequest(c, "invalid JSON: "+err.Error())
		return
	}
	var doc *uuid.UUID
	if req.DocumentID != "" {
		d, err := uuid.Parse(req.DocumentID)
		if err != nil {
			h.badRequest(c, "invalid document_id")
			return
		}
		doc = &d
	}
	out, err := h.Wiki.TestSchema(ctx, u.ID, c.Param("name"), req.Text, doc)
	if err != nil {
		h.serviceError(c, err)
		return
	}
	c.JSON(consts.StatusOK, out)
}
