package prompt

import (
	"fmt"
	"sort"
	"strings"
)

// CaseInfo describes the case bound to the session (§8.1): data only, no
// workflow — what to extract or check comes from the user's message.
type CaseInfo struct {
	ID        string
	Code      string
	TypeTitle string
	Title     string
	Status    string
	Metadata  map[string]any
	// Deleted is set when the case no longer exists.
	Deleted bool
	// Documents counts files by status.
	Documents map[string]int
	Fields    []MetadataField
}

// MetadataField is a metadata key the model can filter by.
type MetadataField struct {
	Key         string
	Type        string
	Description string
	Values      []string
}

// sectionCase tells the model which case the conversation works on, what
// data it holds and how to cite (§8.1). It describes data only.
func sectionCase(tc TurnContext) string {
	c := tc.Case
	if c == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("<case>\n")
	if c.Deleted {
		b.WriteString("This conversation was bound to a case that no longer exists; its files are gone and the document tools will fail.\n</case>")
		return b.String()
	}
	fmt.Fprintf(&b, "This conversation works on exactly one case: code %s", c.Code)
	if c.TypeTitle != "" {
		fmt.Fprintf(&b, " (%s)", c.TypeTitle)
	}
	if c.Title != "" {
		fmt.Fprintf(&b, ", %q", c.Title)
	}
	fmt.Fprintf(&b, ", status %s. Every kb_* tool only sees this case; other cases cannot be reached, whatever code a message mentions.\n", c.Status)
	if len(c.Metadata) > 0 {
		keys := make([]string, 0, len(c.Metadata))
		for k := range c.Metadata {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("Case metadata:")
		for _, k := range keys {
			fmt.Fprintf(&b, " %s=%v;", k, c.Metadata[k])
		}
		b.WriteString("\n")
	}
	total := 0
	var parts []string
	statuses := make([]string, 0, len(c.Documents))
	for st := range c.Documents {
		statuses = append(statuses, st)
	}
	sort.Strings(statuses)
	for _, st := range statuses {
		total += c.Documents[st]
		parts = append(parts, fmt.Sprintf("%d %s", c.Documents[st], st))
	}
	fmt.Fprintf(&b, "Files: %d", total)
	if len(parts) > 0 {
		fmt.Fprintf(&b, " (%s)", strings.Join(parts, ", "))
	}
	b.WriteString(".\n")
	if len(c.Fields) > 0 {
		b.WriteString("File metadata fields (filter kb_search / kb_list_documents with `metadata`):\n")
		for _, f := range c.Fields {
			fmt.Fprintf(&b, "- %s (%s)", f.Key, f.Type)
			if f.Description != "" {
				fmt.Fprintf(&b, ": %s", f.Description)
			}
			if len(f.Values) > 0 {
				fmt.Fprintf(&b, " — one of %s", strings.Join(f.Values, ", "))
			}
			b.WriteString("\n")
		}
	}
	b.WriteString(`</case>
<citations>
- Every statement taken from a file ends with its citation id in brackets, e.g. [doc:<id>:p3:l5-7], exactly as returned by the tools.
- Never invent citation ids; if the tools found nothing, say so.
</citations>`)
	return b.String()
}
