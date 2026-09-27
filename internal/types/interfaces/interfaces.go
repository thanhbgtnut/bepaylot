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
	// SetWikiStatus records the wiki ingest outcome of a document (§6.8).
	SetWikiStatus(ctx context.Context, doc uuid.UUID, gen int, status string, failed error) error
}

// DocumentCard is the document-level summary produced by Module 2. It
// describes content only; documents are not classified at index time.
type DocumentCard struct {
	Title, Summary string
}

// CaseRef names a case by id, or by code (created on demand when Create).
type CaseRef struct {
	ID       uuid.UUID
	Code     string
	CaseType string
	Create   bool
}

// CaseService is the case contract (§6.2) for the other modules.
type CaseService interface {
	// GetCase returns a live case without an owner check (workers).
	GetCase(ctx context.Context, id uuid.UUID) (types.Case, error)
	// GetCaseOwned returns a live case whose KB the owner owns.
	GetCaseOwned(ctx context.Context, owner, id uuid.UUID) (types.Case, error)
	// ResolveCase finds (or creates) the case an upload goes into and checks
	// it accepts files; created reports a new case.
	ResolveCase(ctx context.Context, owner, kb uuid.UUID, ref CaseRef) (c types.Case, created bool, err error)
	// CaseType returns the named type, or the default type.
	CaseType(name string) types.CaseType
	// WikiEnabled reports whether documents of the case are ingested.
	WikiEnabled(c types.Case) bool
}

// SectionReader is Module 2 (Index)'s read contract for the wiki.
type SectionReader interface {
	Sections(ctx context.Context, doc uuid.UUID, gen int) ([]types.Section, error)
	Tree(ctx context.Context, doc uuid.UUID, gen int) ([]types.TreeNode, error)
}

// Searcher is Module 2's search contract for the HTTP API and the agent.
// Every per-document call takes the case it must belong to.
type Searcher interface {
	Search(ctx context.Context, req types.SearchRequest) (*types.SearchResponse, error)
	// FindInDocument searches one document, limited to pages from..to when
	// they are > 0.
	FindInDocument(ctx context.Context, owner, doc uuid.UUID, query, mode string, from, to int) ([]types.PageSearchHit, error)
	// PageOverview describes pages from..to of one document (all when 0).
	PageOverview(ctx context.Context, owner, doc uuid.UUID, from, to int) ([]types.PageOverview, error)
	DocumentTree(ctx context.Context, owner, doc uuid.UUID) ([]types.TreeNode, error)
	ReadPages(ctx context.Context, owner, doc uuid.UUID, from, to int) (string, error)
	// ListDocuments lists the documents of one case.
	ListDocuments(ctx context.Context, owner, caseID uuid.UUID, filter types.MetadataFilter, statuses []string, limit int) ([]types.DocumentBrief, error)
	// MetadataValues counts the documents of one case per value of key.
	MetadataValues(ctx context.Context, owner, caseID uuid.UUID, key string) ([]MetadataValue, error)
	// DocumentInCase reports whether a live document belongs to the case
	// (§8.1). It is false for documents the owner cannot see.
	DocumentInCase(ctx context.Context, owner, doc, caseID uuid.UUID) (bool, error)
	// CaseAlive reports whether the case still exists for the owner.
	CaseAlive(ctx context.Context, owner, caseID uuid.UUID) bool
	Locate(ctx context.Context, owner uuid.UUID, citationID string) ([]types.SearchHit, error)
}

// MetadataValue is a distinct metadata value with its document count.
type MetadataValue struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// WikiReader is the read contract of the case wiki (§6.6) for search and the
// agent. Every call is scoped to one case.
type WikiReader interface {
	// IndexView returns the wiki index of the case. With docs, source lines
	// keep only those documents and other pages only when they cite one.
	// A case without wiki gets an index of document cards and trees.
	IndexView(ctx context.Context, caseID uuid.UUID, docs []uuid.UUID) (*types.WikiIndex, error)
	// Expand renders the branches of an index entry that were cut.
	Expand(ctx context.Context, caseID uuid.UUID, ref string) (string, error)
	// Page returns a page with footnotes and links; slug of another case is
	// not found.
	Page(ctx context.Context, caseID uuid.UUID, slug string) (*types.WikiPage, error)
	SearchPages(ctx context.Context, caseID uuid.UUID, query string, limit int) ([]WikiSearchHit, error)
	Links(ctx context.Context, caseID uuid.UUID, slug, relation, direction string) ([]types.WikiLink, error)
	// MarkFootnotesStale flags footnotes that no longer match their lines.
	MarkFootnotesStale(ctx context.Context, caseID, page uuid.UUID, ns []int) error
	// LogQuery appends a query line to the wiki log (the log records
	// ingests, queries and lint passes).
	LogQuery(ctx context.Context, caseID, owner uuid.UUID, question string, pages []string, hits int) error
}

// WikiSearchHit is one full-text match in a case wiki.
type WikiSearchHit struct {
	Slug    string  `json:"slug"`
	Title   string  `json:"title"`
	Kind    string  `json:"kind"`
	Snippet string  `json:"snippet"`
	Score   float64 `json:"score"`
}

// Completer asks an LLM for a JSON answer and decodes it into out.
type Completer interface {
	CompleteJSON(ctx context.Context, system, user string, out any) error
}
