package index

import (
	"context"
	"sort"

	"github.com/thanhenti/bepaylot/docpipe"
	"github.com/thanhenti/bepaylot/internal/types"
)

// SegmentsFromPages groups the pages of a file into documents (§6.9.4):
// every first page (is_first_page) starts a segment, the pages after it
// belong to it (docpipe.GroupByFirstPage). A gap in the page numbers (pages
// not picked for processing, U111) ends a segment: each run of consecutive
// pages is grouped on its own. It is pure.
func SegmentsFromPages(pages []types.PageDocType, min float64) []types.Segment {
	var out []types.Segment
	for start := 0; start < len(pages); {
		end := start + 1
		for end < len(pages) && pages[end].PageNo == pages[end-1].PageNo+1 {
			end++
		}
		for _, d := range docpipe.GroupByFirstPage(pages[start:end], min) {
			out = append(out, types.Segment{PageStart: d.PageStart, PageEnd: d.PageEnd, Label: d.Label, ProposedLabel: d.ProposedLabel, Confidence: d.Confidence})
		}
		start = end
	}
	return out
}

// uncovered reports whether a classified page lies outside every segment:
// pages picked after the segments were proposed (U111).
func uncovered(pages []types.PageDocType, segs []types.Segment) bool {
	for _, p := range pages {
		if p.Label == "" {
			continue
		}
		in := false
		for _, g := range segs {
			in = in || (p.PageNo >= g.PageStart && p.PageNo <= g.PageEnd)
		}
		if !in {
			return true
		}
	}
	return false
}

// docSpans turns the active segments of a file into the spans of its tree:
// the user's segments win over proposals overlapping them, and pages no
// segment covers are an unknown span, or a not-processed span for pages not
// picked for processing (skipped, U111), so the spans cover every page. No
// segment gives no span.
func docSpans(segs []types.Segment, pageCount int, skipped map[int]bool, rule types.ClassificationRule) []docSpan {
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
		for next <= to {
			end, skip := next, skipped[next]
			for end < to && skipped[end+1] == skip {
				end++
			}
			label := types.LabelUnknown
			if skip {
				label = types.LabelNotProcessed
			}
			out = append(out, docSpan{label: label, title: rule.Title(label), pageStart: next, pageEnd: end})
			next = end + 1
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
	pages, err := s.st.Pages.DocTypes(ctx, d.ID, d.Gen)
	if err != nil {
		return nil, err
	}
	// Proposals are made once, and again when pages picked later (U111)
	// are outside every segment; the user's segments are kept.
	if len(segs) == 0 || uncovered(pages, segs) {
		min := rule.MinConfidence
		if min <= 0 {
			min = s.cfg.Classify.MinConfidence
		}
		if props := SegmentsFromPages(pages, min); len(props) > 0 {
			if err := s.st.Segments.SaveProposals(ctx, d.CaseID, d.ID, d.Gen, props, "image", s.cfg.Classify.Decision.Model); err != nil {
				return nil, err
			}
			if segs, err = s.st.Segments.ByDocument(ctx, d.ID); err != nil {
				return nil, err
			}
		}
	}
	skipped := map[int]bool{}
	for p := 1; p <= pageCount; p++ {
		skipped[p] = true
	}
	for _, p := range pages {
		delete(skipped, p.PageNo)
	}
	return docSpans(segs, pageCount, skipped, rule), nil
}
