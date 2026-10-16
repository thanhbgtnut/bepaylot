package types

import (
	"time"

	"github.com/google/uuid"
)

// CheckConfig is how a KB cross-checks the documents of a case (§6.9.9,
// U87), set by prompt editors in Settings → Quy tắc đối chiếu. Documents are
// grouped around one document of the Anchor type (a contract): each other
// type has ordered steps that tell which contract a document belongs to, and
// each rule is a yes/no formula over the documents of one group. Every step
// and rule keeps the sentence the user wrote; the agent turned it into the
// formula once, or marked it AI when it could not (it is then judged by the
// agent at each run).
type CheckConfig struct {
	Anchor string      `json:"anchor"`
	Links  []CheckLink `json:"links"`
	Rules  []CheckRule `json:"rules"`
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

// CheckRule is one check of a contract group.
type CheckRule struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Text    string `json:"text"`
	Formula string `json:"formula,omitempty"`
	AI      bool   `json:"ai,omitempty"`
	Off     bool   `json:"off,omitempty"`
	// Explanation says in plain words what the formula checks.
	Explanation string `json:"explanation,omitempty"`
}

// Check run statuses reuse the sheet statuses (pending, running, done, failed).

// CheckRun is one cross-check of a case: the sheet whose values it read and
// its result (groups, unlinked documents, outcome of each rule).
type CheckRun struct {
	ID        uuid.UUID `json:"id"`
	CaseID    uuid.UUID `json:"case_id"`
	SheetID   uuid.UUID `json:"sheet_id"`
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	Result    any       `json:"result,omitempty"`
	CreatedBy uuid.UUID `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CheckLinkOverride is a user's choice of the contract of a document
// (Anchor nil = belongs to none); it outlives runs.
type CheckLinkOverride struct {
	SegmentID uuid.UUID  `json:"segment_id"`
	Anchor    *uuid.UUID `json:"anchor_segment_id"`
}
