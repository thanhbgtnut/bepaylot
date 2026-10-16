package sheets

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/thanhenti/bepaylot/docpipe/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
)

// formRecord is one record of a form: the values of the fields of one
// document, by key.
type formRecord map[string]string

// RenderForm fills a copy of the form (§6.9.8) in place: one record per row
// from the first empty row of the table, inserting rows styled like the
// first one when there are more records than empty rows (the total row and
// what is under it move down); sequence numbers, values (money and numbers
// as numbers), the row formulas, the formulas of the total row and the
// placeholders. Excel recalculates the formulas when the file is opened.
func RenderForm(f *excelize.File, l types.ExportLayout, m types.ExportMapping, valueTypes map[string]string, recs []formRecord, fills map[string]string) error {
	sh := l.Sheet
	n := len(recs)
	extra := n - l.DataRows
	at := l.DataStart + l.DataRows // the row new rows go before
	if extra > 0 {
		styles := map[string]int{}
		height := 0.0
		if l.DataRows > 0 {
			for _, c := range l.Columns {
				styles[c.Col], _ = f.GetCellStyle(sh, fmt.Sprintf("%s%d", c.Col, l.DataStart))
			}
			height, _ = f.GetRowHeight(sh, l.DataStart)
		}
		if err := f.InsertRows(sh, at, extra); err != nil {
			return err
		}
		for r := at; r < at+extra; r++ {
			for col, st := range styles {
				_ = f.SetCellStyle(sh, fmt.Sprintf("%s%d", col, r), fmt.Sprintf("%s%d", col, r), st)
			}
			if height > 0 {
				_ = f.SetRowHeight(sh, r, height)
			}
		}
	}
	shift := func(cell string) string {
		c, r, err := excelize.CellNameToCoordinates(cell)
		if err != nil || extra <= 0 || r < at {
			return cell
		}
		out, _ := excelize.CoordinatesToCellName(c, r+extra)
		return out
	}
	first := l.DataStart
	last := l.DataStart + max(n, l.DataRows, 1) - 1
	expand := func(formula string, row int) string {
		return strings.NewReplacer("{r}", strconv.Itoa(row), "{first}", strconv.Itoa(first), "{last}", strconv.Itoa(last)).Replace(formula)
	}
	for i, rec := range recs {
		row := first + i
		for _, c := range m.Columns {
			cell := fmt.Sprintf("%s%d", c.Col, row)
			var err error
			switch c.Kind {
			case types.ExportSeq:
				err = f.SetCellInt(sh, cell, int64(i+1))
			case types.ExportField:
				err = setValue(f, sh, cell, rec[c.Key], valueTypes[c.Key])
			case types.ExportFormula:
				err = f.SetCellFormula(sh, cell, expand(c.Formula, row))
			}
			if err != nil {
				return err
			}
		}
	}
	if l.TotalRow > 0 {
		total := l.TotalRow
		if extra > 0 {
			total += extra
		}
		for _, c := range m.Columns {
			if c.Total == "" {
				continue
			}
			if err := f.SetCellFormula(sh, fmt.Sprintf("%s%d", c.Col, total), expand(c.Total, total)); err != nil {
				return err
			}
		}
	}
	for _, p := range l.Placeholders {
		v := strings.TrimSpace(fills[p.Cell])
		if v == "" {
			continue
		}
		cell := shift(p.Cell)
		cur, _ := f.GetCellValue(sh, cell)
		loc := reDots.FindStringIndex(cur)
		if loc == nil {
			continue
		}
		if err := f.SetCellStr(sh, cell, cur[:loc[0]]+v+cur[loc[1]:]); err != nil {
			return err
		}
	}
	full := true
	return f.SetCalcProps(&excelize.CalcPropsOptions{FullCalcOnLoad: &full})
}

// setValue writes a field value: a money or number value that reads as a
// number is written as one, so the formulas of the form can add it.
func setValue(f *excelize.File, sh, cell, v, valueType string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	if valueType == "money" || valueType == "number" {
		if x, ok := textutil.ParseNumber(v); ok {
			return f.SetCellFloat(sh, cell, x, -1, 64)
		}
	}
	return f.SetCellStr(sh, cell, v)
}
