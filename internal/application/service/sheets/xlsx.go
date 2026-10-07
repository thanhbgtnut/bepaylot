package sheets

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"

	"github.com/thanhenti/bepaylot/internal/textutil"
)

// Sheet names of the exported workbook (§6.9.6). The hidden one maps each
// value cell to its field so a re-uploaded file can be compared.
const (
	xlSummary = "Tổng hợp"
	xlSources = "Nguồn"
	xlMap     = "_bp"
	firstRow  = 4 // first value row of the summary sheet
)

var unsafeName = regexp.MustCompile(`[^\p{L}\p{N}._-]+`)

// XLSX writes the workbook of a sheet and returns its file name.
func (s *Service) XLSX(ctx context.Context, owner, id uuid.UUID, w io.Writer) (string, error) {
	v, err := s.Get(ctx, owner, id)
	if err != nil {
		return "", err
	}
	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetSheetName("Sheet1", xlSummary); err != nil {
		return "", err
	}
	style := func(fill string, bold bool) int {
		st := &excelize.Style{Font: &excelize.Font{Bold: bold}, Alignment: &excelize.Alignment{Vertical: "center", WrapText: true}}
		if fill != "" {
			st.Fill = excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{fill}}
		}
		n, _ := f.NewStyle(st)
		return n
	}
	head, edited, low, calc := style("DDF2E5", true), style("D3E3FD", false), style("FFDF99", false), style("EADDFF", false)

	_ = f.SetCellValue(xlSummary, "A1", fmt.Sprintf("%s · %s", strings.ToUpper(v.Name), v.CaseCode))
	_ = f.MergeCell(xlSummary, "A1", "E1")
	_ = f.SetCellStyle(xlSummary, "A1", "E1", head)
	_ = f.SetCellValue(xlSummary, "A2", "Giá trị AI chưa duyệt. Sửa ở cột B rồi tải file lên lại trang bảng để ghi nhận; không sửa sheet ẩn _bp.")
	_ = f.MergeCell(xlSummary, "A2", "E2")
	for i, h := range []string{"Trường", "Giá trị", "Tin cậy", "Nguồn", "Trạng thái"} {
		cell, _ := excelize.CoordinatesToCellName(i+1, 3)
		_ = f.SetCellValue(xlSummary, cell, h)
	}
	_ = f.SetCellStyle(xlSummary, "A3", "E3", head)
	_ = f.SetColWidth(xlSummary, "A", "A", 34)
	_ = f.SetColWidth(xlSummary, "B", "B", 42)
	_ = f.SetColWidth(xlSummary, "C", "C", 9)
	_ = f.SetColWidth(xlSummary, "D", "D", 34)
	_ = f.SetColWidth(xlSummary, "E", "E", 16)

	if _, err := f.NewSheet(xlSources); err != nil {
		return "", err
	}
	for i, h := range []string{"key", "file", "trang", "câu gốc"} {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue(xlSources, cell, h)
	}
	_ = f.SetCellStyle(xlSources, "A1", "D1", head)
	_ = f.SetColWidth(xlSources, "B", "B", 30)
	_ = f.SetColWidth(xlSources, "D", "D", 80)

	if _, err := f.NewSheet(xlMap); err != nil {
		return "", err
	}
	_ = f.SetSheetRow(xlMap, "A1", &[]any{"sheet_id", v.ID.String()})
	_ = f.SetSheetRow(xlMap, "A2", &[]any{"cell", "key", "field_id", "value"})

	srcRow := 2
	for i, r := range v.RowsView {
		n := firstRow + i
		val := fmt.Sprintf("B%d", n)
		status := "AI đề xuất"
		st := 0
		switch {
		case r.Edited:
			status, st = "người dùng sửa", edited
		case r.Status == "confirmed":
			status = "đã duyệt"
		case r.Calc:
			status, st = "agent tính", calc
		case r.Low:
			status, st = "cần xem", low
		case r.FieldID == nil:
			status = "không tìm thấy"
		}
		source := ""
		if len(r.Evidence) > 0 {
			source = fmt.Sprintf("%s · tr.%d", r.Evidence[0].FileName, r.Evidence[0].PageNo)
		}
		conf := ""
		if r.FieldID != nil {
			conf = fmt.Sprintf("%d%%", int(r.Confidence*100+0.5))
		}
		_ = f.SetSheetRow(xlSummary, fmt.Sprintf("A%d", n), &[]any{r.Label, r.Value, conf, source, status})
		if st != 0 {
			_ = f.SetCellStyle(xlSummary, val, val, st)
		}
		fieldID := ""
		if r.FieldID != nil {
			fieldID = r.FieldID.String()
		}
		_ = f.SetSheetRow(xlMap, fmt.Sprintf("A%d", i+3), &[]any{val, r.Key, fieldID, r.Value})
		for _, e := range r.Evidence {
			_ = f.SetSheetRow(xlSources, fmt.Sprintf("A%d", srcRow), &[]any{r.Key, e.FileName, e.PageNo, e.Quote})
			srcRow++
		}
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

// ImportEdit is a cell the uploaded file changed.
type ImportEdit struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Cell     string `json:"cell"`
	Value    string `json:"value"`
	AIValue  string `json:"ai_value"`
	WebValue string `json:"web_value,omitempty"` // conflicts: the value changed on the web since download
}

// Ignored is a cell of the file that maps to no field.
type Ignored struct {
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
	rowByKey := map[string]RowView{}
	for _, rv := range v.RowsView {
		rowByKey[rv.Key] = rv
	}
	res := ImportResult{Edits: []ImportEdit{}, Conflicts: []ImportEdit{}, Ignored: []Ignored{}}
	known := map[string]bool{}
	for _, m := range mapRows[2:] {
		if len(m) < 2 {
			continue
		}
		get := func(i int) string {
			if i < len(m) {
				return m[i]
			}
			return ""
		}
		cell, key, fieldID, downloaded := get(0), get(1), get(2), get(3)
		known[cell] = true
		rv, ok := rowByKey[key]
		if !ok {
			continue
		}
		val, _ := f.GetCellValue(xlSummary, cell)
		val = strings.TrimSpace(val)
		if textutil.ValueKey(val) == textutil.ValueKey(downloaded) {
			continue
		}
		e := ImportEdit{Key: key, Label: rv.Label, Cell: cell, Value: val, AIValue: rv.AIValueText}
		curID := ""
		if rv.FieldID != nil {
			curID = rv.FieldID.String()
		}
		if curID != fieldID && textutil.ValueKey(rv.Value) != textutil.ValueKey(downloaded) {
			e.WebValue = rv.Value
			res.Conflicts = append(res.Conflicts, e)
			continue
		}
		res.Edits = append(res.Edits, e)
	}
	rows, _ := f.GetRows(xlSummary)
	for i := firstRow - 1 + len(v.RowsView); i < len(rows); i++ {
		var parts []string
		for _, text := range rows[i] {
			if t := strings.TrimSpace(text); t != "" {
				parts = append(parts, t)
			}
		}
		if len(parts) > 0 {
			res.Ignored = append(res.Ignored, Ignored{Cell: fmt.Sprintf("A%d", i+1), Text: textutil.Truncate(strings.Join(parts, " · "), 120),
				Reason: "dòng thêm tay, không gắn với trường nào"})
		}
	}
	return res, nil
}
