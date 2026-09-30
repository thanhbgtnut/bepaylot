package vlm

import (
	"sort"
	"strings"

	"github.com/thanhenti/bepaylot/internal/parser"
	"github.com/thanhenti/bepaylot/internal/parser/assemble"
	"github.com/thanhenti/bepaylot/internal/types"
)

// AlignReport records how the page markdown was checked against the OCR text
// and carried onto the OCR regions (kept in RawPage.Raw).
type AlignReport struct {
	OCRWords int `json:"ocr_words"`
	// MatchedWords are OCR words with a close word in the markdown; Coverage
	// is their share. A low coverage means the markdown left out or
	// rewrote much of what OCR read.
	MatchedWords int     `json:"matched_words"`
	Coverage     float64 `json:"coverage"`
	Segments     int     `json:"segments"`
	// Regions lists the regions that received markdown, with the segment
	// indexes they got.
	Regions []RegionAssignment `json:"regions,omitempty"`
	// Unmatched holds markdown segments no OCR word agrees with. They are not
	// verified by OCR and are dropped.
	Unmatched []string `json:"unmatched,omitempty"`
	// Skipped says why no alignment was done (the page keeps OCR text).
	Skipped string `json:"skipped,omitempty"`
}

// RegionAssignment is one region that received markdown.
type RegionAssignment struct {
	LayoutID int    `json:"layout_id"`
	Class    string `json:"class"`
	Segments []int  `json:"segments"`
	// Created is set for a region made from OCR lines outside every layout
	// region.
	Created bool `json:"created,omitempty"`
}

// distribute carries the page markdown onto the OCR regions:
//
//  1. the markdown is split into segments (headings, paragraphs, tables,
//     formulas; figures are left to the layout);
//  2. the OCR words of the page, in reading order, are aligned with the
//     markdown words (monotone edit distance, as in assemble);
//  3. every segment goes to the region whose OCR words agree with most of
//     its words; OCR lines outside every region that a segment agrees with
//     become a new region;
//  4. a segment no OCR word agrees with is dropped (not verified by OCR).
//
// The segments of a region, joined, become its RawRegion.Text (or HTML for a
// table, LaTeX for a formula); assemble then aligns that text with the
// region's lines and puts back any OCR line the markdown left out.
func distribute(page *parser.RawPage, md string) AlignReport {
	segs := splitSegments(md, page.Width, page.Height, 1)
	rep := AlignReport{Segments: len(segs)}
	regionOf := lineRegions(page)

	var ocr []assemble.Word
	for _, li := range readingOrder(page) {
		ocr = append(ocr, assemble.Words(page.Lines[li].Text, li)...)
	}
	var vw []assemble.Word
	for si, s := range segs {
		if s.class == "figure" {
			continue
		}
		vw = append(vw, assemble.Words(s.text, si)...) // Line = segment index
	}
	rep.OCRWords = len(ocr)
	switch {
	case len(ocr) == 0 || len(vw) == 0:
		rep.Skipped = "no words to align"
		return rep
	case len(ocr)*len(vw) > assemble.MaxAlignCells:
		rep.Skipped = "page too long to align"
		return rep
	}

	pair, cost := assemble.Align(ocr, vw)
	votes := make([]map[int]int, len(segs))    // region index (-1: outside every region) → words
	orphans := make([]map[int]bool, len(segs)) // lines outside every region that agree with the segment
	for i, w := range ocr {
		if pair[i] < 0 || cost[i] > assemble.GoodMatch {
			continue
		}
		rep.MatchedWords++
		si, r := vw[pair[i]].Line, regionOf[w.Line]
		if votes[si] == nil {
			votes[si], orphans[si] = map[int]int{}, map[int]bool{}
		}
		votes[si][r]++
		if r < 0 {
			orphans[si][w.Line] = true
		}
	}
	rep.Coverage = float64(rep.MatchedWords) / float64(len(ocr))

	nextID := 0
	for _, r := range page.Regions {
		nextID = max(nextID, r.ID+1)
	}
	texts := map[int][]string{}
	segsOf := map[int][]int{}
	created := map[int]bool{}
	var order []int // regions in the order they first got a segment
	for si, s := range segs {
		if s.class == "figure" {
			continue
		}
		best, bestN := 0, 0
		for _, r := range voters(votes[si]) {
			if n := votes[si][r]; n > bestN {
				best, bestN = r, n
			}
		}
		if bestN == 0 {
			rep.Unmatched = append(rep.Unmatched, s.text)
			continue
		}
		if best < 0 {
			var isNew bool
			best, isNew = claimOrphans(page, regionOf, orphans[si], s.class, &nextID)
			created[best] = created[best] || isNew
		}
		if _, ok := texts[best]; !ok {
			order = append(order, best)
		}
		texts[best] = append(texts[best], s.text)
		segsOf[best] = append(segsOf[best], si)
	}
	for _, ri := range order {
		r := &page.Regions[ri]
		apply(r, strings.Join(texts[ri], "\n\n"))
		rep.Regions = append(rep.Regions, RegionAssignment{LayoutID: r.ID, Class: r.Class, Segments: segsOf[ri], Created: created[ri]})
	}
	return rep
}

// voters returns the regions that voted for a segment: real regions in
// index order, then -1 (outside every region), so ties go to a real region
// and then to the earlier one.
func voters(votes map[int]int) []int {
	out := make([]int, 0, len(votes))
	for r := range votes {
		out = append(out, r)
	}
	sort.Slice(out, func(a, b int) bool {
		if (out[a] < 0) != (out[b] < 0) {
			return out[b] < 0
		}
		return out[a] < out[b]
	})
	return out
}

// claimOrphans turns the OCR lines outside every region that agree with one
// segment into a new region (confidence 0) and returns its index. When an
// earlier segment already claimed all of them, the segment joins that region.
func claimOrphans(page *parser.RawPage, regionOf []int, lines map[int]bool, class string, nextID *int) (int, bool) {
	all := make([]int, 0, len(lines))
	for li := range lines {
		all = append(all, li)
	}
	sort.Ints(all)
	var free []int
	for _, li := range all {
		if regionOf[li] < 0 {
			free = append(free, li)
		}
	}
	if len(free) == 0 {
		return regionOf[all[0]], false
	}
	if class == "" {
		class = "text"
	}
	var box types.BBox
	for _, li := range free {
		box = box.Union(page.Lines[li].Quad.BBox())
	}
	id := *nextID
	*nextID++
	page.Regions = append(page.Regions, parser.RawRegion{ID: id, Class: class, Quad: types.QuadFromBBox(box)})
	ri := len(page.Regions) - 1
	for _, li := range free {
		page.Lines[li].LayoutID = id
		regionOf[li] = ri
	}
	return ri, true
}

// lineRegions returns, for every OCR line, the index of its region in
// page.Regions: the engine's assignment, else the smallest region holding
// the line's centre (as assemble.Build does), else -1.
func lineRegions(page *parser.RawPage) []int {
	byID := make(map[int]int, len(page.Regions))
	for i, r := range page.Regions {
		byID[r.ID] = i
	}
	out := make([]int, len(page.Lines))
	for i, l := range page.Lines {
		if ri, ok := byID[l.LayoutID]; ok {
			out[i] = ri
			continue
		}
		out[i] = -1
		b := l.Quad.BBox()
		cx, cy := (b.X0+b.X1)/2, (b.Y0+b.Y1)/2
		var bestArea float64
		for ri, r := range page.Regions {
			rb := r.Quad.BBox()
			if !rb.Contains(cx, cy) {
				continue
			}
			if area := rb.Width() * rb.Height(); out[i] < 0 || area < bestArea {
				out[i], bestArea = ri, area
			}
		}
	}
	return out
}

// readingOrder returns line indexes in reading order (assemble.LineRanks).
func readingOrder(page *parser.RawPage) []int {
	rank := assemble.LineRanks(page)
	out := make([]int, len(page.Lines))
	for i, r := range rank {
		out[r] = i
	}
	return out
}
