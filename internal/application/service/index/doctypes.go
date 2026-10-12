package index

import (
	"context"
	"sort"

	"github.com/thanhenti/bepaylot/internal/types"
)

// SegmentsFromPages groups the pages of a file into documents by the type
// the Decision API gave each page (§6.9.4): consecutive pages of one type
// are one segment and a change of type starts a new one; so does a page the
// Decision API marked as the first page of a document (Break), which cuts
// consecutive documents of one type apart. A blank page is
// other, whatever the model said. A page below min (or not classified)
// continues the document before it; so does a page typed other that has
// content but no document title of its own, when the document before it has
// a type: the pages after a document's first page often look like no type.
// At the start of the file pages below min are an unknown segment. A
// segment's confidence is the mean of its confident pages. Without any
// classified page there is no segment. It is pure.
func SegmentsFromPages(pages []types.PageDocType, min float64) []types.Segment {
	classified := false
	for _, p := range pages {
		classified = classified || p.Label != ""
	}
	if !classified {
		return nil
	}
	var out []types.Segment
	sure := 0 // confident pages of the last segment
	for _, p := range pages {
		if p.Blank {
			p.Label, p.Confidence = types.LabelOther, 1
		}
		ok := p.Label != "" && p.Confidence >= min
		last := len(out) - 1
		typed := last >= 0 && out[last].Label != types.LabelOther && out[last].Label != types.LabelUnknown
		switch {
		case last >= 0 && !ok:
			out[last].PageEnd = p.PageNo
		case typed && p.Label == types.LabelOther && !p.Blank && !p.Opens:
			out[last].PageEnd = p.PageNo
		case last >= 0 && out[last].Label == p.Label && !(typed && p.Break):
			out[last].PageEnd = p.PageNo
			out[last].Confidence = (out[last].Confidence*float64(sure) + p.Confidence) / float64(sure+1)
			sure++
		case ok:
			out = append(out, types.Segment{PageStart: p.PageNo, PageEnd: p.PageNo, Label: p.Label, Confidence: p.Confidence})
			sure = 1
		default:
			out = append(out, types.Segment{PageStart: p.PageNo, PageEnd: p.PageNo, Label: types.LabelUnknown, ProposedLabel: p.Label, Confidence: p.Confidence})
			sure = 0
		}
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
// proposals (source=pipeline, mode=image). blank and opens mark the pages
// without content and the pages that start with a document title.
func (s *Service) spansOf(ctx context.Context, d types.Document, pageCount int, blank, opens map[int]bool) ([]docSpan, error) {
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
		for i, p := range pages {
			pages[i].Blank, pages[i].Opens = blank[p.PageNo], opens[p.PageNo]
		}
		if segs = SegmentsFromPages(pages, min); len(segs) > 0 {
			if err := s.st.Segments.SaveProposals(ctx, d.CaseID, d.ID, d.Gen, segs, "image", s.cfg.Classify.Decision.Model); err != nil {
				return nil, err
			}
		}
	}
	return docSpans(segs, pageCount, rule), nil
}
