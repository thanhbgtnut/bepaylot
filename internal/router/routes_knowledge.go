package router

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/hertz/pkg/route"

	"github.com/thanhenti/bepaylot/internal/handler"
	"github.com/thanhenti/bepaylot/internal/handler/dto"
)

// registerKnowledgeRoutes mounts the case, document and search APIs
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
	g.GET("/cases/:id/toc", h.CaseTOC)
	g.GET("/documents/:id/tree", h.DocumentTree)
	g.GET("/citations", h.LocateCitation)

	// Operations (§10.4).
	g.GET("/admin/queues", h.QueueStats)
	g.GET("/admin/dead-letters", h.ListDeadLetters)
	g.POST("/admin/dead-letters/:id/retry", h.RetryDeadLetter)

	// Prompt templates, case sheets, usage and admin of users (U43–U46).
	g.GET("/templates", h.ListTemplates)
	g.POST("/templates", h.CreateTemplate)
	g.GET("/templates/:id", h.GetTemplate)
	g.POST("/templates/:id/versions", h.AddTemplateVersion)
	g.POST("/templates/:id/publish", h.PublishTemplate)
	g.GET("/templates/:id/corrections", h.TemplateCorrections)
	g.POST("/cases/:id/sheets", h.CreateSheet)
	g.GET("/cases/:id/sheets", h.ListSheets)
	g.GET("/sheets/:id", h.GetSheet)
	g.POST("/sheets/:id/edits", h.SaveSheetEdits)
	g.POST("/sheets/:id/import", h.ImportSheet)
	g.GET("/sheets/:id/xlsx", h.DownloadSheet)
	g.GET("/me/usage", h.MyUsage)
	g.GET("/admin/usage", h.AdminUsage)
	g.GET("/admin/users", h.AdminListUsers)
	g.PATCH("/admin/users/:id", h.AdminUpdateUser)
	g.POST("/admin/users/:id/api-keys", h.AdminIssueAPIKey)
	g.GET("/admin/llm", h.AdminGetLLM)
	g.PUT("/admin/llm", h.AdminSetLLM)
}

func requireModules(h *handler.Handlers) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		if h.Docs == nil || h.Searcher == nil || h.Cases == nil {
			c.AbortWithStatusJSON(consts.StatusServiceUnavailable, dto.NewError("unavailable_error",
				"document modules are not configured on this server (check storage.s3 and redis settings)"))
			return
		}
		c.Next(ctx)
	}
}
