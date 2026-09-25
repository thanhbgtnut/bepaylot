package router

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/hertz/pkg/route"

	"github.com/thanhenti/bepaylot/internal/handler"
	"github.com/thanhenti/bepaylot/internal/handler/dto"
)

// registerKnowledgeRoutes mounts the document, search, graph and wiki APIs
// (§10). They answer 503 when the document modules are not configured.
func registerKnowledgeRoutes(v1 *route.RouterGroup, h *handler.Handlers) {
	g := v1.Group("", requireModules(h))

	// Knowledge bases and metadata (§10.1, §6.2).
	g.POST("/kbs", h.CreateKB)
	g.GET("/kbs", h.ListKBs)
	g.GET("/kbs/:id", h.GetKB)
	g.PATCH("/kbs/:id", h.UpdateKB)
	g.DELETE("/kbs/:id", h.DeleteKB)
	g.GET("/kbs/:id/metadata-schema", h.GetMetadataSchema)
	g.PUT("/kbs/:id/metadata-schema", h.PutMetadataSchema)
	g.GET("/kbs/:id/metadata/values", h.MetadataValues)

	// Documents and parsed content (§10.2).
	g.POST("/kbs/:id/documents", h.UploadDocuments)
	g.GET("/kbs/:id/documents", h.ListDocuments)
	g.POST("/kbs/:id/documents/metadata/bulk-update", h.BulkUpdateMetadata)
	g.GET("/documents/:id", h.GetDocument)
	g.GET("/documents/:id/events", h.DocumentEvents)
	g.POST("/documents/:id/cancel", h.CancelDocument)
	g.POST("/documents/:id/reparse", h.ReparseDocument)
	g.DELETE("/documents/:id", h.DeleteDocument)
	g.PATCH("/documents/:id/metadata", h.UpdateDocumentMetadata)
	g.GET("/documents/:id/file", h.DocumentFile)
	g.GET("/documents/:id/markdown", h.DocumentMarkdown)
	g.GET("/documents/:id/pages", h.ListPages)
	g.GET("/documents/:id/pages/:n", h.GetPage)
	g.GET("/documents/:id/pages/:n/image", h.PageImage)
	g.POST("/documents/:id/locate", h.LocateInDocument)
	g.GET("/parser/engines", h.ListEngines)
	g.POST("/sessions/:id/attachments", h.UploadAttachment)

	// Search (§10.3).
	g.POST("/search", h.Search)
	g.POST("/documents/:id/search", h.SearchInDocument)
	g.GET("/documents/:id/tree", h.DocumentTree)
	g.GET("/citations", h.LocateCitation)

	// Graph and wiki (§10.4), schemas (§10.5).
	g.GET("/kbs/:id/graph/entities", h.SearchEntities)
	g.GET("/kbs/:id/graph/path", h.GraphPath)
	g.POST("/kbs/:id/graph/rebuild", h.RebuildGraph)
	g.GET("/graph/entities/:id", h.GetEntity)
	g.GET("/graph/entities/:id/neighbors", h.EntityNeighbors)
	g.GET("/kbs/:id/wiki/pages", h.ListWikiPages)
	g.GET("/kbs/:id/wiki/pages/:slug", h.GetWikiPage)
	g.PUT("/kbs/:id/wiki/pages/:slug", h.EditWikiPage)
	g.GET("/kbs/:id/wiki/pages/:slug/revisions", h.WikiRevisions)
	g.GET("/graph/schemas", h.ListSchemas)
	g.POST("/graph/schemas", h.CreateSchema)
	g.GET("/graph/schemas/:name", h.GetSchemaVersions)
	g.POST("/graph/schemas/:name/test", h.TestSchema)

	// Operations (§10.6).
	g.GET("/admin/queues", h.QueueStats)
	g.GET("/admin/dead-letters", h.ListDeadLetters)
	g.POST("/admin/dead-letters/:id/retry", h.RetryDeadLetter)
}

func requireModules(h *handler.Handlers) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		if h.Docs == nil || h.Searcher == nil || h.Graph == nil || h.Wiki == nil {
			c.AbortWithStatusJSON(consts.StatusServiceUnavailable, dto.NewError("unavailable_error",
				"document modules are not configured on this server (check storage.s3 and redis settings)"))
			return
		}
		c.Next(ctx)
	}
}
