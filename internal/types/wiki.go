package types

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

// Wiki page kinds (wiki_pages.kind, §6.6).
const (
	WikiKindOverview = "overview"
	WikiKindSource   = "source"
	WikiKindEntity   = "entity"
	WikiKindTopic    = "topic"
	WikiKindNote     = "note"
)

// Wiki log operations (wiki_log.op).
const (
	WikiOpIngest  = "ingest"
	WikiOpRetract = "retract"
	WikiOpLint    = "lint"
	WikiOpEdit    = "edit"
	WikiOpNote    = "note"
	WikiOpRebuild = "rebuild"
	WikiOpQuery   = "query"
)

// Wiki lint issue kinds (§6.9).
const (
	LintStale         = "stale"
	LintContradiction = "contradiction"
	LintOrphan        = "orphan"
	LintMissingLink   = "missing_link"
	LintGap           = "gap"
	LintIndexDrift    = "index_drift"
)

// Footnote states.
const (
	FootnoteValid = "valid"
	FootnoteStale = "stale"
)

// Edit sources of a wiki page.
const (
	EditSystem = "system"
	EditUser   = "user"
)

// WikiSchema is the schema layer of the case wiki (§6.7): which entity types,
// relations and topics the wiki has and how it is written.
type WikiSchema struct {
	ID          uuid.UUID          `json:"id,omitempty" yaml:"-"`
	Name        string             `json:"name" yaml:"name"`
	Version     int                `json:"version" yaml:"version"`
	Language    string             `json:"language,omitempty" yaml:"language"`
	Description string             `json:"description,omitempty" yaml:"description"`
	EntityTypes []WikiEntityType   `json:"entity_types" yaml:"entity_types"`
	Relations   []WikiRelationType `json:"relations" yaml:"relations"`
	Topics      []string           `json:"topics,omitempty" yaml:"topics"`
	Conventions string             `json:"conventions,omitempty" yaml:"conventions"`
	Limits      struct {
		MaxPagesTouched int `json:"max_pages_touched,omitempty" yaml:"max_pages_touched"`
		MaxPages        int `json:"max_pages,omitempty" yaml:"max_pages"`
	} `json:"limits" yaml:"limits"`
	CreatedAt time.Time `json:"created_at,omitempty" yaml:"-"`
}

// WikiEntityType declares one entity type; its pages are identified by the
// normalized Identity attributes.
type WikiEntityType struct {
	Name        string         `json:"name" yaml:"name"`
	Title       string         `json:"title,omitempty" yaml:"title"`
	Description string         `json:"description,omitempty" yaml:"description"`
	Identity    []string       `json:"identity,omitempty" yaml:"identity"`
	Attributes  []AttributeDef `json:"attributes,omitempty" yaml:"attributes"`
}

// WikiRelationType declares a typed link between entity pages.
type WikiRelationType struct {
	Name        string         `json:"name" yaml:"name"`
	Description string         `json:"description,omitempty" yaml:"description"`
	From        StringList     `json:"from" yaml:"from"`
	To          StringList     `json:"to" yaml:"to"`
	Attributes  []AttributeDef `json:"attributes,omitempty" yaml:"attributes"`
}

// AttributeDef declares a typed attribute.
type AttributeDef struct {
	Name     string `json:"name" yaml:"name"`
	Type     string `json:"type" yaml:"type"` // string | number | money | date | bool
	Required bool   `json:"required,omitempty" yaml:"required"`
	Pattern  string `json:"pattern,omitempty" yaml:"pattern"`
}

// EntityType returns the declared entity type or nil.
func (s *WikiSchema) EntityType(name string) *WikiEntityType {
	for i := range s.EntityTypes {
		if s.EntityTypes[i].Name == name {
			return &s.EntityTypes[i]
		}
	}
	return nil
}

// Relation returns the declared relation or nil.
func (s *WikiSchema) Relation(name string) *WikiRelationType {
	for i := range s.Relations {
		if s.Relations[i].Name == name {
			return &s.Relations[i]
		}
	}
	return nil
}

// Attribute returns the declared attribute or nil.
func (e *WikiEntityType) Attribute(name string) *AttributeDef {
	for i := range e.Attributes {
		if e.Attributes[i].Name == name {
			return &e.Attributes[i]
		}
	}
	return nil
}

// StringList accepts either one string or a list of strings.
type StringList []string

// Has reports whether v is in the list.
func (l StringList) Has(v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (l *StringList) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		*l = StringList{n.Value}
		return nil
	case yaml.SequenceNode:
		var out []string
		if err := n.Decode(&out); err != nil {
			return err
		}
		*l = out
		return nil
	}
	return fmt.Errorf("expected a string or a list of strings")
}

// UnmarshalJSON implements json.Unmarshaler.
func (l *StringList) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*l = StringList{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*l = many
	return nil
}

// WikiAttribute is one entity attribute with its footnotes; two sources that
// disagree keep both values in History and set Conflict (§6.8 step 5).
type WikiAttribute struct {
	Value     any             `json:"value"`
	Footnotes []int           `json:"footnotes,omitempty"`
	Conflict  bool            `json:"conflict,omitempty"`
	History   []WikiAttrValue `json:"history,omitempty"`
}

// WikiAttrValue is one sourced value of an attribute.
type WikiAttrValue struct {
	Value      any    `json:"value"`
	Footnotes  []int  `json:"footnotes,omitempty"`
	CitationID string `json:"citation_id,omitempty"`
}

// WikiPage is one page of a case wiki, stored in Postgres (§6.6).
type WikiPage struct {
	ID                 uuid.UUID                `json:"id"`
	CaseID             uuid.UUID                `json:"case_id"`
	Slug               string                   `json:"slug"`
	Kind               string                   `json:"kind"`
	EntityType         string                   `json:"entity_type,omitempty"`
	IdentityKey        string                   `json:"identity_key,omitempty"`
	DocumentID         *uuid.UUID               `json:"document_id,omitempty"`
	Title              string                   `json:"title"`
	Aliases            []string                 `json:"aliases,omitempty"`
	Summary            string                   `json:"summary"`
	Content            string                   `json:"content"`
	Attributes         map[string]WikiAttribute `json:"attributes,omitempty"`
	Ord                int                      `json:"ord"`
	Version            int                      `json:"version"`
	LastEditSource     string                   `json:"last_edit_source"`
	ProposedContent    *string                  `json:"proposed_content,omitempty"`
	ProposedAttributes map[string]WikiAttribute `json:"proposed_attributes,omitempty"`
	CreatedAt          time.Time                `json:"created_at"`
	UpdatedAt          time.Time                `json:"updated_at"`

	// Filled on single-page reads.
	Footnotes []WikiFootnote `json:"footnotes,omitempty"`
	LinksOut  []WikiLink     `json:"links_out,omitempty"`
	LinksIn   []WikiLink     `json:"links_in,omitempty"`
}

// WikiFootnote is the bridge from a wiki page back to source lines.
type WikiFootnote struct {
	N          int       `json:"n"`
	DocumentID uuid.UUID `json:"document_id"`
	Gen        int       `json:"gen"`
	PageNo     int       `json:"page_no"`
	LineFrom   int       `json:"line_from"`
	LineTo     int       `json:"line_to"`
	Quote      string    `json:"quote"`
	CitationID string    `json:"citation_id"`
	Status     string    `json:"status"`
	FileName   string    `json:"file_name,omitempty"`
}

// WikiLink is a plain ([[slug]], Relation "") or typed link between pages.
type WikiLink struct {
	From       string         `json:"from"`
	FromTitle  string         `json:"from_title,omitempty"`
	To         string         `json:"to"`
	ToTitle    string         `json:"to_title,omitempty"`
	Relation   string         `json:"relation,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
	FootnoteN  *int           `json:"footnote_n,omitempty"`
}

// WikiRevision is one stored version of a page.
type WikiRevision struct {
	Version    int                      `json:"version"`
	Title      string                   `json:"title"`
	Content    string                   `json:"content"`
	Attributes map[string]WikiAttribute `json:"attributes,omitempty"`
	EditSource string                   `json:"edit_source"`
	EditorID   *uuid.UUID               `json:"editor_id,omitempty"`
	LogID      *int64                   `json:"log_id,omitempty"`
	EditedAt   time.Time                `json:"edited_at"`
}

// WikiLogEntry is one append-only log line (log.md of LLM Wiki).
type WikiLogEntry struct {
	ID         int64      `json:"id"`
	CaseID     uuid.UUID  `json:"case_id"`
	At         time.Time  `json:"at"`
	Op         string     `json:"op"`
	Ref        string     `json:"ref"`
	DocumentID *uuid.UUID `json:"document_id,omitempty"`
	Pages      []string   `json:"pages"`
	Summary    string     `json:"summary"`
	Actor      string     `json:"actor"`
	LLMCalls   int        `json:"llm_calls,omitempty"`
	TokensIn   int        `json:"tokens_in,omitempty"`
	TokensOut  int        `json:"tokens_out,omitempty"`
}

// Line renders the entry the way the log is shown (§6.6).
func (e WikiLogEntry) Line() string {
	return fmt.Sprintf("## [%s] %s | %s — %s", e.At.Format("2006-01-02 15:04"), e.Op, e.Ref, e.Summary)
}

// WikiLintIssue is one open or resolved lint finding (§6.9).
type WikiLintIssue struct {
	ID         int64          `json:"id"`
	CaseID     uuid.UUID      `json:"case_id"`
	Kind       string         `json:"kind"`
	PageIDs    []uuid.UUID    `json:"page_ids"`
	Pages      []string       `json:"pages,omitempty"`
	Detail     map[string]any `json:"detail"`
	Status     string         `json:"status"`
	FoundAt    time.Time      `json:"found_at"`
	ResolvedAt *time.Time     `json:"resolved_at,omitempty"`
	ResolvedBy *uuid.UUID     `json:"resolved_by,omitempty"`
}

// WikiIndex is the catalogue of a case wiki put first into search prompts
// (index.md of LLM Wiki, §6.6). It is built by code, never by the LLM.
type WikiIndex struct {
	CaseID     uuid.UUID          `json:"case_id"`
	Version    int                `json:"version"`
	Content    string             `json:"content"`
	Refs       map[string]WikiRef `json:"refs"`
	TokenCount int                `json:"token_count"`
	DocGens    map[string]int     `json:"doc_gens"`
	BuiltAt    time.Time          `json:"built_at"`
}

// WikiRef resolves a short index id: w<n> is a page, w<n>.n<k> a tree node of
// a source page, and a pending file line points at its document only.
type WikiRef struct {
	PageID     *uuid.UUID `json:"page_id,omitempty"`
	Slug       string     `json:"slug,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	DocumentID *uuid.UUID `json:"document_id,omitempty"`
	NodeID     string     `json:"node_id,omitempty"` // doc_tree_nodes.short_id
	// Collapsed marks a source page whose tree branches were cut to fit the
	// token budget; expand re-opens them.
	Collapsed bool `json:"collapsed,omitempty"`
}

// WikiTOC is the navigation of a case wiki for Module 3 (§7.2).
type WikiTOC struct {
	Case           Case          `json:"case"`
	Pages          []WikiPageRef `json:"pages"`
	Pending        []WikiPending `json:"pending,omitempty"`
	OpenLintIssues int           `json:"open_lint_issues"`
	DocumentsTotal int           `json:"documents_total"`
}

// WikiPageRef is a TOC entry.
type WikiPageRef struct {
	Slug       string     `json:"slug"`
	Title      string     `json:"title"`
	Kind       string     `json:"kind"`
	EntityType string     `json:"entity_type,omitempty"`
	Summary    string     `json:"summary"`
	DocumentID *uuid.UUID `json:"document_id,omitempty"`
	Proposed   bool       `json:"proposed,omitempty"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// WikiPending is a file not yet in the wiki.
type WikiPending struct {
	DocumentID uuid.UUID `json:"document_id"`
	FileName   string    `json:"file_name"`
	Status     string    `json:"status"`
	WikiStatus string    `json:"wiki_status"`
}
