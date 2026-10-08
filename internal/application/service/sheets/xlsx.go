package sheets

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"

	"github.com/thanhenti/bepaylot/internal/textutil"
)

// The hidden sheet of an exported workbook (§6.9.6) maps each value cell to
// its sub-table, document and field, so a re-uploaded file can be compared.
const xlMap = "_bp"

var (
	unsafeName  = regexp.MustCompile(`[^\p{L}\p{N}._-]+`)
	sheetUnsafe = regexp.MustCompile(`[\[\]:*?/\\]`)
)

// sheetName is a valid, unique worksheet name for a sub-table title.
func sheetName(title string, used map[string]bool) string {
	name := strings.TrimSpace(sheetUnsafe.ReplaceAllString(title, " "))
	if name == "" || strings.EqualFold(name, xlMap) {
		name = "Bảng"
	}
	for utf8.RuneCountInString(name) > 28 {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	base := name
	for i := 2; used[strings.ToLower(name)]; i++ {
		name = fmt.Sprintf("%s %d", base, i)
	}
	used[strings.ToLower(name)] = true
	return name
}

// XLSX writes the workbook of a sheet and returns its file name: one sheet
// per chosen sub-table (all when tables is empty) with only the data —
// field labels in row 1, the bundle (or file) in column A, one document per
// row — plus the hidden _bp sheet.
func (s *Service) XLSX(ctx context.Context, owner, id uuid.UUID, tables []string, w io.Writer) (string, error) {
	v, err := s.Get(ctx, owner, id)
	if err != nil {
		return "", err
	}
	want := map[string]bool{}
	for _, t := range tables {
		want[t] = true
	}
	f := excelize.NewFile()
	defer f.Close()
	head, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"DDF2E5"}},
		Alignment: &excelize.Alignment{Vertical: "center", WrapText: true}})
	if _, err := f.NewSheet(xlMap); err != nil {
		return "", err
	}
	_ = f.SetSheetRow(xlMap, "A1", &[]any{"sheet_id", v.ID.String()})
	_ = f.SetSheetRow(xlMap, "A2", &[]any{"sheet", "cell", "table", "segment_id", "document_id", "key", "field_id", "value"})
	mapRow := 3
	used := map[string]bool{}
	first := true
	for _, tv := range v.TablesView {
		if len(want) > 0 && !want[tv.Label] {
			continue
		}
		name := sheetName(tableTitle(tv.SheetTable), used)
		if first {
			if err := f.SetSheetName("Sheet1", name); err != nil {
				return "", err
			}
			first = false
		} else if _, err := f.NewSheet(name); err != nil {
			return "", err
		}
		colA := "Bộ"
		if tv.Label == "" {
			colA = "File"
		}
		header := []any{colA}
		for _, fd := range tv.Fields {
			header = append(header, fd.Label)
		}
		_ = f.SetSheetRow(name, "A1", &header)
		last, _ := excelize.CoordinatesToCellName(len(header), 1)
		_ = f.SetCellStyle(name, "A1", last, head)
		_ = f.SetColWidth(name, "A", "A", 12)
		if len(header) > 1 {
			lastCol, _ := excelize.ColumnNumberToName(len(header))
			_ = f.SetColWidth(name, "B", lastCol, 22)
		}
		_ = f.SetPanes(name, &excelize.Panes{Freeze: true, Split: false, XSplit: 1, YSplit: 1, TopLeftCell: "B2", ActivePane: "bottomRight"})
		for i, r := range tv.Rows {
			n := i + 2
			a := r.Bundle
			if tv.Label == "" {
				a = r.FileName
			}
			vals := []any{a}
			for _, fd := range tv.Fields {
				vals = append(vals, r.CellsView[fd.Key].Value)
			}
			_ = f.SetSheetRow(name, fmt.Sprintf("A%d", n), &vals)
			seg := ""
			if r.SegmentID != nil {
				seg = r.SegmentID.String()
			}
			for j, fd := range tv.Fields {
				cell, _ := excelize.CoordinatesToCellName(j+2, n)
				cv := r.CellsView[fd.Key]
				fieldID := ""
				if cv.FieldID != nil {
					fieldID = cv.FieldID.String()
				}
				_ = f.SetSheetRow(xlMap, fmt.Sprintf("A%d", mapRow), &[]any{name, cell, tv.Label, seg, r.DocumentID.String(), fd.Key, fieldID, cv.Value})
				mapRow++
			}
		}
	}
	if first {
		return "", ErrNoTables
	}
	_ = f.SetSheetVisible(xlMap, false, true)
	_ = f.ProtectSheet(xlMap, &excelize.SheetProtectionOptions{Password: v.ID.String()[:8]})
	f.SetActiveSheet(0)
	if err := f.Write(w); err != nil {
		return "", err
	}
	name := unsafeName.ReplaceAllString(v.CaseCode+"_"+v.Name, "-")
	return strings.Trim(name, "-") + ".xlsx", nil
}

// ImportEdit is a cell the uploaded file changed, with what POST
// /sheets/{id}/edits needs to save it.
type ImportEdit struct {
	Table      string     `json:"table"`
	SegmentID  *uuid.UUID `json:"segment_id,omitempty"`
	DocumentID *uuid.UUID `json:"document_id,omitempty"`
	Key        string     `json:"key"`
	Label      string     `json:"label"`
	Bundle     string     `json:"bundle,omitempty"`
	Sheet      string     `json:"sheet"`
	Cell       string     `json:"cell"`
	Value      string     `json:"value"`
	AIValue    string     `json:"ai_value"`
	WebValue   string     `json:"web_value,omitempty"` // conflicts: the value changed on the web since download
}

// Ignored is a cell of the file that maps to no field.
type Ignored struct {
	Sheet  string `json:"sheet"`
	Cell   string `json:"cell"`
	Text   string `json:"text"`
	Reason string `json:"reason"`
}

// ImportResult lists what an uploaded file changes; nothing is written.
type ImportResult struct {
	Edits     []ImportEdit `json:"edits"`
	Conflicts []ImportEdit `json:"conflicts"`
	Ignored   []Ignored    `json:"ignored"`
}

// Import compares an uploaded workbook with the values it was downloaded
// with (§6.9.6). It only reads.
func (s *Service) Import(ctx context.Context, owner, id uuid.UUID, r io.Reader) (ImportResult, error) {
	v, err := s.Get(ctx, owner, id)
	if err != nil {
		return ImportResult{}, err
	}
	f, err := excelize.OpenReader(r)
	if err != nil {
		return ImportResult{}, ErrBadFile
	}
	defer f.Close()
	mapRows, err := f.GetRows(xlMap)
	if err != nil || len(mapRows) < 2 || len(mapRows[0]) < 2 || mapRows[0][1] != v.ID.String() {
		return ImportResult{}, ErrBadFile
	}
	type cellKey struct{ table, row, key string }
	current := map[cellKey]CellView{}
	labels := map[string]map[string]string{}
	bundles := map[string]string{}
	for _, tv := range v.TablesView {
		labels[tv.Label] = map[string]string{}
		for _, fd := range tv.Fields {
			labels[tv.Label][fd.Key] = fd.Label
		}
		for _, rv := range tv.Rows {
			rowID := rv.DocumentID.String()
			if rv.SegmentID != nil {
				rowID = rv.SegmentID.String()
			}
			bundles[rowID] = rv.Bundle
			for k, cv := range rv.CellsView {
				current[cellKey{tv.Label, rowID, k}] = cv
			}
		}
	}
	res := ImportResult{Edits: []ImportEdit{}, Conflicts: []ImportEdit{}, Ignored: []Ignored{}}
	lastRow := map[string]int{}
	lastCol := map[string]int{}
	for _, m := range mapRows[2:] {
		get := func(i int) string {
			if i < len(m) {
				return m[i]
			}
			return ""
		}
		sheet, cell, table, seg, doc, key, fieldID, downloaded := get(0), get(1), get(2), get(3), get(4), get(5), get(6), get(7)
		if col, row, err := excelize.CellNameToCoordinates(cell); err == nil {
			lastRow[sheet] = max(lastRow[sheet], row)
			lastCol[sheet] = max(lastCol[sheet], col)
		}
		rowID := doc
		if seg != "" {
			rowID = seg
		}
		cv, ok := current[cellKey{table, rowID, key}]
		if !ok {
			continue
		}
		val, _ := f.GetCellValue(sheet, cell)
		val = strings.TrimSpace(val)
		if textutil.ValueKey(val) == textutil.ValueKey(downloaded) {
			continue
		}
		e := ImportEdit{Table: table, Key: key, Label: labels[table][key], Bundle: bundles[rowID], Sheet: sheet, Cell: cell, Value: val, AIValue: cv.AIValueText}
		if id, err := uuid.Parse(seg); err == nil {
			e.SegmentID = &id
		} else if id, err := uuid.Parse(doc); err == nil {
			e.DocumentID = &id
		}
		curID := ""
		if cv.FieldID != nil {
			curID = cv.FieldID.String()
		}
		if curID != fieldID && textutil.ValueKey(cv.Value) != textutil.ValueKey(downloaded) {
			e.WebValue = cv.Value
			res.Conflicts = append(res.Conflicts, e)
			continue
		}
		res.Edits = append(res.Edits, e)
	}
	for _, sheet := range f.GetSheetList() {
		if sheet == xlMap {
			continue
		}
		rows, _ := f.GetRows(sheet)
		if _, known := lastRow[sheet]; !known {
			if len(rows) > 0 {
				res.Ignored = append(res.Ignored, Ignored{Sheet: sheet, Cell: "A1", Reason: "sheet thêm tay hoặc đổi tên, không gắn với bảng nào"})
			}
			continue
		}
		for i, row := range rows {
			n := i + 1
			for j, text := range row {
				t := strings.TrimSpace(text)
				if t == "" || n == 1 {
					continue
				}
				if n > lastRow[sheet] || j+1 > lastCol[sheet] {
					cell, _ := excelize.CoordinatesToCellName(j+1, n)
					reason := "dòng thêm tay, không gắn với giấy tờ nào"
					if n <= lastRow[sheet] {
						reason = "cột thêm tay, không gắn với trường nào"
					}
					res.Ignored = append(res.Ignored, Ignored{Sheet: sheet, Cell: cell, Text: textutil.Truncate(t, 120), Reason: reason})
					break
				}
			}
		}
	}
	return res, nil
}
