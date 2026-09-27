package config

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

func (c *Config) applyCasesDefaults() {
	setString(&c.Cases.TypesDir, "configs/case_types")
	setString(&c.Cases.DefaultType, "default")
}
