package graph

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/thanhenti/bepaylot/internal/application/service/metadata"
	"github.com/thanhenti/bepaylot/internal/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
)

// Extraction is the LLM output for one batch (§7.3).
type Extraction struct {
	Entities  []ExtractedEntity   `json:"entities"`
	Relations []ExtractedRelation `json:"relations"`
}

// ExtractedEntity is one entity as returned by the model.
type ExtractedEntity struct {
	Type       string         `json:"type"`
	Name       string         `json:"name"`
	Attributes map[string]any `json:"attributes"`
	Evidence   string         `json:"evidence"`
	Unit       string         `json:"unit"`
}

// EntityRef identifies a relation endpoint by type and name.
type EntityRef struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

// ExtractedRelation is one relation as returned by the model.
type ExtractedRelation struct {
	Type       string         `json:"type"`
	Source     EntityRef      `json:"source"`
	Target     EntityRef      `json:"target"`
	Attributes map[string]any `json:"attributes"`
	Evidence   string         `json:"evidence"`
	Unit       string         `json:"unit"`
}

const promptExtract = `You extract a knowledge graph from document text, following a fixed schema.
Schema:
%s
Rules:
- Only use the entity and relation types of the schema; only their declared attributes.
- Extract only facts stated in the text; never guess or complete missing values.
- Every entity and relation needs "evidence": a verbatim quote (one sentence or line) from the text that states it.
- "unit" is the id of the text unit the evidence comes from.
- Names are written as in the text (keep Vietnamese diacritics).
- Dates as YYYY-MM-DD; money and numbers as plain numbers without separators.
%s
Reply with JSON only: {"entities": [{"type": "...", "name": "...", "attributes": {...}, "evidence": "...", "unit": "u1"}],
 "relations": [{"type": "...", "source": {"type": "...", "name": "..."}, "target": {"type": "...", "name": "..."}, "attributes": {...}, "evidence": "...", "unit": "u1"}]}`

// ExtractPrompt renders the system prompt from a schema.
func ExtractPrompt(s types.GraphSchema) string {
	var sb strings.Builder
	sb.WriteString("Entity types:\n")
	for _, e := range s.EntityTypes {
		fmt.Fprintf(&sb, "- %s: %s. Attributes: ", e.Name, e.Description)
		var attrs []string
		for _, a := range e.Attributes {
			t := a.Type
			if t == "" {
				t = "string"
			}
			req := ""
			if a.Required {
				req = ", required"
			}
			attrs = append(attrs, fmt.Sprintf("%s (%s%s)", a.Name, t, req))
		}
		sb.WriteString(strings.Join(attrs, ", "))
		sb.WriteString("\n")
	}
	sb.WriteString("Relation types:\n")
	for _, r := range s.Relations {
		fmt.Fprintf(&sb, "- %s: %s → %s", r.Name, r.Source, r.Target)
		if r.Description != "" {
			fmt.Fprintf(&sb, " (%s)", r.Description)
		}
		if len(r.Attributes) > 0 {
			var attrs []string
			for _, a := range r.Attributes {
				attrs = append(attrs, a.Name)
			}
			fmt.Fprintf(&sb, ". Attributes: %s", strings.Join(attrs, ", "))
		}
		sb.WriteString("\n")
	}
	extra := ""
	if s.Extraction.Instructions != "" {
		extra = "- " + strings.TrimSpace(s.Extraction.Instructions)
	}
	if len(s.Extraction.Examples) > 0 {
		b, _ := json.Marshal(s.Extraction.Examples)
		extra += "\nExamples: " + string(b)
	}
	return fmt.Sprintf(promptExtract, sb.String(), extra)
}

// Warning records a dropped or corrected item.
type Warning struct {
	Kind   string `json:"kind"`
	Item   string `json:"item"`
	Reason string `json:"reason"`
}

// ValidateExtraction enforces the schema (§7.3 step 3): unknown types and
// attributes are dropped, values are coerced to their declared types,
// required attributes must be present, relations must connect the declared
// endpoint types. unitText maps unit id → text for evidence checks (§7.3 step
// 4); items whose evidence is not found in their unit are dropped.
func ValidateExtraction(s types.GraphSchema, ex Extraction, unitText map[string]string) (Extraction, []Warning) {
	var out Extraction
	var warns []Warning
	for _, e := range ex.Entities {
		def := s.EntityType(e.Type)
		label := e.Type + ":" + e.Name
		if def == nil {
			warns = append(warns, Warning{"entity", label, "type not in schema"})
			continue
		}
		e.Name = textutil.CollapseSpace(e.Name)
		if e.Name == "" {
			warns = append(warns, Warning{"entity", label, "empty name"})
			continue
		}
		attrs, reason := coerceAttrs(def.Attributes, e.Attributes)
		if reason != "" {
			warns = append(warns, Warning{"entity", label, reason})
			continue
		}
		unit, ok := evidenceUnit(e.Evidence, e.Unit, unitText)
		if !ok {
			warns = append(warns, Warning{"entity", label, "evidence not found in source text"})
			continue
		}
		e.Attributes, e.Unit = attrs, unit
		out.Entities = append(out.Entities, e)
	}
	for _, r := range ex.Relations {
		def := s.RelationType(r.Type)
		label := r.Type + ":" + r.Source.Name + "→" + r.Target.Name
		if def == nil {
			warns = append(warns, Warning{"relation", label, "type not in schema"})
			continue
		}
		if r.Source.Type != def.Source || r.Target.Type != def.Target {
			warns = append(warns, Warning{"relation", label, fmt.Sprintf("must connect %s→%s", def.Source, def.Target)})
			continue
		}
		attrs, reason := coerceAttrs(def.Attributes, r.Attributes)
		if reason != "" {
			warns = append(warns, Warning{"relation", label, reason})
			continue
		}
		unit, ok := evidenceUnit(r.Evidence, r.Unit, unitText)
		if !ok {
			warns = append(warns, Warning{"relation", label, "evidence not found in source text"})
			continue
		}
		r.Attributes, r.Unit = attrs, unit
		r.Source.Name, r.Target.Name = textutil.CollapseSpace(r.Source.Name), textutil.CollapseSpace(r.Target.Name)
		out.Relations = append(out.Relations, r)
	}
	return out, warns
}

func coerceAttrs(defs []types.AttributeDef, in map[string]any) (map[string]any, string) {
	out := map[string]any{}
	for _, d := range defs {
		v, ok := in[d.Name]
		if !ok || v == nil || fmt.Sprint(v) == "" {
			if d.Required {
				return nil, "missing required attribute " + d.Name
			}
			continue
		}
		cv, err := coerce(d, v)
		if err != nil {
			if d.Required {
				return nil, fmt.Sprintf("attribute %s: %v", d.Name, err)
			}
			continue
		}
		out[d.Name] = cv
	}
	return out, ""
}

var nonDigits = regexp.MustCompile(`[^0-9\-]`)

func coerce(d types.AttributeDef, v any) (any, error) {
	switch d.Type {
	case "number", "money":
		switch x := v.(type) {
		case float64:
			return x, nil
		case string:
			s := strings.TrimSpace(x)
			if d.Type == "money" {
				s = nonDigits.ReplaceAllString(s, "") // "50.000.000 đồng" → 50000000
			} else {
				s = strings.ReplaceAll(s, ",", "")
			}
			n, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return nil, fmt.Errorf("not a number")
			}
			return n, nil
		}
		return nil, fmt.Errorf("not a number")
	case "date":
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("not a date")
		}
		return metadata.ParseDate(s)
	case "bool":
		switch x := v.(type) {
		case bool:
			return x, nil
		case string:
			return strconv.ParseBool(strings.TrimSpace(x))
		}
		return nil, fmt.Errorf("not a boolean")
	default:
		s := strings.TrimSpace(fmt.Sprint(v))
		if d.Pattern != "" && !regexp.MustCompile(d.Pattern).MatchString(s) {
			return nil, fmt.Errorf("does not match %s", d.Pattern)
		}
		return s, nil
	}
}

// evidenceUnit finds the unit whose text contains the evidence (accent- and
// space-insensitive), preferring the unit the model named.
func evidenceUnit(evidence, unit string, units map[string]string) (string, bool) {
	ev := textutil.Normalize(evidence)
	if len([]rune(ev)) < 3 {
		return "", false
	}
	found := func(text string) bool {
		t := textutil.Normalize(text)
		if strings.Contains(t, ev) {
			return true
		}
		// Tolerate small OCR differences line by line.
		for _, line := range strings.Split(text, "\n") {
			if textutil.Similarity(textutil.Normalize(line), ev) >= 0.85 {
				return true
			}
		}
		return false
	}
	if t, ok := units[unit]; ok && found(t) {
		return unit, true
	}
	keys := make([]string, 0, len(units))
	for k := range units {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if found(units[k]) {
			return k, true
		}
	}
	return "", false
}

var (
	nameStrip  = regexp.MustCompile(`[^\p{L}\p{N} ]+`)
	namePrefix = []string{"cong ty co phan ", "cong ty tnhh ", "cong ty ", "ctcp ", "tnhh ", "ho kinh doanh ", "ong ", "ba ", "anh ", "chi "}
)

// NormName folds a name for merging: accent-free, lower-case, punctuation and
// common legal/honorific prefixes removed.
func NormName(name string) string {
	n := textutil.Normalize(name)
	n = textutil.CollapseSpace(nameStrip.ReplaceAllString(n, " "))
	for _, p := range namePrefix {
		n = strings.TrimPrefix(n, p)
	}
	return n
}

// NormKey is the merge key of an entity (§7.4): identity attributes when all
// are present, else the normalized name; optionally scoped by document
// metadata values (resolve_scope).
func NormKey(def *types.EntityTypeDef, name string, attrs map[string]any, docMeta map[string]any) string {
	var parts []string
	for _, k := range def.ResolveScope {
		parts = append(parts, fmt.Sprintf("%s=%v", k, docMeta[k]))
	}
	var ids []string
	for _, id := range def.Identity {
		v, ok := attrs[id]
		if !ok || fmt.Sprint(v) == "" {
			ids = nil
			break
		}
		ids = append(ids, id+"="+textutil.Normalize(fmt.Sprint(v)))
	}
	if len(ids) > 0 {
		parts = append(parts, ids...)
	} else {
		parts = append(parts, "name="+NormName(name))
	}
	return strings.Join(parts, "|")
}
