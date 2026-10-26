package doctypes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/docpipe/parser"
	dtypes "github.com/thanhenti/bepaylot/docpipe/types"
	"github.com/thanhenti/bepaylot/internal/application/service/docmodel/doctypes"
	"github.com/thanhenti/bepaylot/internal/config"
	"github.com/thanhenti/bepaylot/internal/testkit"
	"github.com/thanhenti/bepaylot/internal/types"
)

// fakeOCR reads every page as two lines and a markdown.
type fakeOCR struct{}

func (fakeOCR) Name() string                 { return "fake" }
func (fakeOCR) Health(context.Context) error { return nil }
func (fakeOCR) ParsePage(_ context.Context, in parser.PageImage, _ parser.PageOptions) (*parser.RawPage, error) {
	q := func(y float64) dtypes.Quad { return dtypes.Quad{{1, y}, {30, y}, {30, y + 5}, {1, y + 5}} }
	return &parser.RawPage{Width: in.Width, Height: in.Height, Markdown: "# UỶ NHIỆM CHI\nSố tiền: 1.500.000 đồng",
		Lines: []parser.RawLine{{ID: 0, Text: "UY NHIEM CHI", Confidence: 0.98, Quad: q(1), LayoutID: -1}, {ID: 1, Text: "So tien: 1.500.000 dong", Confidence: 0.9, Quad: q(10), LayoutID: -1}},
		Raw:   []byte("{}")}, nil
}

// readLLM proposes a rule when asked to optimize.
type readLLM struct{ calls map[string]int }

func (f *readLLM) CompleteJSON(_ context.Context, system, user string, out any) error {
	f.calls["optimize"]++
	return json.Unmarshal([]byte(`{"patterns":[{"field":"so_tien","finding":"AI giữ chữ đồng","rule":"Số tiền chỉ ghi chữ số, bỏ chữ đồng"},{"field":"khong_co","finding":"x","rule":"y"}],"shots":[]}`), out)
}

// agentExtract stands for the agent's extraction turn of a test run: it
// answers like the agent (prose around a fenced JSON block), reads the
// amount wrong until the instructions have a rule, and checks it gets the
// sample's page content and no case. fail makes the provider refuse the key.
type agentExtract struct {
	t     *testing.T
	calls int
	fail  error
	hang  bool // never answers: the run's time limit must stop it
}

func (f *agentExtract) RunExtract(ctx context.Context, _ types.User, caseID, _ uuid.UUID, text string) (string, error) {
	f.calls++
	if f.hang {
		<-ctx.Done()
		return "", ctx.Err()
	}
	if f.fail != nil {
		return "", f.fail
	}
	if caseID != uuid.Nil || !strings.Contains(text, "UỶ NHIỆM CHI") || !strings.Contains(text, "So tien: 1.500.000 dong") {
		f.t.Errorf("extract turn: case %s, text:\n%s", caseID, text)
	}
	v := "1.500.000 đồng"
	if strings.Contains(text, "bỏ chữ đồng") {
		v = "1500000"
	}
	return "Kết quả trích xuất:\n```json\n" + `{"fields":[{"key":"so_tien","value":"` + v + `","page":1,"line":2},{"key":"ngan_hang","value":null}]}` + "\n```", nil
}

func pngBytes(t *testing.T) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 60, 40))
	for x := 0; x < 60; x++ {
		img.Set(x, 20, color.Black)
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// N61 and U94/U98 on the database: the catalogue is shared by the KBs of
// its owner; a sample is read once into content and lines; runs are scored
// against the reviewed values; an optimization proposes a rule, scored
// before/after, and applying it makes a new version in use.
func TestDocTypeCatalogAndLab(t *testing.T) {
	h := testkit.NewWithEngines(t, []parser.Engine{fakeOCR{}}, func(c *config.Config) { c.Parser.DefaultEngine = "fake" })
	ctx, owner := h.Ctx, h.Owner.ID
	ai := &readLLM{calls: map[string]int{}}
	ex := &agentExtract{t: t}
	svc := doctypes.New(doctypes.Deps{Store: h.Store, Objects: h.Objects, Queue: h.Queue, Engines: engines(h), LLM: ai, Extract: ex, Config: h.Config})
	h.Queue.Register(svc.Handlers(), func(context.Context, string, string, []byte, error, int) {})

	kbA := h.KB(types.KBConfig{}, nil)
	kbB := h.KB(types.KBConfig{}, nil)
	unc, err := svc.Create(ctx, owner, doctypes.CreateInput{Title: "Uỷ nhiệm chi", KBIDs: []uuid.UUID{kbA.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if unc.Name != "uy_nhiem_chi" || unc.PromptVersion != 1 || len(unc.KBIDs) != 1 {
		t.Fatalf("created %+v", unc)
	}
	again, _ := svc.Create(ctx, owner, doctypes.CreateInput{Title: "Uỷ nhiệm chi"})
	if again.Name != "uy_nhiem_chi_2" {
		t.Fatalf("second code %q", again.Name)
	}
	fields := []types.SheetField{{Label: "Số tiền", ValueType: "money"}, {Label: "Ngân hàng"}}
	if unc, err = svc.Update(ctx, owner, unc.Name, doctypes.Patch{Fields: &fields}); err != nil {
		t.Fatal(err)
	}
	if len(unc.Fields) != 2 || unc.Fields[0].Key != "so_tien" || unc.Fields[1].Key != "ngan_hang" {
		t.Fatalf("fields %+v", unc.Fields)
	}
	if err := svc.SetKBs(ctx, owner, unc.Name, []uuid.UUID{kbA.ID, kbB.ID}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{kbA.ID, kbB.ID} {
		kb, _ := h.Store.KBs.Get(ctx, id)
		if len(kb.Config.DocTypes) != 1 || len(kb.Config.DocTypes[0].Fields) != 2 || kb.Config.DocTypes[0].Title != "Uỷ nhiệm chi" {
			t.Fatalf("kb %s doc types %+v", id, kb.Config.DocTypes)
		}
	}
	var inUse *doctypes.InUseError
	if err := svc.Delete(ctx, owner, unc.Name, false); !errors.As(err, &inUse) || len(inUse.KBs) != 2 {
		t.Fatalf("delete in use: %v", err)
	}
	if err := svc.Delete(ctx, owner, again.Name, false); err != nil {
		t.Fatal(err)
	}
	if list, _ := svc.List(ctx, owner, doctypes.ListFilter{KB: &kbB.ID}); len(list) != 1 || list[0].Name != unc.Name {
		t.Fatalf("list by kb %+v", list)
	}

	smp, err := svc.Upload(ctx, owner, unc.Name, "unc.png", bytes.NewReader(pngBytes(t)), owner)
	if err != nil {
		t.Fatal(err)
	}
	h.Drain()
	d, err := svc.Sample(ctx, owner, unc.Name, smp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != types.SampleReady || len(d.Pages) != 1 || !strings.Contains(d.Pages[0].Markdown, "UỶ NHIỆM CHI") || len(d.Pages[0].Lines) != 2 {
		t.Fatalf("sample read %+v", d.DocTypeSample)
	}
	if _, err := svc.PageImage(ctx, owner, unc.Name, smp.ID, 1); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Optimize(ctx, owner, unc.Name, 0, owner); !errors.Is(err, doctypes.ErrNoReviewed) {
		t.Fatalf("optimize without reviewed: %v", err)
	}
	// A provider that refuses the key: the run fails at once with the reason.
	// Replies cached by an earlier run of this test would answer instead.
	if _, err := h.Store.Pool.Exec(ctx, `DELETE FROM model_cache WHERE key LIKE 'doctype-run:%'`); err != nil {
		t.Fatal(err)
	}
	ex.fail = errors.New("error, status code: 401, status: 401 Unauthorized, message: Invalid API Key")
	bad, err := svc.Run(ctx, owner, unc.Name, nil, 0, owner)
	if err != nil {
		t.Fatal(err)
	}
	h.Drain()
	if bad, _ = svc.Job(ctx, owner, unc.Name, bad.ID); bad.Status != types.JobFailed || !strings.Contains(bad.Error, "Khoá AI không hợp lệ") || ex.calls != 1 {
		t.Fatalf("refused key: job %s %q after %d calls", bad.Status, bad.Error, ex.calls)
	}
	ex.fail = nil

	// A model that never answers: the run returns when the time limit is
	// reached, failed, instead of keeping the page waiting.
	h.Config.DocTypes.RunTimeout = 300 * time.Millisecond
	ex.hang = true
	start := time.Now()
	slow, err := svc.Run(ctx, owner, unc.Name, nil, 0, owner)
	if err != nil {
		t.Fatal(err)
	}
	if slow.Status != types.JobFailed || !strings.Contains(slow.Error, "quá lâu") || time.Since(start) > 5*time.Second {
		t.Fatalf("hanging model: job %s %q after %v", slow.Status, slow.Error, time.Since(start))
	}
	ex.hang = false
	h.Config.DocTypes.RunTimeout = time.Minute

	if _, err := svc.Run(ctx, owner, unc.Name, nil, 0, owner); err != nil {
		t.Fatal(err)
	}
	h.Drain()
	if _, err := svc.SetTruth(ctx, owner, unc.Name, smp.ID, map[string]string{"so_tien": "1500000"}, owner); err != nil {
		t.Fatal(err)
	}
	list, _ := svc.Samples(ctx, owner, unc.Name)
	if len(list) != 1 || !list[0].Ran || list[0].Score == nil || *list[0].Score != 0.5 {
		t.Fatalf("sample score %+v", list)
	}

	job, err := svc.Optimize(ctx, owner, unc.Name, 0, owner)
	if err != nil {
		t.Fatal(err)
	}
	h.Drain()
	job, _ = svc.Job(ctx, owner, unc.Name, job.ID)
	p := job.Result
	if job.Status != types.JobDone || p == nil || p.None || len(p.Patterns) != 1 || len(p.Rules) != 1 {
		t.Fatalf("optimize job %+v %+v", job, p)
	}
	if *p.Before != 0.5 || *p.After != 1 || len(p.Worse) != 0 {
		t.Fatalf("scores %v → %v worse %v", *p.Before, *p.After, p.Worse)
	}
	added := false
	for _, l := range p.Diff {
		if l.Op == "+" && l.Why == "f:so_tien" {
			added = true
		}
	}
	if !added {
		t.Fatalf("diff has no line for the rule: %+v", p.Diff)
	}
	v2, err := svc.AddPrompt(ctx, owner, unc.Name, doctypes.PromptInput{Instruction: p.Instruction, Rules: p.Rules, Shots: p.Shots, Use: true}, owner)
	if err != nil || v2.Version != 2 {
		t.Fatalf("apply: %+v %v", v2, err)
	}
	if err := svc.DeletePrompt(ctx, owner, unc.Name, 2); !errors.Is(err, doctypes.ErrCurrent) {
		t.Fatalf("delete current: %v", err)
	}
	if _, err := svc.Run(ctx, owner, unc.Name, nil, 0, owner); err != nil {
		t.Fatal(err)
	}
	h.Drain()
	got, _ := svc.Get(ctx, owner, unc.Name)
	if got.PromptVersion != 2 || got.Accuracy == nil || *got.Accuracy != 1 || got.Reviewed != 1 {
		t.Fatalf("after apply %+v", got)
	}
	if text, _ := svc.PromptText(ctx, owner, unc.Name, 2); !strings.Contains(text, "bỏ chữ đồng") {
		t.Fatalf("prompt text lacks the rule:\n%s", text)
	}

	cp, err := svc.Copy(ctx, owner, unc.Name, owner)
	if err != nil {
		t.Fatal(err)
	}
	ps, _ := svc.Prompts(ctx, owner, cp.Name)
	if len(ps) != 1 || len(ps[0].Rules) != 1 || len(cp.Fields) != 2 {
		t.Fatalf("copy %+v %+v", cp, ps)
	}
	if err := svc.Delete(ctx, owner, unc.Name, true); err != nil {
		t.Fatal(err)
	}
	if kb, _ := h.Store.KBs.Get(ctx, kbA.ID); len(kb.Config.DocTypes) != 0 {
		t.Fatalf("detach left %+v", kb.Config.DocTypes)
	}
}

func engines(h *testkit.Harness) *parser.Registry {
	r := parser.NewRegistry(h.Config.Parser.DefaultEngine)
	r.Register(fakeOCR{})
	return r
}
