package sheets_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/docpipe/parser/pdf/pdftest"
	"github.com/thanhenti/bepaylot/docpipe/textutil"
	"github.com/thanhenti/bepaylot/internal/application/service/cases"
	"github.com/thanhenti/bepaylot/internal/application/service/docmodel"
	"github.com/thanhenti/bepaylot/internal/testkit"
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

// U48, U49 end to end on the database (fake LLM): one scan with two bundles
// is split by code + one classification call and reviewed.
func TestBundleSplitFlow(t *testing.T) {
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
	split := docmodel.NewSplit(docmodel.SplitDeps{Store: h.Store, Queue: q, Cases: h.Cases, LLM: llmFake, Tree: h.Index, Config: h.Config})

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
