package types

import (
	"time"

	"github.com/google/uuid"
)

// GraphSchema is a versioned extraction schema (§7.2).
type GraphSchema struct {
	ID          uuid.UUID          `json:"id"`
	Name        string             `json:"name" yaml:"name"`
	Version     int                `json:"version" yaml:"version"`
	Description string             `json:"description,omitempty" yaml:"description"`
	Model       string             `json:"model,omitempty" yaml:"model"`
	EntityTypes []EntityTypeDef    `json:"entity_types" yaml:"entity_types"`
	Relations   []RelationTypeDef  `json:"relation_types" yaml:"relation_types"`
	Extraction  ExtractionSettings `json:"extraction" yaml:"extraction"`
	CreatedAt   time.Time          `json:"created_at,omitempty" yaml:"-"`
}

// EntityTypeDef declares one entity type.
type EntityTypeDef struct {
	Name        string         `json:"name" yaml:"name"`
	Description string         `json:"description,omitempty" yaml:"description"`
	Identity    []string       `json:"identity,omitempty" yaml:"identity"`
	Attributes  []AttributeDef `json:"attributes,omitempty" yaml:"attributes"`
	// ResolveScope lists document metadata keys that must match for two
	// mentions to merge (e.g. ma_ho_so).
	ResolveScope []string `json:"resolve_scope,omitempty" yaml:"resolve_scope"`
}

// RelationTypeDef declares one relation type.
type RelationTypeDef struct {
	Name        string         `json:"name" yaml:"name"`
	Description string         `json:"description,omitempty" yaml:"description"`
	Source      string         `json:"source" yaml:"source"`
	Target      string         `json:"target" yaml:"target"`
	Attributes  []AttributeDef `json:"attributes,omitempty" yaml:"attributes"`
}

// AttributeDef declares a typed attribute.
type AttributeDef struct {
	Name     string `json:"name" yaml:"name"`
	Type     string `json:"type" yaml:"type"` // string | number | money | date | bool
	Required bool   `json:"required,omitempty" yaml:"required"`
	Pattern  string `json:"pattern,omitempty" yaml:"pattern"`
}

// ExtractionSettings tunes extraction.
type ExtractionSettings struct {
	Unit         string `json:"unit,omitempty" yaml:"unit"` // section | page
	Instructions string `json:"instructions,omitempty" yaml:"instructions"`
	Examples     []any  `json:"examples,omitempty" yaml:"examples"`
}

// EntityType returns the declared entity type or nil.
func (s *GraphSchema) EntityType(name string) *EntityTypeDef {
	for i := range s.EntityTypes {
		if s.EntityTypes[i].Name == name {
			return &s.EntityTypes[i]
		}
	}
	return nil
}

// RelationType returns the declared relation type or nil.
func (s *GraphSchema) RelationType(name string) *RelationTypeDef {
	for i := range s.Relations {
		if s.Relations[i].Name == name {
			return &s.Relations[i]
		}
	}
	return nil
}

// Entity is a resolved graph node.
type Entity struct {
	ID         uuid.UUID      `json:"id"`
	KBID       uuid.UUID      `json:"kb_id"`
	Type       string         `json:"type"`
	Name       string         `json:"name"`
	Aliases    []string       `json:"aliases,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
	Conflict   bool           `json:"conflict,omitempty"`
	Summary    string         `json:"summary,omitempty"`
	Mentions   int            `json:"mentions,omitempty"`
	WikiSlug   string         `json:"wiki_slug,omitempty"`
}

// Relation is a resolved graph edge.
type Relation struct {
	ID         uuid.UUID      `json:"id"`
	Type       string         `json:"type"`
	SourceID   uuid.UUID      `json:"source_id"`
	TargetID   uuid.UUID      `json:"target_id"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// Mention is the evidence for an entity or relation.
type Mention struct {
	ID          int64        `json:"id"`
	EntityID    *uuid.UUID   `json:"entity_id,omitempty"`
	RelationID  *uuid.UUID   `json:"relation_id,omitempty"`
	DocumentID  uuid.UUID    `json:"document_id"`
	FileName    string       `json:"file_name,omitempty"`
	SectionID   *uuid.UUID   `json:"section_id,omitempty"`
	Evidence    string       `json:"evidence"`
	SourceSpans []SourceSpan `json:"source_spans"`
}

// Subgraph is a neighbourhood result.
type Subgraph struct {
	Entities  []Entity   `json:"entities"`
	Relations []Relation `json:"relations"`
}

// WikiPage is an entity wiki page (§7.5).
type WikiPage struct {
	ID             uuid.UUID  `json:"id"`
	KBID           uuid.UUID  `json:"kb_id"`
	EntityID       *uuid.UUID `json:"entity_id,omitempty"`
	Slug           string     `json:"slug"`
	Title          string     `json:"title"`
	PageType       string     `json:"page_type"`
	Summary        string     `json:"summary"`
	Content        string     `json:"content"`
	Aliases        []string   `json:"aliases,omitempty"`
	SourceRefs     []string   `json:"source_refs,omitempty"`
	InLinks        []string   `json:"in_links,omitempty"`
	OutLinks       []string   `json:"out_links,omitempty"`
	Version        int        `json:"version"`
	LastEditSource string     `json:"last_edit_source"`
	UpdatedAt      time.Time  `json:"updated_at"`
}
