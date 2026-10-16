package sheets

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/llm"
	"github.com/thanhenti/bepaylot/internal/types"
)

// Errors of the Excel forms (§6.9.8).
var (
	ErrFormNotFound = errors.New("export template not found")
	ErrBadForm      = errors.New("file mẫu phải là .xlsx đọc được")
	ErrBadMapping   = errors.New("ánh xạ mẫu không hợp lệ")
	ErrFormSheet    = errors.New("bảng này không có loại giấy tờ của mẫu")
)

// formKB loads a KB the user owns.
func (s *Service) formKB(ctx context.Context, owner, kbID uuid.UUID) (types.KnowledgeBase, error) {
	kb, err := s.st.KBs.GetOwned(ctx, kbID, owner)
	if err != nil {
		return kb, ErrFormNotFound
	}
	return kb, nil
}

// form loads a form of a KB the user owns.
func (s *Service) form(ctx context.Context, owner, id uuid.UUID) (types.ExportTemplate, types.KnowledgeBase, error) {
	t, err := s.st.Forms.Get(ctx, id)
	if err != nil {
		return t, types.KnowledgeBase{}, ErrFormNotFound
	}
	kb, err := s.formKB(ctx, owner, t.KBID)
	return t, kb, err
}

// formName is the name a new form gets: its title in sentence case, else
// the file name.
func formName(title, fileName string) string {
	name := strings.TrimSpace(title)
	if name == "" {
		name = strings.TrimSuffix(path.Base(fileName), path.Ext(fileName))
	}
	if strings.ToUpper(name) == name {
		r := []rune(strings.ToLower(name))
		if len(r) > 0 {
			r[0] = unicode.ToUpper(r[0])
		}
		name = string(r)
	}
	return name
}

// UploadForm stores an Excel form of a KB (§6.9.8): the code finds its
// regions, one model call proposes how its cells are filled, and the file
// goes to S3 unchanged. When the model fails, the form is still stored with
// every column blank for the user to map.
func (s *Service) UploadForm(ctx context.Context, user types.User, kbID uuid.UUID, fileName string, data []byte) (types.ExportTemplate, error) {
	kb, err := s.formKB(ctx, user.ID, kbID)
	if err != nil {
		return types.ExportTemplate{}, err
	}
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return types.ExportTemplate{}, ErrBadForm
	}
	defer f.Close()
	l, err := DetectForm(f)
	if err != nil {
		return types.ExportTemplate{}, fmt.Errorf("%w: %v", ErrBadForm, err)
	}
	var m types.ExportMapping
	if s.formLLM != nil {
		mctx := llm.WithCaller(ctx, llm.Caller{UserID: &user.ID, Kind: types.UsageSheet})
		if m, err = ProposeMapping(mctx, s.formLLM, l, kb.Config); err != nil {
			s.log.Warn("form: no mapping proposed", "kb", kbID, "err", err)
		}
	}
	if m.DocType == "" {
		m = blankMapping(l, kb.Config)
	}
	id := uuid.New()
	key := fmt.Sprintf("export-templates/%s/%s.xlsx", kbID, id)
	if _, err := s.objects.Put(ctx, key, bytes.NewReader(data), int64(len(data)),
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"); err != nil {
		return types.ExportTemplate{}, err
	}
	return s.st.Forms.Create(ctx, types.ExportTemplate{ID: id, KBID: kbID, Name: formName(l.Title, fileName), FileName: path.Base(fileName),
		ObjectKey: key, Layout: l, Mapping: m, CreatedBy: user.ID})
}

// blankMapping leaves every column and placeholder for the user.
func blankMapping(l types.ExportLayout, kb types.KBConfig) types.ExportMapping {
	m := types.ExportMapping{Columns: []types.ExportColumnMap{}, Placeholders: []types.ExportFillMap{}}
	for _, t := range kb.DocTypes {
		if len(t.Fields) > 0 {
			m.DocType = t.Name
			break
		}
	}
	if m.DocType == "" && len(kb.DocTypes) > 0 {
		m.DocType = kb.DocTypes[0].Name
	}
	for _, c := range l.Columns {
		m.Columns = append(m.Columns, types.ExportColumnMap{Col: c.Col, Kind: types.ExportBlank})
	}
	for _, p := range l.Placeholders {
		m.Placeholders = append(m.Placeholders, types.ExportFillMap{Cell: p.Cell})
	}
	return m
}

// ListForms returns the forms of a KB.
func (s *Service) ListForms(ctx context.Context, owner, kbID uuid.UUID) ([]types.ExportTemplate, error) {
	if _, err := s.formKB(ctx, owner, kbID); err != nil {
		return nil, err
	}
	return s.st.Forms.ListByKB(ctx, kbID)
}

// GetForm returns a form.
func (s *Service) GetForm(ctx context.Context, owner, id uuid.UUID) (types.ExportTemplate, error) {
	t, _, err := s.form(ctx, owner, id)
	return t, err
}

// SaveForm checks and stores the mapping the user reviewed. The new fields
// it proposes are added to their document types in the KB first, so the
// next sheet extracts them (U80); the stored mapping then has none left.
func (s *Service) SaveForm(ctx context.Context, user types.User, id uuid.UUID, name string, m types.ExportMapping) (types.ExportTemplate, error) {
	t, kb, err := s.form(ctx, user.ID, id)
	if err != nil {
		return t, err
	}
	m, err = CheckMapping(t.Layout, kb.Config, m)
	if err != nil {
		return t, fmt.Errorf("%w: %v", ErrBadMapping, err)
	}
	if len(m.NewFields) > 0 {
		cfg := kb.Config
		cfg.DocTypes = append([]types.DocType(nil), cfg.DocTypes...)
		for _, nf := range m.NewFields {
			for i := range cfg.DocTypes {
				if cfg.DocTypes[i].Name == nf.DocType {
					cfg.DocTypes[i].Fields = append(append([]types.SheetField(nil), cfg.DocTypes[i].Fields...), nf.Field)
				}
			}
		}
		if err := types.ValidateDocTypes(cfg.DocTypes); err != nil {
			return t, fmt.Errorf("%w: %v", ErrBadMapping, err)
		}
		if _, err := s.st.KBs.Update(ctx, kb.ID, user.ID, postgres.KBPatch{Config: &cfg}); err != nil {
			return t, err
		}
		m.NewFields = nil
	}
	if name = strings.TrimSpace(name); name == "" {
		name = t.Name
	}
	return s.st.Forms.Update(ctx, t.ID, name, m)
}

// DeleteForm removes a form and its file; sheets built for it stay.
func (s *Service) DeleteForm(ctx context.Context, owner, id uuid.UUID) error {
	t, _, err := s.form(ctx, owner, id)
	if err != nil {
		return err
	}
	if err := s.st.Forms.Delete(ctx, t.ID); err != nil {
		return err
	}
	_ = s.objects.Delete(ctx, t.ObjectKey)
	return nil
}

// formTables are the document types a form reads: that of its rows, then
// those of its placeholders.
func formTables(m types.ExportMapping) []string {
	out := []string{m.DocType}
	for _, p := range m.Placeholders {
		if p.DocType != "" && p.Key != "" && !contains(out, p.DocType) {
			out = append(out, p.DocType)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// CreateForForm builds a sheet of a case for a form (§6.9.8): the usual sheet
// of the form's document types (extracting only what the case does not have
// yet, U80), named after the form, whose Studio row downloads the form.
func (s *Service) CreateForForm(ctx context.Context, user types.User, caseID, formID uuid.UUID) (types.Sheet, error) {
	c, err := s.cases.GetCaseOwned(ctx, user.ID, caseID)
	if err != nil {
		return types.Sheet{}, err
	}
	t, err := s.st.Forms.Get(ctx, formID)
	if err != nil || t.KBID != c.KBID {
		return types.Sheet{}, ErrFormNotFound
	}
	return s.create(ctx, user, c, formTables(t.Mapping), t.Name, &t.ID)
}

// FormXLSX fills a form with the values of a sheet (§6.9.8) — the current
// ones, so what the user corrected on the sheet page is in the file — and
// returns its file name. formID nil means the form the sheet was built for.
func (s *Service) FormXLSX(ctx context.Context, owner, sheetID uuid.UUID, formID *uuid.UUID, w io.Writer) (string, error) {
	sh, c, err := s.owned(ctx, owner, sheetID)
	if err != nil {
		return "", err
	}
	if formID == nil {
		formID = sh.FormID
	}
	if formID == nil {
		return "", ErrFormNotFound
	}
	t, err := s.st.Forms.Get(ctx, *formID)
	if err != nil || t.KBID != c.KBID {
		return "", ErrFormNotFound
	}
	if sh.Status != types.SheetDone {
		return "", ErrNotFinished
	}
	v, err := s.view(ctx, sh, c)
	if err != nil {
		return "", err
	}
	var rows *TableView
	byLabel := map[string]*TableView{}
	for i := range v.TablesView {
		byLabel[v.TablesView[i].Label] = &v.TablesView[i]
	}
	if rows = byLabel[t.Mapping.DocType]; rows == nil {
		return "", ErrFormSheet
	}
	valueTypes := map[string]string{}
	for _, fd := range rows.Fields {
		valueTypes[fd.Key] = fd.ValueType
	}
	recs := make([]formRecord, 0, len(rows.Rows))
	for _, r := range rows.Rows {
		rec := formRecord{}
		for k, cv := range r.CellsView {
			rec[k] = cv.Value
		}
		recs = append(recs, rec)
	}
	fills := map[string]string{}
	for _, p := range t.Mapping.Placeholders {
		switch {
		case p.Text != "":
			fills[p.Cell] = p.Text
		case p.Key != "":
			if tv := byLabel[p.DocType]; tv != nil {
				for _, r := range tv.Rows {
					if val := strings.TrimSpace(r.CellsView[p.Key].Value); val != "" {
						fills[p.Cell] = val
						break
					}
				}
			}
		}
	}
	rc, _, err := s.objects.Get(ctx, t.ObjectKey)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	f, err := excelize.OpenReader(rc)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := RenderForm(f, t.Layout, t.Mapping, valueTypes, recs, fills); err != nil {
		return "", err
	}
	if err := f.Write(w); err != nil {
		return "", err
	}
	name := unsafeName.ReplaceAllString(c.Code+"_"+t.Name, "-")
	return strings.Trim(name, "-") + ".xlsx", nil
}
