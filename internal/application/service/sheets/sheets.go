// Package sheets builds the case sheets of U43/U44 (§6.9.6): a template's
// fields gathered across the files of a case, confirmed values reused, the
// missing ones extracted by one agent turn; users edit the values on the
// sheet page or in the downloaded .xlsx, and every value they change from the
// AI's is recorded as a correction.
package sheets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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

// Runner runs the hidden agent turn that extracts the missing fields of a
// sheet, in a session bound to the case; it returns the agent's reply.
type Runner interface {
	RunSheet(ctx context.Context, user types.User, caseID, sheetID uuid.UUID, text string) (string, error)
}

// Errors the HTTP layer maps to status codes.
var (
	ErrNotFound    = errors.New("sheet not found")
	ErrNotSheet    = errors.New("template is not a published sheet template")
	ErrBadFile     = errors.New("the file is not a sheet exported from this page")
	ErrNeedSource  = errors.New("choose the source file of this row first")
	ErrUnknownKey  = errors.New("unknown field key")
	ErrNotFinished = errors.New("the sheet is still being built")
)

// Deps are the service's collaborators.
type Deps struct {
	Store  *postgres.Store
	Queue  queue.Enqueuer
	Cases  interfaces.CaseService
	Runner Runner
	Config *config.Config
	Log    *slog.Logger
}

// Service implements the sheet use cases.
type Service struct {
	st     *postgres.Store
	q      queue.Enqueuer
	cases  interfaces.CaseService
	runner Runner
	cfg    *config.Config
	log    *slog.Logger
}

// New builds the service.
func New(d Deps) *Service {
	return &Service{st: d.Store, q: d.Queue, cases: d.Cases, runner: d.Runner, cfg: d.Config, log: d.Log}
}

// Handlers returns the task handlers.
func (s *Service) Handlers() map[string]queue.Handler {
	return map[string]queue.Handler{types.TaskCaseSheet: s.handleBuild}
}

// Create starts a sheet of a case from a published sheet template.
func (s *Service) Create(ctx context.Context, user types.User, caseID, templateID uuid.UUID) (types.Sheet, error) {
	c, err := s.cases.GetCaseOwned(ctx, user.ID, caseID)
	if err != nil {
		return types.Sheet{}, err
	}
	t, err := s.st.Templates.Get(ctx, templateID, 0)
	if err != nil || t.Kind != types.TemplateSheet || t.Status != types.TemplatePublished || len(t.Fields) == 0 {
		return types.Sheet{}, ErrNotSheet
	}
	if t.CaseType != "" && t.CaseType != c.CaseType {
		return types.Sheet{}, ErrNotSheet
	}
	sh, err := s.st.Sheets.Create(ctx, types.Sheet{CaseID: c.ID, TemplateID: t.ID, TemplateVersion: t.CurrentVersion,
		Name: t.Name, Total: len(t.Fields), CreatedBy: user.ID})
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
	if err := s.st.Sheets.SetState(ctx, sh.ID, types.SheetRunning, 0, nil, ""); err != nil {
		return err
	}

	best, err := s.bestFields(ctx, sh.CaseID)
	if err != nil {
		return fail(err)
	}
	var missing []types.SheetField
	for _, f := range t.Fields {
		if b, ok := best[f.Key]; !ok || b.Status != types.FieldConfirmed {
			missing = append(missing, f)
		}
	}
	notes := map[string]string{}
	if len(missing) > 0 {
		stop := s.watchProgress(ctx, sh, t.Fields)
		reply, err := s.runner.RunSheet(ctx, user, sh.CaseID, sh.ID, sheetPrompt(t.Body, missing))
		stop()
		if err != nil {
			return fail(err)
		}
		notes = missingNotes(reply, missing)
		if best, err = s.bestFields(ctx, sh.CaseID); err != nil {
			return fail(err)
		}
	}
	rows := make([]types.SheetRow, 0, len(t.Fields))
	filled := 0
	for _, f := range t.Fields {
		row := types.SheetRow{Key: f.Key, Label: f.Label, ValueType: f.ValueType}
		if b, ok := best[f.Key]; ok {
			id := b.ID
			row.FieldID, row.AIFieldID, row.AIValueText = &id, &id, b.ValueText
			filled++
		} else {
			row.Note = notes[f.Key]
		}
		rows = append(rows, row)
	}
	return s.st.Sheets.SetState(ctx, sh.ID, types.SheetDone, filled, rows, "")
}

// bestFields picks, per key, the field a sheet shows: the confirmed one,
// else the newest proposal (first value, ord 0).
func (s *Service) bestFields(ctx context.Context, caseID uuid.UUID) (map[string]types.Field, error) {
	list, err := s.st.Fields.ListByCase(ctx, caseID, nil, "")
	if err != nil {
		return nil, err
	}
	out := map[string]types.Field{}
	for _, f := range list {
		if f.Ord != 0 {
			continue
		}
		cur, ok := out[f.Key]
		switch {
		case !ok:
			out[f.Key] = f
		case cur.Status != types.FieldConfirmed && f.Status == types.FieldConfirmed:
			out[f.Key] = f
		case cur.Status == f.Status && f.CreatedAt.After(cur.CreatedAt):
			out[f.Key] = f
		}
	}
	return out, nil
}

// watchProgress updates the filled count while the agent works, so the
// Studio shows "Đang bóc tách 9/13 trường".
func (s *Service) watchProgress(ctx context.Context, sh types.Sheet, fields []types.SheetField) func() {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				best, err := s.bestFields(ctx, sh.CaseID)
				if err != nil {
					continue
				}
				n := 0
				for _, f := range fields {
					if _, ok := best[f.Key]; ok {
						n++
					}
				}
				_ = s.st.Sheets.SetState(ctx, sh.ID, types.SheetRunning, n, nil, "")
			}
		}
	}()
	return cancel
}

// sheetPrompt is the message of the hidden turn: the template body (§8.4)
// plus the fields still missing.
func sheetPrompt(body string, missing []types.SheetField) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(body))
	b.WriteString("\n\nCác trường cần bóc tách (key · nhãn · kiểu):\n")
	for _, f := range missing {
		vt := f.ValueType
		if vt == "" {
			vt = "string"
		}
		fmt.Fprintf(&b, "- %s · %s · %s\n", f.Key, f.Label, vt)
	}
	b.WriteString("\nCách làm: đọc các trang có liên quan, rồi gọi kb_save_fields ngay (một lần cho nhiều trường của cùng một file), " +
		"mỗi giá trị kèm citation_id của dòng gốc và đúng key ở trên; không đợi tìm đủ mọi trường mới ghi. " +
		"Trường nào đã tìm hai lần không thấy thì bỏ qua, không tìm tiếp. " +
		"Cuối câu trả lời liệt kê mỗi trường không tìm thấy trên một dòng dạng `key: lý do`.")
	return b.String()
}

// missingNotes reads "key: reason" lines of the agent's reply for the
// fields it did not find.
func missingNotes(reply string, missing []types.SheetField) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(reply, "\n") {
		line = strings.Trim(strings.TrimSpace(line), "-*• `")
		for _, f := range missing {
			if rest, ok := strings.CutPrefix(line, f.Key); ok {
				if r := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(rest), ":`")); r != "" {
					out[f.Key] = textutil.Truncate(r, 300)
				}
			}
		}
	}
	return out
}

// RowView is one row of a sheet as the page shows it.
type RowView struct {
	types.SheetRow
	Value      string           `json:"value"`
	Confidence float64          `json:"confidence"`
	Status     string           `json:"status,omitempty"` // field status
	Source     string           `json:"source,omitempty"` // agent | user
	Matched    bool             `json:"value_matched"`
	FieldNote  string           `json:"field_note,omitempty"`
	DocumentID *uuid.UUID       `json:"document_id,omitempty"`
	Evidence   []types.Evidence `json:"evidence"`
	Edited     bool             `json:"edited"` // differs from the AI value
	Calc       bool             `json:"calc"`   // value derived by the agent
	Low        bool             `json:"low"`    // confidence below sheets.low_confidence
}

// View is a sheet with its rows resolved.
type View struct {
	types.Sheet
	CaseCode string                `json:"case_code"`
	RowsView []RowView             `json:"rows_view"`
	Sources  []types.DocumentBrief `json:"sources,omitempty"`
}

// Get returns a sheet with the current value of each row.
func (s *Service) Get(ctx context.Context, owner, id uuid.UUID) (View, error) {
	sh, c, err := s.owned(ctx, owner, id)
	if err != nil {
		return View{}, err
	}
	return s.view(ctx, sh, c)
}

func (s *Service) view(ctx context.Context, sh types.Sheet, c types.Case) (View, error) {
	v := View{Sheet: sh, CaseCode: c.Code, RowsView: make([]RowView, 0, len(sh.Rows))}
	var ids []uuid.UUID
	for _, r := range sh.Rows {
		if r.FieldID != nil {
			ids = append(ids, *r.FieldID)
		}
	}
	fields, err := s.st.Fields.GetMany(ctx, ids)
	if err != nil {
		return v, err
	}
	for _, r := range sh.Rows {
		rv := RowView{SheetRow: r, Evidence: []types.Evidence{}}
		if r.FieldID != nil {
			if f, ok := fields[*r.FieldID]; ok {
				doc := f.DocumentID
				rv.Value, rv.Confidence, rv.Status, rv.Source = f.ValueText, f.Confidence, f.Status, f.Source
				rv.Matched, rv.FieldNote, rv.DocumentID = f.ValueMatched, f.Note, &doc
				if f.Evidence != nil {
					rv.Evidence = f.Evidence
				}
				rv.Calc = !f.ValueMatched && f.Note != "" && f.Source == types.FieldSourceAgent
				rv.Low = f.Source == types.FieldSourceAgent && f.Confidence < s.cfg.Sheets.LowConfidence
			}
		}
		rv.Edited = textutil.ValueKey(rv.Value) != textutil.ValueKey(r.AIValueText)
		v.RowsView = append(v.RowsView, rv)
	}
	if docs, err := s.st.Documents.ByCase(ctx, sh.CaseID); err == nil {
		for _, d := range docs {
			v.Sources = append(v.Sources, types.DocumentBrief{ID: d.ID, FileName: d.FileName, PageCount: d.PageCount})
		}
	}
	return v, nil
}

// EditInput is one cell the user saves.
type EditInput struct {
	Key        string     `json:"key"`
	Value      string     `json:"value"`
	Origin     string     `json:"origin,omitempty"` // page | xlsx
	DocumentID *uuid.UUID `json:"document_id,omitempty"`
}

// SaveEdits writes the user's values (§6.9.6): each becomes a confirmed user
// field that supersedes the current one, with the AI field's evidence; a
// value different from the AI's is recorded as a correction.
func (s *Service) SaveEdits(ctx context.Context, user types.User, id uuid.UUID, in []EditInput) (View, error) {
	sh, c, err := s.owned(ctx, user.ID, id)
	if err != nil {
		return View{}, err
	}
	if sh.Status != types.SheetDone {
		return View{}, ErrNotFinished
	}
	byKey := map[string]int{}
	var ids []uuid.UUID
	for i, r := range sh.Rows {
		byKey[r.Key] = i
		if r.FieldID != nil {
			ids = append(ids, *r.FieldID)
		}
	}
	current, err := s.st.Fields.GetMany(ctx, ids)
	if err != nil {
		return View{}, err
	}
	var edits []postgres.SheetEdit
	for _, e := range in {
		i, ok := byKey[e.Key]
		if !ok {
			return View{}, fmt.Errorf("%w: %s", ErrUnknownKey, e.Key)
		}
		row := sh.Rows[i]
		value := strings.TrimSpace(e.Value)
		var cur *types.Field
		if row.FieldID != nil {
			if f, ok := current[*row.FieldID]; ok {
				cur = &f
			}
		}
		if cur != nil && cur.Status == types.FieldConfirmed && textutil.ValueKey(cur.ValueText) == textutil.ValueKey(value) {
			continue // nothing to change
		}
		doc := uuid.Nil
		switch {
		case cur != nil:
			doc = cur.DocumentID
		case e.DocumentID != nil:
			d, err := s.st.Documents.Get(ctx, *e.DocumentID)
			if err != nil || d.CaseID != sh.CaseID {
				return View{}, fmt.Errorf("%w (%s)", ErrNeedSource, row.Label)
			}
			doc = d.ID
		default:
			return View{}, fmt.Errorf("%w (%s)", ErrNeedSource, row.Label)
		}
		vt := row.ValueType
		if vt == "" {
			vt = "string"
		}
		w := postgres.FieldWrite{CaseID: sh.CaseID, DocumentID: doc, Key: row.Key, Value: value, ValueType: vt, ValueText: value,
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
		if textutil.ValueKey(value) != textutil.ValueKey(row.AIValueText) {
			origin := e.Origin
			if origin != "xlsx" {
				origin = "page"
			}
			edit.Correction = &types.Correction{AIFieldID: row.AIFieldID, AIValueText: row.AIValueText, UserValueText: value,
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

// CorrectionStats is the AI error rate per field of a template (§6.9.6).
func (s *Service) CorrectionStats(ctx context.Context, templateID uuid.UUID) ([]types.CorrectionStat, error) {
	return s.st.Sheets.CorrectionStats(ctx, templateID)
}
