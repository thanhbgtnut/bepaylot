package vlm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thanhenti/bepaylot/internal/parser"
	"github.com/thanhenti/bepaylot/internal/parser/assemble"
	"github.com/thanhenti/bepaylot/internal/parser/turboocr"
	"github.com/thanhenti/bepaylot/internal/types"
)

const sampleLayout = "../turboocr/testdata/output_example.json"

func TestSplitFrontMatter(t *testing.T) {
	meta, text := SplitFrontMatter("---\nprimary_language: vi\nis_table: True\n---\nGIẤY CHỨNG NHẬN\nđăng ký")
	if meta["primary_language"] != "vi" || meta["is_table"] != "True" || text != "GIẤY CHỨNG NHẬN\nđăng ký" {
		t.Fatalf("meta=%v text=%q", meta, text)
	}
	meta, text = SplitFrontMatter("```markdown\nplain\n```")
	if meta != nil || text != "plain" {
		t.Fatalf("fence: meta=%v text=%q", meta, text)
	}
	if _, text = SplitFrontMatter("no front matter --- here"); text != "no front matter --- here" {
		t.Fatalf("text=%q", text)
	}
}

func TestClientTranscribe(t *testing.T) {
	var got chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer k" {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"---\nprimary_language: vi\n---\nXin chào"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":3}}`)
	}))
	defer srv.Close()
	c := NewClient(ClientConfig{BaseURL: srv.URL + "/v1/", APIKey: "k", Model: "allenai/olmocr-2-7b"})
	tr, err := c.Transcribe(context.Background(), []byte{0xff, 0xd8})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Text != "Xin chào" || tr.Meta["primary_language"] != "vi" || tr.PromptTokens != 10 {
		t.Fatalf("transcription = %+v", tr)
	}
	parts := got.Messages[0].Content
	if got.Model != "allenai/olmocr-2-7b" || len(parts) != 2 || parts[0].Text != DefaultPrompt ||
		!strings.HasPrefix(parts[1].ImageURL.URL, "data:image/jpeg;base64,") {
		t.Fatalf("request = %+v", got)
	}
}

func TestClientStatusErrors(t *testing.T) {
	code := http.StatusServiceUnavailable
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "busy", code) }))
	defer srv.Close()
	c := NewClient(ClientConfig{BaseURL: srv.URL})
	_, err := c.Transcribe(context.Background(), nil)
	if !Retryable(err) {
		t.Fatalf("503 should be retryable: %v", err)
	}
	code = http.StatusBadRequest
	if _, err = c.Transcribe(context.Background(), nil); Retryable(err) {
		t.Fatalf("400 should not be retryable: %v", err)
	}
}

// fakeLayout serves the TurboOCR sample.
type fakeLayout struct{ calls atomic.Int32 }

func (f *fakeLayout) Name() string                 { return turboocr.Name }
func (f *fakeLayout) Health(context.Context) error { return nil }
func (f *fakeLayout) ParsePage(_ context.Context, in parser.PageImage, _ parser.PageOptions) (*parser.RawPage, error) {
	f.calls.Add(1)
	if _, err := io.ReadAll(in.Body); err != nil {
		return nil, err
	}
	body, err := os.ReadFile(sampleLayout)
	if err != nil {
		return nil, err
	}
	p, err := turboocr.Decode(body)
	if err != nil {
		return nil, err
	}
	p.Width, p.Height = in.Width, in.Height
	return p, nil
}

// fakeVLM recognizes the region from the crop size and returns its OCR text
// with accents fixed, a HTML table for the table region, and fails one region.
type fakeVLM struct {
	page     *parser.RawPage
	pad      int
	inflight atomic.Int32
	peak     atomic.Int32
	mu       sync.Mutex
	seen     map[int]bool
	failID   int
}

func (f *fakeVLM) Model() string                { return "fake-olmocr" }
func (f *fakeVLM) Health(context.Context) error { return nil }
func (f *fakeVLM) Transcribe(ctx context.Context, img []byte) (*Transcription, error) {
	n := f.inflight.Add(1)
	defer f.inflight.Add(-1)
	for {
		p := f.peak.Load()
		if n <= p || f.peak.CompareAndSwap(p, n) {
			break
		}
	}
	time.Sleep(5 * time.Millisecond)
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(img))
	if err != nil {
		return nil, err
	}
	for _, r := range f.page.Regions {
		b := r.Quad.BBox()
		w, h := int(b.X1+float64(f.pad)+0.5)-int(b.X0-float64(f.pad)), int(b.Y1+float64(f.pad)+0.5)-int(b.Y0-float64(f.pad))
		if w != cfg.Width || h != cfg.Height {
			continue
		}
		f.mu.Lock()
		f.seen[r.ID] = true
		f.mu.Unlock()
		if r.ID == f.failID {
			return nil, &StatusError{Code: 400, Body: "bad image"}
		}
		if r.Class == "table" {
			return &Transcription{Text: "---\nis_table: True\n---\n<table><tr><th>STT</th><th>Tên ngành</th></tr><tr><td>1</td><td>Gia công cơ khí<br>Chi tiết: Gia công tôn</td></tr></table>"}, nil
		}
		var parts []string
		for _, l := range f.page.Lines {
			if l.LayoutID == r.ID {
				parts = append(parts, strings.ReplaceAll(l.Text, "BẢNGỐC", "BẢN GỐC"))
			}
		}
		return &Transcription{Text: "---\nprimary_language: vi\n---\n" + strings.Join(parts, " ")}, nil
	}
	return nil, errors.New("unknown crop")
}

func pageJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	img.Set(10, 10, color.Black)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 50}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestEngineRefinesRegionsConcurrently(t *testing.T) {
	layout := &fakeLayout{}
	sample, err := layout.ParsePage(context.Background(), parser.PageImage{Body: strings.NewReader("")}, parser.PageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	fv := &fakeVLM{page: sample, pad: 0, seen: map[int]bool{}, failID: 3}
	e, err := New(Config{Layout: layout, Client: fv, MaxConcurrency: 3, Padding: 0})
	if err != nil {
		t.Fatal(err)
	}
	img := pageJPEG(t, 3319, 4939)
	in := parser.PageImage{PageNo: 1, Body: bytes.NewReader(img), Size: int64(len(img)), Width: 3319, Height: 4939}
	raw, err := e.ParsePage(context.Background(), in, parser.PageOptions{Layout: true, ReadingOrder: true, Tables: true, Refine: true})
	if err != nil {
		t.Fatal(err)
	}
	if p := fv.peak.Load(); p < 2 || p > 3 {
		t.Errorf("peak concurrency = %d, want 2..3", p)
	}
	selected := 0
	for _, r := range raw.Regions {
		if e.classes[r.Class] {
			selected++
		}
	}
	// Regions 30 and 33 hold no lines and enclose 26/27 and 21/24; region 31
	// holds no line and lies inside 9, which has its line. All are skipped.
	selected -= 3
	if len(fv.seen) != selected || fv.seen[30] || fv.seen[33] || fv.seen[31] {
		t.Errorf("VLM saw %d regions %v, want %d without nested 30, 31, 33", len(fv.seen), fv.seen, selected)
	}

	var env rawEnvelope
	if err := json.Unmarshal(raw.Raw, &env); err != nil || env.Engine != Name || len(env.Regions) != selected || len(env.Layout) == 0 {
		t.Fatalf("raw envelope: %v %+v", err, env.Engine)
	}

	page := assemble.Build(raw, assemble.Options{PageNo: 1, DPI: 300, Engine: Name, ReadingOrderFix: true})
	assemble.Render(page)
	md := page.Markdown
	for _, want := range []string{"ĐÃ ĐỐI CHIẾU BẢN GỐC", "| STT | Tên ngành |", "| 1 | Gia công cơ khí Chi tiết: Gia công tôn |", "GIẤY CHỨNG NHẬN ĐĂNG KÝ HỘ KINH DOANH"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
	refined, failedKept := 0, false
	for _, b := range page.Blocks {
		if b.TextSource == types.TextSourceVLM {
			refined++
		}
		if b.SourceID == 3 && b.TextSource == "" && b.Text != "" {
			failedKept = true // failed region keeps OCR text
		}
	}
	if refined < selected/2 || !failedKept {
		t.Errorf("refined blocks = %d of %d, failed region kept OCR = %v", refined, selected, failedKept)
	}
	runes := []rune(md)
	for _, l := range page.Lines {
		if l.MdStart >= 0 && string(runes[l.MdStart:l.MdEnd]) != l.Text {
			t.Errorf("line %d offsets point at %q, text %q", l.LineNo, string(runes[l.MdStart:l.MdEnd]), l.Text)
		}
	}
}

func TestEngineSkipsVLMWhenRefineOff(t *testing.T) {
	layout := &fakeLayout{}
	fv := &fakeVLM{seen: map[int]bool{}}
	e, _ := New(Config{Layout: layout, Client: fv})
	_, err := e.ParsePage(context.Background(), parser.PageImage{Body: strings.NewReader("x")}, parser.PageOptions{Refine: false})
	if err != nil || len(fv.seen) != 0 || layout.calls.Load() != 1 {
		t.Fatalf("err=%v seen=%v calls=%d", err, fv.seen, layout.calls.Load())
	}
}

func TestEngineOnErrorFail(t *testing.T) {
	layout := &fakeLayout{}
	sample, _ := layout.ParsePage(context.Background(), parser.PageImage{Body: strings.NewReader("")}, parser.PageOptions{})
	fv := &fakeVLM{page: sample, seen: map[int]bool{}, failID: 3}
	e, _ := New(Config{Layout: layout, Client: fv, OnError: OnErrorFail})
	img := pageJPEG(t, 3319, 4939)
	_, err := e.ParsePage(context.Background(), parser.PageImage{Body: bytes.NewReader(img), Width: 3319, Height: 4939}, parser.PageOptions{Refine: true})
	if err == nil || !strings.Contains(err.Error(), "regions failed") {
		t.Fatalf("err = %v", err)
	}
}

// TestLiveVLM runs the whole engine against a real OpenAI-compatible VLM:
//
//	VLM_BASE_URL=http://localhost:1234/v1 VLM_TEST_IMAGE=page.jpg go test -run TestLiveVLM -v ./internal/parser/vlm
//
// The image must match the layout in turboocr/testdata (the sample page).
func TestLiveVLM(t *testing.T) {
	base, imgPath := os.Getenv("VLM_BASE_URL"), os.Getenv("VLM_TEST_IMAGE")
	if base == "" || imgPath == "" {
		t.Skip("VLM_BASE_URL and VLM_TEST_IMAGE not set")
	}
	img, err := os.ReadFile(imgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(img))
	if err != nil {
		t.Fatal(err)
	}
	model := os.Getenv("VLM_MODEL")
	if model == "" {
		model = "allenai/olmocr-2-7b"
	}
	client := NewClient(ClientConfig{BaseURL: base, Model: model, Temperature: 0.1})
	if err := client.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	e, _ := New(Config{Layout: &fakeLayout{}, Client: client, MaxConcurrency: 4, Padding: 12, MaxSide: 1288, Retries: 1})
	t0 := time.Now()
	raw, err := e.ParsePage(context.Background(), parser.PageImage{PageNo: 1, Body: bytes.NewReader(img), Width: cfg.Width, Height: cfg.Height},
		parser.PageOptions{Layout: true, ReadingOrder: true, Tables: true, Refine: true})
	if err != nil {
		t.Fatal(err)
	}
	var env rawEnvelope
	_ = json.Unmarshal(raw.Raw, &env)
	failed := 0
	for _, r := range env.Regions {
		t.Logf("region %2d %-15s %5dms %q%s", r.LayoutID, r.Class, r.Ms, trim(r.Text, 80), r.Error)
		if r.Error != "" {
			failed++
		}
	}
	page := assemble.Build(raw, assemble.Options{PageNo: 1, DPI: 300, Engine: Name, ReadingOrderFix: true})
	assemble.Render(page)
	refined, vlmLines, located := 0, 0, 0
	for _, b := range page.Blocks {
		if b.TextSource == types.TextSourceVLM {
			refined++
		}
	}
	for _, l := range page.Lines {
		if l.TextSource == types.TextSourceVLM {
			vlmLines++
			if l.MdStart >= 0 {
				located++
			}
		}
	}
	t.Logf("%d regions (%d failed) in %s; %d blocks refined; %d/%d VLM lines located\n%s",
		len(env.Regions), failed, time.Since(t0).Round(time.Millisecond), refined, located, vlmLines, page.Markdown)
	if refined == 0 || failed == len(env.Regions) {
		t.Fatal("no region was refined")
	}
}

func trim(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", "⏎")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

const fullPageMD = `---
primary_language: vi
is_table: False
---
# GIẤY CHỨNG NHẬN ĐĂNG KÝ HỘ KINH DOANH

Mã số hộ kinh doanh: 070082001498

## 3. Ngành, nghề kinh doanh

<table>
  <tr><th>STT</th><th>Tên ngành</th></tr>
  <tr><td>1</td><td>Gia công cơ khí</td></tr>
</table>

![Con dấu đỏ](page_100_200_300_150.png)

Họ và tên: NGUYỄN VĂN TÌNH
Giới tính: Nam`

func TestSplitMarkdown(t *testing.T) {
	_, md := SplitFrontMatter(fullPageMD)
	rs := SplitMarkdown(md, 2000, 3000, 2)
	var classes []string
	for _, r := range rs {
		classes = append(classes, r.Class)
	}
	if got := strings.Join(classes, ","); got != "doc_title,text,paragraph_title,table,figure,text" {
		t.Fatalf("classes = %s", got)
	}
	if !strings.Contains(rs[3].HTML, "<td>Gia công cơ khí</td>") || rs[3].Text != "" {
		t.Errorf("table = %+v", rs[3])
	}
	if fb := rs[4].Quad.BBox(); fb.X0 != 200 || fb.Y0 != 400 || fb.X1 != 800 || fb.Y1 != 700 {
		t.Errorf("figure box = %+v (scaled ×2)", fb)
	}
	if rs[5].Text != "Họ và tên: NGUYỄN VĂN TÌNH\nGiới tính: Nam" {
		t.Errorf("paragraph = %q", rs[5].Text)
	}
	for i := 1; i < len(rs); i++ {
		if rs[i].Class != "figure" && rs[i-1].Class != "figure" && rs[i].Quad.BBox().Y0 < rs[i-1].Quad.BBox().Y1-0.001 {
			t.Errorf("bands overlap at %d", i)
		}
	}
}

type failingLayout struct{}

func (failingLayout) Name() string { return turboocr.Name }
func (failingLayout) Health(context.Context) error {
	return errors.New("turboocr: connection refused")
}
func (failingLayout) ParsePage(context.Context, parser.PageImage, parser.PageOptions) (*parser.RawPage, error) {
	return nil, errors.New("turboocr: request: dial tcp: i/o timeout")
}

type pageVLM struct{ calls atomic.Int32 }

func (p *pageVLM) Model() string                { return "fake-olmocr" }
func (p *pageVLM) Health(context.Context) error { return nil }
func (p *pageVLM) Transcribe(context.Context, []byte) (*Transcription, error) {
	p.calls.Add(1)
	meta, text := SplitFrontMatter(fullPageMD)
	return &Transcription{Text: text, Meta: meta}, nil
}

func TestEngineFullPageWhenLayoutUnavailable(t *testing.T) {
	pv := &pageVLM{}
	e, _ := New(Config{Layout: failingLayout{}, Client: pv, FullPage: true, MaxSide: 1288})
	if err := e.Health(context.Background()); err != nil {
		t.Fatalf("health with full_page should tolerate the layout engine: %v", err)
	}
	img := pageJPEG(t, 2000, 3000)
	for _, refine := range []bool{true, false} {
		raw, err := e.ParsePage(context.Background(), parser.PageImage{PageNo: 1, Body: bytes.NewReader(img), Width: 2000, Height: 3000},
			parser.PageOptions{Refine: refine})
		if err != nil {
			t.Fatal(err)
		}
		var env rawEnvelope
		if err := json.Unmarshal(raw.Raw, &env); err != nil || env.Mode != "full_page" || !strings.Contains(env.LayoutError, "i/o timeout") {
			t.Fatalf("envelope %+v %v", env, err)
		}
		page := assemble.Build(raw, assemble.Options{PageNo: 1, Engine: Name, ReadingOrderFix: true,
			AssetKey: func(pg, b int) string { return "fig.jpg" }})
		assemble.Render(page)
		for _, want := range []string{"# GIẤY CHỨNG NHẬN", "Mã số hộ kinh doanh: 070082001498", "## 3. Ngành", "| 1 | Gia công cơ khí |", "![figure", "Giới tính: Nam"} {
			if !strings.Contains(page.Markdown, want) {
				t.Errorf("markdown lacks %q:\n%s", want, page.Markdown)
			}
		}
		runes := []rune(page.Markdown)
		located := 0
		for _, l := range page.Lines {
			if l.MdStart >= 0 {
				located++
				if string(runes[l.MdStart:l.MdEnd]) != l.Text {
					t.Errorf("line offsets wrong: %q", l.Text)
				}
			}
		}
		if located < 5 {
			t.Errorf("only %d synthetic lines located", located)
		}
	}
	e2, _ := New(Config{Layout: failingLayout{}, Client: pv, FullPage: false})
	if _, err := e2.ParsePage(context.Background(), parser.PageImage{Body: bytes.NewReader(img)}, parser.PageOptions{Refine: true}); err == nil {
		t.Fatal("without full_page a layout failure must fail the page")
	}
}

func TestSplitMarkdownPlainHeadings(t *testing.T) {
	md := "HỢP ĐỒNG THUÊ MẶT BẰNG KINH DOANH\nSố: 15/2026/HĐTMB\n\nĐiều 1. Đối tượng hợp đồng\nBên A cho Bên B thuê mặt bằng.\n\nĐiều 2. Giá thuê\n\n| Khoản mục | Số tiền |\n|---|---|\n| Tiền thuê | 12.000.000 |"
	rs := SplitMarkdown(md, 2480, 3508, 1)
	var got []string
	for _, r := range rs {
		got = append(got, r.Class+":"+strings.SplitN(r.Text, "\n", 2)[0])
	}
	want := "doc_title:HỢP ĐỒNG THUÊ MẶT BẰNG KINH DOANH,text:Số: 15/2026/HĐTMB,paragraph_title:Điều 1. Đối tượng hợp đồng," +
		"text:Bên A cho Bên B thuê mặt bằng.,paragraph_title:Điều 2. Giá thuê,table:| Khoản mục | Số tiền |"
	if strings.Join(got, ",") != want {
		t.Fatalf("got  %s\nwant %s", strings.Join(got, ","), want)
	}
	page := assemble.Build(&parser.RawPage{Width: 2480, Height: 3508, Regions: rs}, assemble.Options{PageNo: 2})
	assemble.Render(page)
	runes := []rune(page.Markdown)
	found := false
	for _, l := range page.Lines {
		if l.Text == "Tiền thuê | 12.000.000" {
			found = l.MdStart >= 0 && string(runes[l.MdStart:l.MdEnd]) == l.Text
		}
	}
	if !found || !strings.Contains(page.Markdown, "# HỢP ĐỒNG") || !strings.Contains(page.Markdown, "## Điều 1.") {
		t.Fatalf("table row line not located or headings missing:\n%s", page.Markdown)
	}
}
