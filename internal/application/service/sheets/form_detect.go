package sheets

import (
	"errors"
	"regexp"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/thanhenti/bepaylot/docpipe/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
)

// ErrNoFormTable is returned when an uploaded form has no table to fill.
var ErrNoFormTable = errors.New("không tìm thấy bảng trong file mẫu: cần một dòng tiêu đề có kẻ viền với ít nhất 2 cột")

var (
	reDots     = regexp.MustCompile(`(?:…|\.{2,})[….]*`)
	reNoteRef  = regexp.MustCompile(`\[(\d+)\]`)
	reNoteLine = regexp.MustCompile(`^\s*\[(\d+)\]\s*(.+)$`)
	reTotal    = regexp.MustCompile(`^(tong|tong cong|cong|tong so|total)\b`)
)

// formSheet is a worksheet read for detection: its cell texts, which cells
// have a border and the merged ranges.
type formSheet struct {
	f      *excelize.File
	name   string
	rows   [][]string
	merges []excelize.MergeCell
	border map[int]bool // style id → has a border
}

func (s *formSheet) text(col, row int) string {
	if row < 1 || row > len(s.rows) || col < 1 || col > len(s.rows[row-1]) {
		return ""
	}
	return strings.TrimSpace(s.rows[row-1][col-1])
}

// bordered reports whether the cell, or the merged range it is in, has a
// border.
func (s *formSheet) bordered(col, row int) bool {
	cell, _ := excelize.CoordinatesToCellName(col, row)
	id, err := s.f.GetCellStyle(s.name, cell)
	if err != nil {
		return false
	}
	if b, ok := s.border[id]; ok {
		return b
	}
	st, err := s.f.GetStyle(id)
	b := err == nil && st != nil
	if b {
		b = false
		for _, x := range st.Border {
			if x.Style > 0 {
				b = true
			}
		}
	}
	s.border[id] = b
	return b
}

// merge returns the merged range that covers the cell, as corner
// coordinates; a plain cell is its own range.
func (s *formSheet) merge(col, row int) (c1, r1, c2, r2 int) {
	for _, m := range s.merges {
		a, b, _ := excelize.CellNameToCoordinates(m.GetStartAxis())
		c, d, _ := excelize.CellNameToCoordinates(m.GetEndAxis())
		if col >= a && col <= c && row >= b && row <= d {
			return a, b, c, d
		}
	}
	return col, row, col, row
}

// mergedText is the text a cell shows: that of the top-left cell of its
// merged range.
func (s *formSheet) mergedText(col, row int) string {
	c, r, _, _ := s.merge(col, row)
	return s.text(c, r)
}

// formSheetOf picks the sheet to fill: the active one when it has content,
// else the first with content.
func formSheetOf(f *excelize.File) (*formSheet, error) {
	list := f.GetSheetList()
	order := []string{}
	if i := f.GetActiveSheetIndex(); i >= 0 && i < len(list) {
		order = append(order, list[i])
	}
	order = append(order, list...)
	for _, name := range order {
		rows, err := f.GetRows(name)
		if err != nil || len(rows) == 0 {
			continue
		}
		merges, _ := f.GetMergeCells(name)
		return &formSheet{f: f, name: name, rows: rows, merges: merges, border: map[int]bool{}}, nil
	}
	return nil, ErrNoFormTable
}

// DetectForm reads the regions of an Excel form (§6.9.8) by code, without a
// model: the header is the first row with at least two bordered cells of
// text, grown down over the rows its merged cells span; the record rows are
// the empty bordered rows under it; the total row is the first row under them
// that starts with "Tổng"/"Cộng"; placeholders are the cells outside the
// table with a run of dots; footnotes ("[1] …") explain the headers and
// placeholders that point to them.
func DetectForm(f *excelize.File) (types.ExportLayout, error) {
	s, err := formSheetOf(f)
	if err != nil {
		return types.ExportLayout{}, err
	}
	l := types.ExportLayout{Sheet: s.name, Columns: []types.ExportColumn{}, Placeholders: []types.ExportPlaceholder{}}
	width := 0
	for _, r := range s.rows {
		width = max(width, len(r))
	}
	for _, m := range s.merges {
		c, _, _ := excelize.CellNameToCoordinates(m.GetEndAxis())
		width = max(width, c)
	}

	// Header: the first row with two or more bordered cells of text.
	top, c1, c2 := 0, 0, 0
	for r := 1; r <= len(s.rows) && top == 0; r++ {
		n, lo, hi := 0, 0, 0
		for c := 1; c <= width; c++ {
			if s.mergedText(c, r) != "" && s.bordered(c, r) {
				n++
				if lo == 0 {
					lo = c
				}
				hi = c
			}
		}
		if n >= 2 {
			top, c1, c2 = r, lo, hi
		}
	}
	if top == 0 {
		return l, ErrNoFormTable
	}
	// Widen to the merged ranges of its edge cells.
	a, _, _, _ := s.merge(c1, top)
	_, _, b, _ := s.merge(c2, top)
	c1, c2 = a, b
	bottom := top
	for c := c1; c <= c2; c++ {
		_, _, _, r2 := s.merge(c, top)
		bottom = max(bottom, r2)
	}
	// A sub-header row: every column has text, from itself or a merge above.
	for r := bottom + 1; r <= len(s.rows); r++ {
		full := true
		own := false
		for c := c1; c <= c2; c++ {
			_, mr, _, _ := s.merge(c, r)
			if s.mergedText(c, r) == "" || !s.bordered(c, r) {
				full = false
				break
			}
			if mr == r {
				own = true
			}
		}
		if !full || !own || rowIsTotal(s, r, c1, c2) {
			break
		}
		bottom = r
	}
	l.HeaderTop, l.HeaderBottom = top, bottom
	l.Title = titleOf(s, top)

	notes := map[string]string{}
	for r := 1; r <= len(s.rows); r++ {
		for c := 1; c <= width; c++ {
			if m := reNoteLine.FindStringSubmatch(s.text(c, r)); m != nil && (r < top || r > bottom) {
				notes[m[1]] = textutil.CollapseSpace(m[2])
			}
		}
	}
	noteOf := func(text string) string {
		var out []string
		for _, m := range reNoteRef.FindAllStringSubmatch(text, -1) {
			if n, ok := notes[m[1]]; ok {
				out = append(out, n)
			}
		}
		return strings.Join(out, "; ")
	}

	for c := c1; c <= c2; c++ {
		mc, _, _, _ := s.merge(c, top)
		if mc != c { // the inner column of a header cell merged across columns
			if bottom == top {
				continue
			}
		}
		var parts []string
		for r := top; r <= bottom; r++ {
			t := textutil.CollapseSpace(s.mergedText(c, r))
			if t != "" && (len(parts) == 0 || parts[len(parts)-1] != t) {
				parts = append(parts, t)
			}
		}
		col, _ := excelize.ColumnNumberToName(c)
		h := strings.Join(parts, " / ")
		l.Columns = append(l.Columns, types.ExportColumn{Col: col, Header: h, Note: noteOf(h)})
	}

	// Record rows: empty bordered rows; then maybe the total row.
	l.DataStart = bottom + 1
	r := bottom + 1
	for ; r <= len(s.rows)+1; r++ {
		if !rowBordered(s, r, c1, c2) {
			break
		}
		if rowIsTotal(s, r, c1, c2) {
			l.TotalRow = r
			for c := c1; c <= c2; c++ {
				if reTotal.MatchString(textutil.Normalize(s.text(c, r))) {
					l.TotalLabel, _ = excelize.CoordinatesToCellName(c, r)
					break
				}
			}
			break
		}
		if rowHasText(s, r, c1, c2) {
			break
		}
		l.DataRows++
	}
	end := bottom + l.DataRows
	if l.TotalRow > 0 {
		end = l.TotalRow
	}

	for r := 1; r <= len(s.rows); r++ {
		for c := 1; c <= width; c++ {
			if r >= top && r <= end && c >= c1 && c <= c2 {
				continue
			}
			t := s.text(c, r)
			if t == "" || !reDots.MatchString(t) || reNoteLine.MatchString(t) {
				continue
			}
			cell, _ := excelize.CoordinatesToCellName(c, r)
			l.Placeholders = append(l.Placeholders, types.ExportPlaceholder{Cell: cell, Text: textutil.CollapseSpace(t), Note: noteOf(t)})
		}
	}
	return l, nil
}

func rowBordered(s *formSheet, r, c1, c2 int) bool {
	for c := c1; c <= c2; c++ {
		if s.bordered(c, r) {
			return true
		}
	}
	return false
}

func rowHasText(s *formSheet, r, c1, c2 int) bool {
	for c := c1; c <= c2; c++ {
		if s.text(c, r) != "" {
			return true
		}
	}
	return false
}

func rowIsTotal(s *formSheet, r, c1, c2 int) bool {
	for c := c1; c <= c2; c++ {
		if t := textutil.Normalize(s.text(c, r)); t != "" {
			return reTotal.MatchString(t)
		}
	}
	return false
}

// titleOf is the first text above the header, for the model and the UI.
func titleOf(s *formSheet, top int) string {
	for r := 1; r < top; r++ {
		for _, t := range s.rows[r-1] {
			if t = textutil.CollapseSpace(t); t != "" {
				return t
			}
		}
	}
	return ""
}
