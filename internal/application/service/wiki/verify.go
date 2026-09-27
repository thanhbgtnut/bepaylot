package wiki

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
)

// lineTexts is the text of a document's lines: page → line → text.
type lineTexts map[int]map[int]string

func linesOf(pages []*types.ParsedPage) lineTexts {
	out := lineTexts{}
	for _, p := range pages {
		m := map[int]string{}
		for _, l := range p.Lines {
			if strings.TrimSpace(l.Text) != "" {
				m[l.LineNo] = l.Text
			}
		}
		out[p.PageNo] = m
	}
	return out
}

// matchQuote finds the lines of a page that hold quote: the cited lines
// first, then any window of up to 4 consecutive lines. It returns the line
// range, or ok=false when the quote is not on the page (a hallucinated or
// stale citation).
func matchQuote(page map[int]string, lines []int, quote string, minSim float64) (int, int, bool) {
	q := textutil.Normalize(strings.TrimSuffix(strings.TrimSpace(quote), " (?)"))
	if q == "" || len(page) == 0 {
		return 0, 0, false
	}
	check := func(nums []int) bool {
		var parts []string
		for _, n := range nums {
			if t, ok := page[n]; ok {
				parts = append(parts, t)
			}
		}
		if len(parts) == 0 {
			return false
		}
		joined := textutil.Normalize(strings.Join(parts, " "))
		return strings.Contains(joined, q) || (strings.Contains(q, joined) && len(joined) > len(q)/2) ||
			textutil.Similarity(q, joined) >= minSim
	}
	nums := append([]int(nil), lines...)
	sort.Ints(nums)
	if len(nums) > 0 && check(nums) {
		return nums[0], nums[len(nums)-1], true
	}
	var all []int
	for n := range page {
		all = append(all, n)
	}
	sort.Ints(all)
	for w := 1; w <= 4; w++ {
		for i := 0; i+w <= len(all); i++ {
			if check(all[i : i+w]) {
				return all[i], all[i+w-1], true
			}
		}
	}
	return 0, 0, false
}

// citationID formats doc:<uuid>:p<page>:l<a>-<b> (same as search hits).
func citationID(doc uuid.UUID, page, from, to int) string {
	return fmt.Sprintf("doc:%s:p%d:l%d-%d", doc, page, from, to)
}

var citationRe = regexp.MustCompile(`doc:([0-9a-fA-F-]{36}):p(\d+)(?::l(\d+)(?:-l?(\d+))?)?`)

// parseCitation is the inverse of citationID; a page-only citation has
// lines -1.
func parseCitation(c string) (uuid.UUID, int, int, int, bool) {
	m := citationRe.FindStringSubmatch(strings.TrimSpace(c))
	if m == nil || m[0] != strings.TrimSpace(c) {
		return uuid.Nil, 0, 0, 0, false
	}
	id, err := uuid.Parse(m[1])
	if err != nil {
		return uuid.Nil, 0, 0, 0, false
	}
	page, _ := strconv.Atoi(m[2])
	lo, hi := -1, -1
	if m[3] != "" {
		lo, _ = strconv.Atoi(m[3])
		hi = lo
		if m[4] != "" {
			hi, _ = strconv.Atoi(m[4])
		}
	}
	return id, page, lo, hi, true
}

var footRefRe = regexp.MustCompile(`\[\^(\d+)\]`)

// footnoteRefs lists the footnote numbers used in text.
func footnoteRefs(text string) map[int]bool {
	out := map[int]bool{}
	for _, m := range footRefRe.FindAllStringSubmatch(text, -1) {
		n, _ := strconv.Atoi(m[1])
		out[n] = true
	}
	return out
}

// dropFootnotes removes what only rests on dropped footnotes (§6.8 step 5):
// a table row, list item or heading whose footnotes are all dropped goes
// entirely; in prose, the sentences whose footnotes are all dropped go.
// Remaining markers of dropped footnotes are erased.
func dropFootnotes(content string, dropped map[int]bool) string {
	if len(dropped) == 0 {
		return content
	}
	onlyDropped := func(s string) bool {
		refs := footnoteRefs(s)
		if len(refs) == 0 {
			return false
		}
		for n := range refs {
			if !dropped[n] {
				return false
			}
		}
		return true
	}
	var out []string
	inCode := false
	for _, line := range strings.Split(content, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") {
			inCode = !inCode
		}
		if inCode || t == "" {
			out = append(out, line)
			continue
		}
		structural := strings.HasPrefix(t, "|") || strings.HasPrefix(t, "-") || strings.HasPrefix(t, "*") ||
			strings.HasPrefix(t, "#") || orderedItem.MatchString(t)
		if structural {
			if onlyDropped(t) {
				continue
			}
			out = append(out, line)
			continue
		}
		var kept []string
		for _, sent := range splitSentences(line) {
			if !onlyDropped(sent) {
				kept = append(kept, sent)
			}
		}
		if len(kept) > 0 {
			out = append(out, strings.Join(kept, " "))
		}
	}
	res := footRefRe.ReplaceAllStringFunc(strings.Join(out, "\n"), func(m string) string {
		n, _ := strconv.Atoi(footRefRe.FindStringSubmatch(m)[1])
		if dropped[n] {
			return ""
		}
		return m
	})
	return strings.TrimSpace(res)
}

var orderedItem = regexp.MustCompile(`^\d+[.)]\s`)

// splitSentences cuts prose after ". ", "! ", "? " or "; " (footnote markers
// stay with their sentence).
func splitSentences(s string) []string {
	var out []string
	start := 0
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		switch rs[i] {
		case '.', '!', '?', ';':
			j := i + 1
			for j < len(rs) && rs[j] == '[' { // keep trailing [^n] markers
				k := j
				for k < len(rs) && rs[k] != ']' {
					k++
				}
				if k >= len(rs) {
					break
				}
				j = k + 1
			}
			if j < len(rs) && rs[j] == ' ' {
				out = append(out, strings.TrimSpace(string(rs[start:j])))
				start = j + 1
				i = j
			}
		}
	}
	if start < len(rs) {
		if rest := strings.TrimSpace(string(rs[start:])); rest != "" {
			out = append(out, rest)
		}
	}
	return out
}

var wikiLinkRe = regexp.MustCompile(`\[\[([^\]|#]+)(?:[|#][^\]]*)?\]\]`)

// wikiLinks lists the slugs linked with [[slug]] in content.
func wikiLinks(content string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range wikiLinkRe.FindAllStringSubmatch(content, -1) {
		s := strings.TrimSpace(m[1])
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

var mermaidBlock = regexp.MustCompile("(?s)```mermaid\\s*\\n(.*?)```")

var mermaidKinds = []string{"flowchart", "graph", "sequenceDiagram", "timeline", "classDiagram", "erDiagram", "pie", "gantt",
	"stateDiagram", "stateDiagram-v2", "mindmap", "journey", "quadrantChart"}

// sanitizeMermaid removes mermaid blocks that fail a structural parse: an
// unknown diagram kind or unbalanced brackets/quotes (§6.8 step 5).
func sanitizeMermaid(content string) (string, int) {
	removed := 0
	out := mermaidBlock.ReplaceAllStringFunc(content, func(block string) string {
		body := mermaidBlock.FindStringSubmatch(block)[1]
		if mermaidOK(body) {
			return block
		}
		removed++
		return ""
	})
	return out, removed
}

func mermaidOK(body string) bool {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) == 0 {
		return false
	}
	first := strings.Fields(strings.TrimSpace(lines[0]))
	if len(first) == 0 {
		return false
	}
	known := false
	for _, k := range mermaidKinds {
		if first[0] == k {
			known = true
		}
	}
	if !known {
		return false
	}
	depth := map[rune]int{}
	pairs := map[rune]rune{')': '(', ']': '[', '}': '{'}
	quotes := 0
	for _, r := range body {
		switch r {
		case '(', '[', '{':
			depth[r]++
		case ')', ']', '}':
			depth[pairs[r]]--
			if depth[pairs[r]] < 0 {
				return false
			}
		case '"':
			quotes++
		}
	}
	for _, v := range depth {
		if v != 0 {
			return false
		}
	}
	return quotes%2 == 0
}

// attrValueKey normalizes an attribute value for conflict detection.
func attrValueKey(v any) string {
	s := textutil.Normalize(fmt.Sprint(v))
	return strings.NewReplacer(".", "", ",", "", " ", "", "-", "", "đ", "", "vnd", "").Replace(s)
}

// checkAttr validates a value against its declared type and pattern.
func checkAttr(def *types.AttributeDef, v any) error {
	if def == nil {
		return fmt.Errorf("not in the schema")
	}
	s := strings.TrimSpace(fmt.Sprint(v))
	if v == nil || s == "" {
		return fmt.Errorf("empty value")
	}
	if def.Pattern != "" {
		if ok, _ := regexp.MatchString(def.Pattern, s); !ok {
			return fmt.Errorf("value %q does not match %s", s, def.Pattern)
		}
	}
	switch def.Type {
	case "number", "money":
		if !strings.ContainsAny(s, "0123456789") {
			return fmt.Errorf("value %q is not a number", s)
		}
	case "bool":
		switch strings.ToLower(s) {
		case "true", "false", "co", "có", "khong", "không":
		default:
			return fmt.Errorf("value %q is not a boolean", s)
		}
	}
	return nil
}
