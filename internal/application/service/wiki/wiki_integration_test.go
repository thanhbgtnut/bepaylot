package wiki_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/application/service/cases"
	"github.com/thanhenti/bepaylot/internal/application/service/wiki"
	"github.com/thanhenti/bepaylot/internal/config"
	"github.com/thanhenti/bepaylot/internal/parser/pdf/pdftest"
	"github.com/thanhenti/bepaylot/internal/testkit"
	"github.com/thanhenti/bepaylot/internal/types"
)

var fileLineRe = regexp.MustCompile(`\[p(\d+):L(\d+)\] (.*)`)

// extractCompany extracts one organization per file, citing its lines: the
// tax code (identity) and the address, so that two files can disagree.
func extractCompany(user string) map[string]any {
	ent := map[string]any{"id": "e1", "type": "to_chuc", "role": "bên thụ hưởng"}
	attrs := map[string]any{}
	for _, m := range fileLineRe.FindAllStringSubmatch(user, -1) {
		at, text := "p"+m[1]+":L"+m[2], m[3]
		switch {
		case strings.Contains(strings.ToLower(text), "cong ty"):
			ent["name"] = strings.TrimSpace(text[strings.Index(strings.ToLower(text), "cong ty"):])
			ent["at"] = at
		case strings.HasPrefix(text, "Ma so thue: "):
			attrs["ma_so_thue"] = map[string]any{"value": strings.TrimPrefix(text, "Ma so thue: "), "at": at}
		case strings.HasPrefix(text, "Dia chi: "):
			attrs["dia_chi"] = map[string]any{"value": strings.TrimPrefix(text, "Dia chi: "), "at": at}
		}
	}
	if ent["name"] == nil {
		return map[string]any{"entities": []any{}}
	}
	// A value the file does not hold is dropped by the line check.
	attrs["ten"] = map[string]any{"value": "Cong ty bia dat", "at": "p1:L1"}
	ent["attributes"] = attrs
	return map[string]any{"entities": []any{ent}, "relations": []any{}}
}

func TestWikiIngestSearchEditRetract(t *testing.T) {
	h := testkit.New(t)
	h.LLM.Extract = extractCompany
	kb := h.KB(types.KBConfig{}, nil)
	c, err := h.Cases.Create(h.Ctx, h.Owner.ID, kb.ID, cases.CreateRequest{Code: " rt112233", CaseType: "thanh_toan"})
	if err != nil || c.Code != "RT112233" || c.WikiSchema != "thanh_toan" {
		t.Fatalf("case = %+v %v", c, err)
	}
	if _, err := h.Cases.Create(h.Ctx, h.Owner.ID, kb.ID, cases.CreateRequest{Code: "RT11", CaseType: "thanh_toan"}); err == nil {
		t.Fatal("a code outside the pattern must be refused")
	}
	// The raw code finds the case through its type's normalization.
	hd := h.UploadTo(kb.ID, " rt112233", "hop-dong.pdf", nil, []pdftest.Page{
		{"HOP DONG THI CONG", "Ben B: Cong ty Anh Duong", "Ma so thue: 0101234567", "Dia chi: 12 Le Loi, Ha Noi"},
	})
	h.Drain()
	unc := h.UploadTo(kb.ID, "RT112233", "unc.pdf", nil, []pdftest.Page{
		{"UY NHIEM CHI", "Don vi thu huong: CONG TY TNHH ANH DUONG", "Ma so thue: 0101-234-567", "Dia chi: 99 Tran Phu, Ha Noi"},
	})
	h.Drain()

	c = h.Case(kb.ID, "RT112233")
	if c.WikiStatus != types.WikiReady || c.WikiDocsCovered != 2 {
		t.Fatalf("wiki state = %s covered %d", c.WikiStatus, c.WikiDocsCovered)
	}
	pages, err := h.Store.Wiki.Pages(h.Ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string][]types.WikiPage{}
	for _, p := range pages {
		kinds[p.Kind] = append(kinds[p.Kind], p)
	}
	// One entity page for one tax code written two ways (N22).
	if len(kinds[types.WikiKindOverview]) != 1 || len(kinds[types.WikiKindSource]) != 2 || len(kinds[types.WikiKindEntity]) != 1 {
		t.Fatalf("pages by kind = %v", kinds)
	}
	ent := kinds[types.WikiKindEntity][0]
	if ent.IdentityKey != "0101234567" || ent.EntityType != "to_chuc" || !strings.HasPrefix(ent.Slug, "to-chuc/") {
		t.Fatalf("entity = %+v", ent)
	}
	addr := ent.Attributes["dia_chi"]
	if !addr.Conflict || len(addr.History) != 2 {
		t.Fatalf("two addresses must be kept as a conflict: %+v", addr)
	}
	if _, ok := ent.Attributes["ten"]; ok {
		t.Fatalf("a value not on its line was kept: %+v", ent.Attributes)
	}
	// Roles accumulate (no conflict) and make the one-line summary, which
	// carries no type (the index groups by type) and no slug.
	if r := ent.Attributes["vai_tro"]; r.Conflict || len(r.History) != 1 || len(r.Footnotes) != 2 {
		t.Fatalf("roles = %+v", r)
	}
	if ent.Summary != "Ma so thue 0101234567; bên thụ hưởng (2 tệp)" {
		t.Fatalf("entity summary = %q", ent.Summary)
	}
	// One extraction call per file; every page is written by code.
	if n := h.LLM.Calls["wiki_extract"]; n != 2 {
		t.Fatalf("extraction calls = %d, want 2", n)
	}
	issues, _ := h.Store.Wiki.Issues(h.Ctx, c.ID, "open", types.LintContradiction, 10)
	if len(issues) == 0 {
		t.Fatal("no contradiction issue")
	}
	logs, _ := h.Store.Wiki.Log(h.Ctx, c.ID, postgres.LogFilter{Op: types.WikiOpIngest})
	if len(logs) != 2 {
		t.Fatalf("one ingest log line per file, got %d", len(logs))
	}

	// Every footnote is a citation of this case that matches its line (N21).
	full, err := h.Wiki.Page(h.Ctx, c.ID, ent.Slug)
	if err != nil || len(full.Footnotes) == 0 || len(full.LinksIn) == 0 {
		t.Fatalf("entity page = %+v %v", full, err)
	}
	for _, p := range pages {
		pg, _ := h.Wiki.Page(h.Ctx, c.ID, p.Slug)
		for _, f := range pg.Footnotes {
			loc, err := h.Index.Locate(h.Ctx, h.Owner.ID, f.CitationID)
			if err != nil || len(loc) != 1 || !strings.Contains(loc[0].Quote, strings.TrimSpace(f.Quote)) {
				t.Fatalf("footnote %s of %s = %+v %v", f.CitationID, p.Slug, loc, err)
			}
		}
	}

	// The index lists pages by kind, the entity included.
	idx, err := h.Wiki.IndexView(h.Ctx, c.ID, nil)
	if err != nil || !strings.Contains(idx.Content, "] "+ent.Title+" — "+ent.Summary) || strings.Contains(idx.Content, ent.Slug) ||
		!strings.Contains(idx.Content, "## Tổ chức") || idx.TokenCount > h.Config.Wiki.Index.MaxTokens {
		t.Fatalf("index = %v\n%s", err, idx.Content)
	}

	// Search by the wiki: the index picks the entity page, the footnotes
	// answer and are verified against the source lines.
	entID := ""
	for id, r := range idx.Refs {
		if r.Slug == ent.Slug {
			entID = id
		}
	}
	h.LLM.WikiIndex = func(string) map[string]any { return map[string]any{"wiki": []string{entID}, "raw": []any{}} }
	resp, err := h.Index.Search(h.Ctx, types.SearchRequest{Query: "ma so thue", CaseIDs: []uuid.UUID{c.ID}, OwnerID: h.Owner.ID})
	if err != nil || len(resp.Hits) == 0 {
		t.Fatalf("wiki search = %+v %v", resp, err)
	}
	for _, hit := range resp.Hits {
		if hit.Via != "wiki" || len(hit.WikiPages) != 1 || len(hit.BBoxes) == 0 || hit.CaseCode != "RT112233" {
			t.Fatalf("wiki hit = %+v", hit)
		}
	}
	// The log records queries too, with the pages read.
	if ql, _ := h.Store.Wiki.Log(h.Ctx, c.ID, postgres.LogFilter{Op: types.WikiOpQuery}); len(ql) != 1 || len(ql[0].Pages) != 1 || ql[0].Pages[0] != ent.Slug {
		t.Fatalf("query log = %+v", ql)
	}

	// A footnote whose quote no longer matches is dropped and marked stale.
	stale := full.Footnotes[0]
	if _, err := h.Store.Pool.Exec(h.Ctx, `UPDATE wiki_footnotes SET quote = 'ma so khong ton tai 42' WHERE page_id = $1 AND n = $2`, full.ID, stale.N); err != nil {
		t.Fatal(err)
	}
	h.LLM.WikiRead = func(string) map[string]any {
		return map[string]any{"hits": []map[string]any{{"page": entID, "footnotes": []int{stale.N}, "relevance": 1}}}
	}
	resp, err = h.Index.Search(h.Ctx, types.SearchRequest{Query: "ma so thue la gi", CaseIDs: []uuid.UUID{c.ID}, OwnerID: h.Owner.ID})
	h.LLM.WikiIndex, h.LLM.WikiRead = nil, nil
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range resp.Hits {
		if hit.Via == "wiki" {
			t.Fatalf("a stale footnote answered: %+v", hit)
		}
	}
	if resp.Trace.StaleFootnotes == 0 {
		t.Fatalf("trace = %+v", resp.Trace)
	}
	after, _ := h.Wiki.Page(h.Ctx, c.ID, ent.Slug)
	for _, f := range after.Footnotes {
		if f.N == stale.N && f.Status != types.FootnoteStale {
			t.Fatalf("footnote not marked stale: %+v", f)
		}
	}

	// A hand edit protects the page: the next ingest leaves a proposal (N22).
	edited := "Trang do người dùng sửa. Mã số thuế 0101234567[^1]."
	res, err := h.Wiki.EditPage(h.Ctx, h.Owner.ID, c.ID, ent.Slug, wiki.EditRequest{Content: &edited,
		Footnotes: map[string]string{"1": full.Footnotes[len(full.Footnotes)-1].CitationID}})
	if err != nil || res.Page.LastEditSource != types.EditUser || len(res.Invalid) != 0 {
		t.Fatalf("edit = %+v %v", res, err)
	}
	if _, err := h.Wiki.EditPage(h.Ctx, uuid.New(), c.ID, ent.Slug, wiki.EditRequest{Content: &edited}); err == nil {
		t.Fatal("another owner edited the page")
	}
	h.UploadTo(kb.ID, "RT112233", "bien-ban.pdf", nil, []pdftest.Page{
		{"BIEN BAN NGHIEM THU", "Ben B: Cong ty Anh Duong", "Ma so thue: 0101234567"},
	})
	h.Drain()
	after, _ = h.Wiki.Page(h.Ctx, c.ID, ent.Slug)
	if after.Content != edited || after.ProposedContent == nil {
		t.Fatalf("user edit overwritten or no proposal: %q %v", after.Content, after.ProposedContent)
	}
	if p, err := h.Wiki.Proposal(h.Ctx, h.Owner.ID, c.ID, ent.Slug, true); err != nil || p.ProposedContent != nil || p.Content == edited {
		t.Fatalf("accept proposal = %+v %v", p, err)
	}
	revs, _ := h.Wiki.Revisions(h.Ctx, h.Owner.ID, c.ID, ent.Slug)
	if len(revs) < 3 {
		t.Fatalf("revisions = %d", len(revs))
	}

	// Saving an answer keeps only the sentences with a valid citation.
	other := uuid.New()
	note, err := h.Wiki.CreateNote(h.Ctx, h.Owner.ID, c.ID, wiki.NoteRequest{Title: "Mã số thuế bên B",
		Content: "Bên B có MST 0101234567 [" + full.Footnotes[len(full.Footnotes)-1].CitationID + "]. Câu này không có nguồn. Câu bịa [doc:" + other.String() + ":p1:l0-0]."})
	if err != nil || note.Kind != types.WikiKindNote || len(note.Footnotes) != 1 || strings.Contains(note.Content, "không có nguồn") || strings.Contains(note.Content, "bịa") {
		t.Fatalf("note = %+v %v", note, err)
	}

	// Deleting a file retracts it: its source page goes, the entity stays.
	if err := h.Docs.Delete(h.Ctx, h.Owner.ID, hd); err != nil {
		t.Fatal(err)
	}
	h.Drain()
	if _, err := h.Store.Wiki.SourcePage(h.Ctx, c.ID, hd); err == nil {
		t.Fatal("the source page of a deleted file survived")
	}
	after, err = h.Wiki.Page(h.Ctx, c.ID, ent.Slug)
	if err != nil {
		t.Fatalf("entity page lost with one of its files: %v", err)
	}
	for _, f := range after.Footnotes {
		if f.DocumentID == hd {
			t.Fatalf("footnote of the deleted file kept: %+v", f)
		}
	}
	if rl, _ := h.Store.Wiki.Log(h.Ctx, c.ID, postgres.LogFilter{Op: types.WikiOpRetract}); len(rl) != 1 {
		t.Fatalf("retract log lines = %d", len(rl))
	}
	_ = unc

	// Lint found nothing new to invent: every issue kind is one of §6.9.
	if err := h.Wiki.RunLint(h.Ctx, h.Owner.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	h.Drain()
	all, _ := h.Store.Wiki.Issues(h.Ctx, c.ID, "", "", 100)
	for _, is := range all {
		switch is.Kind {
		case types.LintStale, types.LintContradiction, types.LintOrphan, types.LintMissingLink, types.LintGap, types.LintIndexDrift:
		default:
			t.Fatalf("unknown issue kind %q", is.Kind)
		}
	}

	// Deleting the case removes its documents and its whole wiki.
	if err := h.Cases.Delete(h.Ctx, h.Owner.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	h.Drain()
	if left, _ := h.Store.Wiki.Pages(h.Ctx, c.ID); len(left) != 0 {
		t.Fatalf("%d wiki pages left after case delete", len(left))
	}
	if docs, _ := h.Store.Documents.ByCase(h.Ctx, c.ID); len(docs) != 0 {
		t.Fatalf("%d documents left after case delete", len(docs))
	}
}

// With the wiki turned off for a case type, files are searched from source
// and the index lists them as not in the wiki (N20).
func TestFilesWithoutWikiStaySearchable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "no_wiki.yaml"), []byte("name: no_wiki\ntitle: Không wiki\nwiki: { enabled: false }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := testkit.New(t, func(c *config.Config) { c.Cases.TypesDir = dir })
	kb := h.KB(types.KBConfig{}, nil)
	c, err := h.Cases.Create(h.Ctx, h.Owner.ID, kb.ID, cases.CreateRequest{Code: "NW-1", CaseType: "no_wiki"})
	if err != nil {
		t.Fatal(err)
	}
	doc := h.UploadTo(kb.ID, "NW-1", "bang-ke.pdf", nil, []pdftest.Page{{"BANG KE KHOI LUONG", "Tong gia tri 1.250.000.000"}})
	h.Drain()
	d, _ := h.Store.Documents.Get(h.Ctx, doc)
	if d.Status != types.DocCompleted || d.WikiStatus != types.StageSkipped {
		t.Fatalf("doc = %s wiki %s", d.Status, d.WikiStatus)
	}
	idx, err := h.Wiki.IndexView(h.Ctx, c.ID, nil)
	if err != nil || !strings.Contains(idx.Content, "(chưa vào wiki) bang-ke.pdf") {
		t.Fatalf("index = %v\n%s", err, idx.Content)
	}
	resp, err := h.Index.Search(h.Ctx, types.SearchRequest{Query: "tong gia tri", CaseIDs: []uuid.UUID{c.ID}, OwnerID: h.Owner.ID})
	if err != nil || len(resp.Hits) == 0 || resp.Hits[0].DocumentID != doc {
		t.Fatalf("search without wiki = %+v %v", resp, err)
	}
}

// Without an LLM the wiki still gets template source pages and an overview
// linking them.
func TestTemplateWikiWithoutLLM(t *testing.T) {
	h := testkit.NewWithScript(t, func(s *testkit.ScriptLLM) { s.NoWikiLLM = true })
	kb := h.KB(types.KBConfig{}, nil)
	doc := h.Upload(kb.ID, "hd.pdf", nil, []pdftest.Page{{"HOP DONG", "Gia tri 5.200.000.000"}})
	h.Drain()
	c := h.Case(kb.ID, testkit.DefaultCase)
	src, err := h.Store.Wiki.SourcePage(h.Ctx, c.ID, doc)
	if err != nil || src.Summary == "" {
		t.Fatalf("template source page = %+v %v", src, err)
	}
	ov, err := h.Wiki.Page(h.Ctx, c.ID, "tong-quan")
	if err != nil || !strings.Contains(ov.Content, "[["+src.Slug+"]]") || len(ov.LinksOut) != 1 {
		t.Fatalf("template overview = %+v %v", ov, err)
	}
	if err := h.Docs.Delete(h.Ctx, h.Owner.ID, doc); err != nil {
		t.Fatal(err)
	}
	h.Drain()
	ov, _ = h.Wiki.Page(h.Ctx, c.ID, "tong-quan")
	if strings.Contains(ov.Content, src.Slug) {
		t.Fatalf("overview still lists the deleted file:\n%s", ov.Content)
	}
}

// An LLM that never answers ingest does not loop: after the retries the file
// gets a template source page, wiki_status partial and a gap issue.
func TestIngestFallsBackWhenTheLLMFails(t *testing.T) {
	h := testkit.New(t)
	h.LLM.FailWiki = true
	kb := h.KB(types.KBConfig{}, nil)
	doc := h.Upload(kb.ID, "a.pdf", nil, []pdftest.Page{{"HOP DONG", "Gia tri 100"}})
	h.Drain()
	d, _ := h.Store.Documents.Get(h.Ctx, doc)
	if d.Status != types.DocCompleted || d.WikiStatus != types.StagePartial {
		t.Fatalf("doc = %s wiki %s", d.Status, d.WikiStatus)
	}
	c := h.Case(kb.ID, testkit.DefaultCase)
	if _, err := h.Store.Wiki.SourcePage(h.Ctx, c.ID, doc); err != nil {
		t.Fatalf("no template source page: %v", err)
	}
	if gaps, _ := h.Store.Wiki.Issues(h.Ctx, c.ID, "open", types.LintGap, 10); len(gaps) == 0 {
		t.Fatal("no gap issue")
	}
}
