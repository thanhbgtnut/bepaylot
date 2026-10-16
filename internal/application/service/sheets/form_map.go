package sheets

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/thanhenti/bepaylot/docpipe/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
	"github.com/thanhenti/bepaylot/internal/types/interfaces"
)

// formFuncs are the Excel functions a mapping formula may use.
var formFuncs = map[string]bool{
	"SUM": true, "SUMIF": true, "SUMIFS": true, "SUMPRODUCT": true, "COUNT": true, "COUNTA": true,
	"COUNTIF": true, "COUNTIFS": true, "AVERAGE": true, "MIN": true, "MAX": true, "ROUND": true,
	"ROUNDUP": true, "ROUNDDOWN": true, "ABS": true, "IF": true, "IFERROR": true, "AND": true, "OR": true,
}

var (
	reFormRef  = regexp.MustCompile(`\$?([A-Z]{1,3})\$?\{(r|first|last)\}`)
	reFormFunc = regexp.MustCompile(`([A-Za-z][A-Za-z0-9.]*)\s*\(`)
	reFormStr  = regexp.MustCompile(`"[^"]*"`)
	reFormRest = regexp.MustCompile(`^[0-9.,:;+\-*/^&%<>=()\s]*$`)
)

// checkFormula validates a mapping formula: only the functions of formFuncs,
// cell references to columns of the form with a row token ({r} in a record,
// {first}/{last} anywhere), no fixed row number, so the formula stays right
// when rows are inserted. It returns the formula without its leading "=".
func checkFormula(formula string, cols map[string]bool, total bool) (string, error) {
	f := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(formula), "="))
	if f == "" {
		return "", nil
	}
	rest := reFormStr.ReplaceAllString(f, `""`)
	for _, m := range reFormRef.FindAllStringSubmatch(rest, -1) {
		if !cols[m[1]] {
			return "", fmt.Errorf("công thức %q tham chiếu cột %s không có trong bảng", formula, m[1])
		}
		if total && m[2] == "r" {
			return "", fmt.Errorf("công thức dòng tổng %q không dùng được {r}, chỉ {first} và {last}", formula)
		}
	}
	rest = reFormRef.ReplaceAllString(rest, "0")
	for _, m := range reFormFunc.FindAllStringSubmatch(rest, -1) {
		if !formFuncs[strings.ToUpper(m[1])] {
			return "", fmt.Errorf("công thức %q dùng hàm %s không được hỗ trợ", formula, m[1])
		}
	}
	rest = reFormFunc.ReplaceAllString(rest, "(")
	rest = strings.ReplaceAll(rest, `""`, "0")
	if !reFormRest.MatchString(rest) {
		return "", fmt.Errorf("công thức %q có tham chiếu không hợp lệ: ô phải viết dạng D{r}, D{first}, D{last}", formula)
	}
	if strings.Count(rest, "(") != strings.Count(rest, ")") {
		return "", fmt.Errorf("công thức %q thiếu dấu ngoặc", formula)
	}
	return f, nil
}

// fieldKeyOf makes a field key from its label (U65): accent-folded,
// lower-case, runs of other characters as one "_", at most 40 characters,
// made unique against used.
func fieldKeyOf(label string, used map[string]bool) string {
	var b strings.Builder
	for _, r := range textutil.Unaccent(label) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if b.Len() > 0 && !strings.HasSuffix(b.String(), "_") {
			b.WriteByte('_')
		}
	}
	k := strings.Trim(b.String(), "_")
	if len(k) > 40 {
		k = strings.Trim(k[:40], "_")
	}
	if len(k) == 1 {
		k += "_1"
	}
	if k == "" {
		k = "truong"
	}
	base := k
	for i := 2; used[k]; i++ {
		k = fmt.Sprintf("%s_%d", base, i)
	}
	used[k] = true
	return k
}

// CheckMapping validates a mapping against the form and the KB (§6.9.8) and
// returns it cleaned: formulas without "=", new fields given keys. A field
// column must name a field of the row type or one of NewFields.
func CheckMapping(l types.ExportLayout, kb types.KBConfig, m types.ExportMapping) (types.ExportMapping, error) {
	byType := map[string]map[string]bool{}
	for _, t := range kb.DocTypes {
		keys := map[string]bool{}
		for _, f := range t.Fields {
			keys[f.Key] = true
		}
		byType[t.Name] = keys
	}
	if _, ok := byType[m.DocType]; !ok {
		return m, fmt.Errorf("loại giấy tờ %q không có trong thư viện", m.DocType)
	}
	newKeys := map[string]map[string]bool{}
	var news []types.ExportNewField
	for _, nf := range m.NewFields {
		keys, ok := byType[nf.DocType]
		if !ok || strings.TrimSpace(nf.Field.Label) == "" {
			continue
		}
		if nf.Field.Key == "" || keys[nf.Field.Key] {
			nf.Field.Key = fieldKeyOf(nf.Field.Label, keys)
		}
		keys[nf.Field.Key] = true
		if newKeys[nf.DocType] == nil {
			newKeys[nf.DocType] = map[string]bool{}
		}
		newKeys[nf.DocType][nf.Field.Key] = true
		if nf.Field.ValueType == "" {
			nf.Field.ValueType = "string"
		}
		news = append(news, nf)
	}
	cols := map[string]bool{}
	for _, c := range l.Columns {
		cols[c.Col] = true
	}
	out := types.ExportMapping{DocType: m.DocType, NewFields: news, Columns: []types.ExportColumnMap{}, Placeholders: []types.ExportFillMap{}}
	var err error
	for _, c := range m.Columns {
		if !cols[c.Col] {
			continue
		}
		cm := types.ExportColumnMap{Col: c.Col, Kind: c.Kind}
		switch c.Kind {
		case types.ExportSeq, types.ExportBlank:
		case types.ExportField:
			if !byType[m.DocType][c.Key] {
				return m, fmt.Errorf("cột %s: trường %q không có ở loại giấy tờ %s", c.Col, c.Key, m.DocType)
			}
			cm.Key = c.Key
		case types.ExportFormula:
			if cm.Formula, err = checkFormula(c.Formula, cols, false); err != nil {
				return m, fmt.Errorf("cột %s: %w", c.Col, err)
			}
			if cm.Formula == "" {
				cm.Kind = types.ExportBlank
			}
		default:
			cm.Kind = types.ExportBlank
		}
		if l.TotalRow > 0 {
			if cm.Total, err = checkFormula(c.Total, cols, true); err != nil {
				return m, fmt.Errorf("dòng tổng, cột %s: %w", c.Col, err)
			}
		}
		out.Columns = append(out.Columns, cm)
	}
	cells := map[string]bool{}
	for _, p := range l.Placeholders {
		cells[p.Cell] = true
	}
	for _, p := range m.Placeholders {
		if !cells[p.Cell] {
			continue
		}
		fm := types.ExportFillMap{Cell: p.Cell}
		switch {
		case p.Key != "":
			if !byType[p.DocType][p.Key] {
				return m, fmt.Errorf("ô %s: trường %q không có ở loại giấy tờ %q", p.Cell, p.Key, p.DocType)
			}
			fm.DocType, fm.Key = p.DocType, p.Key
		default:
			fm.Text = strings.TrimSpace(p.Text)
		}
		out.Placeholders = append(out.Placeholders, fm)
	}
	return out, nil
}

const formSystem = `Bạn ánh xạ một biểu mẫu Excel vào các trường thông tin của thư viện hồ sơ.
Mỗi dòng dữ liệu của bảng trong biểu mẫu là một giấy tờ thuộc đúng một loại giấy tờ của thư viện.
Với mỗi cột, chọn một kiểu:
- "seq": số thứ tự 1, 2, 3…
- "field": giá trị một trường của loại giấy tờ (key có sẵn). Nếu cột cần một thông tin mà loại giấy tờ chưa có trường nào phù hợp và thông tin đó đọc được trên giấy tờ, đặt "new_field": {"label", "value_type" (string|number|money|date|bool), "description"} và bỏ "key".
- "formula": cột tính từ cột khác của bảng, viết công thức Excel không có dấu "=", ô viết dạng D{r} (cùng dòng), $D${first}:$D${last} (cả cột dữ liệu). Ví dụ cộng theo nhóm: SUMIF($G${first}:$G${last},G{r},$D${first}:$D${last}).
- "blank": để trống cho người dùng (ký, tích chọn, thông tin không có trên giấy tờ, hoặc không chắc cách tính).
Cột tổng tiền của dòng tổng ("total"): công thức như SUM(D{first}:D{last}); cột không cần tổng thì bỏ "total". Chỉ dùng SUM, SUMIF, SUMIFS, COUNT, COUNTA, COUNTIF, ROUND, IF, MIN, MAX, AVERAGE. Không chắc thì không viết công thức.
Ô chờ điền ngoài bảng ("placeholders"): điền bằng một trường của một loại giấy tờ khác trong hồ sơ ({"cell","doc_type","key"} hoặc "new_field"), hoặc để trống ({"cell"}) khi người lập tự ghi.
Chỉ trả lời một JSON: {"doc_type": "...", "columns": [{"col","kind","key"?,"new_field"?,"formula"?,"total"?}], "placeholders": [{"cell","doc_type"?,"key"?,"new_field"?}]}`

// proposal is the model's answer for a form.
type proposal struct {
	DocType string `json:"doc_type"`
	Columns []struct {
		types.ExportColumnMap
		NewField *types.SheetField `json:"new_field"`
	} `json:"columns"`
	Placeholders []struct {
		types.ExportFillMap
		NewField *types.SheetField `json:"new_field"`
	} `json:"placeholders"`
}

// ProposeMapping asks the model once how to fill a form (§6.9.8) and returns
// a mapping that passes CheckMapping: what it cannot check (an unknown key, a
// formula it cannot accept) is left blank for the user rather than failing.
func ProposeMapping(ctx context.Context, llm interfaces.Completer, l types.ExportLayout, kb types.KBConfig) (types.ExportMapping, error) {
	type field struct {
		Key, Label, ValueType, Description string `json:",omitempty"`
	}
	type docType struct {
		Code, Title, Description string `json:",omitempty"`
		Fields                   []field
	}
	var dts []docType
	for _, t := range kb.DocTypes {
		d := docType{Code: t.Name, Title: t.Title, Description: t.Description, Fields: []field{}}
		for _, f := range t.Fields {
			d.Fields = append(d.Fields, field{f.Key, f.Label, f.ValueType, f.Description})
		}
		dts = append(dts, d)
	}
	in, _ := json.Marshal(map[string]any{"title": l.Title, "columns": l.Columns, "has_total_row": l.TotalRow > 0,
		"placeholders": l.Placeholders, "doc_types": dts})
	var p proposal
	if err := llm.CompleteJSON(ctx, formSystem, "Biểu mẫu và thư viện:\n"+string(in), &p); err != nil {
		return types.ExportMapping{}, err
	}
	m := types.ExportMapping{DocType: p.DocType}
	if !hasType(kb, m.DocType) {
		m.DocType = blankMapping(l, kb).DocType
	}
	used := map[string]map[string]bool{}
	keysOf := func(dt string) map[string]bool {
		if used[dt] == nil {
			used[dt] = map[string]bool{}
			for _, t := range kb.DocTypes {
				if t.Name == dt {
					for _, f := range t.Fields {
						used[dt][f.Key] = true
					}
				}
			}
		}
		return used[dt]
	}
	addField := func(dt string, nf types.SheetField) string {
		if strings.TrimSpace(nf.Label) == "" || !hasType(kb, dt) {
			return ""
		}
		nf.Key = fieldKeyOf(nf.Label, keysOf(dt))
		m.NewFields = append(m.NewFields, types.ExportNewField{DocType: dt, Field: nf})
		return nf.Key
	}
	cols := map[string]bool{}
	for _, c := range l.Columns {
		cols[c.Col] = true
	}
	given := map[string]types.ExportColumnMap{}
	for _, c := range p.Columns {
		cm := c.ExportColumnMap
		if c.Kind == types.ExportField && c.NewField != nil && !keysOf(m.DocType)[cm.Key] {
			cm.Key = addField(m.DocType, *c.NewField)
		}
		given[cm.Col] = cm
	}
	for _, c := range l.Columns {
		cm, ok := given[c.Col]
		if !ok {
			cm = types.ExportColumnMap{Col: c.Col, Kind: types.ExportBlank}
		}
		if cm.Kind == types.ExportField && !keysOf(m.DocType)[cm.Key] {
			cm.Kind, cm.Key = types.ExportBlank, ""
		}
		if cm.Kind == types.ExportFormula {
			if f, err := checkFormula(cm.Formula, cols, false); err != nil || f == "" {
				cm.Kind, cm.Formula = types.ExportBlank, ""
			}
		}
		if f, err := checkFormula(cm.Total, cols, true); err != nil || l.TotalRow == 0 {
			cm.Total = ""
		} else {
			cm.Total = f
		}
		m.Columns = append(m.Columns, cm)
	}
	gp := map[string]types.ExportFillMap{}
	for _, ph := range p.Placeholders {
		fm := ph.ExportFillMap
		if ph.NewField != nil && fm.DocType != "" && !keysOf(fm.DocType)[fm.Key] {
			fm.Key = addField(fm.DocType, *ph.NewField)
		}
		gp[fm.Cell] = fm
	}
	for _, ph := range l.Placeholders {
		fm := gp[ph.Cell]
		fm.Cell, fm.Text = ph.Cell, ""
		if fm.Key == "" || !keysOf(fm.DocType)[fm.Key] {
			fm.DocType, fm.Key = "", ""
		}
		m.Placeholders = append(m.Placeholders, fm)
	}
	return CheckMapping(l, kb, m)
}

func hasType(kb types.KBConfig, name string) bool {
	for _, t := range kb.DocTypes {
		if t.Name == name {
			return true
		}
	}
	return false
}
