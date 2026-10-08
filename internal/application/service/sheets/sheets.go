// Package sheets builds the case sheets of U43/U44/U47/U48 (§6.9.6): one
// sub-table per document type, fields as columns and one row per reviewed
// document (segment) of the case; confirmed values are reused, the missing
// ones are extracted by one agent turn per bundle. Users edit the values on
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
	"time"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/config"
	"github.com/thanhenti/bepaylot/internal/queue"
	"github.com/thanhenti/bepaylot/internal/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
	"github.com/thanhenti/bepaylot/internal/types/interfaces"
)

// Runner runs the hidden agent turn that extracts the missing cells of a
// sheet, in a session bound to the case; it returns the agent's reply.
type Runner interface {
	RunSheet(ctx context.Context, user types.User, caseID, sheetID uuid.UUID, text string) (string, error)
}

// SplitChecker lists the indexed files of a case whose split into documents
// is not reviewed yet (§6.9.7).
type SplitChecker interface {
	Unreviewed(ctx context.Context, caseID uuid.UUID) ([]string, error)
}

// Errors the HTTP layer maps to status codes.
var (
	ErrNotFound    = errors.New("sheet not found")
	ErrNotSheet    = errors.New("template is not a published sheet template")
	ErrNoTables    = errors.New("choose at least one table of the template")
	ErrBadFile     = errors.New("the file is not a sheet exported from this page")
	ErrUnknownKey  = errors.New("unknown cell")
	ErrNotFinished = errors.New("the sheet is still being built")
)

// SplitNotReviewedError is returned when a sub-table needs the reviewed split
// of files that are not reviewed yet (§6.9.6).
type SplitNotReviewedError struct{ Files []string }

func (e *SplitNotReviewedError) Error() string {
	return "split_not_reviewed: review the split of " + strings.Join(e.Files, ", ") + " first (Tách & gom trang)"
}

// Deps are the service's collaborators.
type Deps struct {
	Store    *postgres.Store
	Queue    queue.Enqueuer
	Cases    interfaces.CaseService
	Searcher interfaces.Searcher // refs d<n> of the case files
	Split    SplitChecker
	Runner   Runner
	Config   *config.Config
	Log      *slog.Logger
}

// Service implements the sheet use cases.
type Service struct {
	st       *postgres.Store
	q        queue.Enqueuer
	cases    interfaces.CaseService
	searcher interfaces.Searcher
	split    SplitChecker
	runner   Runner
	cfg      *config.Config
	log      *slog.Logger
}

// New builds the service.
func New(d Deps) *Service {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{st: d.Store, q: d.Queue, cases: d.Cases, searcher: d.Searcher, split: d.Split, runner: d.Runner, cfg: d.Config, log: log}
}

// Handlers returns the task handlers.
func (s *Service) Handlers() map[string]queue.Handler {
	return map[string]queue.Handler{types.TaskCaseSheet: s.handleBuild}
}

// chosen returns the sub-tables of a template whose label is in labels, in
// template order; nil labels means all.
func chosen(t types.PromptTemplate, labels []string) []types.SheetTable {
	want := map[string]bool{}
	for _, l := range labels {
		want[l] = true
	}
	var out []types.SheetTable
	for _, tb := range t.Tables {
		if labels == nil || want[tb.Label] {
			out = append(out, tb)
		}
	}
	return out
}

// Create starts a sheet of a case from a published sheet template, with the
// chosen sub-tables (nil = all of them).
func (s *Service) Create(ctx context.Context, user types.User, caseID, templateID uuid.UUID, tables []string) (types.Sheet, error) {
	c, err := s.cases.GetCaseOwned(ctx, user.ID, caseID)
	if err != nil {
		return types.Sheet{}, err
	}
	t, err := s.st.Templates.Get(ctx, templateID, 0)
	if err != nil || t.Kind != types.TemplateSheet || t.Status != types.TemplatePublished || len(t.Tables) == 0 {
		return types.Sheet{}, ErrNotSheet
	}
	if t.CaseType != "" && t.CaseType != c.CaseType {
		return types.Sheet{}, ErrNotSheet
	}
	picked := chosen(t, tables)
	if len(picked) == 0 {
		return types.Sheet{}, ErrNoTables
	}
	labels := make([]string, 0, len(picked))
	needSplit := false
	for _, tb := range picked {
		labels = append(labels, tb.Label)
		needSplit = needSplit || tb.Label != ""
	}
	if needSplit && s.split != nil {
		files, err := s.split.Unreviewed(ctx, c.ID)
		if err != nil {
			return types.Sheet{}, err
		}
		if len(files) > 0 {
			return types.Sheet{}, &SplitNotReviewedError{Files: files}
		}
	}
	sh, err := s.st.Sheets.Create(ctx, types.Sheet{CaseID: c.ID, TemplateID: t.ID, TemplateVersion: t.CurrentVersion,
		Name: t.Name, Tables: labels, CreatedBy: user.ID})
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

func slotOf(r types.SheetRow, key string) slot {
	sl := slot{doc: r.DocumentID, key: key}
	if r.SegmentID != nil {
		sl.seg = *r.SegmentID
	}
	return sl
}

// rowsOf lays out the rows of the chosen sub-tables (§6.9.6): one per
// reviewed segment with the sub-table's label, by bundle then case order; a
// sub-table without label has one row per indexed file.
func (s *Service) rowsOf(ctx context.Context, owner, caseID uuid.UUID, tables []types.SheetTable) ([]types.SheetRow, map[uuid.UUID]string, error) {
	docs, err := s.st.Segments.SplitDocs(ctx, caseID)
	if err != nil {
		return nil, nil, err
	}
	segs, err := s.st.Segments.ByCase(ctx, caseID)
	if err != nil {
		return nil, nil, err
	}
	names := map[uuid.UUID]string{}
	order := map[uuid.UUID]int{}
	for i, d := range docs {
		names[d.ID] = d.FileName
		order[d.ID] = i
	}
	refs := map[uuid.UUID]string{}
	if s.searcher != nil {
		if m, err := s.searcher.CaseRefs(ctx, owner, caseID); err == nil {
			for ref, id := range m {
				refs[id] = ref
			}
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
		if tb.Label == "" {
			for _, d := range docs {
				if d.Seq > 0 {
					rows = append(rows, types.SheetRow{Table: "", DocumentID: d.ID, FileName: d.FileName, PageStart: 1, PageEnd: d.PageCount})
				}
			}
			continue
		}
		for _, g := range segs {
			if g.Source != types.SegmentUser || g.Label != tb.Label {
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
	return rows, refs, nil
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
	t, err := s.st.Templates.Get(ctx, sh.TemplateID, sh.TemplateVersion)
	if err != nil {
		return fail(fmt.Errorf("%w: template: %v", queue.ErrSkipRetry, err))
	}
	user, err := s.st.Users.Get(ctx, p.UserID)
	if err != nil {
		return fail(fmt.Errorf("%w: user: %v", queue.ErrSkipRetry, err))
	}
	tables := chosen(t, sh.Tables)
	byLabel := map[string]types.SheetTable{}
	for _, tb := range tables {
		byLabel[tb.Label] = tb
	}
	rows, refs, err := s.rowsOf(ctx, user.ID, sh.CaseID, tables)
	if err != nil {
		return fail(err)
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
				if _, ok := best[slotOf(r, f.Key)]; ok {
					n++
				}
			}
		}
		return n
	}
	if err := s.st.Sheets.SetProgress(ctx, sh.ID, types.SheetRunning, count(), total, nil, ""); err != nil {
		return err
	}

	// Missing cells, grouped by bundle: one agent turn per bundle.
	type miss struct {
		row    types.SheetRow
		fields []types.SheetField
	}
	groups := map[string][]miss{}
	var bundles []string
	for _, r := range rows {
		var fs []types.SheetField
		for _, f := range byLabel[r.Table].Fields {
			if b, ok := best[slotOf(r, f.Key)]; !ok || b.Status != types.FieldConfirmed {
				fs = append(fs, f)
			}
		}
		if len(fs) == 0 {
			continue
		}
		if _, ok := groups[r.Bundle]; !ok {
			bundles = append(bundles, r.Bundle)
		}
		groups[r.Bundle] = append(groups[r.Bundle], miss{row: r, fields: fs})
	}
	sort.Strings(bundles)
	notes := map[slot]string{}
	for _, b := range bundles {
		var items []promptItem
		for _, m := range groups[b] {
			items = append(items, promptItem{ref: rowRef(m.row, refs), title: tableTitle(byLabel[m.row.Table]), file: m.row.FileName,
				pages: pageRange(m.row), fields: m.fields, row: m.row})
		}
		stop := s.watchProgress(ctx, sh.ID, count, total)
		reply, err := s.runner.RunSheet(ctx, user, sh.CaseID, sh.ID, sheetPrompt(t.Body, b, items))
		stop()
		if err != nil {
			return fail(err)
		}
		for k, v := range missingNotes(reply, items) {
			notes[k] = v
		}
		if best, err = s.bestFields(ctx, sh.CaseID); err != nil {
			return fail(err)
		}
		_ = s.st.Sheets.SetProgress(ctx, sh.ID, types.SheetRunning, count(), total, nil, "")
	}
	for i := range rows {
		r := &rows[i]
		r.Cells = map[string]types.SheetCell{}
		for _, f := range byLabel[r.Table].Fields {
			cell := types.SheetCell{}
			if b, ok := best[slotOf(*r, f.Key)]; ok {
				id := b.ID
				cell.FieldID, cell.AIFieldID, cell.AIValueText = &id, &id, b.ValueText
			} else {
				cell.Note = notes[slotOf(*r, f.Key)]
			}
			r.Cells[f.Key] = cell
		}
	}
	if rows == nil {
		rows = []types.SheetRow{}
	}
	return s.st.Sheets.SetProgress(ctx, sh.ID, types.SheetDone, count(), total, rows, "")
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

// watchProgress updates the filled count while the agent works, so the
// Studio shows "Đang bóc tách 31/48 ô".
func (s *Service) watchProgress(ctx context.Context, id uuid.UUID, count func() int, total int) func() {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				_ = s.st.Sheets.SetProgress(ctx, id, types.SheetRunning, count(), total, nil, "")
			}
		}
	}()
	return cancel
}

// promptItem is one document of a bundle with its missing fields.
type promptItem struct {
	ref, title, file, pages string
	fields                  []types.SheetField
	row                     types.SheetRow
}

// rowRef is how the agent names a row's document: the segment ref d<n>.s<k>
// (the k-th document of the file), or d<n> for a whole file.
func rowRef(r types.SheetRow, refs map[uuid.UUID]string) string {
	d := refs[r.DocumentID]
	if d == "" {
		d = r.DocumentID.String()
	}
	if r.SegmentID == nil {
		return d
	}
	return fmt.Sprintf("%s.s%d", d, r.SegmentNo)
}

func tableTitle(t types.SheetTable) string {
	if t.Title != "" {
		return t.Title
	}
	return "Cả file"
}

func pageRange(r types.SheetRow) string {
	if r.PageStart == r.PageEnd {
		return fmt.Sprintf("tr. %d", r.PageStart)
	}
	return fmt.Sprintf("tr. %d–%d", r.PageStart, r.PageEnd)
}

// sheetPrompt is the message of one hidden turn: the template body (§8.4)
// plus, for each document of the bundle, its ref, type, pages and the fields
// still missing.
func sheetPrompt(body, bundle string, items []promptItem) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(body))
	if bundle != "" {
		fmt.Fprintf(&b, "\n\nBộ chứng từ %s. Các giấy tờ và trường cần bóc tách (key · nhãn · kiểu):\n", bundle)
	} else {
		b.WriteString("\n\nCác file và trường cần bóc tách (key · nhãn · kiểu):\n")
	}
	for _, it := range items {
		fmt.Fprintf(&b, "\n[%s] %s · %s · %s\n", it.ref, it.title, it.file, it.pages)
		for _, f := range it.fields {
			vt := f.ValueType
			if vt == "" {
				vt = "string"
			}
			fmt.Fprintf(&b, "- %s · %s · %s\n", f.Key, f.Label, vt)
		}
	}
	b.WriteString("\nCách làm: đọc đúng các trang của từng giấy tờ, rồi gọi kb_save_fields ngay (một lần cho các trường của cùng một giấy tờ, " +
		"truyền segment là ref trong ngoặc vuông, ví dụ d2.s3; ref dạng d<n> là cả file thì truyền document_id), " +
		"mỗi giá trị kèm citation_id của dòng gốc và đúng key ở trên; không đợi tìm đủ mọi trường mới ghi. " +
		"Trường nào đã tìm hai lần không thấy thì bỏ qua, không tìm tiếp. " +
		"Cuối câu trả lời liệt kê mỗi trường không tìm thấy trên một dòng dạng `ref key: lý do`.")
	return b.String()
}

// missingNotes reads the "ref key: reason" lines of the agent's reply for
// the cells it did not fill; a line "key: reason" applies when only one
// document asks for that key.
func missingNotes(reply string, items []promptItem) map[slot]string {
	out := map[slot]string{}
	for _, line := range strings.Split(reply, "\n") {
		line = strings.Trim(strings.TrimSpace(line), "-*• `")
		for _, it := range items {
			rest := line
			if r, ok := strings.CutPrefix(line, it.ref+" "); ok {
				rest = strings.TrimSpace(r)
			} else if r, ok := strings.CutPrefix(line, "["+it.ref+"] "); ok {
				rest = strings.TrimSpace(r)
			} else if len(items) > 1 {
				continue
			}
			for _, f := range it.fields {
				if r, ok := strings.CutPrefix(rest, f.Key); ok {
					if reason := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(r), ":`")); reason != "" {
						out[slotOf(it.row, f.Key)] = textutil.Truncate(reason, 300)
					}
				}
			}
		}
	}
	return out
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
	t, err := s.st.Templates.Get(ctx, sh.TemplateID, sh.TemplateVersion)
	if err != nil {
		return v, err
	}
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
	for _, tb := range chosen(t, sh.Tables) {
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
// segment (or document, for a sub-table without label), and the key.
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
	t, err := s.st.Templates.Get(ctx, sh.TemplateID, sh.TemplateVersion)
	if err != nil {
		return View{}, err
	}
	valueTypes := map[string]map[string]string{} // label → key → value type
	for _, tb := range chosen(t, sh.Tables) {
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
		w := postgres.FieldWrite{CaseID: sh.CaseID, DocumentID: doc, SegmentID: seg, Key: e.Key, Value: value, ValueType: vt, ValueText: value,
			Confidence: 1, Status: types.FieldConfirmed, Source: types.FieldSourceUser, CreatedBy: &user.ID}
		if cur != nil {
			w.Evidence = cur.Evidence
			for _, ev := range cur.Evidence {
				if textutil.ValueIn(value, ev.Quote) {
					w.ValueMatched = true
				}
			}
		}
		edit := postgres.SheetEdit{Row: i, Field: w}
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

// CorrectionStats is the AI error rate per sub-table and field of a
// template (§6.9.6).
func (s *Service) CorrectionStats(ctx context.Context, templateID uuid.UUID) ([]types.CorrectionStat, error) {
	return s.st.Sheets.CorrectionStats(ctx, templateID)
}
