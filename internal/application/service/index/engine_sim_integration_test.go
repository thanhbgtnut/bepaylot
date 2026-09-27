package index_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"

	"github.com/thanhenti/bepaylot/internal/config"
	"github.com/thanhenti/bepaylot/internal/parser"
	"github.com/thanhenti/bepaylot/internal/parser/pdf/pdftest"
	"github.com/thanhenti/bepaylot/internal/parser/turboocr"
	"github.com/thanhenti/bepaylot/internal/parser/vlm"
	"github.com/thanhenti/bepaylot/internal/testkit"
	"github.com/thanhenti/bepaylot/internal/textutil"
	"github.com/thanhenti/bepaylot/internal/tools"
	"github.com/thanhenti/bepaylot/internal/types"
)

// A scanned 20-page land-transfer bundle (ID card, certificate, contract,
// tax return, hand-over record, residence record, checklist, blank page) is
// run through turboocr (lines with pixel coordinates) and turboocr_vlm (the
// same layout, region text from a VLM). The page tools then find the right
// pages by page range without any index-time typing.

type simBlock struct {
	class string
	lines []string // clean text, one entry per printed line
	html  string   // tables: the HTML TurboOCR and the VLM return
}

type simPage []simBlock

var simBundle = []simPage{
	1: {{class: "doc_title", lines: []string{"CĂN CƯỚC CÔNG DÂN"}},
		{class: "text", lines: []string{"Số định danh cá nhân: 001099012345", "Họ và tên: NGUYỄN VĂN AN", "Ngày sinh: 12/03/1985 Giới tính: Nam",
			"Quê quán: Xã Nha Bích, Chơn Thành, Bình Phước", "Nơi thường trú: 25 Lê Lợi, Bến Thành, Quận 1, TP Hồ Chí Minh"}}},
	2: {{class: "text", lines: []string{"Đặc điểm nhận dạng: Nốt ruồi cách 2cm dưới mép trái", "Ngày cấp: 10/08/2021 Có giá trị đến: 12/03/2045"}},
		{class: "text", lines: []string{"CỤC TRƯỞNG CỤC CẢNH SÁT QUẢN LÝ HÀNH CHÍNH VỀ TRẬT TỰ XÃ HỘI"}}},
	3: {{class: "doc_title", lines: []string{"GIẤY CHỨNG NHẬN QUYỀN SỬ DỤNG ĐẤT", "QUYỀN SỞ HỮU NHÀ Ở VÀ TÀI SẢN KHÁC GẮN LIỀN VỚI ĐẤT"}},
		{class: "text", lines: []string{"Số vào sổ cấp giấy chứng nhận: CS 04567", "I. Người sử dụng đất: Ông NGUYỄN VĂN AN", "CCCD số 001099012345 cấp ngày 10/08/2021"}}},
	4: {{class: "paragraph_title", lines: []string{"II. Thửa đất, nhà ở và tài sản khác gắn liền với đất"}},
		{class: "text", lines: []string{"Thửa đất số: 215 Tờ bản đồ số: 12", "Địa chỉ: Ấp 3, xã Nha Bích, huyện Chơn Thành", "Diện tích: 1.250 m2 Hình thức sử dụng: riêng",
			"Mục đích sử dụng: Đất trồng cây lâu năm", "Thời hạn sử dụng: đến ngày 15/10/2064"}}},
	5: {{class: "paragraph_title", lines: []string{"III. Sơ đồ thửa đất"}},
		{class: "table", lines: []string{"Cạnh Chiều dài", "1-2 25,00 m", "2-3 50,00 m", "3-4 25,00 m", "4-1 50,00 m"},
			html: "<table><tr><td>Cạnh</td><td>Chiều dài</td></tr><tr><td>1-2</td><td>25,00 m</td></tr><tr><td>2-3</td><td>50,00 m</td></tr><tr><td>3-4</td><td>25,00 m</td></tr><tr><td>4-1</td><td>50,00 m</td></tr></table>"}},
	6: {{class: "paragraph_title", lines: []string{"IV. Những thay đổi sau khi cấp giấy chứng nhận"}},
		{class: "text", lines: []string{"Chuyển nhượng cho bà TRẦN THỊ BÌNH", "theo hồ sơ số 0456/CN ngày 20/05/2024", "Thửa đất số 215 không thay đổi ranh giới"}}},
	7: {{class: "doc_title", lines: []string{"HỢP ĐỒNG CHUYỂN NHƯỢNG QUYỀN SỬ DỤNG ĐẤT"}},
		{class: "text", lines: []string{"Số công chứng: 1234/2024/CCGD", "Bên chuyển nhượng (Bên A): Ông NGUYỄN VĂN AN", "Bên nhận chuyển nhượng (Bên B): Bà TRẦN THỊ BÌNH"}}},
	8: {{class: "paragraph_title", lines: []string{"Điều 1. Đối tượng của hợp đồng"}},
		{class: "text", lines: []string{"Quyền sử dụng thửa đất số 215, tờ bản đồ số 12", "diện tích 1.250 m2 tại xã Nha Bích"}}},
	9: {{class: "paragraph_title", lines: []string{"Điều 2. Giá chuyển nhượng và phương thức thanh toán"}},
		{class: "text", lines: []string{"Giá chuyển nhượng: 1.800.000.000 đồng", "(Bằng chữ: Một tỷ tám trăm triệu đồng)", "Thanh toán bằng chuyển khoản trong 05 ngày làm việc"}}},
	10: {{class: "paragraph_title", lines: []string{"Điều 3. Thời hạn và phương thức giao đất"}},
		{class: "text", lines: []string{"Bên A giao đất cho Bên B trước ngày 01/06/2024", "kèm bản gốc giấy chứng nhận CS 04567"}}},
	11: {{class: "paragraph_title", lines: []string{"Điều 4. Quyền và nghĩa vụ của các bên"}},
		{class: "text", lines: []string{"Bên B nộp thuế thu nhập cá nhân thay Bên A", "Lệ phí trước bạ do Bên B chịu"}}},
	12: {{class: "paragraph_title", lines: []string{"Điều 5. Cam đoan của các bên"}},
		{class: "text", lines: []string{"Các bên đã đọc lại hợp đồng và ký tên dưới đây", "BÊN A BÊN B"}}},
	13: {{class: "doc_title", lines: []string{"TỜ KHAI THUẾ THU NHẬP CÁ NHÂN"}},
		{class: "text", lines: []string{"Mẫu số 03/BĐS-TNCN", "Người nộp thuế: NGUYỄN VĂN AN", "Thu nhập chịu thuế: 1.800.000.000 đồng"}}},
	14: {{class: "text", lines: []string{"Thuế suất: 2%", "Thuế thu nhập cá nhân phải nộp: 36.000.000 đồng", "Ngày khai: 22/05/2024"}}},
	15: {{class: "doc_title", lines: []string{"BIÊN BẢN BÀN GIAO ĐẤT"}},
		{class: "text", lines: []string{"Hôm nay, ngày 01/06/2024 tại thửa đất số 215", "Bên A: NGUYỄN VĂN AN Bên B: TRẦN THỊ BÌNH"}}},
	16: {{class: "text", lines: []string{"Bên B đã nhận đủ diện tích 1.250 m2", "mốc ranh giới đầy đủ, không tranh chấp"}}},
	17: {{class: "doc_title", lines: []string{"XÁC NHẬN THÔNG TIN VỀ CƯ TRÚ"}},
		{class: "text", lines: []string{"Họ và tên: TRẦN THỊ BÌNH", "Số định danh cá nhân: 079188004321"}}},
	18: {{class: "text", lines: []string{"Nơi thường trú: 8 Nguyễn Huệ, Bến Nghé, Quận 1", "Quan hệ với chủ hộ: Chủ hộ"}}},
	19: {{class: "doc_title", lines: []string{"DANH MỤC HỒ SƠ KÈM THEO"}},
		{class: "table", lines: []string{"STT Tên giấy tờ Trang", "1 Căn cước công dân 1-2", "2 Giấy chứng nhận 3-6", "3 Hợp đồng chuyển nhượng 7-12"},
			html: "<table><tr><td>STT</td><td>Tên giấy tờ</td><td>Trang</td></tr><tr><td>1</td><td>Căn cước công dân</td><td>1-2</td></tr><tr><td>2</td><td>Giấy chứng nhận</td><td>3-6</td></tr><tr><td>3</td><td>Hợp đồng chuyển nhượng</td><td>7-12</td></tr></table>"}},
	20: {}, // blank scan
}

// ocrNoise mimics OCR on a scan: every third word loses its diacritics.
func ocrNoise(s string) string {
	w := strings.Fields(s)
	for i := range w {
		if i%3 == 1 {
			w[i] = textutil.Unaccent(w[i])
		}
	}
	return strings.Join(w, " ")
}

// tableLine marks fixture lines (page|text) that sit in a table region.
var tableLine map[string]bool

type simLine struct {
	ID          int          `json:"id"`
	Text        string       `json:"text"`
	Confidence  float64      `json:"confidence"`
	BoundingBox [][2]float64 `json:"bounding_box"`
	LayoutID    int          `json:"layout_id"`
}

type simRegion struct {
	ID          int          `json:"id"`
	Class       string       `json:"class"`
	Confidence  float64      `json:"confidence"`
	BoundingBox [][2]float64 `json:"bounding_box"`
}

type simTable struct {
	LayoutID int    `json:"layout_id"`
	HTML     string `json:"html"`
}

type simOCR struct {
	Results      []simLine   `json:"results"`
	Layout       []simRegion `json:"layout"`
	ReadingOrder []int       `json:"reading_order"`
	Tables       []simTable  `json:"tables"`
}

func box(x0, y0, x1, y1 float64) [][2]float64 {
	return [][2]float64{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}}
}

// simFixtures lays every page out in pixels (72 dpi: 612x792) and returns
// the /ocr/raw body per page, the VLM text per (page, crop size) and the
// expected bbox of every line keyed by page and clean text.
func simFixtures(t *testing.T) (map[int][]byte, map[string]string, map[string]types.BBox) {
	tableLine = map[string]bool{}
	ocr := map[int][]byte{}
	vlmText := map[string]string{}
	bboxes := map[string]types.BBox{}
	for pg := 1; pg < len(simBundle); pg++ {
		var out simOCR
		out.Results, out.Layout, out.ReadingOrder, out.Tables = []simLine{}, []simRegion{}, []int{}, []simTable{}
		y := 60.0
		for ri, b := range simBundle[pg] {
			longest := 0
			for _, l := range b.lines {
				longest = max(longest, len([]rune(l)))
			}
			x0, x1 := 72.0, 72.0+float64(min(460, 8*longest)+ri) // +ri keeps crop sizes unique on a page
			top := y
			for _, l := range b.lines {
				lx1 := 72.0 + float64(min(460, 8*len([]rune(l))))
				out.Results = append(out.Results, simLine{ID: len(out.Results), Text: ocrNoise(l), Confidence: 0.93,
					BoundingBox: box(72, y, lx1, y+18), LayoutID: ri})
				out.ReadingOrder = append(out.ReadingOrder, len(out.Results)-1)
				bboxes[fmt.Sprintf("%d|%s", pg, l)] = types.BBox{X0: 72, Y0: y, X1: lx1, Y1: y + 18}
				tableLine[fmt.Sprintf("%d|%s", pg, l)] = b.html != ""
				y += 28
			}
			bottom := y - 10
			out.Layout = append(out.Layout, simRegion{ID: ri, Class: b.class, Confidence: 0.9, BoundingBox: box(x0, top, x1, bottom)})
			text := strings.Join(b.lines, "\n")
			if b.html != "" {
				out.Tables = append(out.Tables, simTable{LayoutID: ri, HTML: b.html})
				text = b.html
			}
			key := fmt.Sprintf("%d|%dx%d", pg, int(x1-x0), int(bottom-top))
			if _, dup := vlmText[key]; dup {
				t.Fatalf("crop size %s is not unique", key)
			}
			vlmText[key] = text
			y += 24
		}
		body, _ := json.Marshal(out)
		ocr[pg] = body
	}
	return ocr, vlmText, bboxes
}

type pageKey struct{}

func pageOf(ctx context.Context) int { n, _ := ctx.Value(pageKey{}).(int); return n }

// pageCtx passes the page number down to the fake OCR service and VLM, which
// only see the image bytes.
type pageCtx struct{ parser.Engine }

func (e pageCtx) ParsePage(ctx context.Context, in parser.PageImage, opt parser.PageOptions) (*parser.RawPage, error) {
	return e.Engine.ParsePage(context.WithValue(ctx, pageKey{}, in.PageNo), in, opt)
}

// ocrService answers POST /ocr/raw with the page's fixture.
type ocrService struct {
	pages map[int][]byte
	query map[int]string
}

func (s *ocrService) RoundTrip(r *http.Request) (*http.Response, error) {
	io.Copy(io.Discard, r.Body)
	pg := pageOf(r.Context())
	s.query[pg] = r.URL.RawQuery
	body := s.pages[pg]
	if r.URL.Path != "/ocr/raw" || body == nil {
		return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("no fixture")), Request: r}, nil
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(body)), Request: r}, nil
}

// simVLM transcribes a crop by looking up its page and pixel size.
type simVLM struct {
	text  map[string]string
	calls map[int]int
}

func (v *simVLM) Transcribe(ctx context.Context, jpg []byte) (*vlm.Transcription, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(jpg))
	if err != nil {
		return nil, err
	}
	pg := pageOf(ctx)
	v.calls[pg]++
	return &vlm.Transcription{Text: v.text[fmt.Sprintf("%d|%dx%d", pg, cfg.Width, cfg.Height)]}, nil
}
func (v *simVLM) Health(context.Context) error { return nil }
func (v *simVLM) Model() string                { return "sim/olmocr" }

func TestSimulatedScan20PagesBothEngines(t *testing.T) {
	ocrPages, vlmText, bboxes := simFixtures(t)
	svc := &ocrService{pages: ocrPages, query: map[int]string{}}
	turbo := turboocr.New(turboocr.Config{BaseURL: "http://turboocr.sim", Timeout: 10 * time.Second, HTTPClient: &http.Client{Transport: svc}})
	fakeVLM := &simVLM{text: vlmText, calls: map[int]int{}}
	refining, err := vlm.New(vlm.Config{Layout: turbo, Client: fakeVLM, MaxConcurrency: 1, FullPage: true})
	if err != nil {
		t.Fatal(err)
	}
	h := testkit.NewWithEngines(t, []parser.Engine{pageCtx{turbo}, pageCtx{refining}}, func(c *config.Config) {
		c.Parser.TextLayer.Enabled = "off" // a scan: OCR is the only text source
	})

	// The rendered PDF only provides page images; the text comes from OCR.
	blank := make([]pdftest.Page, 20)
	for i := range blank {
		blank[i] = pdftest.Page{" "}
	}
	pdf := pdftest.Build(blank, pdftest.Options{})

	reg, err := tools.NewRegistry(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.SetKnowledgeTools(h.Index, nil); err != nil {
		t.Fatal(err)
	}
	sess := reg.NewSession()
	sess.EnableKnowledge()

	for _, engine := range []string{turboocr.Name, vlm.Name} {
		t.Run(engine, func(t *testing.T) {
			kb := h.KB(types.KBConfig{ParserEngine: engine}, nil)
			doc := h.UploadPDF(kb.ID, "ho-so-"+engine+".pdf", map[string]any{"group_code": "HS-2024-0456"}, pdf)
			h.Drain()
			d, err := h.Store.Documents.Get(h.Ctx, doc)
			if err != nil {
				t.Fatal(err)
			}
			if d.Status != types.DocCompleted || d.PageCount != 20 || d.Engine != engine {
				t.Fatalf("document = status %s pages %d engine %s (%s)", d.Status, d.PageCount, d.Engine, d.Error)
			}
			if q := svc.query[4]; !strings.Contains(q, "layout=1") {
				t.Fatalf("ocr query = %q", q)
			}

			// Every stored line keeps the OCR coordinates; turboocr stores the
			// noisy OCR text, turboocr_vlm the VLM transcription.
			lines, err := h.Store.Pages.Lines(h.Ctx, doc, 1, 20)
			if err != nil {
				t.Fatal(err)
			}
			checked := 0
			for _, l := range lines {
				for key, want := range bboxes {
					pg, clean, _ := strings.Cut(key, "|")
					if pg != fmt.Sprint(l.PageNo) || textutil.Unaccent(l.Text) != textutil.Unaccent(clean) {
						continue
					}
					if l.BBox != want {
						t.Errorf("p%d %q bbox = %+v, want %+v", l.PageNo, l.Text, l.BBox, want)
					}
					wantText := ocrNoise(clean)
					// Table rows keep their OCR text even under the VLM: its
					// HTML only replaces the table's markdown.
					if engine == vlm.Name && !tableLine[key] {
						wantText = clean
					}
					if l.Text != wantText {
						t.Errorf("p%d text = %q, want %q", l.PageNo, l.Text, wantText)
					}
					checked++
				}
			}
			if checked < 50 {
				t.Fatalf("only %d text lines matched the fixture", checked)
			}

			scope := tools.WithCaseScope(h.Ctx, tools.CaseScope{Owner: h.Owner.ID, CaseID: d.CaseID})
			call := func(name, args string) string {
				t.Helper()
				for _, bt := range sess.Executable() {
					if info, _ := bt.Info(scope); info.Name == name {
						out, err := bt.(tool.InvokableTool).InvokableRun(scope, args)
						if err != nil {
							t.Fatalf("%s: %v", name, err)
						}
						t.Logf("── %s %s\n%s", name, args, out)
						return out
					}
				}
				t.Fatalf("tool %s not bound", name)
				return ""
			}
			id := doc.String()

			var ov struct{ Pages []types.PageOverview }
			json.Unmarshal([]byte(call("kb_page_overview", `{"document_id":"`+id+`"}`)), &ov)
			if len(ov.Pages) != 20 || !ov.Pages[19].Blank || !strings.Contains(textutil.Unaccent(ov.Pages[8].Preview), "gia chuyen nhuong") {
				t.Fatalf("overview = %+v", ov.Pages)
			}

			// "215" is on the certificate, the contract and the hand-over
			// record; pages 7-12 narrow it to the contract.
			var all, contract struct{ Pages []types.PageSearchHit }
			json.Unmarshal([]byte(call("kb_find_in_document", `{"document_id":"`+id+`","query":"215"}`)), &all)
			json.Unmarshal([]byte(call("kb_find_in_document", `{"document_id":"`+id+`","query":"215","page_from":7,"page_to":12}`)), &contract)
			if len(all.Pages) < 4 || len(contract.Pages) != 1 || contract.Pages[0].PageNo != 8 {
				t.Fatalf("find 215: all=%+v contract=%+v", all.Pages, contract.Pages)
			}
			if b := contract.Pages[0].Hits[0].BBox; b.X0 != 72 || b.Width() <= 0 {
				t.Fatalf("hit bbox = %+v", b)
			}

			read := call("kb_read_pages", `{"document_id":"`+id+`","page_from":9,"page_to":9}`)
			if !strings.Contains(textutil.Unaccent(read), "1.800.000.000") {
				t.Fatalf("read page 9 = %s", read)
			}
			var hits struct{ Hits []struct{ Page int } }
			json.Unmarshal([]byte(call("kb_search", `{"query":"1.800.000.000","mode":"keyword","document_ids":["`+id+`"],"page_from":13,"page_to":14}`)), &hits)
			if len(hits.Hits) == 0 {
				t.Fatal("kb_search found nothing on the tax return pages")
			}
			for _, x := range hits.Hits {
				if x.Page < 13 || x.Page > 14 {
					t.Fatalf("kb_search hit outside 13-14: %+v", hits.Hits)
				}
			}
			if engine == vlm.Name && fakeVLM.calls[4] == 0 {
				t.Fatal("the VLM was not called")
			}
		})
	}
}
