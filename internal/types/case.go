package types

import (
	"time"

	"github.com/google/uuid"
)

// Case states (cases.status, §6.2).
const (
	CaseOpen   = "open"
	CaseClosed = "closed"
)

// DefaultCaseType is the case type used when none is given (§6.2).
const DefaultCaseType = "default"

// Case is one business file identified by a code (e.g. a payment code
// RT112233). Every document belongs to exactly one case, and the case is the
// hard search scope of an agent session (§6.2, §8.1).
type Case struct {
	ID        uuid.UUID      `json:"id"`
	KBID      uuid.UUID      `json:"kb_id"`
	Code      string         `json:"code"`
	CaseType  string         `json:"case_type"`
	Title     string         `json:"title,omitempty"`
	Status    string         `json:"status"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedBy uuid.UUID      `json:"created_by"`

	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
	// Documents counts the case's live documents by status (list/detail only).
	Documents map[string]int `json:"documents,omitempty"`
}

// DocumentTotal sums Documents.
func (c *Case) DocumentTotal() int {
	n := 0
	for _, v := range c.Documents {
		n += v
	}
	return n
}

// CaseType configures one kind of case (configs/case_types/*.yaml, §6.2). It
// holds data rules only: no workflow, prompt, field list or rule list.
type CaseType struct {
	Name  string       `json:"name" yaml:"name"`
	Title string       `json:"title,omitempty" yaml:"title"`
	Code  CaseCodeRule `json:"code" yaml:"code"`
	// CaseMetadataSchema validates the metadata of the case itself.
	CaseMetadataSchema *MetadataSchema `json:"case_metadata_schema,omitempty" yaml:"case_metadata_schema"`
	// MetadataSchema validates document metadata in cases of this type; it
	// replaces the KB's metadata_schema.
	MetadataSchema *MetadataSchema `json:"metadata_schema,omitempty" yaml:"metadata_schema"`
	Parser         struct {
		Engine string `json:"engine,omitempty" yaml:"engine"`
	} `json:"parser" yaml:"parser"`
}

// CaseCodeRule validates and normalizes case codes.
type CaseCodeRule struct {
	Pattern   string `json:"pattern,omitempty" yaml:"pattern"`
	Normalize string `json:"normalize,omitempty" yaml:"normalize"` // upper_trim | lower_trim | trim (default)
}

// CaseTaskPayload is the payload of case-scoped tasks (case:delete).
type CaseTaskPayload struct {
	CaseID uuid.UUID `json:"case_id"`
}
