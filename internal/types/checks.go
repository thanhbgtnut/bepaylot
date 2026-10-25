package types

import (
	"time"

	"github.com/google/uuid"
)

// CheckRuleset is a set of cross-check rules shared by every knowledge base
// (§6.9.9, U105), set by prompt editors in Settings → Quy tắc đối chiếu; a KB
// uses the sets that list it and Studio runs one set at a time. Documents are
// grouped around one document of the Anchor type (a contract): each other
// type has ordered steps that tell which contract a document belongs to, and
// each rule checks the documents of one group.
type CheckRuleset struct {
	ID          uuid.UUID   `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	KBIDs       []uuid.UUID `json:"kb_ids"`
	Anchor      string      `json:"anchor"`
	Links       []CheckLink `json:"links"`
	Rules       []CheckRule `json:"rules"`
	CreatedBy   *uuid.UUID  `json:"created_by,omitempty"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// FieldRef names a field of a document type.
type FieldRef struct {
	Type string `json:"type"`
	Key  string `json:"key"`
}

// CheckLink is how documents of one type find their contract.
type CheckLink struct {
	DocType string      `json:"doc_type"`
	Steps   []CheckStep `json:"steps"`
}

// CheckStep narrows the contracts a document may belong to: those for which
// Formula is true (the document is named by its type code, the contract by
// the anchor code). A step that cannot tell (missing data) keeps them all.
type CheckStep struct {
	ID      string `json:"id"`
	Text    string `json:"text"`
	Formula string `json:"formula,omitempty"`
	AI      bool   `json:"ai,omitempty"`
	// Explanation says in plain words what the formula checks.
	Explanation string `json:"explanation,omitempty"`
}

// CheckRule is one check of a contract group (U104): field A of one type
// compared with field B of another by Op, the formula built by code; or a
// sentence (Text, AI) the agent judges at each run. Rules saved before 0.33
// may carry a formula the agent wrote from Text.
type CheckRule struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	A       *FieldRef `json:"a,omitempty"`
	Op      string    `json:"op,omitempty"`
	B       *FieldRef `json:"b,omitempty"`
	Text    string    `json:"text,omitempty"`
	Formula string    `json:"formula,omitempty"`
	AI      bool      `json:"ai,omitempty"`
	Off     bool      `json:"off,omitempty"`
	// Explanation says in plain words what the formula checks.
	Explanation string `json:"explanation,omitempty"`
}

// Check run statuses reuse the sheet statuses (pending, running, done, failed).

// CheckRun is one cross-check of a case: the sheet whose values it read and
// its result (groups, unlinked documents, outcome of each rule).
type CheckRun struct {
	ID        uuid.UUID  `json:"id"`
	CaseID    uuid.UUID  `json:"case_id"`
	SheetID   uuid.UUID  `json:"sheet_id"`
	RulesetID *uuid.UUID `json:"ruleset_id,omitempty"`
	Status    string     `json:"status"`
	Error     string     `json:"error,omitempty"`
	Result    any        `json:"result,omitempty"`
	CreatedBy uuid.UUID  `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// CheckLinkOverride is a user's choice of the contract of a document
// (Anchor nil = belongs to none); it outlives runs.
type CheckLinkOverride struct {
	SegmentID uuid.UUID  `json:"segment_id"`
	Anchor    *uuid.UUID `json:"anchor_segment_id"`
}
