// Package handler implements the HTTP handlers: an Anthropic-compatible
// /v1/messages endpoint (streaming and buffered) plus session and skill
// management endpoints.
package handler

import (
	"log/slog"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/thanhenti/bepaylot/internal/agent"
	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/application/service/document"
	"github.com/thanhenti/bepaylot/internal/application/service/graph"
	"github.com/thanhenti/bepaylot/internal/application/service/wiki"
	"github.com/thanhenti/bepaylot/internal/config"
	"github.com/thanhenti/bepaylot/internal/handler/dto"
	"github.com/thanhenti/bepaylot/internal/llm"
	"github.com/thanhenti/bepaylot/internal/mcp"
	"github.com/thanhenti/bepaylot/internal/parser"
	"github.com/thanhenti/bepaylot/internal/queue"
	"github.com/thanhenti/bepaylot/internal/skills"
	"github.com/thanhenti/bepaylot/internal/types/interfaces"
)

// Handlers bundles the dependencies for every HTTP handler.
type Handlers struct {
	Store    *postgres.Store
	Agent    *agent.Agent
	Registry *llm.Registry
	Skills   *skills.Service
	MCP      *mcp.Manager // nil when MCP is disabled
	LLM      config.LLM
	Agentcfg config.AgentCfg
	Log      *slog.Logger

	// Document modules (§10). Nil fields make their routes answer 503.
	Config    *config.Config
	Docs      *document.Service
	Searcher  interfaces.Searcher
	Graph     *graph.Service
	Wiki      *wiki.Service
	Engines   *parser.Registry
	Queue     queue.Enqueuer
	Inspector QueueInspector
}

func (h *Handlers) badRequest(c *app.RequestContext, msg string) {
	c.JSON(consts.StatusBadRequest, dto.NewError("invalid_request_error", msg))
}

func (h *Handlers) notFound(c *app.RequestContext, msg string) {
	c.JSON(consts.StatusNotFound, dto.NewError("not_found_error", msg))
}

func (h *Handlers) serverError(c *app.RequestContext, err error) {
	h.Log.Error("handler error", "err", err, "path", string(c.Path()))
	c.JSON(consts.StatusInternalServerError, dto.NewError("api_error", err.Error()))
}
