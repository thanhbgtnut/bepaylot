package postgres

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/thanhenti/bepaylot/internal/types"
)

var metaKeyRe = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,64}$`)

// sqlArgs accumulates positional arguments.
type sqlArgs struct{ vals []any }

func (a *sqlArgs) add(v any) string {
	a.vals = append(a.vals, v)
	return fmt.Sprintf("$%d", len(a.vals))
}

// metaFilterSQL renders a metadata filter (§6.2) against the JSONB column col.
// Keys and values are always bound as parameters. The schema, when present,
// decides how range operators cast values (number | date | string).
func metaFilterSQL(col string, f types.MetadataFilter, schema *types.MetadataSchema, a *sqlArgs) (string, error) {
	if len(f) == 0 {
		return "", nil
	}
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		if !metaKeyRe.MatchString(k) {
			return "", fmt.Errorf("metadata filter: invalid key %q", k)
		}
		v := f[k]
		ops, isOps := v.(map[string]any)
		if !isOps {
			ops = map[string]any{"eq": v}
		}
		fieldType := ""
		if fd := schema.Field(k); fd != nil {
			fieldType = fd.Type
		}
		opNames := make([]string, 0, len(ops))
		for op := range ops {
			opNames = append(opNames, op)
		}
		sort.Strings(opNames)
		for _, op := range opNames {
			arg := ops[op]
			kp := a.add(k)
			text := fmt.Sprintf("(%s->>%s)", col, kp)
			switch op {
			case "eq":
				b, err := json.Marshal(map[string]any{k: arg})
				if err != nil {
					return "", err
				}
				// Containment matches scalars and array members alike.
				a.vals = a.vals[:len(a.vals)-1] // key not needed
				parts = append(parts, fmt.Sprintf("%s @> %s::jsonb", col, a.add(string(b))))
			case "ne":
				parts = append(parts, fmt.Sprintf("%s IS DISTINCT FROM %s", text, a.add(scalarText(arg))))
			case "in":
				list, ok := arg.([]any)
				if !ok {
					return "", fmt.Errorf("metadata filter: %q.in needs a list", k)
				}
				vals := make([]string, len(list))
				for i, x := range list {
					vals[i] = scalarText(x)
				}
				parts = append(parts, fmt.Sprintf("%s = ANY(%s::text[])", text, a.add(vals)))
			case "prefix":
				s := scalarText(arg)
				s = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
				parts = append(parts, fmt.Sprintf("%s LIKE %s", text, a.add(s+"%")))
			case "exists":
				want, _ := arg.(bool)
				expr := fmt.Sprintf("%s ? %s", col, kp)
				if !want {
					expr = "NOT (" + expr + ")"
				}
				parts = append(parts, expr)
			case "gt", "gte", "lt", "lte":
				sqlOp := map[string]string{"gt": ">", "gte": ">=", "lt": "<", "lte": "<="}[op]
				ft := fieldType
				if ft == "" {
					ft = inferType(arg)
				}
				switch ft {
				case "number":
					parts = append(parts, fmt.Sprintf(
						"(CASE WHEN %s ~ '^-?[0-9]+(\\.[0-9]+)?$' THEN %s::numeric END) %s %s::numeric", text, text, sqlOp, a.add(scalarText(arg))))
				case "date":
					parts = append(parts, fmt.Sprintf(
						"(CASE WHEN %s ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}' THEN left(%s, 10)::date END) %s %s::date", text, text, sqlOp, a.add(scalarText(arg))))
				default:
					parts = append(parts, fmt.Sprintf("%s %s %s", text, sqlOp, a.add(scalarText(arg))))
				}
			default:
				return "", fmt.Errorf("metadata filter: unknown operator %q on %q", op, k)
			}
		}
	}
	return strings.Join(parts, " AND "), nil
}

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)

func inferType(v any) string {
	switch x := v.(type) {
	case float64, float32, int, int64, json.Number:
		return "number"
	case string:
		if dateRe.MatchString(x) {
			return "date"
		}
	}
	return "string"
}

func scalarText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%g", x)
	default:
		b, _ := json.Marshal(x)
		return strings.Trim(string(b), `"`)
	}
}
