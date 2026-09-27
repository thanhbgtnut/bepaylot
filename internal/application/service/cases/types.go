package cases

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/thanhenti/bepaylot/internal/application/service/metadata"
	"github.com/thanhenti/bepaylot/internal/types"
)

// LoadTypes reads every *.yaml / *.yml file of dir as a case type (§6.2). A
// missing dir yields no types; the built-in default type is always present.
func LoadTypes(dir, defaultName string) (map[string]types.CaseType, error) {
	out := map[string]types.CaseType{}
	if dir != "" {
		entries, err := os.ReadDir(dir)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		for _, e := range entries {
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if e.IsDir() || (ext != ".yaml" && ext != ".yml") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				return nil, err
			}
			var t types.CaseType
			if err := yaml.Unmarshal(b, &t); err != nil {
				return nil, fmt.Errorf("%s: %w", e.Name(), err)
			}
			if t.Name == "" {
				t.Name = strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
			}
			if err := ValidateType(t); err != nil {
				return nil, fmt.Errorf("%s: %w", e.Name(), err)
			}
			if _, dup := out[t.Name]; dup {
				return nil, fmt.Errorf("%s: case type %q declared twice", e.Name(), t.Name)
			}
			out[t.Name] = t
		}
	}
	if defaultName == "" {
		defaultName = types.DefaultCaseType
	}
	if _, ok := out[defaultName]; !ok {
		out[defaultName] = types.CaseType{Name: defaultName, Title: "Mặc định"}
	}
	return out, nil
}

var typeNameRe = regexp.MustCompile(`^[a-z0-9_][a-z0-9_-]{0,63}$`)

// ValidateType checks a case type's name, code rule and metadata schemas.
func ValidateType(t types.CaseType) error {
	if !typeNameRe.MatchString(t.Name) {
		return fmt.Errorf("invalid case type name %q (lowercase letters, digits, _ and -)", t.Name)
	}
	if t.Code.Pattern != "" {
		if _, err := regexp.Compile(t.Code.Pattern); err != nil {
			return fmt.Errorf("code.pattern: %w", err)
		}
	}
	switch t.Code.Normalize {
	case "", "trim", "upper_trim", "lower_trim", "none":
	default:
		return fmt.Errorf("code.normalize %q is not supported (upper_trim | lower_trim | trim | none)", t.Code.Normalize)
	}
	if err := metadata.ValidateSchema(t.CaseMetadataSchema); err != nil {
		return fmt.Errorf("case_metadata_schema: %w", err)
	}
	if err := metadata.ValidateSchema(t.MetadataSchema); err != nil {
		return fmt.Errorf("metadata_schema: %w", err)
	}
	return nil
}

// NormalizeCode applies a code rule's normalization: " rt112233" becomes
// "RT112233" with upper_trim. Every rule trims except "none".
func NormalizeCode(rule types.CaseCodeRule, raw string) string {
	switch rule.Normalize {
	case "none":
		return raw
	case "upper_trim":
		return strings.ToUpper(strings.TrimSpace(raw))
	case "lower_trim":
		return strings.ToLower(strings.TrimSpace(raw))
	}
	return strings.TrimSpace(raw)
}

// CheckCode validates a normalized code against the rule.
func CheckCode(rule types.CaseCodeRule, code string) error {
	if code == "" {
		return fmt.Errorf("case code is empty")
	}
	if len(code) > 200 {
		return fmt.Errorf("case code is longer than 200 characters")
	}
	for _, r := range code {
		if unicode.IsControl(r) {
			return fmt.Errorf("case code contains control characters")
		}
	}
	if rule.Pattern != "" {
		if ok, _ := regexp.MatchString(rule.Pattern, code); !ok {
			return fmt.Errorf("case code %q does not match pattern %s", code, rule.Pattern)
		}
	}
	return nil
}

func sortedTypes(m map[string]types.CaseType) []types.CaseType {
	out := make([]types.CaseType, 0, len(m))
	for _, t := range m {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
