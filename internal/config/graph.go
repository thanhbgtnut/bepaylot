package config

import "time"

// Graph configures schema-driven entity/relation extraction.
type Graph struct {
	EnabledByDefault bool   `yaml:"enabled_by_default"`
	DefaultSchema    string `yaml:"default_schema"`
	SchemaDir        string `yaml:"schema_dir"`
	BatchSize        int    `yaml:"batch_size"`
	Provider         string `yaml:"provider"`
	Model            string `yaml:"model"`
	Resolve          struct {
		NameSimilarity float64 `yaml:"name_similarity"`
		LLMConfirm     bool    `yaml:"llm_confirm"`
	} `yaml:"resolve"`
}

// Wiki configures entity wiki page generation.
type Wiki struct {
	MinMentions int           `yaml:"min_mentions"`
	PageTypes   []string      `yaml:"page_types"`
	IngestDelay time.Duration `yaml:"ingest_delay"`
}

func (c *Config) applyGraphDefaults() {
	setString(&c.Graph.DefaultSchema, "generic")
	setString(&c.Graph.SchemaDir, "configs/graph_schemas")
	setInt(&c.Graph.BatchSize, 8)
	if c.Graph.Resolve.NameSimilarity == 0 {
		c.Graph.Resolve.NameSimilarity = 0.85
	}
	setInt(&c.Wiki.MinMentions, 2)
	setDuration(&c.Wiki.IngestDelay, 30*time.Second)
}
