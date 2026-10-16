package types

import (
	"time"

	"github.com/google/uuid"
)

// ExportTemplate is an Excel form of a KB (§6.9.8, U85): the uploaded file,
// the regions the code found in it and how its cells are filled from the
// fields of a case. The file stays in S3 untouched; every export fills a copy.
type ExportTemplate struct {
	ID        uuid.UUID     `json:"id"`
	KBID      uuid.UUID     `json:"kb_id"`
	Name      string        `json:"name"`
	FileName  string        `json:"file_name"`
	ObjectKey string        `json:"-"`
	Layout    ExportLayout  `json:"layout"`
	Mapping   ExportMapping `json:"mapping"`
	CreatedBy uuid.UUID     `json:"created_by"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// ExportLayout is what the code read from the form (§6.9.8): the table of the
// sheet (its header rows, its columns and the empty rows below the header
// that hold one record each), the total row under it, and the cells outside
// the table that wait for a value ("CÔNG TY ……….").
type ExportLayout struct {
	Sheet        string              `json:"sheet"`
	Title        string              `json:"title,omitempty"`
	HeaderTop    int                 `json:"header_top"`
	HeaderBottom int                 `json:"header_bottom"`
	DataStart    int                 `json:"data_start"`
	DataRows     int                 `json:"data_rows"` // empty rows of the form; more records insert rows
	TotalRow     int                 `json:"total_row,omitempty"`
	TotalLabel   string              `json:"total_label,omitempty"` // the cell that says "TỔNG"
	Columns      []ExportColumn      `json:"columns"`
	Placeholders []ExportPlaceholder `json:"placeholders"`
}

// ExportColumn is one column of the form's table.
type ExportColumn struct {
	Col    string `json:"col"` // letter
	Header string `json:"header"`
	Note   string `json:"note,omitempty"` // the footnote its header points to ([1] …)
}

// ExportPlaceholder is a cell outside the table with a run of dots to fill.
type ExportPlaceholder struct {
	Cell string `json:"cell"`
	Text string `json:"text"`
	Note string `json:"note,omitempty"`
}

// Column kinds of an export mapping.
const (
	ExportSeq     = "seq"     // 1, 2, 3…
	ExportField   = "field"   // a field of the row's document type
	ExportFormula = "formula" // an Excel formula of the row
	ExportBlank   = "blank"   // left for the user (signatures, ticks…)
)

// ExportMapping says how each cell of the form is filled (§6.9.8). Rows are
// the documents of one type of the KB. Formulas use {r} for the row being
// written and {first}, {last} for the first and last record rows, so they
// stay right when rows are inserted.
type ExportMapping struct {
	DocType      string            `json:"doc_type"`
	Columns      []ExportColumnMap `json:"columns"`
	Placeholders []ExportFillMap   `json:"placeholders"`
	NewFields    []ExportNewField  `json:"new_fields,omitempty"` // proposed, added to the KB when the mapping is saved
}

// ExportColumnMap fills one column: its records (Kind, Key or Formula) and its
// cell on the total row (Total, a formula; "" = leave it).
type ExportColumnMap struct {
	Col     string `json:"col"`
	Kind    string `json:"kind"`
	Key     string `json:"key,omitempty"`
	Formula string `json:"formula,omitempty"`
	Total   string `json:"total,omitempty"`
}

// ExportFillMap fills a placeholder: with a field of the first document of
// DocType in the case, or a fixed text; neither leaves it as it is.
type ExportFillMap struct {
	Cell    string `json:"cell"`
	DocType string `json:"doc_type,omitempty"`
	Key     string `json:"key,omitempty"`
	Text    string `json:"text,omitempty"`
}

// ExportNewField is a field the form needs that its document type does not
// have yet.
type ExportNewField struct {
	DocType string     `json:"doc_type"`
	Field   SheetField `json:"field"`
}
