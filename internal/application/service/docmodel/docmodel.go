// Package docmodel owns the Extracted Fields of the document model (§6.9.3):
// it resolves each field's citations into evidence inside the case, checks
// the value against the quoted text and stores the field.
package docmodel

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
	"github.com/thanhenti/bepaylot/internal/types/interfaces"
)

// Service implements interfaces.FieldService.
type Service struct {
	st       *postgres.Store
	searcher interfaces.Searcher
}

// New builds the service.
func New(st *postgres.Store, searcher interfaces.Searcher) *Service {
	return &Service{st: st, searcher: searcher}
}

var valueTypes = map[string]bool{"string": true, "number": true, "money": true, "date": true, "bool": true, "json": true}

// SaveAgentFields stores the agent's fields as proposals (§6.9.3). A field
// without one valid evidence in the case is refused, and so is a value not
// found in its quotes without a note saying how it was derived.
func (s *Service) SaveAgentFields(ctx context.Context, owner, caseID uuid.UUID, sessionID *uuid.UUID, in []types.FieldInput) []interfaces.FieldResult {
	out := make([]interfaces.FieldResult, 0, len(in))
	for _, f := range in {
		res := interfaces.FieldResult{Key: f.Key}
		saved, err := s.saveOne(ctx, owner, caseID, sessionID, f)
		if err != nil {
			res.Error = err.Error()
		} else {
			res.ID, res.Status, res.ValueMatched = saved.ID.String(), saved.Status, saved.ValueMatched
		}
		out = append(out, res)
	}
	return out
}

func (s *Service) saveOne(ctx context.Context, owner, caseID uuid.UUID, sessionID *uuid.UUID, f types.FieldInput) (types.Field, error) {
	key := strings.TrimSpace(f.Key)
	if key == "" {
		return types.Field{}, fmt.Errorf("key is required")
	}
	ok, err := s.searcher.DocumentInCase(ctx, owner, f.DocumentID, caseID)
	if err != nil {
		return types.Field{}, err
	}
	if !ok {
		return types.Field{}, fmt.Errorf("document %s not found", f.DocumentID)
	}
	if f.SegmentID != nil {
		g, err := s.st.Segments.Get(ctx, *f.SegmentID)
		if err != nil || g.DocumentID != f.DocumentID {
			return types.Field{}, fmt.Errorf("segment %s is not a document of file %s", f.SegmentID, f.DocumentID)
		}
	}
	vt := f.ValueType
	if !valueTypes[vt] {
		vt = "string"
	}
	evidence := s.resolve(ctx, owner, caseID, f.Citations)
	if len(evidence) == 0 {
		return types.Field{}, fmt.Errorf("no valid evidence: give citation_id of lines in this case")
	}
	text := textutil.ValueText(f.Value)
	matched := false
	for _, e := range evidence {
		if textutil.ValueIn(text, e.Quote) {
			matched = true
			break
		}
	}
	if !matched && strings.TrimSpace(f.Note) == "" {
		return types.Field{}, fmt.Errorf("value %q is not in the quoted text; add a note saying how it was derived", text)
	}
	conf := math.Max(0, math.Min(1, f.Confidence))
	return s.st.Fields.Save(ctx, postgres.FieldWrite{
		CaseID: caseID, DocumentID: f.DocumentID, SegmentID: f.SegmentID, Key: key, Ord: f.Ord, Value: f.Value, ValueType: vt, ValueText: text,
		ValueMatched: matched, Confidence: conf, Status: types.FieldProposed, Source: types.FieldSourceAgent,
		SessionID: sessionID, CreatedBy: &owner, Note: f.Note, Evidence: evidence,
	})
}

// resolve turns citations into evidence; citations outside the case are
// dropped like missing ones (§6.9.2).
func (s *Service) resolve(ctx context.Context, owner, caseID uuid.UUID, citations []string) []types.Evidence {
	var out []types.Evidence
	for _, c := range citations {
		hits, err := s.searcher.Locate(ctx, owner, strings.TrimSpace(c))
		if err != nil {
			continue
		}
		for _, h := range hits {
			if ok, _ := s.searcher.DocumentInCase(ctx, owner, h.DocumentID, caseID); !ok {
				continue
			}
			e := types.Evidence{DocumentID: h.DocumentID, FileName: h.FileName, PageNo: h.PageNo, Quote: h.Quote, CitationID: h.CitationID, Status: "valid"}
			if e.CitationID == "" {
				e.CitationID = c
			}
			if len(h.Lines) > 0 {
				e.LineFrom, e.LineTo = h.Lines[0], h.Lines[len(h.Lines)-1]
			}
			e.BBox = union(h.BBoxes)
			out = append(out, e)
		}
	}
	return out
}

func union(bs []types.BBox) []float64 {
	if len(bs) == 0 {
		return nil
	}
	u := bs[0]
	for _, b := range bs[1:] {
		u.X0, u.Y0 = math.Min(u.X0, b.X0), math.Min(u.Y0, b.Y0)
		u.X1, u.Y1 = math.Max(u.X1, b.X1), math.Max(u.Y1, b.Y1)
	}
	return []float64{u.X0, u.Y0, u.X1, u.Y1}
}

// SegmentAt returns the k-th (1-based) document of a file in page order: the
// s<k> of a segment ref d<n>.s<k> (§8.2).
func (s *Service) SegmentAt(ctx context.Context, doc uuid.UUID, k int) (uuid.UUID, error) {
	segs, err := s.st.Segments.ByDocument(ctx, doc)
	if err != nil {
		return uuid.Nil, err
	}
	if k < 1 || k > len(segs) {
		return uuid.Nil, fmt.Errorf("segment s%d not found (the file has %d documents)", k, len(segs))
	}
	return segs[k-1].ID, nil
}

// CaseFields lists the current and unreviewed fields of a case.
func (s *Service) CaseFields(ctx context.Context, caseID uuid.UUID, doc *uuid.UUID, key string) ([]types.Field, error) {
	return s.st.Fields.ListByCase(ctx, caseID, doc, key)
}

var _ interfaces.FieldService = (*Service)(nil)
