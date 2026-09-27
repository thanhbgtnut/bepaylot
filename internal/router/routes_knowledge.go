package router

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/hertz/pkg/route"

	"github.com/thanhenti/bepaylot/internal/handler"
	"github.com/thanhenti/bepaylot/internal/handler/dto"
)

// registerKnowledgeRoutes mounts the case, document, search and wiki APIs
// (§10). They answer 503 when the document modules are not configured.
func registerKnowledgeRoutes(v1 *route.RouterGroup, h *handler.Handlers) {
	g := v1.Group("", requireModules(h))

	// Knowledge bases, cases and metadata (§10.1, §6.2, §6.3).
	g.POST("/kbs", h.CreateKB)
	g.GET("/kbs", h.ListKBs)
	g.GET("/kbs/:id", h.GetKB)
	g.PATCH("/kbs/:id", h.UpdateKB)
	g.DELETE("/kbs/:id", h.DeleteKB)
	g.GET("/kbs/:id/metadata-schema", h.GetMetadataSchema)
	g.PUT("/kbs/:id/metadata-schema", h.PutMetadataSchema)
	g.GET("/kbs/:id/metadata/values", h.MetadataValues)
	g.GET("/case-types", h.ListCaseTypes)
	g.POST("/kbs/:id/cases", h.CreateCase)
	g.GET("/kbs/:id/cases", h.ListCases)
	g.GET("/kbs/:id/cases/by-code/:code", h.GetCaseByCode)
	g.GET("/cases/:id", h.GetCase)
	g.PATCH("/cases/:id", h.UpdateCase)
	g.DELETE("/cases/:id", h.DeleteCase)
	g.GET("/cases/:id/documents", h.ListCaseDocuments)
	g.POST("/cases/:id/documents", h.UploadCaseDocuments)
	g.POST("/cases/:id/search", h.SearchCase)

	// Documents and parsed content (§10.2).
	g.POST("/kbs/:id/documents", h.UploadDocuments)
	g.GET("/kbs/:id/documents", h.ListDocuments)
	g.POST("/kbs/:id/documents/metadata/bulk-update", h.BulkUpdateMetadata)
	g.GET("/documents/:id", h.GetDocument)
	g.GET("/documents/:id/events", h.DocumentEvents)
	g.POST("/documents/:id/cancel", h.CancelDocument)
	g.POST("/documents/:id/reparse", h.ReparseDocument)
	g.GET("/documents/:id/callbacks", h.ListDocumentCallbacks)
	g.POST("/documents/:id/callbacks/retry", h.RetryDocumentCallback)
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

	// Case wiki (§10.4); slugs contain "/", hence the catch-all page path.
	g.GET("/cases/:id/wiki", h.GetWiki)
	g.GET("/cases/:id/wiki/index", h.GetWikiIndex)
	g.GET("/cases/:id/wiki/pages/*slug", h.GetWikiPage)
	g.PUT("/cases/:id/wiki/pages/*slug", h.EditWikiPage)
	g.POST("/cases/:id/wiki/pages/*slug", h.PostWikiPage)
	g.DELETE("/cases/:id/wiki/pages/*slug", h.DeleteWikiPage)
	g.GET("/cases/:id/wiki/links", h.WikiLinks)
	g.GET("/cases/:id/wiki/search", h.SearchWiki)
	g.POST("/cases/:id/wiki/notes", h.CreateWikiNote)
	g.GET("/cases/:id/wiki/log", h.WikiLog)
	g.GET("/cases/:id/wiki/lint", h.ListWikiLint)
	g.POST("/cases/:id/wiki/lint", h.RunWikiLint)
	g.PATCH("/cases/:id/wiki/lint/:issue_id", h.PatchWikiLint)
	g.POST("/cases/:id/wiki/rebuild", h.RebuildWiki)
	g.GET("/cases/:id/wiki/export", h.ExportWiki)
	g.GET("/cases/:id/wiki/events", h.WikiEvents)

	// Wiki schemas (§10.5).
	g.GET("/wiki/schemas", h.ListWikiSchemas)
	g.POST("/wiki/schemas", h.CreateWikiSchema)
	g.GET("/wiki/schemas/:name", h.GetWikiSchema)
	g.POST("/wiki/schemas/:name/test", h.TestWikiSchema)

	// Operations (§10.6).
	g.GET("/admin/queues", h.QueueStats)
	g.GET("/admin/dead-letters", h.ListDeadLetters)
	g.POST("/admin/dead-letters/:id/retry", h.RetryDeadLetter)
}

func requireModules(h *handler.Handlers) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		if h.Docs == nil || h.Searcher == nil || h.Cases == nil || h.Wiki == nil {
			c.AbortWithStatusJSON(consts.StatusServiceUnavailable, dto.NewError("unavailable_error",
				"document modules are not configured on this server (check storage.s3 and redis settings)"))
			return
		}
		c.Next(ctx)
	}
}
