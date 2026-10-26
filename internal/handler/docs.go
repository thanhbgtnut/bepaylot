package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/thanhenti/bepaylot/docs"
)

// OpenAPISpec serves the generated OpenAPI/Swagger document (YAML) for API
// clients and codegen. Regenerate with `make swag`.
func (h *Handlers) OpenAPISpec(_ context.Context, c *app.RequestContext) {
	c.Data(consts.StatusOK, "application/yaml; charset=utf-8", docs.SwaggerYAML)
}

// contextRoot is the path prefix of every route (http.context_root), "" at
// the domain root.
func (h *Handlers) contextRoot() string {
	if h.Config == nil {
		return ""
	}
	return h.Config.HTTP.ContextRoot
}

// DocsRedirect sends /docs to the Swagger UI served under /swagger.
func (h *Handlers) DocsRedirect(_ context.Context, c *app.RequestContext) {
	c.Redirect(consts.StatusFound, []byte(h.contextRoot()+"/swagger/index.html"))
}
