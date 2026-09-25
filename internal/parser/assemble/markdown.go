package assemble

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/thanhenti/bepaylot/internal/types"
)

// Render renumbers blocks and lines in slice order, renders the page markdown
// and records rune offsets of every block and line in it. Lines keep their
// relative order; they are grouped by block.
func Render(page *types.ParsedPage) {
	renumber(page)

	var sb strings.Builder
	pos := 0 // rune offset of sb
	write := func(s string) {
		sb.WriteString(s)
		pos += utf8.RuneCountInString(s)
	}

	byBlock := make(map[int][]int, len(page.Blocks))
	for i := range page.Lines {
		page.Lines[i].MdStart, page.Lines[i].MdEnd = -1, -1
		byBlock[page.Lines[i].BlockNo] = append(byBlock[page.Lines[i].BlockNo], i)
	}

	first := true
	sep := func() {
		if !first {
			write("\n\n")
		}
		first = false
	}

	for bi := range page.Blocks {
		b := &page.Blocks[bi]
		b.MdStart, b.MdEnd = -1, -1
		lines := byBlock[b.BlockNo]
		if b.IsFurniture {
			continue
		}
		// writeLines emits one markdown line per text line, with a prefix,
		// and records the line offsets (after the prefix).
		writeLines := func(prefix string) {
			n := 0
			for _, li := range lines {
				l := &page.Lines[li]
				if l.Text == "" {
					continue
				}
				if n > 0 {
					write("\n")
				}
				write(prefix)
				l.MdStart = pos
				write(l.Text)
				l.MdEnd = pos
				n++
			}
		}
		hasText := false
		for _, li := range lines {
			if page.Lines[li].Text != "" {
				hasText = true
				break
			}
		}

		switch b.Type {
		case types.BlockTable:
			md := tableMarkdown(b.HTML)
			if md == "" && !hasText {
				continue
			}
			sep()
			b.MdStart = pos
			if md != "" {
				write(md)
			} else {
				writeLines("")
			}
			b.MdEnd = pos
		case types.BlockFigure:
			if b.AssetKey == "" && !hasText {
				continue
			}
			sep()
			b.MdStart = pos
			if b.AssetKey != "" {
				write(fmt.Sprintf("![figure p%d-b%d](%s)", page.PageNo, b.BlockNo, b.AssetKey))
				if hasText {
					write("\n")
				}
			}
			writeLines("> ")
			b.MdEnd = pos
		case types.BlockFormula:
			if b.LaTeX == "" && !hasText {
				continue
			}
			sep()
			b.MdStart = pos
			if b.LaTeX != "" {
				write("$$\n" + strings.TrimSpace(b.LaTeX) + "\n$$")
			} else {
				writeLines("")
			}
			b.MdEnd = pos
		case types.BlockTitle, types.BlockHeading:
			if !hasText {
				continue
			}
			sep()
			b.MdStart = pos
			prefix := "# "
			if b.Type == types.BlockHeading {
				prefix = headingPrefix(b.Text)
			}
			// A multi-line heading is one markdown heading line.
			write(prefix)
			n := 0
			for _, li := range lines {
				l := &page.Lines[li]
				if l.Text == "" {
					continue
				}
				if n > 0 {
					write(" ")
				}
				l.MdStart = pos
				write(l.Text)
				l.MdEnd = pos
				n++
			}
			b.MdEnd = pos
		default:
			if !hasText {
				continue
			}
			sep()
			b.MdStart = pos
			writeLines("")
			b.MdEnd = pos
		}
	}
	page.Markdown = sb.String()

	blank := true
	for _, l := range page.Lines {
		if strings.TrimSpace(l.Text) != "" {
			blank = false
			break
		}
	}
	for _, b := range page.Blocks {
		if b.HTML != "" || b.LaTeX != "" {
			blank = false
		}
	}
	page.IsBlank = blank
}

var (
	reNumbered2 = regexp.MustCompile(`^\d+\.\d+(\.\d+)*[.)]?\s`)
	reRoman     = regexp.MustCompile(`^[IVXLC]+[.)]\s`)
)

func headingPrefix(text string) string {
	switch {
	case reNumbered2.MatchString(text):
		return "### "
	case reRoman.MatchString(text):
		return "## "
	default:
		return "## "
	}
}

// renumber assigns BlockNo by slice order, remaps line block references and
// sorts lines by block (stable), then assigns LineNo.
func renumber(page *types.ParsedPage) {
	remap := make(map[int]int, len(page.Blocks))
	for i := range page.Blocks {
		remap[page.Blocks[i].BlockNo] = i
		page.Blocks[i].BlockNo = i
	}
	for i := range page.Lines {
		if nb, ok := remap[page.Lines[i].BlockNo]; ok {
			page.Lines[i].BlockNo = nb
		}
	}
	sort.SliceStable(page.Lines, func(a, b int) bool { return page.Lines[a].BlockNo < page.Lines[b].BlockNo })
	for i := range page.Lines {
		page.Lines[i].LineNo = i
	}
}

// PlainText returns the page text (non-furniture lines joined by newlines),
// used for page-level full-text search.
func PlainText(page *types.ParsedPage) string {
	furniture := map[int]bool{}
	for _, b := range page.Blocks {
		if b.IsFurniture {
			furniture[b.BlockNo] = true
		}
	}
	var parts []string
	for _, l := range page.Lines {
		if l.Text != "" && !furniture[l.BlockNo] {
			parts = append(parts, l.Text)
		}
	}
	return strings.Join(parts, "\n")
}
