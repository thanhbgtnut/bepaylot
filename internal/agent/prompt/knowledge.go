package prompt

import (
	"fmt"
	"strings"
)

// KnowledgeBase describes one knowledge base attached to the session (§8.1).
type KnowledgeBase struct {
	ID          string
	Name        string
	Description string
	Documents   int
	Fields      []MetadataField
}

// MetadataField is a metadata key the model can filter by.
type MetadataField struct {
	Key         string
	Type        string
	Description string
	Values      []string
}

// sectionKnowledge tells the model which knowledge bases are attached, which
// metadata it can filter by, and how to cite (§8.1).
func sectionKnowledge(tc TurnContext) string {
	if len(tc.KnowledgeBases) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<knowledge_bases>\nThese document collections are attached to the conversation. Answer questions about their content with the kb_* tools, not from memory.\n")
	for _, kb := range tc.KnowledgeBases {
		fmt.Fprintf(&b, "- %s (id %s, %d documents)", kb.Name, kb.ID, kb.Documents)
		if kb.Description != "" {
			fmt.Fprintf(&b, ": %s", kb.Description)
		}
		b.WriteString("\n")
		if len(kb.Fields) > 0 {
			b.WriteString("  Metadata fields (filter kb_search / kb_list_documents with `metadata`):\n")
			for _, f := range kb.Fields {
				fmt.Fprintf(&b, "  - %s (%s)", f.Key, f.Type)
				if f.Description != "" {
					fmt.Fprintf(&b, ": %s", f.Description)
				}
				if len(f.Values) > 0 {
					fmt.Fprintf(&b, " — one of %s", strings.Join(f.Values, ", "))
				}
				b.WriteString("\n")
			}
		}
	}
	if tc.KnowledgeFilter != "" {
		fmt.Fprintf(&b, "This conversation is pinned to documents matching %s; every search is limited to them.\n", tc.KnowledgeFilter)
	}
	b.WriteString(`When the user refers to a record by a metadata value (for example a case code), pass it as a metadata filter instead of searching the whole collection.
</knowledge_bases>
<citations>
- Every statement taken from a document ends with its citation id in brackets, e.g. [doc:<id>:p3:l5-7], exactly as returned by the tools.
- Never invent citation ids; if the tools found nothing, say so.
</citations>`)
	return b.String()
}
