// Package router wires the Hertz HTTP server: middleware stack and routes.
package router

import (
	"context"
	"log/slog"
	"runtime"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/config"
	hertzSwagger "github.com/hertz-contrib/swagger"
	swaggerFiles "github.com/swaggo/files"

	appcfg "github.com/thanhenti/bepaylot/internal/config"
	"github.com/thanhenti/bepaylot/internal/handler"
	"github.com/thanhenti/bepaylot/internal/middleware"
)

// New builds the Hertz engine with all routes mounted.
func New(cfg appcfg.HTTP, h *handler.Handlers, log *slog.Logger) *server.Hertz {
	opts := []config.Option{
		server.WithHostPorts(cfg.Addr),
		server.WithDisablePrintRoute(true),
		server.WithReadTimeout(cfg.ReadTimeout),
		server.WithWriteTimeout(cfg.WriteTimeout),
		server.WithExitWaitTime(cfg.ShutdownTimeout),
		server.WithStreamBody(true),
	}
	// Cancelling a request's context when the client hangs up needs connection
	// state listening, which Hertz has on netpoll and on the standard transport
	// on Unix — but not on Windows, where enabling it only logs "ListenConnState
	// failed ... connection close detection disabled" for every connection.
	if runtime.GOOS != "windows" {
		opts = append(opts, server.WithSenseClientDisconnection(true))
	}
	hz := server.New(opts...)

	hz.Use(
		middleware.RequestID(),
		middleware.Recovery(log),
		middleware.AccessLog(log),
		middleware.CORS(cfg.CORSOrigins),
	)

	hz.GET("/healthz", h.Healthz)
	hz.GET("/readyz", h.Readyz)

	// API documentation (no auth). Swagger UI at /swagger/index.html; the raw
	// generated spec at /openapi.yaml; /docs redirects to the UI.
	hz.GET("/swagger/*any", hertzSwagger.WrapHandler(swaggerFiles.Handler))
	hz.GET("/openapi.yaml", h.OpenAPISpec)
	hz.GET("/docs", h.DocsRedirect)

	// Sign-in (no auth): the login page, token refresh and the OIDC flow.
	pub := hz.Group("/v1/auth")
	{
		pub.GET("/config", h.AuthConfig)
		pub.POST("/register", h.Register)
		pub.POST("/login", h.Login)
		pub.POST("/refresh", h.RefreshToken)
		pub.POST("/logout", h.Logout)
		pub.GET("/oidc/start", h.OIDCStart)
		pub.GET("/oidc/callback", h.OIDCCallback)
	}

	var tokens middleware.TokenAuthenticator
	if h.Auth != nil {
		tokens = h.Auth
	}
	v1 := hz.Group("/v1", middleware.Auth(tokens, h.Store.APIKeys, h.Store.Users, cfg.AuthBypass))
	{
		v1.GET("/auth/me", h.Me)
		v1.POST("/auth/change-password", h.ChangePassword)
		v1.GET("/auth/api-keys", h.ListAPIKeys)
		v1.POST("/auth/api-keys", h.CreateAPIKey)
		v1.DELETE("/auth/api-keys/:id", h.RevokeAPIKey)

		v1.POST("/messages", h.Messages)

		// AG-UI protocol surface for client SDKs (CopilotKit et al.).
		agui := v1.Group("/ag-ui")
		agui.POST("/run", h.AGUIRunAgent)

		v1.POST("/sessions", h.CreateSession)
		v1.GET("/sessions", h.ListSessions)
		v1.GET("/sessions/:id", h.GetSession)
		v1.GET("/sessions/:id/messages", h.ListSessionMessages)
		v1.GET("/sessions/:id/report", h.GetSessionReport)
		v1.PATCH("/sessions/:id", h.UpdateSession)
		v1.DELETE("/sessions/:id", h.DeleteSession)

		v1.POST("/skills/sync", h.SyncSkills)
		v1.GET("/skills", h.ListSkills)

		v1.GET("/mcp/servers", h.ListMCPServers)
		v1.POST("/mcp/servers", h.AddMCPServer)
		v1.DELETE("/mcp/servers/:name", h.DeleteMCPServer)

		registerKnowledgeRoutes(v1, h)
	}

	return hz
}

// Run starts the server and blocks until ctx is cancelled, then shuts down
// gracefully.
func Run(ctx context.Context, hz *server.Hertz) {
	go func() {
		<-ctx.Done()
		_ = hz.Shutdown(context.Background())
	}()
	hz.Spin()
}
