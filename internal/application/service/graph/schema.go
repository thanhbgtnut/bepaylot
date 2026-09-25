package graph

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thanhenti/bepaylot/internal/types"
)

var identRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

// GenericSchema is used by KBs that configure none (§7.2).
func GenericSchema() types.GraphSchema {
	str := func(n string) types.AttributeDef { return types.AttributeDef{Name: n, Type: "string"} }
	return types.GraphSchema{
		Name: "generic", Version: 1, Description: "Generic entities for any document",
		EntityTypes: []types.EntityTypeDef{
			{Name: "Person", Description: "A named person", Identity: []string{"id_number"}, Attributes: []types.AttributeDef{str("role"), str("id_number"), {Name: "birth_date", Type: "date"}}},
			{Name: "Organization", Description: "A company, agency, bank or other organization", Identity: []string{"tax_code"}, Attributes: []types.AttributeDef{str("tax_code"), str("address")}},
			{Name: "Location", Description: "An address or place", Attributes: []types.AttributeDef{str("kind")}},
			{Name: "Document", Description: "A referenced document, certificate, contract or decision", Identity: []string{"number"}, Attributes: []types.AttributeDef{str("number"), {Name: "date", Type: "date"}}},
			{Name: "Money", Description: "An amount of money with its purpose", Attributes: []types.AttributeDef{{Name: "amount", Type: "money"}, str("currency"), str("purpose")}},
			{Name: "Concept", Description: "A business line, product or other key concept", Attributes: []types.AttributeDef{str("code")}},
		},
		Relations: []types.RelationTypeDef{
			{Name: "WORKS_FOR", Source: "Person", Target: "Organization"},
			{Name: "OWNS", Source: "Person", Target: "Organization"},
			{Name: "LOCATED_AT", Source: "Organization", Target: "Location"},
			{Name: "LIVES_AT", Source: "Person", Target: "Location"},
			{Name: "ISSUED_BY", Source: "Document", Target: "Organization"},
			{Name: "CONCERNS", Source: "Document", Target: "Organization"},
			{Name: "HAS_AMOUNT", Source: "Organization", Target: "Money"},
			{Name: "RELATED_TO", Source: "Concept", Target: "Organization"},
		},
		Extraction: types.ExtractionSettings{Unit: "section", Instructions: "Extract only facts stated in the text. Do not guess."},
	}
}

// ValidateSchema checks a schema definition.
func ValidateSchema(s *types.GraphSchema) error {
	if !regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`).MatchString(s.Name) {
		return fmt.Errorf("schema name %q must be snake_case", s.Name)
	}
	if s.Version <= 0 {
		return fmt.Errorf("schema %s: version must be >= 1", s.Name)
	}
	if len(s.EntityTypes) == 0 {
		return fmt.Errorf("schema %s: at least one entity type is required", s.Name)
	}
	ents := map[string]bool{}
	for _, e := range s.EntityTypes {
		if !identRe.MatchString(e.Name) || ents[e.Name] {
			return fmt.Errorf("schema %s: invalid or duplicate entity type %q", s.Name, e.Name)
		}
		ents[e.Name] = true
		attrs := map[string]bool{}
		for _, a := range e.Attributes {
			if err := validateAttr(a); err != nil {
				return fmt.Errorf("schema %s: entity %s: %w", s.Name, e.Name, err)
			}
			attrs[a.Name] = true
		}
		for _, id := range e.Identity {
			if !attrs[id] {
				return fmt.Errorf("schema %s: entity %s: identity %q is not an attribute", s.Name, e.Name, id)
			}
		}
	}
	rels := map[string]bool{}
	for _, r := range s.Relations {
		if !identRe.MatchString(r.Name) || rels[r.Name] {
			return fmt.Errorf("schema %s: invalid or duplicate relation %q", s.Name, r.Name)
		}
		rels[r.Name] = true
		if !ents[r.Source] || !ents[r.Target] {
			return fmt.Errorf("schema %s: relation %s references unknown types %s→%s", s.Name, r.Name, r.Source, r.Target)
		}
		for _, a := range r.Attributes {
			if err := validateAttr(a); err != nil {
				return fmt.Errorf("schema %s: relation %s: %w", s.Name, r.Name, err)
			}
		}
	}
	switch s.Extraction.Unit {
	case "", "section", "page":
	default:
		return fmt.Errorf("schema %s: extraction.unit must be section or page", s.Name)
	}
	return nil
}

func validateAttr(a types.AttributeDef) error {
	if !identRe.MatchString(a.Name) {
		return fmt.Errorf("invalid attribute name %q", a.Name)
	}
	switch a.Type {
	case "", "string", "number", "money", "date", "bool":
	default:
		return fmt.Errorf("attribute %s: unsupported type %q", a.Name, a.Type)
	}
	if a.Pattern != "" {
		if _, err := regexp.Compile(a.Pattern); err != nil {
			return fmt.Errorf("attribute %s: bad pattern: %w", a.Name, err)
		}
	}
	return nil
}

// LoadSchemaFiles reads every *.yaml/*.yml schema in dir.
func LoadSchemaFiles(dir string) ([]types.GraphSchema, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []types.GraphSchema
	for _, e := range entries {
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if e.IsDir() || (ext != ".yaml" && ext != ".yml") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var s types.GraphSchema
		if err := yaml.Unmarshal(b, &s); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		if err := ValidateSchema(&s); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		out = append(out, s)
	}
	return out, nil
}
