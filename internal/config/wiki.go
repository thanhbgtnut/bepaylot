package config

import "time"

// Cases configures case types (§6.2).
type Cases struct {
	// TypesDir holds one YAML file per case type.
	TypesDir string `yaml:"types_dir"`
	// DefaultType is used when an upload or create names no case type.
	DefaultType string `yaml:"default_type"`
	// AutoCreateOnUpload creates the case of an unknown case_code at upload
	// (default true); false requires POST /kbs/:id/cases first.
	AutoCreateOnUpload *bool `yaml:"auto_create_on_upload"`
}

// AutoCreate defaults to true.
func (c Cases) AutoCreate() bool { return c.AutoCreateOnUpload == nil || *c.AutoCreateOnUpload }

// Wiki configures the LLM Wiki of each case (§6.6–6.9).
type Wiki struct {
	// EnabledByDefault applies to case types without wiki.enabled (default true).
	EnabledByDefault *bool  `yaml:"enabled_by_default"`
	SchemasDir       string `yaml:"schemas_dir"`
	DefaultSchema    string `yaml:"default_schema"`
	Provider         string `yaml:"provider"`
	// Model extracts entities and relations for the wiki.
	Model           string `yaml:"model"`
	StrictCitations bool   `yaml:"strict_citations"`
	Index           struct {
		MaxTokens int `yaml:"max_tokens"`
	} `yaml:"index"`
	Ingest struct {
		// Extract turns on the one LLM call per file (per part of a big
		// file) that finds entities and relations (default true). Off, the
		// wiki holds the source pages and the overview only: 0 LLM calls.
		Extract        *bool   `yaml:"extract"`
		DocTokenBudget int     `yaml:"doc_token_budget"`
		Parallel       int     `yaml:"parallel"`
		MaxLLMCalls    int     `yaml:"max_llm_calls"`
		MaxEntities    int     `yaml:"max_entities"`
		NameSimilarity float64 `yaml:"name_similarity"`
	} `yaml:"ingest"`
	Lint struct {
		Interval time.Duration `yaml:"interval"`
		LLM      bool          `yaml:"llm"`
	} `yaml:"lint"`
}

// Enabled reports the default for case types that do not decide.
func (w Wiki) Enabled() bool { return w.EnabledByDefault == nil || *w.EnabledByDefault }

// ExtractEnabled reports whether ingest calls the LLM to extract entities.
func (w Wiki) ExtractEnabled() bool { return w.Ingest.Extract == nil || *w.Ingest.Extract }

func (c *Config) applyWikiDefaults() {
	setString(&c.Cases.TypesDir, "configs/case_types")
	setString(&c.Cases.DefaultType, "default")
	w := &c.Wiki
	setString(&w.SchemasDir, "configs/wiki_schemas")
	setString(&w.DefaultSchema, "generic")
	setInt(&w.Index.MaxTokens, 6000)
	setInt(&w.Ingest.DocTokenBudget, 24000)
	setInt(&w.Ingest.Parallel, 2)
	setInt(&w.Ingest.MaxLLMCalls, 8)
	setInt(&w.Ingest.MaxEntities, 40)
	if w.Ingest.NameSimilarity == 0 {
		w.Ingest.NameSimilarity = 0.85
	}
	setDuration(&w.Lint.Interval, 24*time.Hour)
}
