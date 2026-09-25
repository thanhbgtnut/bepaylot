package types

// MetadataSchema optionally declares the per-file metadata fields of a KB
// (§6.2). Without a schema, metadata is free-form JSON.
type MetadataSchema struct {
	Fields []MetadataField `json:"fields" yaml:"fields"`
	// Strict rejects keys that are not declared.
	Strict bool `json:"strict,omitempty" yaml:"strict"`
}

// MetadataField declares one metadata key.
type MetadataField struct {
	Key         string   `json:"key" yaml:"key"`
	Type        string   `json:"type" yaml:"type"` // string | number | bool | date | enum
	Required    bool     `json:"required,omitempty" yaml:"required"`
	Description string   `json:"description,omitempty" yaml:"description"`
	Values      []string `json:"values,omitempty" yaml:"values"`       // enum
	Normalize   string   `json:"normalize,omitempty" yaml:"normalize"` // upper_trim | lower_trim | none
	Indexed     bool     `json:"indexed,omitempty" yaml:"indexed"`
}

// Field returns the declared field for key, or nil.
func (s *MetadataSchema) Field(key string) *MetadataField {
	if s == nil {
		return nil
	}
	for i := range s.Fields {
		if s.Fields[i].Key == key {
			return &s.Fields[i]
		}
	}
	return nil
}

// MetadataFilter maps a metadata key to either a literal (equality) or an
// operator object: {"eq"|"in"|"gte"|"gt"|"lte"|"lt"|"prefix"|"exists": ...}.
type MetadataFilter map[string]any
