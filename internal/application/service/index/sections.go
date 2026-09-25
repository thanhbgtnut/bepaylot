package index

import (
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
)

// piece is one rendered block of a page.
type piece struct {
	page      *types.ParsedPage
	block     types.ParsedBlock
	md        string
	lineFrom  int
	lineTo    int
	hasLines  bool
	level     int // heading level, 0 for body
	tokens    int
	docMdFrom int
	docMdTo   int
}

// BuildSections splits parsed pages into sections (§6.3): headings open a new
// section, tables are sections of their own (split by rows with the header
// repeated when long), and body text is cut at maxTokens. Furniture and empty
// blocks are skipped. It is pure.
func BuildSections(docID, kbID uuid.UUID, gen int, pages []*types.ParsedPage, maxTokens int) []types.Section {
	if maxTokens <= 0 {
		maxTokens = 1500
	}
	sorted := append([]*types.ParsedPage(nil), pages...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].PageNo < sorted[j].PageNo })

	var out []types.Section
	var cur []piece
	var headings []string // current heading path
	var levels []int

	flush := func(kind string) {
		if len(cur) == 0 {
			return
		}
		out = append(out, makeSection(docID, kbID, gen, len(out), kind, cur, headings))
		cur = nil
	}
	curTokens := func() int {
		n := 0
		for _, p := range cur {
			n += p.tokens
		}
		return n
	}
	onlyHeadings := func() bool {
		for _, p := range cur {
			if p.level == 0 {
				return false
			}
		}
		return true
	}

	for _, pg := range sorted {
		runes := []rune(pg.Markdown)
		lineRange := map[int][2]int{}
		for _, l := range pg.Lines {
			r, ok := lineRange[l.BlockNo]
			if !ok {
				r = [2]int{l.LineNo, l.LineNo}
			}
			r[0], r[1] = min(r[0], l.LineNo), max(r[1], l.LineNo)
			lineRange[l.BlockNo] = r
		}
		blocks := append([]types.ParsedBlock(nil), pg.Blocks...)
		sort.Slice(blocks, func(i, j int) bool { return blocks[i].BlockNo < blocks[j].BlockNo })
		for _, b := range blocks {
			if b.IsFurniture || b.MdStart < 0 || b.MdEnd <= b.MdStart || b.MdEnd > len(runes) {
				continue
			}
			md := string(runes[b.MdStart:b.MdEnd])
			p := piece{page: pg, block: b, md: md, tokens: textutil.EstimateTokens(md),
				docMdFrom: pg.DocMdOffset + b.MdStart, docMdTo: pg.DocMdOffset + b.MdEnd}
			if r, ok := lineRange[b.BlockNo]; ok {
				p.lineFrom, p.lineTo, p.hasLines = r[0], r[1], true
			}
			switch b.Type {
			case types.BlockTitle, types.BlockHeading:
				p.level = headingLevel(md, b.Type)
				flush("text")
				for len(levels) > 0 && levels[len(levels)-1] >= p.level {
					levels, headings = levels[:len(levels)-1], headings[:len(headings)-1]
				}
				levels = append(levels, p.level)
				headings = append(headings, strings.TrimSpace(strings.TrimLeft(md, "# ")))
				cur = append(cur, p)
			case types.BlockTable:
				if !onlyHeadings() {
					flush("text")
				}
				for _, part := range splitTable(p, maxTokens) {
					cur = append(cur, part)
					flush("table")
				}
			default:
				if len(cur) > 0 && !onlyHeadings() && curTokens()+p.tokens > maxTokens {
					flush("text")
				}
				cur = append(cur, p)
			}
		}
	}
	flush("text")
	return out
}

func headingLevel(md string, t types.BlockType) int {
	if t == types.BlockTitle {
		return 1
	}
	n := 0
	for _, r := range md {
		if r != '#' {
			break
		}
		n++
	}
	return max(n, 2)
}

// splitTable cuts a long GFM table into row groups, repeating the header.
func splitTable(p piece, maxTokens int) []piece {
	if p.tokens <= maxTokens || !strings.HasPrefix(p.md, "|") {
		return []piece{p}
	}
	rows := strings.Split(p.md, "\n")
	if len(rows) < 3 {
		return []piece{p}
	}
	header := rows[0] + "\n" + rows[1]
	var parts []piece
	var buf []string
	flush := func() {
		if len(buf) == 0 {
			return
		}
		q := p
		q.md = header + "\n" + strings.Join(buf, "\n")
		q.tokens = textutil.EstimateTokens(q.md)
		parts = append(parts, q)
		buf = nil
	}
	budget := textutil.EstimateTokens(header)
	for _, r := range rows[2:] {
		if len(buf) > 0 && budget+textutil.EstimateTokens(r) > maxTokens {
			flush()
			budget = textutil.EstimateTokens(header)
		}
		buf = append(buf, r)
		budget += textutil.EstimateTokens(r)
	}
	flush()
	return parts
}

func makeSection(docID, kbID uuid.UUID, gen, seq int, kind string, ps []piece, headings []string) types.Section {
	s := types.Section{
		ID: uuid.New(), DocumentID: docID, KBID: kbID, Gen: gen, Seq: seq, Kind: kind,
		HeadingPath: append([]string(nil), headings...),
		PageStart:   ps[0].page.PageNo, PageEnd: ps[len(ps)-1].page.PageNo,
		LineFrom: -1, LineTo: -1,
		DocMdStart: ps[0].docMdFrom, DocMdEnd: ps[len(ps)-1].docMdTo,
	}
	var parts []string
	spans := map[int]*types.SourceSpan{}
	var order []int
	for _, p := range ps {
		parts = append(parts, p.md)
		s.TokenCount += p.tokens
		sp, ok := spans[p.page.PageNo]
		if !ok {
			sp = &types.SourceSpan{Page: p.page.PageNo, LineFrom: -1, LineTo: -1}
			spans[p.page.PageNo] = sp
			order = append(order, p.page.PageNo)
		}
		sp.BlockNos = append(sp.BlockNos, p.block.BlockNo)
		sp.BBox = sp.BBox.Union(p.block.BBox)
		if p.hasLines {
			if sp.LineFrom < 0 || p.lineFrom < sp.LineFrom {
				sp.LineFrom = p.lineFrom
			}
			sp.LineTo = max(sp.LineTo, p.lineTo)
		}
	}
	for _, pg := range order {
		s.SourceSpans = append(s.SourceSpans, *spans[pg])
	}
	if first := s.SourceSpans[0]; first.LineFrom >= 0 {
		s.LineFrom = first.LineFrom
	}
	if last := s.SourceSpans[len(s.SourceSpans)-1]; last.LineTo >= 0 {
		s.LineTo = last.LineTo
	}
	s.Content = strings.Join(parts, "\n\n")
	return s
}
