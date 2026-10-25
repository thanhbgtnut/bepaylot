package sheets

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/docpipe/textutil"
	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/types"
)

// CaseFieldView is one field of a document as the field column of the case
// page shows it (§7.2, U99): the current value, the AI's value it replaced and
// where it was read.
type CaseFieldView struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	ValueType string `json:"value_type"`
	Value     string `json:"value"`
	AIValue   string `json:"ai_value"`
	Asked     bool   `json:"asked"`  // the AI was asked for it
	Edited    bool   `json:"edited"` // the user's value differs from the AI's
	Low       bool   `json:"low"`    // AI value below sheets.low_confidence
	PageNo    int    `json:"page_no,omitempty"`
	LineNo    int    `json:"line_no,omitempty"`
}

// CaseDocFields is one document (segment) of a file with the fields of its type.
type CaseDocFields struct {
	SegmentID  uuid.UUID       `json:"segment_id"`
	DocumentID uuid.UUID       `json:"document_id"`
	Label      string          `json:"label"`
	PageStart  int             `json:"page_start"`
	PageEnd    int             `json:"page_end"`
	Fields     []CaseFieldView `json:"fields"`
}

// CaseFields returns the documents of one file of a case whose type has
// fields, with the value of each field. Reading costs no model call: fields
// are only extracted when a sheet is built (U80).
func (s *Service) CaseFields(ctx context.Context, owner, caseID, docID uuid.UUID) ([]CaseDocFields, error) {
	c, err := s.cases.GetCaseOwned(ctx, owner, caseID)
	if err != nil {
		return nil, err
	}
	tables, segs, best, err := s.caseFieldData(ctx, c)
	if err != nil {
		return nil, err
	}
	out := []CaseDocFields{}
	for _, g := range segs {
		tb, ok := tables[g.Label]
		if g.DocumentID != docID || !ok {
			continue
		}
		d := CaseDocFields{SegmentID: g.ID, DocumentID: g.DocumentID, Label: g.Label, PageStart: g.PageStart, PageEnd: g.PageEnd, Fields: []CaseFieldView{}}
		for _, f := range tb.Fields {
			v, err := s.fieldView(ctx, f, best[slot{doc: g.DocumentID, seg: g.ID, key: f.Key}])
			if err != nil {
				return nil, err
			}
			d.Fields = append(d.Fields, v)
		}
		out = append(out, d)
	}
	return out, nil
}

// SaveCaseField stores the user's value of one field of a document (U99): a
// confirmed user field that supersedes the current one and keeps its
// evidence, as an edit on the sheet page does (§6.9.6).
func (s *Service) SaveCaseField(ctx context.Context, user types.User, caseID, segID uuid.UUID, key, value string) (CaseFieldView, error) {
	c, err := s.cases.GetCaseOwned(ctx, user.ID, caseID)
	if err != nil {
		return CaseFieldView{}, err
	}
	tables, segs, best, err := s.caseFieldData(ctx, c)
	if err != nil {
		return CaseFieldView{}, err
	}
	var seg *types.Segment
	for i := range segs {
		if segs[i].ID == segID {
			seg = &segs[i]
		}
	}
	if seg == nil {
		return CaseFieldView{}, fmt.Errorf("%w: segment %s", ErrUnknownKey, segID)
	}
	var def *types.SheetField
	for _, f := range tables[seg.Label].Fields {
		if f.Key == key {
			def = &f
		}
	}
	if def == nil {
		return CaseFieldView{}, fmt.Errorf("%w: %s %s", ErrUnknownKey, seg.Label, key)
	}
	value = strings.TrimSpace(value)
	vt := def.ValueType
	if vt == "" {
		vt = "string"
	}
	w := postgres.FieldWrite{CaseID: caseID, DocumentID: seg.DocumentID, SegmentID: &seg.ID, Key: key, Value: value, ValueType: vt, ValueText: value,
		Confidence: 1, Status: types.FieldConfirmed, Source: types.FieldSourceUser, CreatedBy: &user.ID}
	if cur, ok := best[slot{doc: seg.DocumentID, seg: seg.ID, key: key}]; ok {
		w.Evidence = cur.Evidence
		for _, ev := range cur.Evidence {
			if textutil.ValueIn(value, ev.Quote) {
				w.ValueMatched = true
			}
		}
	}
	f, err := s.st.Fields.Save(ctx, w)
	if err != nil {
		return CaseFieldView{}, err
	}
	return s.fieldView(ctx, *def, f)
}

// caseFieldData loads the document types with fields of the case's KB by
// code, the active segments of the case and the field each slot shows.
func (s *Service) caseFieldData(ctx context.Context, c types.Case) (map[string]types.SheetTable, []types.Segment, map[slot]types.Field, error) {
	kb, err := s.st.KBs.Get(ctx, c.KBID)
	if err != nil {
		return nil, nil, nil, err
	}
	tables := map[string]types.SheetTable{}
	for _, tb := range kb.Config.SheetTables() {
		tables[tb.Label] = tb
	}
	segs, err := s.st.Segments.ByCase(ctx, c.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	best, err := s.bestFields(ctx, c.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	return tables, segs, best, nil
}

// fieldView resolves the field a slot shows (zero when never asked): for a
// user's value, the AI value is the agent field it replaced.
func (s *Service) fieldView(ctx context.Context, def types.SheetField, f types.Field) (CaseFieldView, error) {
	v := CaseFieldView{Key: def.Key, Label: def.Label, ValueType: def.ValueType}
	if f.ID == uuid.Nil {
		return v, nil
	}
	v.Asked, v.Value = true, f.ValueText
	ai := f
	for hops := 0; ai.Source != types.FieldSourceAgent && ai.Supersedes != nil && hops < 8; hops++ {
		prev, err := s.st.Fields.Get(ctx, *ai.Supersedes)
		if err != nil {
			break
		}
		ai = prev
	}
	if ai.Source == types.FieldSourceAgent {
		v.AIValue = ai.ValueText
		v.Low = f.Source == types.FieldSourceAgent && ai.ValueText != "" && ai.Confidence < s.cfg.Sheets.LowConfidence
	} else {
		v.Asked = f.Source == types.FieldSourceAgent
	}
	v.Edited = f.Source == types.FieldSourceUser && textutil.ValueKey(f.ValueText) != textutil.ValueKey(v.AIValue)
	if len(f.Evidence) > 0 {
		v.PageNo, v.LineNo = f.Evidence[0].PageNo, f.Evidence[0].LineFrom
	}
	return v, nil
}
