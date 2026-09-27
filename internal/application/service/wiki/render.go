package wiki

import (
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
)

// Pages are rendered by code from stored data (§6.6): a source page from the
// file card and its index tree, an entity page from its attributes,
// footnotes and typed links, the overview from all pages. Re-rendering after
// a retract or a stale footnote therefore needs no LLM.

// renderSource renders the source page of a file: its card, its table of
// contents with the summaries of the index tree and the entities it names.
func renderSource(d types.Document, tree []types.TreeNode, works []*entityWork) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "**Tệp:** %s · %d trang\n\n", d.FileName, d.PageCount)
	if d.Summary != "" {
		sb.WriteString(strings.TrimSpace(d.Summary) + "\n\n")
	}
	if toc := sourceTOC(d.ID, tree); toc != "" {
		sb.WriteString("## Mục lục\n\n" + toc + "\n")
	}
	if len(works) > 0 {
		sb.WriteString("## Thực thể trong tệp\n\n")
		for _, w := range works {
			fmt.Fprintf(&sb, "- [[%s]] — %s\n", w.page.Slug, entityTypeTitle(w.def, w.page.EntityType))
		}
	}
	return strings.TrimSpace(sb.String())
}

// sourceTOC renders the index tree (3 levels) with page links and summaries.
func sourceTOC(doc uuid.UUID, tree []types.TreeNode) string {
	children := map[uuid.UUID][]types.TreeNode{}
	var root uuid.UUID
	for _, n := range tree {
		if n.ParentID == nil {
			root = n.ID
		} else {
			children[*n.ParentID] = append(children[*n.ParentID], n)
		}
	}
	for k := range children {
		c := children[k]
		sort.Slice(c, func(i, j int) bool { return c[i].Ord < c[j].Ord })
	}
	var sb strings.Builder
	var walk func(id uuid.UUID, depth int)
	walk = func(id uuid.UUID, depth int) {
		for _, n := range children[id] {
			title := textutil.Truncate(firstNonEmpty(n.Title, "Trang "+pageRange(n.PageStart, n.PageEnd)), 120)
			// doc:<id>:p<n> opens the page (a citation chip in the UI).
			fmt.Fprintf(&sb, "%s- **%s**", strings.Repeat("  ", depth), mdCell(title))
			if n.PageEnd > n.PageStart {
				fmt.Fprintf(&sb, " · tr. %s", pageRange(n.PageStart, n.PageEnd))
			}
			fmt.Fprintf(&sb, " · doc:%s:p%d", doc, n.PageStart)
			if s := strings.TrimSpace(n.Summary); s != "" {
				fmt.Fprintf(&sb, " — %s", textutil.Truncate(textutil.CollapseSpace(s), 300))
			}
			sb.WriteString("\n")
			if depth+1 < 3 {
				walk(n.ID, depth+1)
			}
		}
	}
	walk(root, 0)
	return sb.String()
}

// renderEntity renders an entity page and its one-line summary for the index.
func renderEntity(p types.WikiPage, def *types.WikiEntityType, fns []types.WikiFootnote, links []linkSpec, srcOf map[uuid.UUID]types.WikiPage) (string, string) {
	var sb strings.Builder
	typeTitle := entityTypeTitle(def, p.EntityType)
	fmt.Fprintf(&sb, "**Loại:** %s", typeTitle)
	if len(p.Aliases) > 0 {
		fmt.Fprintf(&sb, " · **Tên khác:** %s", strings.Join(p.Aliases, "; "))
	}
	sb.WriteString("\n\n")

	names := attrOrder(def, p.Attributes)
	if len(names) > 0 {
		sb.WriteString("| Thuộc tính | Giá trị |\n|---|---|\n")
		for _, k := range names {
			a := p.Attributes[k]
			var cell string
			if k == roleAttr {
				var parts []string
				for _, h := range a.History {
					parts = append(parts, mdCell(valueText(h.Value))+footRefs(h.Footnotes))
				}
				cell = strings.Join(parts, "; ")
			} else if a.Conflict && len(a.History) > 1 {
				var parts []string
				for _, h := range a.History {
					parts = append(parts, mdCell(valueText(h.Value))+footRefs(h.Footnotes))
				}
				cell = "⚠ " + strings.Join(parts, " · ")
			} else {
				cell = mdCell(valueText(a.Value)) + footRefs(a.Footnotes)
			}
			fmt.Fprintf(&sb, "| %s | %s |\n", attrLabel(k), cell)
		}
		sb.WriteString("\n")
	}

	var typed []linkSpec
	for _, l := range links {
		if l.relation != "" {
			typed = append(typed, l)
		}
	}
	if len(typed) > 0 {
		sb.WriteString("## Quan hệ\n\n")
		for _, l := range typed {
			fmt.Fprintf(&sb, "- %s → [[%s]]", attrLabel(l.relation), l.to)
			if l.footnote != nil {
				fmt.Fprintf(&sb, "[^%d]", *l.footnote)
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	// Sources: every file the entity is cited from, with all its footnotes
	// (the ones that only say where it is named included).
	byDoc := map[uuid.UUID][]types.WikiFootnote{}
	var docs []uuid.UUID
	for _, f := range fns {
		if _, ok := byDoc[f.DocumentID]; !ok {
			docs = append(docs, f.DocumentID)
		}
		byDoc[f.DocumentID] = append(byDoc[f.DocumentID], f)
	}
	if len(docs) > 0 {
		sb.WriteString("## Nguồn\n\n")
		for _, doc := range docs {
			list := byDoc[doc]
			sort.Slice(list, func(i, j int) bool { return list[i].N < list[j].N })
			pagesSeen := map[int]bool{}
			var pages []string
			var ns []int
			for _, f := range list {
				if !pagesSeen[f.PageNo] {
					pagesSeen[f.PageNo] = true
					pages = append(pages, fmt.Sprint(f.PageNo))
				}
				ns = append(ns, f.N)
			}
			name := list[0].FileName
			if src, ok := srcOf[doc]; ok {
				name = "[[" + src.Slug + "]]"
			}
			fmt.Fprintf(&sb, "- %s — tr. %s%s\n", firstNonEmpty(name, doc.String()), strings.Join(pages, ", "), footRefs(ns))
		}
	}

	return strings.TrimSpace(sb.String()), entitySummary(p, def, len(docs))
}

// entitySummary is the one line of an entity in the index (§6.6): its
// identity, its roles and how many files cite it. The index groups entities
// by type, so the type is not repeated. Kept short: the index is read by
// every query.
func entitySummary(p types.WikiPage, def *types.WikiEntityType, files int) string {
	var parts []string
	if def != nil {
		for _, k := range def.Identity {
			if a, ok := p.Attributes[k]; ok {
				parts = append(parts, attrLabel(k)+" "+valueText(a.Value))
			}
		}
	}
	if a, ok := p.Attributes[roleAttr]; ok {
		parts = append(parts, textutil.Truncate(valueText(a.Value), 90))
	}
	s := strings.Join(parts, "; ")
	if files > 0 {
		s = strings.TrimPrefix(fmt.Sprintf("%s (%d tệp)", s, files), " ")
	}
	return s
}

// renderOverview renders the overview of a case: its files, its entities by
// type and the attributes on which sources disagree.
func renderOverview(c types.Case, schema types.WikiSchema, pages []types.WikiPage) (string, string) {
	var sources, entities, notes []types.WikiPage
	for _, p := range pages {
		switch p.Kind {
		case types.WikiKindSource:
			sources = append(sources, p)
		case types.WikiKindEntity:
			entities = append(entities, p)
		case types.WikiKindNote, types.WikiKindTopic:
			notes = append(notes, p)
		}
	}
	byTitle := func(ps []types.WikiPage) {
		sort.Slice(ps, func(i, j int) bool { return ps[i].Title < ps[j].Title })
	}
	byTitle(sources)
	byTitle(entities)
	byTitle(notes)

	var sb strings.Builder
	fmt.Fprintf(&sb, "# Hồ sơ %s", c.Code)
	if c.Title != "" {
		fmt.Fprintf(&sb, " — %s", c.Title)
	}
	sb.WriteString("\n\n")
	fmt.Fprintf(&sb, "## Các tệp (%d)\n\n", len(sources))
	if len(sources) > 0 {
		sb.WriteString("| Tệp | Tóm tắt |\n|---|---|\n")
		for _, p := range sources {
			fmt.Fprintf(&sb, "| [[%s]] | %s |\n", p.Slug, mdCell(textutil.Truncate(p.Summary, 200)))
		}
		sb.WriteString("\n")
	}
	if len(entities) > 0 {
		fmt.Fprintf(&sb, "## Thực thể (%d)\n\n", len(entities))
		groups := map[string][]types.WikiPage{}
		var order []string
		for _, e := range schema.EntityTypes {
			order = append(order, e.Name)
		}
		for _, p := range entities {
			if _, ok := groups[p.EntityType]; !ok && schema.EntityType(p.EntityType) == nil {
				order = append(order, p.EntityType)
			}
			groups[p.EntityType] = append(groups[p.EntityType], p)
		}
		for _, t := range order {
			list := groups[t]
			if len(list) == 0 {
				continue
			}
			fmt.Fprintf(&sb, "### %s (%d)\n\n", entityTypeTitle(schema.EntityType(t), t), len(list))
			for _, p := range list {
				fmt.Fprintf(&sb, "- [[%s]] — %s\n", p.Slug, p.Summary)
			}
			sb.WriteString("\n")
		}
	}
	var diffs []string
	for _, p := range entities {
		for _, k := range attrOrder(schema.EntityType(p.EntityType), p.Attributes) {
			if p.Attributes[k].Conflict {
				diffs = append(diffs, fmt.Sprintf("- [[%s]]: %s", p.Slug, attrLabel(k)))
			}
		}
	}
	if len(diffs) > 0 {
		sb.WriteString("## Điểm chênh lệch giữa các nguồn\n\n" + strings.Join(diffs, "\n") + "\n\n")
	}
	if len(notes) > 0 {
		sb.WriteString("## Ghi chú\n\n")
		for _, p := range notes {
			fmt.Fprintf(&sb, "- [[%s]]\n", p.Slug)
		}
	}
	summary := fmt.Sprintf("Hồ sơ %s: %d tệp, %d thực thể", c.Code, len(sources), len(entities))
	if len(diffs) > 0 {
		summary += fmt.Sprintf(", %d điểm chênh lệch", len(diffs))
	}
	return strings.TrimSpace(sb.String()), summary
}

// attrOrder lists the roles, then attributes in schema order, then the
// others by name.
func attrOrder(def *types.WikiEntityType, attrs map[string]types.WikiAttribute) []string {
	var out []string
	seen := map[string]bool{}
	if _, ok := attrs[roleAttr]; ok {
		out = append(out, roleAttr)
		seen[roleAttr] = true
	}
	if def != nil {
		for _, a := range def.Attributes {
			if _, ok := attrs[a.Name]; ok {
				out = append(out, a.Name)
				seen[a.Name] = true
			}
		}
	}
	var rest []string
	for k := range attrs {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

func entityTypeTitle(def *types.WikiEntityType, name string) string {
	if def != nil && def.Title != "" {
		return def.Title
	}
	return attrLabel(name)
}

// attrLabel turns ma_so_thue into "Ma so thue".
func attrLabel(name string) string {
	if name == roleAttr {
		return "Vai trò"
	}
	s := strings.ReplaceAll(strings.TrimSpace(name), "_", " ")
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// mdCell makes text safe inside a markdown table cell.
func mdCell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(s), "|", "/"), "\n", " ")
}
