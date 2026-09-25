// Package interfaces holds the contracts between modules (§3.3). A module's
// service depends on another module only through these interfaces, wired in
// internal/container.
package interfaces

import (
	"context"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/types"
)

// DocumentStore is Module 1 (Parser)'s contract for later modules: read the
// parsed pages and report stage progress on the documents it owns.
type DocumentStore interface {
	GetDocument(ctx context.Context, id uuid.UUID) (types.Document, error)
	GetKB(ctx context.Context, id uuid.UUID) (types.KnowledgeBase, error)
	// LoadPages returns parsed pages [from, to] (0 = unbounded) of gen.
	LoadPages(ctx context.Context, doc uuid.UUID, gen, from, to int) ([]*types.ParsedPage, error)
	// SetIndexResult records the index stage outcome and the document card.
	SetIndexResult(ctx context.Context, doc uuid.UUID, gen int, card DocumentCard, failed error) error
	// SetGraphStatus records the graph stage outcome.
	SetGraphStatus(ctx context.Context, doc uuid.UUID, gen int, status string, failed error) error
}

// DocumentCard is the document-level summary produced by Module 2.
type DocumentCard struct {
	Title, DocType, Summary string
}

// SectionReader is Module 2 (Index)'s read contract for Module 3 (Graph).
type SectionReader interface {
	Sections(ctx context.Context, doc uuid.UUID, gen int) ([]types.Section, error)
}

// Searcher is Module 2's search contract for the HTTP API and the agent.
type Searcher interface {
	Search(ctx context.Context, req types.SearchRequest) (*types.SearchResponse, error)
	FindInDocument(ctx context.Context, owner, doc uuid.UUID, query, mode string) ([]types.PageSearchHit, error)
	DocumentTree(ctx context.Context, owner, doc uuid.UUID) ([]types.TreeNode, error)
	ReadPages(ctx context.Context, owner, doc uuid.UUID, from, to int) (string, error)
	ListDocuments(ctx context.Context, owner uuid.UUID, kb uuid.UUID, filter types.MetadataFilter, limit int) ([]types.DocumentBrief, error)
	MetadataValues(ctx context.Context, owner, kb uuid.UUID, key string) ([]MetadataValue, error)
	Locate(ctx context.Context, owner uuid.UUID, citationID string) ([]types.SearchHit, error)
}

// MetadataValue is a distinct metadata value with its document count.
type MetadataValue struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// GraphQuerier is Module 3's read contract for the agent.
type GraphQuerier interface {
	SearchEntities(ctx context.Context, owner, kb uuid.UUID, query, typ string, limit int) ([]types.Entity, error)
	Neighbors(ctx context.Context, owner, entity uuid.UUID, relTypes []string, depth int) (*types.Subgraph, error)
	WikiPage(ctx context.Context, owner, kb uuid.UUID, slug string) (*types.WikiPage, error)
}

// Completer asks an LLM for a JSON answer and decodes it into out.
type Completer interface {
	CompleteJSON(ctx context.Context, system, user string, out any) error
}
