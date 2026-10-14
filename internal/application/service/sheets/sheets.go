// Package sheets builds the case sheets (§6.9.6): one sub-table per document
// type of the KB, its fields as columns and one row per document (segment) of
// that type in the case. The values are fields of the case, extracted when a
// sheet is built and reused by every later sheet (§6.9.3, U80). Users edit the values on
// the sheet page or in the downloaded .xlsx, and every value they change from
// the AI's is recorded as a correction.
package sheets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/docpipe/textutil"
	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/config"
	"github.com/thanhenti/bepaylot/internal/queue"
	"github.com/thanhenti/bepaylot/internal/types"
	"github.com/thanhenti/bepaylot/internal/types/interfaces"
)

// Extractor asks the agent for the fields of tables that the documents of a
// file still need (§6.9.3) and returns how many it stored. Building a sheet
// is the only time fields are extracted (U80).
type Extractor interface {
	Extract(ctx context.Context, doc uuid.UUID, tables []types.SheetTable) (int, error)
}

// Errors the HTTP layer maps to status codes.
var (
	ErrNotFound    = errors.New("sheet not found")
	ErrNoTables    = errors.New("no document type with fields to build: add fields to the document types of the knowledge base")
	ErrBadFile     = errors.New("the file is not a sheet exported from this page")
	ErrUnknownKey  = errors.New("unknown cell")
	ErrNotFinished = errors.New("the sheet is still being built")
)

// Deps are the service's collaborators.
type Deps struct {
	Store     *postgres.Store
	Queue     queue.Enqueuer
	Cases     interfaces.CaseService
	Extractor Extractor
	Config    *config.Config
	Log       *slog.Logger
}

// Service implements the sheet use cases.
type Service struct {
	st        *postgres.Store
	q         queue.Enqueuer
	cases     interfaces.CaseService
	extractor Extractor
	cfg       *config.Config
	log       *slog.Logger
}

// New builds the service.
func New(d Deps) *Service {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{st: d.Store, q: d.Queue, cases: d.Cases, extractor: d.Extractor, cfg: d.Config, log: log}
}

// Handlers returns the task handlers.
func (s *Service) Handlers() map[string]queue.Handler {
	return map[string]queue.Handler{types.TaskCaseSheet: s.handleBuild}
}

// chosen returns the sub-tables whose label is in labels, in order; nil
// labels means all.
func chosen(all []types.SheetTable, labels []string) []types.SheetTable {
	want := map[string]bool{}
	for _, l := range labels {
		want[l] = true
	}
	var out []types.SheetTable
	for _, tb := range all {
		if labels == nil || want[tb.Label] {
			out = append(out, tb)
		}
	}
	return out
}

// Create starts a sheet of a case: one sub-table per chosen document type of
// the KB that has fields (nil = all of them).
func (s *Service) Create(ctx context.Context, user types.User, caseID uuid.UUID, tables []string) (types.Sheet, error) {
	c, err := s.cases.GetCaseOwned(ctx, user.ID, caseID)
	if err != nil {
		return types.Sheet{}, err
	}
	kb, err := s.st.KBs.Get(ctx, c.KBID)
	if err != nil {
		return types.Sheet{}, err
	}
	picked := chosen(kb.Config.SheetTables(), tables)
	if len(picked) == 0 {
		return types.Sheet{}, ErrNoTables
	}
	labels := make([]string, 0, len(picked))
	for _, tb := range picked {
		labels = append(labels, tb.Label)
	}
	sh, err := s.st.Sheets.Create(ctx, types.Sheet{CaseID: c.ID, Name: "Bảng tổng hợp", Tables: labels, Columns: picked, CreatedBy: user.ID})
	if err != nil {
		return types.Sheet{}, err
	}
	if err := s.q.Enqueue(ctx, types.TaskCaseSheet, types.SheetTaskPayload{SheetID: sh.ID, UserID: user.ID},
		queue.Opts{TaskID: "sheet:" + sh.ID.String(), Interactive: true}); err != nil {
		_ = s.st.Sheets.SetState(ctx, sh.ID, types.SheetFailed, 0, nil, err.Error())
		return types.Sheet{}, err
	}
	return sh, nil
}

// List returns the sheets of a case (Studio "Đã tạo").
func (s *Service) List(ctx context.Context, owner, caseID uuid.UUID) ([]types.Sheet, error) {
	if _, err := s.cases.GetCaseOwned(ctx, owner, caseID); err != nil {
		return nil, err
	}
	return s.st.Sheets.ListByCase(ctx, caseID)
}

// Delete removes a sheet of a case the user owns. A sheet still being built
// can be deleted too: its task ends when it no longer finds the sheet.
func (s *Service) Delete(ctx context.Context, owner, id uuid.UUID) error {
	sh, _, err := s.owned(ctx, owner, id)
	if err != nil {
		return err
	}
	return s.st.Sheets.Delete(ctx, sh.ID)
}

// owned loads a sheet whose case the user owns.
func (s *Service) owned(ctx context.Context, owner, id uuid.UUID) (types.Sheet, types.Case, error) {
	sh, err := s.st.Sheets.Get(ctx, id)
	if err != nil {
		return sh, types.Case{}, ErrNotFound
	}
	c, err := s.cases.GetCaseOwned(ctx, owner, sh.CaseID)
	if err != nil {
		return sh, c, ErrNotFound
	}
	return sh, c, nil
}

// slot identifies the field a cell shows: (document, segment, key).
type slot struct {
	doc, seg uuid.UUID
	key      string
}

// fieldKey is the key the field of a cell is stored under: the column key,
// prefixed with the sub-table's label on a file row of a labelled sub-table
// (sheets made before 0.25), so two such sub-tables keep their own values for
// the same file.
func fieldKey(r types.SheetRow, key string) string {
	if r.Table != "" && r.SegmentID == nil {
		return r.Table + "." + key
	}
	return key
}

func slotOf(r types.SheetRow, key string) slot {
	key = fieldKey(r, key)
	sl := slot{doc: r.DocumentID, key: key}
	if r.SegmentID != nil {
		sl.seg = *r.SegmentID
	}
	return sl
}

// rowsOf lays out the rows of the sub-tables (§6.9.6): one per document
// (segment) of the sub-table's type in the indexed files of the case, by
// bundle, then file in case order, then page.
func (s *Service) rowsOf(ctx context.Context, caseID uuid.UUID, tables []types.SheetTable) ([]types.SheetRow, error) {
	docs, err := s.st.Segments.SplitDocs(ctx, caseID)
	if err != nil {
		return nil, err
	}
	segs, err := s.st.Segments.ByCase(ctx, caseID)
	if err != nil {
		return nil, err
	}
	names := map[uuid.UUID]string{}
	order := map[uuid.UUID]int{}
	for i, d := range docs {
		if d.Seq > 0 {
			names[d.ID] = d.FileName
			order[d.ID] = i
		}
	}
	// s<k> of a segment ref: its position among the file's documents.
	segNo := map[uuid.UUID]int{}
	perDoc := map[uuid.UUID]int{}
	for _, g := range segs {
		perDoc[g.DocumentID]++
		segNo[g.ID] = perDoc[g.DocumentID]
	}
	var rows []types.SheetRow
	for _, tb := range tables {
		start := len(rows)
		for _, g := range segs {
			if _, indexed := order[g.DocumentID]; !indexed || g.Label != tb.Label {
				continue
			}
			id := g.ID
			rows = append(rows, types.SheetRow{Table: tb.Label, Bundle: g.BundleCode, SegmentID: &id, SegmentNo: segNo[g.ID],
				DocumentID: g.DocumentID, FileName: names[g.DocumentID], PageStart: g.PageStart, PageEnd: g.PageEnd})
		}
		part := rows[start:]
		sort.SliceStable(part, func(i, j int) bool {
			a, b := part[i], part[j]
			if a.Bundle != b.Bundle {
				return a.Bundle < b.Bundle
			}
			if order[a.DocumentID] != order[b.DocumentID] {
				return order[a.DocumentID] < order[b.DocumentID]
			}
			return a.PageStart < b.PageStart
		})
	}
	return rows, nil
}

// whyEmpty says why a sheet has no row: what each file of the case is
// classified as, since a row needs a document of one of the sheet's types.
func (s *Service) whyEmpty(ctx context.Context, caseID uuid.UUID, tables []types.SheetTable) string {
	docs, err := s.st.Segments.SplitDocs(ctx, caseID)
	if err != nil {
		return err.Error()
	}
	segs, err := s.st.Segments.ByCase(ctx, caseID)
	if err != nil {
		return err.Error()
	}
	labels := map[uuid.UUID][]string{}
	for _, g := range segs {
		l := fmt.Sprintf("tr.%d-%d %s", g.PageStart, g.PageEnd, g.Label)
		if g.ProposedLabel != "" && g.ProposedLabel != g.Label {
			l += " (AI đoán " + g.ProposedLabel + ", độ tin cậy thấp)"
		}
		labels[g.DocumentID] = append(labels[g.DocumentID], l)
	}
	want := make([]string, 0, len(tables))
	for _, t := range tables {
		want = append(want, t.Label)
	}
	var files []string
	for _, d := range docs {
		switch {
		case d.Seq == 0:
			files = append(files, d.FileName+": chưa xử lý xong")
		case len(labels[d.ID]) == 0:
			files = append(files, d.FileName+": chưa được phân loại")
		default:
			files = append(files, d.FileName+": "+strings.Join(labels[d.ID], ", "))
		}
	}
	if len(files) == 0 {
		return "Hồ sơ chưa có file nào"
	}
	return fmt.Sprintf("Không có giấy tờ nào thuộc loại %s. %s", strings.Join(want, ", "), strings.Join(files, "; "))
}

// handleBuild is case:sheet (§6.9.6).
func (s *Service) handleBuild(ctx context.Context, raw []byte) error {
	var p types.SheetTaskPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("%w: %v", queue.ErrSkipRetry, err)
	}
	sh, err := s.st.Sheets.Get(ctx, p.SheetID)
	if errors.Is(err, postgres.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if sh.Status == types.SheetDone {
		return nil
	}
	fail := func(err error) error {
		if queue.IsFinalAttempt(ctx) || errors.Is(err, queue.ErrSkipRetry) {
			_ = s.st.Sheets.SetState(context.WithoutCancel(ctx), sh.ID, types.SheetFailed, sh.Filled, nil, err.Error())
		}
		return err
	}
	byLabel := map[string]types.SheetTable{}
	for _, tb := range sh.Columns {
		byLabel[tb.Label] = tb
	}
	rows, err := s.rowsOf(ctx, sh.CaseID, sh.Columns)
	if err != nil {
		return fail(err)
	}
	if len(rows) == 0 { // nothing to extract: say why instead of an empty sheet
		return s.st.Sheets.SetState(ctx, sh.ID, types.SheetFailed, 0, nil, s.whyEmpty(ctx, sh.CaseID, sh.Columns))
	}
	total := 0
	for _, r := range rows {
		total += len(byLabel[r.Table].Fields)
	}
	best, err := s.bestFields(ctx, sh.CaseID)
	if err != nil {
		return fail(err)
	}
	count := func() int {
		n := 0
		for _, r := range rows {
			for _, f := range byLabel[r.Table].Fields {
				if b, ok := best[slotOf(r, f.Key)]; ok && !notFound(b) {
					n++
				}
			}
		}
		return n
	}
	if err := s.st.Sheets.SetProgress(ctx, sh.ID, types.SheetRunning, count(), total, nil, ""); err != nil {
		return err
	}

	// The fields are extracted here, one file at a time, and only for the
	// document types of this sheet. The extractor asks for what the case does
	// not have yet, so a file whose fields are all there costs no model call.
	seen := map[uuid.UUID]bool{}
	for _, r := range rows {
		if seen[r.DocumentID] {
			continue
		}
		seen[r.DocumentID] = true
		n, err := s.extractor.Extract(ctx, r.DocumentID, sh.Columns)
		if err != nil {
			return fail(err)
		}
		if n == 0 {
			continue
		}
		if best, err = s.bestFields(ctx, sh.CaseID); err != nil {
			return fail(err)
		}
		_ = s.st.Sheets.SetProgress(ctx, sh.ID, types.SheetRunning, count(), total, nil, "")
	}
	// Another sheet built at the same time may have stored the fields this one
	// waited for.
	if best, err = s.bestFields(ctx, sh.CaseID); err != nil {
		return fail(err)
	}
	for i := range rows {
		r := &rows[i]
		r.Cells = map[string]types.SheetCell{}
		for _, f := range byLabel[r.Table].Fields {
			cell := types.SheetCell{}
			if b, ok := best[slotOf(*r, f.Key)]; ok && !notFound(b) {
				id := b.ID
				cell.FieldID, cell.AIFieldID, cell.AIValueText = &id, &id, b.ValueText
			} else if ok {
				cell.Note = b.Note
			}
			r.Cells[f.Key] = cell
		}
	}
	if rows == nil {
		rows = []types.SheetRow{}
	}
	return s.st.Sheets.SetProgress(ctx, sh.ID, types.SheetDone, count(), total, rows, "")
}

// notFound reports a field the extraction recorded without a value: the
// document does not have it (§6.9.3).
func notFound(f types.Field) bool {
	return f.Source == types.FieldSourceAgent && f.ValueText == ""
}

// bestFields picks, per (document, segment, key), the field a cell shows:
// the confirmed one, else the newest proposal (first value, ord 0).
func (s *Service) bestFields(ctx context.Context, caseID uuid.UUID) (map[slot]types.Field, error) {
	list, err := s.st.Fields.ListByCase(ctx, caseID, nil, "")
	if err != nil {
		return nil, err
	}
	out := map[slot]types.Field{}
	for _, f := range list {
		if f.Ord != 0 {
			continue
		}
		k := slot{doc: f.DocumentID, key: f.Key}
		if f.SegmentID != nil {
			k.seg = *f.SegmentID
		}
		cur, ok := out[k]
		switch {
		case !ok:
			out[k] = f
		case cur.Status != types.FieldConfirmed && f.Status == types.FieldConfirmed:
			out[k] = f
		case cur.Status == f.Status && f.CreatedAt.After(cur.CreatedAt):
			out[k] = f
		}
	}
	return out, nil
}

func tableTitle(t types.SheetTable) string {
	if t.Title != "" {
		return t.Title
	}
	return "Cả file"
}

// CellView is one cell as the page shows it; the grid shows only Value, the
// rest is for the source panel (§7.6).
type CellView struct {
	types.SheetCell
	Value      string           `json:"value"`
	Confidence float64          `json:"confidence"`
	Status     string           `json:"status,omitempty"` // field status
	Source     string           `json:"source,omitempty"` // agent | user
	Matched    bool             `json:"value_matched"`
	FieldNote  string           `json:"field_note,omitempty"`
	Evidence   []types.Evidence `json:"evidence"`
	Edited     bool             `json:"edited"` // differs from the AI value
	Calc       bool             `json:"calc"`   // value derived by the agent
	Low        bool             `json:"low"`    // confidence below sheets.low_confidence
}

// RowView is one row with its cells resolved.
type RowView struct {
	types.SheetRow
	CellsView map[string]CellView `json:"cells_view"`
}

// TableView is one sub-table of a sheet.
type TableView struct {
	types.SheetTable
	Rows []RowView `json:"rows"`
}

// View is a sheet with its sub-tables resolved.
type View struct {
	types.Sheet
	CaseCode   string      `json:"case_code"`
	TablesView []TableView `json:"tables_view"`
}

// Get returns a sheet with the current value of each cell.
func (s *Service) Get(ctx context.Context, owner, id uuid.UUID) (View, error) {
	sh, c, err := s.owned(ctx, owner, id)
	if err != nil {
		return View{}, err
	}
	return s.view(ctx, sh, c)
}

func (s *Service) view(ctx context.Context, sh types.Sheet, c types.Case) (View, error) {
	v := View{Sheet: sh, CaseCode: c.Code, TablesView: []TableView{}}
	var ids []uuid.UUID
	for _, r := range sh.Rows {
		for _, cell := range r.Cells {
			if cell.FieldID != nil {
				ids = append(ids, *cell.FieldID)
			}
		}
	}
	fields, err := s.st.Fields.GetMany(ctx, ids)
	if err != nil {
		return v, err
	}
	for _, tb := range sh.Columns {
		tv := TableView{SheetTable: tb, Rows: []RowView{}}
		for _, r := range sh.Rows {
			if r.Table != tb.Label {
				continue
			}
			rv := RowView{SheetRow: r, CellsView: map[string]CellView{}}
			for _, f := range tb.Fields {
				cell := r.Cells[f.Key]
				cv := CellView{SheetCell: cell, Evidence: []types.Evidence{}}
				if cell.FieldID != nil {
					if fd, ok := fields[*cell.FieldID]; ok {
						cv.Value, cv.Confidence, cv.Status, cv.Source = fd.ValueText, fd.Confidence, fd.Status, fd.Source
						cv.Matched, cv.FieldNote = fd.ValueMatched, fd.Note
						if fd.Evidence != nil {
							cv.Evidence = fd.Evidence
						}
						if rv.DocumentID == uuid.Nil { // a 0.16 sheet: the row's file is the field's
							rv.DocumentID = fd.DocumentID
						}
						cv.Calc = !fd.ValueMatched && fd.Note != "" && fd.Source == types.FieldSourceAgent
						cv.Low = fd.Source == types.FieldSourceAgent && fd.Confidence < s.cfg.Sheets.LowConfidence
					}
				}
				cv.Edited = textutil.ValueKey(cv.Value) != textutil.ValueKey(cell.AIValueText)
				rv.CellsView[f.Key] = cv
			}
			tv.Rows = append(tv.Rows, rv)
		}
		v.TablesView = append(v.TablesView, tv)
	}
	return v, nil
}

// EditInput is one cell the user saves: the row by its sub-table and
// segment (or document, on a sheet with file rows), and the key.
type EditInput struct {
	Table      string     `json:"table"`
	SegmentID  *uuid.UUID `json:"segment_id,omitempty"`
	DocumentID *uuid.UUID `json:"document_id,omitempty"`
	Key        string     `json:"key"`
	Value      string     `json:"value"`
	Origin     string     `json:"origin,omitempty"` // page | xlsx
}

// findRow returns the index of the row an edit addresses.
func findRow(sh types.Sheet, e EditInput) int {
	for i, r := range sh.Rows {
		if r.Table != e.Table {
			continue
		}
		switch {
		case e.SegmentID != nil && r.SegmentID != nil && *r.SegmentID == *e.SegmentID:
			return i
		case e.SegmentID == nil && r.SegmentID == nil && (e.DocumentID == nil || *e.DocumentID == r.DocumentID || r.DocumentID == uuid.Nil):
			return i
		}
	}
	return -1
}

// SaveEdits writes the user's values (§6.9.6): each becomes a confirmed user
// field of the row's document and segment that supersedes the current one,
// with the AI field's evidence; a value different from the AI's is recorded
// as a correction.
func (s *Service) SaveEdits(ctx context.Context, user types.User, id uuid.UUID, in []EditInput) (View, error) {
	sh, c, err := s.owned(ctx, user.ID, id)
	if err != nil {
		return View{}, err
	}
	if sh.Status != types.SheetDone {
		return View{}, ErrNotFinished
	}
	valueTypes := map[string]map[string]string{} // label → key → value type
	for _, tb := range sh.Columns {
		m := map[string]string{}
		for _, f := range tb.Fields {
			m[f.Key] = f.ValueType
		}
		valueTypes[tb.Label] = m
	}
	var ids []uuid.UUID
	for _, r := range sh.Rows {
		for _, cell := range r.Cells {
			if cell.FieldID != nil {
				ids = append(ids, *cell.FieldID)
			}
		}
	}
	current, err := s.st.Fields.GetMany(ctx, ids)
	if err != nil {
		return View{}, err
	}
	var edits []postgres.SheetEdit
	for _, e := range in {
		i := findRow(sh, e)
		vt, known := valueTypes[e.Table][e.Key]
		if i < 0 || !known {
			return View{}, fmt.Errorf("%w: %s %s", ErrUnknownKey, e.Table, e.Key)
		}
		row := sh.Rows[i]
		cell := row.Cells[e.Key]
		value := strings.TrimSpace(e.Value)
		var cur *types.Field
		if cell.FieldID != nil {
			if f, ok := current[*cell.FieldID]; ok {
				cur = &f
			}
		}
		if cur != nil && cur.Status == types.FieldConfirmed && textutil.ValueKey(cur.ValueText) == textutil.ValueKey(value) {
			continue // nothing to change
		}
		doc, seg := row.DocumentID, row.SegmentID
		if cur != nil {
			doc, seg = cur.DocumentID, cur.SegmentID
		}
		if doc == uuid.Nil {
			return View{}, fmt.Errorf("%w: %s %s", ErrUnknownKey, e.Table, e.Key)
		}
		if vt == "" {
			vt = "string"
		}
		w := postgres.FieldWrite{CaseID: sh.CaseID, DocumentID: doc, SegmentID: seg, Key: fieldKey(row, e.Key), Value: value, ValueType: vt, ValueText: value,
			Confidence: 1, Status: types.FieldConfirmed, Source: types.FieldSourceUser, CreatedBy: &user.ID}
		if cur != nil {
			w.Evidence = cur.Evidence
			for _, ev := range cur.Evidence {
				if textutil.ValueIn(value, ev.Quote) {
					w.ValueMatched = true
				}
			}
		}
		edit := postgres.SheetEdit{Row: i, Key: e.Key, Field: w}
		if textutil.ValueKey(value) != textutil.ValueKey(cell.AIValueText) {
			origin := e.Origin
			if origin != "xlsx" {
				origin = "page"
			}
			edit.Correction = &types.Correction{AIFieldID: cell.AIFieldID, AIValueText: cell.AIValueText, UserValueText: value,
				Origin: origin, UserID: user.ID}
		}
		edits = append(edits, edit)
	}
	if len(edits) == 0 {
		return s.view(ctx, sh, c)
	}
	sh, err = s.st.Sheets.SaveEdits(ctx, sh.ID, edits)
	if err != nil {
		return View{}, err
	}
	return s.view(ctx, sh, c)
}
