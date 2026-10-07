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
}

// SectionReader is Module 2 (Index)'s read contract for sections and trees.
type SectionReader interface {
	Sections(ctx context.Context, doc uuid.UUID, gen int) ([]types.Section, error)
	Tree(ctx context.Context, doc uuid.UUID, gen int) ([]types.TreeNode, error)
}

// Searcher is Module 2's search contract for the HTTP API and the agent.
// Every per-document call takes the case it must belong to.
type Searcher interface {
	Search(ctx context.Context, req types.SearchRequest) (*types.SearchResponse, error)
	// CaseTOC renders the table of contents of a case (§6.6 step 2): cards
	// of its searchable documents, narrowed by metadata, with every tree
	// whole when they fit search.tree_token_budget together, otherwise the
	// first branches; expand lists document refs (d<n>) shown with their
	// whole tree.
	CaseTOC(ctx context.Context, owner, caseID uuid.UUID, filter types.MetadataFilter, expand []string) (*types.CaseTOC, error)
	// CaseRefs maps the refs d<n> of the case's documents (their branch in
	// the case tree) to document ids.
	CaseRefs(ctx context.Context, owner, caseID uuid.UUID) (map[string]uuid.UUID, error)
	// FindInDocument searches one document, limited to pages from..to when
	// they are > 0.
	FindInDocument(ctx context.Context, owner, doc uuid.UUID, query, mode string, from, to int) ([]types.PageSearchHit, error)
	// PageOverview describes pages from..to of one document (all when 0).
	PageOverview(ctx context.Context, owner, doc uuid.UUID, from, to int) ([]types.PageOverview, error)
	DocumentTree(ctx context.Context, owner, doc uuid.UUID) ([]types.TreeNode, error)
	// DocumentTreeText renders the tree of a document (or the subtree of
	// node) the way the LLM reads it (§6.5): whole when it fits
	// search.tree_token_budget, cut from the deepest level otherwise.
	DocumentTreeText(ctx context.Context, owner, doc uuid.UUID, node string) (string, error)
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

// Completer asks an LLM for a JSON answer and decodes it into out.
type Completer interface {
	CompleteJSON(ctx context.Context, system, user string, out any) error
}

// FieldService writes and reads Extracted Fields (§6.9.3) for the agent's
// kb_save_fields / kb_get_fields. Evidence citations must belong to the case.
type FieldService interface {
	SaveAgentFields(ctx context.Context, owner, caseID uuid.UUID, sessionID *uuid.UUID, in []types.FieldInput) []FieldResult
	CaseFields(ctx context.Context, caseID uuid.UUID, doc *uuid.UUID, key string) ([]types.Field, error)
}

// FieldResult is the outcome of one field of kb_save_fields.
type FieldResult struct {
	Key          string `json:"key"`
	ID           string `json:"id,omitempty"`
	Status       string `json:"status,omitempty"`
	ValueMatched bool   `json:"value_matched"`
	Error        string `json:"error,omitempty"`
}
