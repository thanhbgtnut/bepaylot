package sheets_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/application/service/cases"
	"github.com/thanhenti/bepaylot/internal/application/service/docmodel"
	"github.com/thanhenti/bepaylot/internal/application/service/sheets"
	"github.com/thanhenti/bepaylot/internal/parser/pdf/pdftest"
	"github.com/thanhenti/bepaylot/internal/testkit"
	"github.com/thanhenti/bepaylot/internal/textutil"
	"github.com/thanhenti/bepaylot/internal/types"
)

// fakeClassifier stands in for the classification LLM: a document starts at
// every page the input shows (first page, cut hints) and its label comes
// from the title text.
type fakeClassifier struct {
	calls  int
	inputs []string
}

var inputLine = regexp.MustCompile(`(?m)^\[p(\d+)\] (.*)$`)

func (f *fakeClassifier) CompleteJSON(_ context.Context, system, user string, out any) error {
	f.calls++
	f.inputs = append(f.inputs, user)
	pages := 0
	if m := regexp.MustCompile(`\((\d+) pages\)`).FindStringSubmatch(user); m != nil {
		pages, _ = strconv.Atoi(m[1])
	}
	label := map[int]string{}
	var starts []int
	for _, m := range inputLine.FindAllStringSubmatch(user, -1) {
		p, _ := strconv.Atoi(m[1])
		t := textutil.Normalize(m[2])
		l := ""
		switch {
		case strings.Contains(t, "hop dong mua ban"):
			l = "hop_dong"
		case strings.Contains(t, "hoa don"):
			l = "hoa_don"
		case strings.Contains(t, "bien ban ban giao"):
			l = "bb_ban_giao"
		}
		if l != "" && label[p] == "" {
			label[p] = l
			starts = append(starts, p)
		}
	}
	var segs []map[string]any
	for i, p := range starts {
		end := pages
		if i+1 < len(starts) {
			end = starts[i+1] - 1
		}
		segs = append(segs, map[string]any{"pages": fmt.Sprintf("%d-%d", p, end), "label": label[p], "confidence": 0.9})
	}
	b, _ := json.Marshal(map[string]any{"segments": segs})
	return json.Unmarshal(b, out)
}

// fakeAgent stands in for the hidden agent turn of a sheet: for each
// document of the prompt it finds "Label: value" lines on the document's
// pages and saves them with kb_save_fields' service, by segment.
type fakeAgent struct {
	h       *testkit.Harness
	dm      *docmodel.Service
	caseID  uuid.UUID
	refs    map[string]uuid.UUID
	skip    map[string]bool // "ref key" the agent does not find
	prompts []string
}

var (
	itemRe  = regexp.MustCompile(`(?m)^\[(d\d+)(?:\.s(\d+))?\] .* · tr\. (\d+)(?:–(\d+))?$`)
	fieldRe = regexp.MustCompile(`^- (\w+) · `)
)

// where each key is written in the test documents.
var keyLine = map[string]string{"so_hop_dong": "So hop dong", "gia_tri": "Gia tri hop dong", "so_hoa_don": "So hoa don", "tong_tien": "Tong thanh toan"}

func (a *fakeAgent) RunSheet(ctx context.Context, user types.User, caseID, _ uuid.UUID, text string) (string, error) {
	a.prompts = append(a.prompts, text)
	var reply strings.Builder
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		m := itemRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		doc := a.refs[m[1]]
		ref := m[1]
		var seg *uuid.UUID
		if m[2] != "" {
			k, _ := strconv.Atoi(m[2])
			id, err := a.dm.SegmentAt(ctx, doc, k)
			if err != nil {
				return "", err
			}
			seg, ref = &id, ref+".s"+m[2]
		}
		from, _ := strconv.Atoi(m[3])
		to := from
		if m[4] != "" {
			to, _ = strconv.Atoi(m[4])
		}
		var in []types.FieldInput
		for _, fl := range lines[i+1:] {
			fm := fieldRe.FindStringSubmatch(fl)
			if fm == nil {
				break
			}
			key := fm[1]
			if a.skip[ref+" "+key] {
				fmt.Fprintf(&reply, "%s %s: không có dòng nào ghi giá trị này\n", ref, key)
				continue
			}
			lines, err := a.h.Store.Pages.Lines(ctx, doc, from, to)
			if err != nil {
				return "", err
			}
			var hit *postgres.PageLine
			for i := range lines {
				if strings.HasPrefix(lines[i].Text, keyLine[key]+": ") {
					hit = &lines[i]
					break
				}
			}
			if hit == nil {
				return "", fmt.Errorf("no line %q in %s", keyLine[key], ref)
			}
			value := strings.TrimPrefix(hit.Text, keyLine[key]+": ")
			in = append(in, types.FieldInput{DocumentID: doc, SegmentID: seg, Key: key, Value: value, Confidence: 0.9,
				Citations: []string{fmt.Sprintf("doc:%s:p%d:l%d-%d", doc, hit.PageNo, hit.LineNo, hit.LineNo)}})
		}
		for _, r := range a.dm.SaveAgentFields(ctx, user.ID, a.caseID, nil, in) {
			if r.Error != "" {
				return "", fmt.Errorf("save %s %s: %s", ref, r.Key, r.Error)
			}
		}
	}
	return reply.String(), nil
}

// U47–U49 end to end on the database (fake LLM and agent): one scan with two
// bundles is split by code + one classification call, reviewed, and built
// into one sub-table per document type with a row per document.
func TestBundleSheetFlow(t *testing.T) {
	h := testkit.New(t)
	kb := h.KB(types.KBConfig{}, nil)
	code := fmt.Sprintf("RT%06d", uuid.New().ID()%1000000)
	if _, err := h.Cases.Create(h.Ctx, h.Owner.ID, kb.ID, cases.CreateRequest{Code: code, CaseType: "thanh_toan"}); err != nil {
		t.Fatal(err)
	}
	contract := func(no, value string, n int) []pdftest.Page {
		out := []pdftest.Page{{"HOP DONG MUA BAN", "So hop dong: " + no, "Gia tri hop dong: " + value, fmt.Sprintf("Trang 1/%d", n)}}
		for k := 2; k <= n; k++ {
			out = append(out, pdftest.Page{fmt.Sprintf("Dieu %d. Dieu khoan chung cua hop dong", k), fmt.Sprintf("Trang %d/%d", k, n)})
		}
		return out
	}
	invoice := func(no, total string) pdftest.Page {
		return pdftest.Page{"HOA DON GIA TRI GIA TANG", "So hoa don: " + no, "Tong thanh toan: " + total, "Trang 1/1"}
	}
	var pages []pdftest.Page
	pages = append(pages, contract("012/2026/HDMB-AP", "660.000.000", 4)...)                    // 1–4
	pages = append(pages, invoice("0001234", "550.000.000"), invoice("0001240", "110.000.000")) // 5, 6
	pages = append(pages, pdftest.Page{"BIEN BAN BAN GIAO HANG HOA", "Can cu hop dong 012/2026/HDMB-AP", "Trang 1/2"},
		pdftest.Page{"Danh muc hang hoa ban giao", "Trang 2/2"}) // 7, 8
	pages = append(pages, contract("031/2026/HD-AP", "1.320.000.000", 4)...)                                         // 9–12
	pages = append(pages, invoice("0000877", "1.320.000.000"))                                                       // 13
	pages = append(pages, pdftest.Page{"BIEN BAN BAN GIAO HANG HOA", "Can cu hop dong 031/2026/HD-AP", "Trang 1/1"}) // 14
	doc := h.UploadTo(kb.ID, code, "scan_dot3.pdf", nil, pages)
	h.Drain()
	cs := h.Case(kb.ID, code)

	q := &stubQueue{}
	llmFake := &fakeClassifier{}
	split := docmodel.NewSplit(docmodel.SplitDeps{Store: h.Store, Queue: q, Cases: h.Cases, LLM: llmFake, Config: h.Config})

	// Cut hints by code: every page whose number goes back to 1.
	d, err := h.Store.Documents.Get(h.Ctx, doc)
	if err != nil {
		t.Fatal(err)
	}
	marks, err := split.Marks(h.Ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []int{5, 6, 7, 9, 13, 14} {
		if !strings.Contains(marks[p], "trang về 1") {
			t.Fatalf("page %d has no cut hint: %v", p, marks)
		}
	}
	if len(marks) != 6 {
		t.Fatalf("marks = %v", marks)
	}

	// One classification call for the file.
	if n, err := split.Classify(h.Ctx, h.Owner, cs.ID, nil, ""); err != nil || n != 1 {
		t.Fatalf("classify queued %d: %v", n, err)
	}
	if err := split.Handlers()[types.TaskDocClassify](h.Ctx, q.payloads[len(q.payloads)-1]); err != nil {
		t.Fatal(err)
	}
	if llmFake.calls != 1 || !strings.Contains(llmFake.inputs[0], "[p5] ✂") {
		t.Fatalf("classify calls = %d, input:\n%s", llmFake.calls, llmFake.inputs)
	}
	v, err := split.View(h.Ctx, h.Owner.ID, cs.ID)
	if err != nil {
		t.Fatal(err)
	}
	f := v.Files[0]
	if f.Status != types.SplitProposed || len(f.Segments) != 7 || strings.Join(v.Bundles, ",") != "B01,B02" {
		t.Fatalf("view = %s %d %v", f.Status, len(f.Segments), v.Bundles)
	}
	if g := f.Segments[1]; g.PageStart != 5 || g.PageEnd != 5 || g.Label != "hoa_don" || g.BundleCode != "B01" {
		t.Fatalf("segment 2 = %+v", g)
	}
	if g := f.Segments[4]; g.Label != "hop_dong" || g.BundleCode != "B02" {
		t.Fatalf("segment 5 = %+v", g)
	}

	// A sheet needs the reviewed split.
	tpl, err := h.Store.Templates.Create(h.Ctx, postgres.TemplateDraft{Kind: types.TemplateSheet, Slug: "tt_" + uuid.NewString()[:8], Name: "Bộ chứng từ",
		CaseType: "thanh_toan", Body: "Bóc tách.", Tables: []types.SheetTable{
			{Label: "hop_dong", Title: "Hợp đồng", Fields: []types.SheetField{{Key: "so_hop_dong", Label: "Số hợp đồng"}, {Key: "gia_tri", Label: "Giá trị", ValueType: "money"}}},
			{Label: "hoa_don", Title: "Hoá đơn", Fields: []types.SheetField{{Key: "so_hoa_don", Label: "Số hoá đơn"}, {Key: "tong_tien", Label: "Tổng thanh toán", ValueType: "money"}}},
			{Label: "bb_ban_giao", Title: "BB bàn giao", Fields: []types.SheetField{{Key: "so_hop_dong", Label: "Căn cứ HĐ"}}},
		}, By: h.Owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Store.Templates.Publish(h.Ctx, tpl.ID, 1); err != nil {
		t.Fatal(err)
	}
	refs, err := h.Index.CaseRefs(h.Ctx, h.Owner.ID, cs.ID)
	if err != nil {
		t.Fatal(err)
	}
	dm := docmodel.New(h.Store, h.Index)
	agent := &fakeAgent{h: h, dm: dm, caseID: cs.ID, refs: refs, skip: map[string]bool{}}
	sq := &stubQueue{}
	svc := sheets.New(sheets.Deps{Store: h.Store, Queue: sq, Cases: h.Cases, Searcher: h.Index, Split: split, Runner: agent, Config: h.Config})
	var notReviewed *sheets.SplitNotReviewedError
	if _, err := svc.Create(h.Ctx, h.Owner, cs.ID, tpl.ID, nil); !errors.As(err, &notReviewed) {
		t.Fatalf("sheet before review: %v", err)
	}

	// Review: a page left out is refused; the proposal as is is accepted.
	input := func(drop int) docmodel.SplitInput {
		var in docmodel.SplitInput
		for _, b := range v.Bundles {
			var bundle struct {
				Segments []struct {
					DocumentID uuid.UUID `json:"document_id"`
					PageStart  int       `json:"page_start"`
					PageEnd    int       `json:"page_end"`
					Label      string    `json:"label"`
				} `json:"segments"`
			}
			for i, g := range f.Segments {
				if g.BundleCode != b || i == drop {
					continue
				}
				bundle.Segments = append(bundle.Segments, struct {
					DocumentID uuid.UUID `json:"document_id"`
					PageStart  int       `json:"page_start"`
					PageEnd    int       `json:"page_end"`
					Label      string    `json:"label"`
				}{g.DocumentID, g.PageStart, g.PageEnd, g.Label})
			}
			in.Bundles = append(in.Bundles, bundle)
		}
		return in
	}
	if err := split.Save(h.Ctx, h.Owner, cs.ID, input(3)); !errors.Is(err, docmodel.ErrBadSplit) {
		t.Fatalf("split with a missing page: %v", err)
	}
	if err := split.Save(h.Ctx, h.Owner, cs.ID, input(-1)); err != nil {
		t.Fatal(err)
	}
	v, _ = split.View(h.Ctx, h.Owner.ID, cs.ID)
	if !v.Reviewed || v.Files[0].Status != types.SplitReviewed {
		t.Fatalf("after review = %+v", v.Files[0])
	}
	segIDs := map[int]uuid.UUID{}
	for _, g := range v.Files[0].Segments {
		segIDs[g.PageStart] = g.ID
	}

	// Build two of the three sub-tables; the agent misses one cell of B02.
	agent.skip["d1.s6 tong_tien"] = true
	sh, err := svc.Create(h.Ctx, h.Owner, cs.ID, tpl.ID, []string{"hop_dong", "hoa_don"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Handlers()[types.TaskCaseSheet](h.Ctx, sq.payloads[len(sq.payloads)-1]); err != nil {
		t.Fatal(err)
	}
	sv, err := svc.Get(h.Ctx, h.Owner.ID, sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(agent.prompts) != 2 || !strings.Contains(agent.prompts[0], "Bộ chứng từ B01") || !strings.Contains(agent.prompts[0], "[d1.s3] Hoá đơn") {
		t.Fatalf("prompts (one per bundle):\n%s", strings.Join(agent.prompts, "\n----\n"))
	}
	if sv.Status != types.SheetDone || len(sv.TablesView) != 2 || sv.Total != 10 || sv.Filled != 9 {
		t.Fatalf("sheet = %s tables=%d %d/%d", sv.Status, len(sv.TablesView), sv.Filled, sv.Total)
	}
	hd, inv := sv.TablesView[0], sv.TablesView[1]
	if len(hd.Rows) != 2 || hd.Rows[0].Bundle != "B01" || hd.Rows[1].Bundle != "B02" || hd.Rows[1].CellsView["gia_tri"].Value != "1.320.000.000" {
		t.Fatalf("contracts = %+v", hd.Rows)
	}
	if len(inv.Rows) != 3 || inv.Rows[0].CellsView["so_hoa_don"].Value != "0001234" || inv.Rows[1].CellsView["so_hoa_don"].Value != "0001240" {
		t.Fatalf("invoices (two in the same file) = %+v", inv.Rows)
	}
	if c := inv.Rows[2].CellsView["tong_tien"]; c.FieldID != nil || c.Note == "" {
		t.Fatalf("missing cell = %+v", c)
	}

	// Edit one invoice: a correction for that table and key.
	seg2 := inv.Rows[1].SegmentID
	sv, err = svc.SaveEdits(h.Ctx, h.Owner, sh.ID, []sheets.EditInput{{Table: "hoa_don", SegmentID: seg2, Key: "so_hoa_don", Value: "0001241"},
		{Table: "hoa_don", SegmentID: inv.Rows[2].SegmentID, Key: "tong_tien", Value: "1.320.000.000"}})
	if err != nil {
		t.Fatal(err)
	}
	if c := sv.TablesView[1].Rows[1].CellsView["so_hoa_don"]; !c.Edited || c.Value != "0001241" || sv.TablesView[1].Rows[0].CellsView["so_hoa_don"].Edited {
		t.Fatalf("after edit = %+v", sv.TablesView[1].Rows[:2])
	}
	stats, err := svc.CorrectionStats(h.Ctx, tpl.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range stats {
		if s.Label == "hoa_don" && s.Key == "so_hoa_don" {
			found = s.Edited == 1 && s.Sheets == 3
		}
	}
	if !found {
		t.Fatalf("stats = %+v", stats)
	}

	// .xlsx: one data sheet per sub-table, no confidence or source columns.
	var buf bytes.Buffer
	if _, err := svc.XLSX(h.Ctx, h.Owner.ID, sh.ID, nil, &buf); err != nil {
		t.Fatal(err)
	}
	wb, err := excelize.OpenReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(wb.GetSheetList(), ","); got != "Hợp đồng,_bp,Hoá đơn" && got != "Hợp đồng,Hoá đơn,_bp" {
		t.Fatalf("sheets = %s", got)
	}
	head, _ := wb.GetRows("Hoá đơn")
	if strings.Join(head[0], "|") != "Bộ|Số hoá đơn|Tổng thanh toán" || len(head) != 4 || head[2][1] != "0001241" {
		t.Fatalf("invoice sheet = %v", head)
	}
	_ = wb.SetCellValue("Hoá đơn", "B2", "0001235")
	_ = wb.SetCellValue("Hoá đơn", "A6", "B09")
	var edited bytes.Buffer
	if err := wb.Write(&edited); err != nil {
		t.Fatal(err)
	}
	imp, err := svc.Import(h.Ctx, h.Owner.ID, sh.ID, bytes.NewReader(edited.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if len(imp.Edits) != 1 || imp.Edits[0].Key != "so_hoa_don" || imp.Edits[0].SegmentID == nil || *imp.Edits[0].SegmentID != *inv.Rows[0].SegmentID ||
		len(imp.Ignored) != 1 || len(imp.Conflicts) != 0 {
		t.Fatalf("import = %+v", imp)
	}
	var one bytes.Buffer
	if _, err := svc.XLSX(h.Ctx, h.Owner.ID, sh.ID, []string{"hoa_don"}, &one); err != nil {
		t.Fatal(err)
	}
	wb1, _ := excelize.OpenReader(bytes.NewReader(one.Bytes()))
	if got := len(wb1.GetSheetList()); got != 2 {
		t.Fatalf("one table + _bp, got %v", wb1.GetSheetList())
	}

	// Confirming the same split again keeps the segments, so the fields stay.
	if err := split.Save(h.Ctx, h.Owner, cs.ID, input(-1)); err != nil {
		t.Fatal(err)
	}
	v, _ = split.View(h.Ctx, h.Owner.ID, cs.ID)
	for _, g := range v.Files[0].Segments {
		if segIDs[g.PageStart] != g.ID {
			t.Fatalf("segment at page %d changed id", g.PageStart)
		}
	}
}
