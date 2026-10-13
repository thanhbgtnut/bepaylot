package index

import (
	"context"
	"sort"

	"github.com/thanhenti/bepaylot/docpipe"
	"github.com/thanhenti/bepaylot/internal/types"
)

// SegmentsFromPages groups the pages of a file into documents (§6.9.4):
// every first page (is_first_page) starts a segment, the pages after it
// belong to it (docpipe.GroupByFirstPage). It is pure.
func SegmentsFromPages(pages []types.PageDocType, min float64) []types.Segment {
	var out []types.Segment
	for _, d := range docpipe.GroupByFirstPage(pages, min) {
		out = append(out, types.Segment{PageStart: d.PageStart, PageEnd: d.PageEnd, Label: d.Label, ProposedLabel: d.ProposedLabel, Confidence: d.Confidence})
	}
	return out
}

// docSpans turns the active segments of a file into the spans of its tree:
// the user's segments win over proposals overlapping them, and pages no
// segment covers are an unknown span, so the spans cover every page. No
// segment gives no span.
func docSpans(segs []types.Segment, pageCount int, rule types.ClassificationRule) []docSpan {
	if len(segs) == 0 {
		return nil
	}
	segs = append([]types.Segment(nil), segs...)
	sort.SliceStable(segs, func(i, j int) bool {
		if ui, uj := segs[i].Source == types.SegmentUser, segs[j].Source == types.SegmentUser; ui != uj {
			return ui
		}
		return segs[i].PageStart < segs[j].PageStart
	})
	var keep []types.Segment
	for _, g := range segs {
		free := true
		for _, k := range keep {
			free = free && (g.PageEnd < k.PageStart || g.PageStart > k.PageEnd)
		}
		if free {
			keep = append(keep, g)
		}
	}
	sort.Slice(keep, func(i, j int) bool { return keep[i].PageStart < keep[j].PageStart })
	var out []docSpan
	next := 1
	gap := func(to int) {
		if next <= to {
			out = append(out, docSpan{label: types.LabelUnknown, title: rule.Title(types.LabelUnknown), pageStart: next, pageEnd: to})
		}
	}
	for _, g := range keep {
		gap(g.PageStart - 1)
		out = append(out, docSpan{label: g.Label, title: rule.Title(g.Label), pageStart: g.PageStart, pageEnd: g.PageEnd})
		next = g.PageEnd + 1
	}
	gap(pageCount)
	return out
}

// spansOf returns the documents of a file for its tree. A file without
// segments yet gets them from the document types of its pages, stored as
// proposals (source=pipeline, mode=image).
func (s *Service) spansOf(ctx context.Context, d types.Document, pageCount int) ([]docSpan, error) {
	if s.cases == nil {
		return nil, nil
	}
	c, err := s.cases.GetCase(ctx, d.CaseID)
	if err != nil {
		return nil, nil // case gone: the tree has no document types
	}
	rule := s.cases.Classification(ctx, c)
	segs, err := s.st.Segments.ByDocument(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	if len(segs) == 0 {
		pages, err := s.st.Pages.DocTypes(ctx, d.ID, d.Gen)
		if err != nil {
			return nil, err
		}
		min := rule.MinConfidence
		if min <= 0 {
			min = s.cfg.Classify.MinConfidence
		}
		if segs = SegmentsFromPages(pages, min); len(segs) > 0 {
			if err := s.st.Segments.SaveProposals(ctx, d.CaseID, d.ID, d.Gen, segs, "image", s.cfg.Classify.Decision.Model); err != nil {
				return nil, err
			}
		}
	}
	return docSpans(segs, pageCount, rule), nil
}
