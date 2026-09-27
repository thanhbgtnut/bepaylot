package wiki

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thanhenti/bepaylot/internal/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
)

// GenericSchema is the built-in schema of case types that name none (§6.7):
// organizations, people, places and assets, with a free relation.
func GenericSchema() types.WikiSchema {
	str := func(n string) types.AttributeDef { return types.AttributeDef{Name: n, Type: "string"} }
	s := types.WikiSchema{
		Name: "generic", Version: 1, Language: "vi", Description: "Schema chung cho mọi loại hồ sơ",
		EntityTypes: []types.WikiEntityType{
			{Name: "to_chuc", Title: "Tổ chức", Identity: []string{"ma_so_thue"},
				Attributes: []types.AttributeDef{{Name: "ten", Type: "string", Required: true}, str("ma_so_thue"), str("dia_chi")}},
			{Name: "ca_nhan", Title: "Cá nhân", Identity: []string{"so_dinh_danh"},
				Attributes: []types.AttributeDef{{Name: "ho_ten", Type: "string", Required: true}, str("so_dinh_danh"), {Name: "ngay_sinh", Type: "date"}}},
			{Name: "dia_diem", Title: "Địa điểm", Attributes: []types.AttributeDef{{Name: "ten", Type: "string", Required: true}, str("dia_chi")}},
			{Name: "tai_san", Title: "Tài sản", Attributes: []types.AttributeDef{{Name: "ten", Type: "string", Required: true}, {Name: "gia_tri", Type: "money"}}},
		},
		Relations: []types.WikiRelationType{
			{Name: "lien_quan", Description: "quan hệ tự do giữa hai đối tượng",
				From: types.StringList{"to_chuc", "ca_nhan", "dia_diem", "tai_san"}, To: types.StringList{"to_chuc", "ca_nhan", "dia_diem", "tai_san"},
				Attributes: []types.AttributeDef{str("vai_tro")}},
		},
		Topics: []string{"Dòng thời gian"},
		Conventions: `Viết tiếng Việt, câu ngắn, trung lập.
Mọi con số, ngày tháng, tên riêng, số hiệu phải có chú thích về dòng gốc.
Không suy đoán, không kết luận đúng/sai hay tuân thủ.
Hai nguồn ghi khác nhau thì nêu cả hai kèm chú thích.`,
	}
	s.Limits.MaxPagesTouched, s.Limits.MaxPages = 15, 200
	return s
}

var (
	schemaNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	identRe      = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

// ValidateSchema checks a schema (§6.7): names, attribute types, identity
// attributes declared, relations pointing at real entity types.
func ValidateSchema(s *types.WikiSchema) error {
	if !schemaNameRe.MatchString(s.Name) {
		return fmt.Errorf("schema name %q must be snake_case", s.Name)
	}
	if s.Version <= 0 {
		return fmt.Errorf("schema %s: version must be >= 1", s.Name)
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
		if len(r.From) == 0 || len(r.To) == 0 {
			return fmt.Errorf("schema %s: relation %s needs from and to", s.Name, r.Name)
		}
		for _, t := range append(append([]string{}, r.From...), r.To...) {
			if !ents[t] {
				return fmt.Errorf("schema %s: relation %s references unknown entity type %q", s.Name, r.Name, t)
			}
		}
		for _, a := range r.Attributes {
			if err := validateAttr(a); err != nil {
				return fmt.Errorf("schema %s: relation %s: %w", s.Name, r.Name, err)
			}
		}
	}
	if s.Limits.MaxPagesTouched < 0 || s.Limits.MaxPages < 0 {
		return fmt.Errorf("schema %s: limits must be >= 0", s.Name)
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

// LoadSchemaFiles reads every *.yaml / *.yml schema of dir.
func LoadSchemaFiles(dir string) ([]types.WikiSchema, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) || dir == "" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []types.WikiSchema
	for _, e := range entries {
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if e.IsDir() || (ext != ".yaml" && ext != ".yml") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var s types.WikiSchema
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

var (
	nameStrip  = regexp.MustCompile(`[^\p{L}\p{N} ]+`)
	namePrefix = []string{"cong ty co phan ", "cong ty tnhh mot thanh vien ", "cong ty tnhh ", "cong ty ", "ctcp ", "tnhh ",
		"ngan hang tmcp ", "ngan hang ", "ho kinh doanh ", "ong ", "ba ", "anh ", "chi "}
	identStrip = regexp.MustCompile(`[\s.\-_/]+`)
)

// NormName folds a name for entity resolution: accent-free, lower-case,
// punctuation and common legal/honorific prefixes removed (§6.8 step 3).
func NormName(name string) string {
	n := textutil.Normalize(name)
	n = textutil.CollapseSpace(nameStrip.ReplaceAllString(n, " "))
	for _, p := range namePrefix {
		n = strings.TrimPrefix(n, p)
	}
	return n
}

// IdentityKey is the normalized identity of an entity: every identity
// attribute of its type, without spaces, dots or dashes, upper-cased
// ("0101-234.567" → "0101234567"). Empty when one is missing.
func IdentityKey(def *types.WikiEntityType, attrs map[string]any) string {
	if def == nil || len(def.Identity) == 0 {
		return ""
	}
	var parts []string
	for _, id := range def.Identity {
		v, ok := attrs[id]
		s := strings.ToUpper(identStrip.ReplaceAllString(strings.TrimSpace(fmt.Sprint(v)), ""))
		if !ok || v == nil || s == "" || s == "<NIL>" {
			return ""
		}
		parts = append(parts, textutil.Unaccent(s))
	}
	return strings.ToUpper(strings.Join(parts, "|"))
}

var slugStrip = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify turns a title into a slug part: "Công ty A" → "cong-ty-a".
func Slugify(s string) string {
	s = slugStrip.ReplaceAllString(textutil.Unaccent(s), "-")
	s = strings.Trim(s, "-")
	if len(s) > 60 {
		s = strings.Trim(s[:60], "-")
	}
	if s == "" {
		s = "trang"
	}
	return s
}

// entityPrefix is the slug prefix of an entity type: "to_chuc" → "to-chuc".
func entityPrefix(t string) string { return strings.ReplaceAll(t, "_", "-") }
