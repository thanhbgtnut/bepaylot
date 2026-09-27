package wiki

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/types"
)

// ---- schemas (§10.5) ----

// Schemas lists schema versions (the latest of each when name is empty).
func (s *Service) Schemas(ctx context.Context, name string) ([]types.WikiSchema, error) {
	out, err := s.st.Wiki.Schemas(ctx, name)
	if err == nil && len(out) == 0 && name != "" {
		return nil, ErrNotFound
	}
	if out == nil {
		out = []types.WikiSchema{}
	}
	return out, err
}

// CreateSchema stores a new schema version (validated, §6.7). Existing
// cases keep their content until POST /cases/:id/wiki/rebuild.
func (s *Service) CreateSchema(ctx context.Context, sc types.WikiSchema) (types.WikiSchema, error) {
	if sc.Version == 0 {
		if cur, err := s.st.Wiki.LatestSchema(ctx, sc.Name); err == nil {
			sc.Version = cur.Version + 1
		} else {
			sc.Version = 1
		}
	}
	if err := ValidateSchema(&sc); err != nil {
		return sc, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	if cur, err := s.st.Wiki.LatestSchema(ctx, sc.Name); err == nil && sc.Version <= cur.Version {
		return sc, fmt.Errorf("%w: version must be greater than %d", ErrBadRequest, cur.Version)
	}
	return s.st.Wiki.UpsertSchema(ctx, sc)
}

// TestResult is the dry run of the extraction of a schema on one text:
// the entities and relations that survive the line check.
type TestResult struct {
	Entities  []map[string]any `json:"entities"`
	Relations []map[string]any `json:"relations"`
	LLMCalls  int              `json:"llm_calls"`
}

// TestSchema runs the extraction of a schema on a text or an owned document
// and returns what an ingest would keep; nothing is stored.
func (s *Service) TestSchema(ctx context.Context, owner uuid.UUID, name, text string, docID *uuid.UUID) (*TestResult, error) {
	if s.llm == nil {
		return nil, fmt.Errorf("%w: no LLM is configured for the wiki", ErrBadRequest)
	}
	sc, err := s.st.Wiki.LatestSchema(ctx, name)
	if err != nil {
		return nil, notFound(err)
	}
	var pages []*types.ParsedPage
	d := types.Document{FileName: "test.txt", PageCount: 1}
	switch {
	case docID != nil:
		doc, err := s.st.Documents.GetOwned(ctx, *docID, owner)
		if err != nil {
			return nil, ErrNotFound
		}
		d = doc
		if pages, err = s.docs.LoadPages(ctx, d.ID, d.Gen, 1, 20); err != nil {
			return nil, err
		}
	case strings.TrimSpace(text) != "":
		p := &types.ParsedPage{PageNo: 1}
		for i, l := range strings.Split(text, "\n") {
			if strings.TrimSpace(l) != "" {
				p.Lines = append(p.Lines, types.ParsedLine{LineNo: i, Text: strings.TrimSpace(l)})
			}
		}
		pages = []*types.ParsedPage{p}
	default:
		return nil, fmt.Errorf("%w: text or document_id is required", ErrBadRequest)
	}
	r := &run{}
	r.max.Store(int64(max(s.cfg.Wiki.Ingest.MaxLLMCalls, 1)))
	ents, rels, err := s.extract(ctx, r, sc, d, pages, nil, linesOf(pages))
	if err != nil {
		return nil, err
	}
	out := &TestResult{Entities: []map[string]any{}, Relations: []map[string]any{}, LLMCalls: int(min(r.calls.Load(), r.max.Load()))}
	for _, e := range ents {
		attrs := map[string]any{}
		for k, v := range e.attrs {
			attrs[k] = map[string]any{"value": v.value, "page": v.at.page, "lines": []int{v.at.from, v.at.to}}
		}
		out.Entities = append(out.Entities, map[string]any{"entity_type": e.def.Name, "name": e.name, "aliases": e.aliases,
			"identity_key": IdentityKey(e.def, e.values()), "slug": entityPrefix(e.def.Name) + "/" + Slugify(e.name), "attributes": attrs})
	}
	for _, rl := range rels {
		out.Relations = append(out.Relations, map[string]any{"from": ents[rl.from].name, "to": ents[rl.to].name, "relation": rl.def.Name})
	}
	return out, nil
}

// ---- export (§7.7) ----

// Export returns a zip of markdown pages or one static HTML page.
func (s *Service) Export(ctx context.Context, owner, caseID uuid.UUID, format string) (string, string, []byte, error) {
	c, err := s.ownedCase(ctx, owner, caseID)
	if err != nil {
		return "", "", nil, err
	}
	pages, err := s.st.Wiki.Pages(ctx, c.ID)
	if err != nil {
		return "", "", nil, err
	}
	ids := make([]uuid.UUID, len(pages))
	for i, p := range pages {
		ids[i] = p.ID
	}
	fns, err := s.st.Wiki.Footnotes(ctx, ids)
	if err != nil {
		return "", "", nil, err
	}
	logs, err := s.st.Wiki.Log(ctx, c.ID, postgres.LogFilter{Limit: 500})
	if err != nil {
		return "", "", nil, err
	}
	base := "wiki-" + Slugify(c.Code)
	switch format {
	case "", "md":
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		write := func(name, body string) error {
			w, err := zw.Create(name)
			if err != nil {
				return err
			}
			_, err = w.Write([]byte(body))
			return err
		}
		idx, err := s.IndexView(ctx, c.ID, nil)
		if err != nil {
			return "", "", nil, err
		}
		if err := write("index.md", idx.Content+"\n"); err != nil {
			return "", "", nil, err
		}
		var lb strings.Builder
		for _, l := range logs {
			lb.WriteString(l.Line() + "\n")
		}
		if err := write("log.md", lb.String()); err != nil {
			return "", "", nil, err
		}
		for _, p := range pages {
			if err := write(p.Slug+".md", pageMarkdown(p, fns[p.ID])); err != nil {
				return "", "", nil, err
			}
		}
		if err := zw.Close(); err != nil {
			return "", "", nil, err
		}
		return base + ".zip", "application/zip", buf.Bytes(), nil
	case "html":
		var sb strings.Builder
		fmt.Fprintf(&sb, "<!doctype html><html lang=\"vi\"><head><meta charset=\"utf-8\"><title>Wiki %s</title>"+
			"<style>body{font-family:system-ui,sans-serif;max-width:920px;margin:2rem auto;padding:0 1rem;line-height:1.55}"+
			"table{border-collapse:collapse}td,th{border:1px solid #ccc;padding:.25rem .5rem}section{border-top:1px solid #ddd;margin-top:2rem}"+
			".fn{font-size:.9em;color:#444}</style></head><body><h1>Wiki hồ sơ %s</h1>\n<nav><ul>",
			html.EscapeString(c.Code), html.EscapeString(c.Code))
		for _, p := range pages {
			fmt.Fprintf(&sb, "<li><a href=\"#%s\">%s</a> <small>(%s)</small></li>", anchor(p.Slug), html.EscapeString(p.Title), p.Kind)
		}
		sb.WriteString("</ul></nav>\n")
		for _, p := range pages {
			fmt.Fprintf(&sb, "<section id=\"%s\"><h2>%s</h2>\n%s", anchor(p.Slug), html.EscapeString(p.Title), markdownHTML(p.Content))
			if len(fns[p.ID]) > 0 {
				sb.WriteString("<ol class=\"fn\">")
				for _, f := range fns[p.ID] {
					fmt.Fprintf(&sb, "<li value=\"%d\">%s, tr. %d, dòng %d–%d: “%s” <code>%s</code></li>", f.N, html.EscapeString(f.FileName),
						f.PageNo, f.LineFrom, f.LineTo, html.EscapeString(f.Quote), html.EscapeString(f.CitationID))
				}
				sb.WriteString("</ol>")
			}
			sb.WriteString("</section>\n")
		}
		sb.WriteString("</body></html>\n")
		return base + ".html", "text/html; charset=utf-8", []byte(sb.String()), nil
	}
	return "", "", nil, fmt.Errorf("%w: format must be md or html", ErrBadRequest)
}

func pageMarkdown(p types.WikiPage, fns []types.WikiFootnote) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "---\nslug: %s\nkind: %s\ntitle: %q\nsummary: %q\nversion: %d\n---\n\n# %s\n\n", p.Slug, p.Kind, p.Title, p.Summary, p.Version, p.Title)
	if len(p.Attributes) > 0 {
		names := make([]string, 0, len(p.Attributes))
		for k := range p.Attributes {
			names = append(names, k)
		}
		sort.Strings(names)
		sb.WriteString("| Thuộc tính | Giá trị |\n|---|---|\n")
		for _, k := range names {
			a := p.Attributes[k]
			v := fmt.Sprintf("%v%s", a.Value, footRefs(a.Footnotes))
			if a.Conflict {
				v += " (chênh lệch giữa các nguồn)"
			}
			fmt.Fprintf(&sb, "| %s | %s |\n", k, strings.ReplaceAll(v, "|", "/"))
		}
		sb.WriteString("\n")
	}
	sb.WriteString(wikiLinkRe.ReplaceAllString(p.Content, "[$1]($1.md)"))
	sb.WriteString("\n\n")
	for _, f := range fns {
		fmt.Fprintf(&sb, "[^%d]: [%s, tr. %d, dòng %d–%d](/v1/citations?id=%s) — “%s”\n", f.N, f.FileName, f.PageNo, f.LineFrom, f.LineTo, f.CitationID, f.Quote)
	}
	return sb.String()
}

func footRefs(ns []int) string {
	var sb strings.Builder
	for _, n := range ns {
		fmt.Fprintf(&sb, "[^%d]", n)
	}
	return sb.String()
}

func anchor(slug string) string { return strings.ReplaceAll(slug, "/", "--") }

var (
	boldRe = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	fnHTML = regexp.MustCompile(`\[\^(\d+)\]`)
)

// markdownHTML renders the markdown subset the wiki uses: headings,
// paragraphs, lists, tables, code/mermaid blocks, bold, [[links]], [^n].
func markdownHTML(md string) string {
	inline := func(s string) string {
		s = html.EscapeString(s)
		s = boldRe.ReplaceAllString(s, "<b>$1</b>")
		s = fnHTML.ReplaceAllString(s, "<sup>[$1]</sup>")
		return wikiLinkRe.ReplaceAllStringFunc(s, func(m string) string {
			slug := wikiLinkRe.FindStringSubmatch(m)[1]
			return fmt.Sprintf("<a href=\"#%s\">%s</a>", anchor(slug), slug)
		})
	}
	var sb strings.Builder
	lines := strings.Split(md, "\n")
	for i := 0; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		switch {
		case t == "":
		case strings.HasPrefix(t, "```"):
			kind := strings.TrimPrefix(t, "```")
			var body []string
			for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```"); i++ {
				body = append(body, lines[i])
			}
			cls := ""
			if kind == "mermaid" {
				cls = " class=\"mermaid\""
			}
			fmt.Fprintf(&sb, "<pre%s>%s</pre>\n", cls, html.EscapeString(strings.Join(body, "\n")))
		case strings.HasPrefix(t, "#"):
			lvl := min(len(t)-len(strings.TrimLeft(t, "#"))+1, 6)
			fmt.Fprintf(&sb, "<h%d>%s</h%d>\n", lvl, inline(strings.TrimSpace(strings.TrimLeft(t, "#"))), lvl)
		case strings.HasPrefix(t, "|"):
			sb.WriteString("<table>")
			for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|"); i++ {
				row := strings.Trim(strings.TrimSpace(lines[i]), "|")
				if strings.Trim(row, "-:| ") == "" {
					continue
				}
				sb.WriteString("<tr>")
				for _, cell := range strings.Split(row, "|") {
					fmt.Fprintf(&sb, "<td>%s</td>", inline(strings.TrimSpace(cell)))
				}
				sb.WriteString("</tr>")
			}
			i--
			sb.WriteString("</table>\n")
		case strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* "):
			sb.WriteString("<ul>")
			for ; i < len(lines); i++ {
				lt := strings.TrimSpace(lines[i])
				if !strings.HasPrefix(lt, "- ") && !strings.HasPrefix(lt, "* ") {
					break
				}
				fmt.Fprintf(&sb, "<li>%s</li>", inline(lt[2:]))
			}
			i--
			sb.WriteString("</ul>\n")
		default:
			fmt.Fprintf(&sb, "<p>%s</p>\n", inline(t))
		}
	}
	return sb.String()
}
