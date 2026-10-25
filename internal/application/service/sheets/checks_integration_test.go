package sheets_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/docpipe/parser/pdf/pdftest"
	"github.com/thanhenti/bepaylot/internal/application/service/docmodel"
	"github.com/thanhenti/bepaylot/internal/application/service/sheets"
	"github.com/thanhenti/bepaylot/internal/storage"
	"github.com/thanhenti/bepaylot/internal/testkit"
	"github.com/thanhenti/bepaylot/internal/types"
)

func init() {
	keyLine["ben_ban"] = "Ben ban"
	keyLine["tai_khoan"] = "Tai khoan"
}

// scriptLLM answers the model calls of the checks in order, by the kind of
// question in the system prompt.
type scriptLLM struct {
	compile []string
	judge   string
	calls   map[string]int
}

func (f *scriptLLM) CompleteJSON(_ context.Context, system, _ string, out any) error {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	switch {
	case strings.Contains(system, "chuyển một câu"):
		f.calls["compile"]++
		r := f.compile[0]
		if len(f.compile) > 1 {
			f.compile = f.compile[1:]
		}
		return json.Unmarshal([]byte(r), out)
	case strings.Contains(system, "kiểm tra một bộ chứng từ"):
		f.calls["judge"]++
		return json.Unmarshal([]byte(f.judge), out)
	}
	f.calls["other"]++
	return json.Unmarshal([]byte(`{}`), out)
}

type runResult struct {
	Groups []struct {
		Anchor  sheets.CheckDoc      `json:"anchor"`
		Members []sheets.CheckMember `json:"members"`
		Results []struct {
			Name    string `json:"name"`
			Status  string `json:"status"`
			Failing []struct {
				Note string `json:"note"`
			} `json:"failing"`
			Rows  []sheets.CheckRow  `json:"rows"`
			Total *sheets.CheckTotal `json:"total"`
			Hint  string             `json:"hint"`
		} `json:"results"`
	} `json:"groups"`
	Unlinked []sheets.CheckMember `json:"unlinked"`
}

func decodeRun(t *testing.T, r types.CheckRun) runResult {
	t.Helper()
	b, _ := json.Marshal(r.Result)
	var out runResult
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// U87, U104, U105 end to end on the database: a rule set used by the KB
// compares fields (formulas built by the server, fields of another kind
// refused), a sentence becomes a formula with one retry or is judged by the
// AI, invoices are linked to the contract of the same seller, the rules are
// evaluated per contract with one row per invoice (U108), an invoice of an
// unknown seller is left for the user, whose choice is kept, and evaluating
// again asks the model nothing new.
func TestChecksFlow(t *testing.T) {
	h := testkit.New(t)
	// Model answers are cached by input (U56): start from none, so the
	// counts below do not depend on an earlier run on this database.
	if _, err := h.Store.Pool.Exec(h.Ctx, `DELETE FROM model_cache WHERE key LIKE 'check:%'`); err != nil {
		t.Fatal(err)
	}
	h.Index.SetSplit(docmodel.NewSplit(docmodel.SplitDeps{Store: h.Store, Queue: h.Queue, Cases: h.Cases, Tree: h.Index, Config: h.Config}))
	ex := docmodel.NewExtractor(h.Store, &fakeExtract{}, nil)
	kb := h.KB(types.KBConfig{DocTypes: []types.DocType{
		docType("hop_dong", "Hợp đồng", types.SheetField{Key: "so_hop_dong", Label: "Số hợp đồng"}, types.SheetField{Key: "ben_ban", Label: "Bên bán"},
			types.SheetField{Key: "gia_tri", Label: "Giá trị", ValueType: "money"}, types.SheetField{Key: "tai_khoan", Label: "Tài khoản"}),
		docType("hoa_don", "Hoá đơn", types.SheetField{Key: "so_hoa_don", Label: "Số hoá đơn"}, types.SheetField{Key: "ben_ban", Label: "Bên bán"},
			types.SheetField{Key: "tong_tien", Label: "Tổng tiền", ValueType: "money"}, types.SheetField{Key: "tai_khoan", Label: "Tài khoản"}),
	}}, nil)
	h.PageTypes.Labels = []string{"hop_dong", "hop_dong", "hoa_don", "hoa_don", "hoa_don"}
	h.PageTypes.FirstPages = []bool{true, true, true, true, true}
	h.Upload(kb.ID, "bo.pdf", nil, []pdftest.Page{
		{"HOP DONG", "So hop dong: 012/2026", "Ben ban: Cong ty TNHH ABC", "Gia tri hop dong: 600.000.000", "Tai khoan: 0123456789"},
		{"HOP DONG", "So hop dong: 099/2026", "Ben ban: Cong ty CP XYZ", "Gia tri hop dong: 100.000.000", "Tai khoan: 555"},
		{"HOA DON", "So hoa don: 0001", "Ben ban: CONG TY ABC", "Tong thanh toan: 500.000.000", "Tai khoan: 0123 456 789"},
		{"HOA DON", "So hoa don: 0002", "Ben ban: Cong ty TNHH ABC", "Tong thanh toan: 250.000.000", "Tai khoan: 0123456789"},
		{"HOA DON", "So hoa don: 0003", "Ben ban: Cong ty DEF", "Tong thanh toan: 10.000.000"},
	})
	h.Drain()
	cs := h.Case(kb.ID, testkit.DefaultCase)
	llm := &scriptLLM{judge: `{"status":"pass","detail":"mặt hàng phù hợp"}`}
	sq := &stubQueue{}
	svc := sheets.New(sheets.Deps{Store: h.Store, Queue: sq, Cases: h.Cases, Extractor: ex, Config: h.Config, Objects: storage.NewMemory(), FormLLM: llm})

	// No rule set yet.
	if _, err := svc.CreateCheck(h.Ctx, h.Owner, cs.ID, uuid.New()); err == nil {
		t.Fatal("a check ran without a rule set")
	}

	ref := func(typ, key string) *types.FieldRef { return &types.FieldRef{Type: typ, Key: key} }
	set := types.CheckRuleset{Name: "Giải ngân", KBIDs: []uuid.UUID{kb.ID}, Anchor: "hop_dong",
		Links: []types.CheckLink{{DocType: "hoa_don", Steps: []types.CheckStep{
			{Text: "Cùng bên bán", Formula: `MATCH(hoa_don.ben_ban, hop_dong.ben_ban, "tên")`},
		}}},
		Rules: []types.CheckRule{
			{A: ref("hoa_don", "tong_tien"), Op: "sum_le", B: ref("hop_dong", "gia_tri")},
			{A: ref("hoa_don", "tai_khoan"), Op: "same", B: ref("hop_dong", "tai_khoan")},
			{Text: "Mặt hàng hoá đơn phù hợp hợp đồng"},
		}}
	bad := set
	bad.Rules = []types.CheckRule{{A: ref("hoa_don", "tong_tien"), Op: "same", B: ref("hop_dong", "ben_ban")}}
	if _, err := svc.SaveRuleset(h.Ctx, h.Owner, bad); err == nil || !strings.Contains(err.Error(), "khác kiểu") {
		t.Fatalf("fields of different kinds were compared: %v", err)
	}
	rs, err := svc.SaveRuleset(h.Ctx, h.Owner, set)
	if err != nil {
		t.Fatal(err)
	}
	if rs.Rules[0].Formula != "SUM(hoa_don.tong_tien) <= hop_dong.gia_tri" || rs.Rules[1].Formula != "SAME(hoa_don.tai_khoan, hop_dong.tai_khoan)" || !rs.Rules[2].AI || rs.Rules[0].ID == "" {
		t.Fatalf("rules stored: %+v", rs.Rules)
	}

	// Compile: a wrong formula is asked again once; no formula → AI.
	llm.compile = []string{`{"name":"Tổng","formula":"SUM(hoa_don.khong_co) <= 1"}`, `{"name":"Tổng hoá đơn","formula":"SUM(hoa_don.tong_tien) <= hop_dong.gia_tri","explanation":"…"}`}
	c1, err := svc.Compile(h.Ctx, h.Owner, rs.ID, "rule", "", "hop_dong", "Tổng hoá đơn không vượt giá trị hợp đồng")
	if err != nil || c1.AI || c1.Formula != "SUM(hoa_don.tong_tien) <= hop_dong.gia_tri" || llm.calls["compile"] != 2 {
		t.Fatalf("compile with retry: %+v %v calls=%d", c1, err, llm.calls["compile"])
	}
	llm.compile = []string{`{"name":"Mặt hàng","ai":true,"explanation":"cần đọc mặt hàng"}`}
	c2, _ := svc.Compile(h.Ctx, h.Owner, rs.ID, "rule", "", "hop_dong", "Mặt hàng hoá đơn phù hợp hợp đồng")
	if !c2.AI || c2.Formula != "" {
		t.Fatalf("compile to AI: %+v", c2)
	}

	// A set the case's KB does not use cannot run on it.
	unused, err := svc.SaveRuleset(h.Ctx, h.Owner, types.CheckRuleset{Name: "Khác", Anchor: "hop_dong", Rules: set.Rules[:1]})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateCheck(h.Ctx, h.Owner, cs.ID, unused.ID); err == nil {
		t.Fatal("a set not used by the KB ran")
	}

	run, err := svc.CreateCheck(h.Ctx, h.Owner, cs.ID, rs.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Handlers()[types.TaskCaseSheet](h.Ctx, sq.payloads[len(sq.payloads)-1]); err != nil {
		t.Fatal(err)
	}
	run, _ = svc.GetCheck(h.Ctx, h.Owner.ID, run.ID)
	if run.Status != types.SheetDone {
		t.Fatalf("run %s: %s", run.Status, run.Error)
	}
	res := decodeRun(t, run)
	if len(res.Groups) != 2 || len(res.Groups[0].Members) != 2 || len(res.Groups[1].Members) != 0 {
		t.Fatalf("groups: %+v", res.Groups)
	}
	g := res.Groups[0]
	if !strings.Contains(g.Anchor.Label, "012/2026") || g.Members[0].Source != "rule" || !strings.Contains(g.Members[0].Reason, "Cùng bên bán ✓") {
		t.Fatalf("group of 012: %+v", g)
	}
	if len(g.Results) != 3 || g.Results[0].Status != "fail" || g.Results[1].Status != "pass" || g.Results[2].Status != "pass" {
		t.Fatalf("results: %+v", g.Results)
	}
	if !strings.Contains(g.Results[0].Failing[0].Note, "750.000.000 không ≤ 600.000.000") || g.Results[0].Total == nil || g.Results[0].Total.A != "750.000.000" ||
		len(g.Results[0].Rows) != 2 || g.Results[0].Hint == "" || !strings.HasPrefix(g.Results[0].Name, "Tổng Tổng tiền của Hoá đơn") {
		t.Fatalf("sum rule: %+v", g.Results[0])
	}
	if len(g.Results[1].Rows) != 2 || g.Results[1].Rows[0].Status != "pass" || g.Results[1].Rows[0].ValueB != "0123456789" {
		t.Fatalf("rows of the account rule: %+v", g.Results[1].Rows)
	}
	if len(res.Unlinked) != 1 || !strings.Contains(res.Unlinked[0].Label, "0003") || !strings.Contains(res.Unlinked[0].Reason, "Cùng bên bán ✗") {
		t.Fatalf("unlinked: %+v", res.Unlinked)
	}
	judged := llm.calls["judge"]

	// The user puts invoice 0003 under contract 099; the choice is kept.
	seg := uuid.MustParse(res.Unlinked[0].ID)
	other := uuid.MustParse(res.Groups[1].Anchor.ID)
	run, err = svc.SetCheckLink(h.Ctx, h.Owner, run.ID, seg, &other, false)
	if err != nil {
		t.Fatal(err)
	}
	res = decodeRun(t, run)
	if len(res.Unlinked) != 0 || len(res.Groups[1].Members) != 1 || res.Groups[1].Members[0].Source != "user" {
		t.Fatalf("after the user's choice: %+v", res)
	}
	// Group 012 asks the model nothing new; group 099 is new input.
	if llm.calls["judge"] != judged+1 {
		t.Fatalf("judge calls %d, want %d", llm.calls["judge"], judged+1)
	}
	if _, err := svc.RerunCheck(h.Ctx, h.Owner, run.ID); err != nil {
		t.Fatal(err)
	}
	if llm.calls["judge"] != judged+1 {
		t.Fatalf("rerun asked the model again: %d", llm.calls["judge"])
	}
	// The KB uses the set; the other set is used by none.
	if list, _ := svc.ListRulesets(h.Ctx, h.Owner.ID, &kb.ID); len(list) != 1 || list[0].ID != rs.ID {
		t.Fatalf("sets of the KB: %+v", list)
	}
}
