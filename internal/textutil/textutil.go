// Package textutil holds small, dependency-free text helpers shared by the
// parser, index and graph modules: Vietnamese-aware accent folding, string
// similarity and a rough token estimate.
package textutil

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Unaccent lower-cases s and strips diacritics, mapping đ/Đ to d. It matches
// the SQL function unaccent_vi closely enough for client-side comparisons.
func Unaccent(s string) string {
	s = strings.NewReplacer("đ", "d", "Đ", "D").Replace(s)
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(t, s)
	if err != nil {
		out = s
	}
	return strings.ToLower(out)
}

// CollapseSpace trims s and collapses runs of whitespace into one space.
func CollapseSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// Normalize is the comparison form: accent-folded, lower-case, collapsed.
func Normalize(s string) string { return CollapseSpace(Unaccent(s)) }

// Similarity returns 1 - levenshtein(a,b)/max(len) over runes, in [0,1].
func Similarity(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 && len(rb) == 0 {
		return 1
	}
	d := levenshtein(ra, rb)
	return 1 - float64(d)/float64(max(len(ra), len(rb)))
}

func levenshtein(a, b []rune) int {
	if len(a) < len(b) {
		a, b = b, a
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// ContainsFold reports whether needle occurs in haystack after Normalize.
func ContainsFold(haystack, needle string) bool {
	return strings.Contains(Normalize(haystack), Normalize(needle))
}

// EstimateTokens is a cheap token estimate (~4 bytes per token for Latin
// scripts; Vietnamese diacritics inflate bytes, so runes/3 is used).
func EstimateTokens(s string) int {
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return 0
	}
	return n/3 + 1
}

// RuneLen is utf8.RuneCountInString.
func RuneLen(s string) int { return utf8.RuneCountInString(s) }

// BadCharRatio is the share of runes that are replacement, private-use or
// control characters (excluding whitespace).
func BadCharRatio(s string) float64 {
	var bad, total int
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		total++
		if r == utf8.RuneError || unicode.Is(unicode.Co, r) || unicode.IsControl(r) {
			bad++
		}
	}
	if total == 0 {
		return 0
	}
	return float64(bad) / float64(total)
}

// Truncate cuts s to at most n runes, adding an ellipsis when cut.
func Truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}
