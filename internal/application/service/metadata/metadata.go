// Package metadata validates and normalizes optional per-file metadata
// (§6.2) against a KB's optional metadata schema. It is pure.
package metadata

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/thanhenti/bepaylot/internal/types"
)

var keyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// FieldError is one validation problem.
type FieldError struct {
	Key     string `json:"key"`
	Message string `json:"message"`
}

func (e FieldError) Error() string { return e.Key + ": " + e.Message }

// Merge overlays own on shared (own wins).
func Merge(shared, own map[string]any) map[string]any {
	out := make(map[string]any, len(shared)+len(own))
	for k, v := range shared {
		out[k] = v
	}
	for k, v := range own {
		out[k] = v
	}
	return out
}

// Validate checks and normalizes metadata. Without a schema, keys must be
// snake_case and values scalars or arrays of scalars. With a schema, declared
// fields are type-checked, normalized and required fields enforced; unknown
// keys are rejected only when the schema is strict.
func Validate(schema *types.MetadataSchema, in map[string]any) (map[string]any, []FieldError) {
	out := map[string]any{}
	var errs []FieldError
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := in[k]
		if !keyRe.MatchString(k) {
			errs = append(errs, FieldError{k, "key must be snake_case (a-z, 0-9, _), max 64 chars"})
			continue
		}
		if v == nil {
			continue
		}
		f := schema.Field(k)
		if f == nil {
			if schema != nil && schema.Strict {
				errs = append(errs, FieldError{k, "key is not declared in the metadata schema"})
				continue
			}
			nv, err := freeValue(v)
			if err != nil {
				errs = append(errs, FieldError{k, err.Error()})
				continue
			}
			out[k] = nv
			continue
		}
		nv, err := typedValue(*f, v)
		if err != nil {
			errs = append(errs, FieldError{k, err.Error()})
			continue
		}
		out[k] = nv
	}
	if schema != nil {
		for _, f := range schema.Fields {
			if _, ok := out[f.Key]; f.Required && !ok {
				errs = append(errs, FieldError{f.Key, "is required"})
			}
		}
	}
	return out, errs
}

func freeValue(v any) (any, error) {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x), nil
	case float64, bool, int, int64:
		return x, nil
	case []any:
		out := make([]any, 0, len(x))
		for _, e := range x {
			switch e.(type) {
			case string, float64, bool, int, int64:
				out = append(out, e)
			default:
				return nil, fmt.Errorf("arrays may only hold strings, numbers or booleans")
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("value must be a string, number, boolean or array of those")
	}
}

func typedValue(f types.MetadataField, v any) (any, error) {
	switch f.Type {
	case "", "string":
		s, ok := v.(string)
		if !ok {
			if n, ok := v.(float64); ok {
				s = strconv.FormatFloat(n, 'f', -1, 64)
			} else {
				return nil, fmt.Errorf("must be a string")
			}
		}
		return NormalizeString(f, s), nil
	case "number":
		switch x := v.(type) {
		case float64:
			return x, nil
		case string:
			n, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(x), ",", ""), 64)
			if err != nil {
				return nil, fmt.Errorf("must be a number")
			}
			return n, nil
		}
		return nil, fmt.Errorf("must be a number")
	case "bool":
		switch x := v.(type) {
		case bool:
			return x, nil
		case string:
			b, err := strconv.ParseBool(strings.TrimSpace(x))
			if err != nil {
				return nil, fmt.Errorf("must be true or false")
			}
			return b, nil
		}
		return nil, fmt.Errorf("must be true or false")
	case "date":
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("must be a date (YYYY-MM-DD)")
		}
		d, err := ParseDate(s)
		if err != nil {
			return nil, err
		}
		return d, nil
	case "enum":
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("must be one of %v", f.Values)
		}
		s = NormalizeString(f, s)
		for _, allowed := range f.Values {
			if strings.EqualFold(allowed, s) {
				return allowed, nil
			}
		}
		return nil, fmt.Errorf("must be one of %v", f.Values)
	}
	return nil, fmt.Errorf("unsupported field type %q", f.Type)
}

// NormalizeString applies a field's normalize rule.
func NormalizeString(f types.MetadataField, s string) string {
	s = strings.TrimSpace(s)
	switch f.Normalize {
	case "upper_trim":
		return strings.ToUpper(s)
	case "lower_trim":
		return strings.ToLower(s)
	}
	return s
}

// ParseDate accepts YYYY-MM-DD, DD/MM/YYYY and DD-MM-YYYY and returns ISO.
func ParseDate(s string) (string, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02", "02/01/2006", "02-01-2006", "2/1/2006", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02"), nil
		}
	}
	return "", fmt.Errorf("must be a date (YYYY-MM-DD or DD/MM/YYYY)")
}

// NormalizeFilter applies schema normalization to filter values so a user
// typing "hs-2026-000123 " matches the stored "HS-2026-000123".
func NormalizeFilter(schema *types.MetadataSchema, f types.MetadataFilter) types.MetadataFilter {
	if len(f) == 0 {
		return f
	}
	out := types.MetadataFilter{}
	for k, v := range f {
		fd := schema.Field(k)
		norm := func(x any) any {
			s, ok := x.(string)
			if !ok {
				return x
			}
			if fd == nil {
				return strings.TrimSpace(s)
			}
			if fd.Type == "date" {
				if d, err := ParseDate(s); err == nil {
					return d
				}
			}
			if fd.Type == "number" {
				if n, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
					return n
				}
			}
			return NormalizeString(*fd, s)
		}
		ops, ok := v.(map[string]any)
		if !ok {
			out[k] = norm(v)
			continue
		}
		nops := map[string]any{}
		for op, arg := range ops {
			if list, ok := arg.([]any); ok {
				nl := make([]any, len(list))
				for i, x := range list {
					nl[i] = norm(x)
				}
				nops[op] = nl
				continue
			}
			nops[op] = norm(arg)
		}
		out[k] = nops
	}
	return out
}

// ValidateSchema checks a metadata schema definition.
func ValidateSchema(s *types.MetadataSchema) error {
	if s == nil {
		return nil
	}
	seen := map[string]bool{}
	for _, f := range s.Fields {
		if !keyRe.MatchString(f.Key) {
			return fmt.Errorf("field key %q must be snake_case", f.Key)
		}
		if seen[f.Key] {
			return fmt.Errorf("field %q declared twice", f.Key)
		}
		seen[f.Key] = true
		switch f.Type {
		case "", "string", "number", "bool", "date":
		case "enum":
			if len(f.Values) == 0 {
				return fmt.Errorf("enum field %q needs values", f.Key)
			}
		default:
			return fmt.Errorf("field %q has unsupported type %q", f.Key, f.Type)
		}
		switch f.Normalize {
		case "", "none", "upper_trim", "lower_trim":
		default:
			return fmt.Errorf("field %q has unsupported normalize %q", f.Key, f.Normalize)
		}
	}
	return nil
}
