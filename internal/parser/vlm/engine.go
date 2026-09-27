package vlm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // page images are JPEG; PNG is accepted for tests and tools
	"io"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/draw"

	"github.com/thanhenti/bepaylot/internal/parser"
	"github.com/thanhenti/bepaylot/internal/types"
)

// Name is the engine name used in config, the registry and documents.engine.
const Name = "turboocr_vlm"

// DefaultClasses are the layout classes sent to the VLM. Figures, seals and
// page numbers are left to OCR.
var DefaultClasses = []string{
	"doc_title", "paragraph_title", "text", "abstract", "content", "reference", "aside_text", "algorithm",
	"table", "formula", "figure_title", "table_title", "chart_title", "header", "footer", "footnote",
}

// Error policies when a region cannot be transcribed.
const (
	OnErrorFallback = "fallback" // keep the OCR text of that region
	OnErrorFail     = "fail"     // fail the page (the task retries it)
)

// Transcriber reads one cropped image; *Client is the production one.
type Transcriber interface {
	Transcribe(ctx context.Context, jpeg []byte) (*Transcription, error)
	Health(ctx context.Context) error
	Model() string
}

// Config configures the engine.
type Config struct {
	// Layout is the engine that finds regions and lines (TurboOCR).
	Layout parser.Engine
	Client Transcriber
	// MaxConcurrency bounds in-flight VLM requests across all pages of this
	// process; the per-page fan-out waits on it.
	MaxConcurrency int
	Classes        []string
	// Padding in pixels added around each region before cropping.
	Padding int
	// MaxSide downscales crops whose longer side exceeds it (olmOCR is
	// trained on 1288 px pages); 0 keeps the crop size.
	MaxSide     int
	MinSide     int // regions thinner than this are skipped
	JPEGQuality int
	Retries     int
	OnError     string
	// FullPage sends the whole page as one region when the layout engine
	// returns no usable region.
	FullPage bool
	Log      *slog.Logger
}

// Engine implements parser.Engine: layout + OCR from Config.Layout, region
// text from the VLM.
type Engine struct {
	cfg     Config
	classes map[string]bool
	sem     chan struct{}
}

// New returns the engine.
func New(cfg Config) (*Engine, error) {
	if cfg.Layout == nil || cfg.Client == nil {
		return nil, errors.New("vlm: layout engine and client are required")
	}
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = 4
	}
	if len(cfg.Classes) == 0 {
		cfg.Classes = DefaultClasses
	}
	if cfg.Padding < 0 {
		cfg.Padding = 0
	}
	if cfg.MinSide <= 0 {
		cfg.MinSide = 8
	}
	if cfg.JPEGQuality <= 0 {
		cfg.JPEGQuality = 90
	}
	if cfg.OnError == "" {
		cfg.OnError = OnErrorFallback
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	e := &Engine{cfg: cfg, classes: map[string]bool{}, sem: make(chan struct{}, cfg.MaxConcurrency)}
	for _, c := range cfg.Classes {
		e.classes[strings.ToLower(strings.TrimSpace(c))] = true
	}
	return e, nil
}

// Name implements parser.Engine.
func (e *Engine) Name() string { return Name }

// Health requires both the layout engine and the VLM.
func (e *Engine) Health(ctx context.Context) error {
	// With full_page the engine still works without the layout engine
	// (degraded: whole pages go to the VLM), so only the VLM is required.
	if err := e.cfg.Layout.Health(ctx); err != nil && !e.cfg.FullPage {
		return err
	}
	return e.cfg.Client.Health(ctx)
}

// RegionResult records one region's VLM call (kept in RawPage.Raw).
type RegionResult struct {
	LayoutID         int               `json:"layout_id"`
	Class            string            `json:"class"`
	BBox             [4]int            `json:"bbox"`
	Ms               int               `json:"ms"`
	Text             string            `json:"text,omitempty"`
	Meta             map[string]string `json:"meta,omitempty"`
	PromptTokens     int               `json:"prompt_tokens,omitempty"`
	CompletionTokens int               `json:"completion_tokens,omitempty"`
	Truncated        bool              `json:"truncated,omitempty"`
	Error            string            `json:"error,omitempty"`
}

// rawEnvelope is what RawPage.Raw holds for this engine.
type rawEnvelope struct {
	Engine string `json:"engine"`
	Model  string `json:"model"`
	// Mode is "regions" (layout + one VLM call per region) or "full_page"
	// (no layout: the whole page went to the VLM).
	Mode        string          `json:"mode"`
	LayoutError string          `json:"layout_error,omitempty"`
	Layout      json.RawMessage `json:"layout,omitempty"`
	Regions     []RegionResult  `json:"regions"`
	Ms          int             `json:"ms"`
}

// ParsePage runs the layout engine, then transcribes the selected regions
// concurrently and attaches the text to them.
//
// With FullPage, a page the layout engine cannot handle (error, circuit open,
// or nothing found) is sent whole to the VLM and its markdown is split into
// blocks (fullPage). This also applies when Refine is off: a page the layout
// engine fails on has no other text source besides the VLM.
func (e *Engine) ParsePage(ctx context.Context, in parser.PageImage, opt parser.PageOptions) (*parser.RawPage, error) {
	img, err := io.ReadAll(io.LimitReader(in.Body, 256<<20))
	if err != nil {
		return nil, fmt.Errorf("vlm: read page image: %w", err)
	}
	if opt.Refine {
		opt.Layout = true // regions are the point
	}
	in.Body, in.Size = bytes.NewReader(img), int64(len(img))
	page, err := e.cfg.Layout.ParsePage(ctx, in, opt)
	if err != nil {
		if !e.cfg.FullPage || ctx.Err() != nil {
			return nil, err
		}
		e.cfg.Log.Warn("vlm: layout engine failed, sending the whole page to the VLM", "page", in.PageNo, "err", err)
		return e.fullPage(ctx, img, in, err.Error())
	}
	if !opt.Refine {
		return page, nil
	}
	if len(page.Regions) == 0 && len(page.Lines) == 0 && e.cfg.FullPage {
		return e.fullPage(ctx, img, in, "layout engine found no region and no line")
	}
	targets := e.selectRegions(page)
	if len(targets) == 0 {
		return page, nil
	}
	src, _, err := image.Decode(bytes.NewReader(img))
	if err != nil {
		return nil, fmt.Errorf("vlm: decode page image: %w", err)
	}

	t0 := time.Now()
	results := make([]RegionResult, len(targets))
	var wg sync.WaitGroup
	for i, ri := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = e.transcribe(ctx, src, page.Regions[ri])
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	failed := 0
	for i, ri := range targets {
		r := results[i]
		if r.Error != "" {
			failed++
			continue
		}
		apply(&page.Regions[ri], r.Text)
	}
	env := rawEnvelope{Engine: Name, Model: e.cfg.Client.Model(), Mode: "regions", Layout: json.RawMessage(page.Raw), Regions: results,
		Ms: int(time.Since(t0).Milliseconds())}
	if !json.Valid(page.Raw) {
		env.Layout = nil
	}
	if b, err := json.Marshal(env); err == nil {
		page.Raw = b
	}
	if failed > 0 {
		e.cfg.Log.Warn("vlm: regions failed", "page", in.PageNo, "failed", failed, "total", len(targets), "first_err", firstErr(results))
		if e.cfg.OnError == OnErrorFail {
			return nil, fmt.Errorf("vlm: %d/%d regions failed: %s", failed, len(targets), firstErr(results))
		}
	}
	return page, nil
}

// selectRegions returns indexes of regions to transcribe. With FullPage and
// no usable region, it adds one region spanning all OCR lines.
//
// A region without OCR lines of its own that encloses, or lies inside, a
// region with lines is a nesting artifact of the layout model: its text is
// already read with that region, so it is skipped rather than duplicated.
func (e *Engine) selectRegions(page *parser.RawPage) []int {
	hasLines := map[int]bool{}
	for _, l := range page.Lines {
		hasLines[l.LayoutID] = true
	}
	var out []int
	for i, r := range page.Regions {
		b := r.Quad.BBox()
		if !e.classes[strings.ToLower(r.Class)] || b.Width() < float64(e.cfg.MinSide) || b.Height() < float64(e.cfg.MinSide) {
			continue
		}
		if !hasLines[r.ID] && nestedWithRegionWithLines(page.Regions, i, hasLines) {
			continue
		}
		out = append(out, i)
	}
	if len(out) > 0 || !e.cfg.FullPage || len(page.Lines) == 0 {
		return out
	}
	var box types.BBox
	id := 0
	for _, r := range page.Regions {
		id = max(id, r.ID+1)
	}
	for i := range page.Lines {
		box = box.Union(page.Lines[i].Quad.BBox())
	}
	for i := range page.Lines {
		if page.Lines[i].LayoutID < 0 {
			page.Lines[i].LayoutID = id
		}
	}
	page.Regions = append(page.Regions, parser.RawRegion{ID: id, Class: "text", Confidence: 1, Quad: types.QuadFromBBox(box)})
	return []int{len(page.Regions) - 1}
}

// nestedWithRegionWithLines reports whether region i and another region that
// has OCR lines overlap by at least 80% of the smaller one's area.
func nestedWithRegionWithLines(regions []parser.RawRegion, i int, hasLines map[int]bool) bool {
	a := regions[i].Quad.BBox()
	for j, r := range regions {
		if j == i || !hasLines[r.ID] {
			continue
		}
		b := r.Quad.BBox()
		iw := min(a.X1, b.X1) - max(a.X0, b.X0)
		ih := min(a.Y1, b.Y1) - max(a.Y0, b.Y0)
		small := min(a.Width()*a.Height(), b.Width()*b.Height())
		if small > 0 && iw > 0 && ih > 0 && iw*ih >= 0.8*small {
			return true
		}
	}
	return false
}

// transcribe crops one region and calls the VLM, retrying transient errors.
func (e *Engine) transcribe(ctx context.Context, src image.Image, r parser.RawRegion) RegionResult {
	return e.transcribeWith(ctx, src, r, e.cfg.Padding)
}

func (e *Engine) transcribeWith(ctx context.Context, src image.Image, r parser.RawRegion, padding int) RegionResult {
	b := r.Quad.BBox()
	pad := float64(padding)
	rect := image.Rect(int(b.X0-pad), int(b.Y0-pad), int(b.X1+pad+0.5), int(b.Y1+pad+0.5)).Intersect(src.Bounds())
	res := RegionResult{LayoutID: r.ID, Class: r.Class, BBox: [4]int{rect.Min.X, rect.Min.Y, rect.Max.X, rect.Max.Y}}
	if rect.Empty() {
		res.Error = "empty crop"
		return res
	}
	crop, err := e.encodeCrop(src, rect)
	if err != nil {
		res.Error = err.Error()
		return res
	}

	select {
	case e.sem <- struct{}{}:
	case <-ctx.Done():
		res.Error = ctx.Err().Error()
		return res
	}
	defer func() { <-e.sem }()

	t0 := time.Now()
	var tr *Transcription
	for attempt := 0; ; attempt++ {
		tr, err = e.cfg.Client.Transcribe(ctx, crop)
		if err == nil || attempt >= e.cfg.Retries || !Retryable(err) || ctx.Err() != nil {
			break
		}
		select {
		case <-time.After(time.Duration(attempt+1) * time.Second):
		case <-ctx.Done():
		}
	}
	res.Ms = int(time.Since(t0).Milliseconds())
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Text, res.Meta, res.Truncated = tr.Text, tr.Meta, tr.Truncated
	res.PromptTokens, res.CompletionTokens = tr.PromptTokens, tr.CompletionTokens
	return res
}

type subImager interface {
	SubImage(r image.Rectangle) image.Image
}

func (e *Engine) encodeCrop(src image.Image, rect image.Rectangle) ([]byte, error) {
	var crop image.Image
	if si, ok := src.(subImager); ok {
		crop = si.SubImage(rect)
	} else {
		dst := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
		draw.Draw(dst, dst.Bounds(), src, rect.Min, draw.Src)
		crop = dst
	}
	if long := max(rect.Dx(), rect.Dy()); e.cfg.MaxSide > 0 && long > e.cfg.MaxSide {
		f := float64(e.cfg.MaxSide) / float64(long)
		w, h := max(1, int(float64(rect.Dx())*f+0.5)), max(1, int(float64(rect.Dy())*f+0.5))
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.CatmullRom.Scale(dst, dst.Bounds(), crop, crop.Bounds(), draw.Src, nil)
		crop = dst
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, crop, &jpeg.Options{Quality: e.cfg.JPEGQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

var (
	reTable   = regexp.MustCompile(`(?is)<table\b.*</table>`)
	reMathDel = regexp.MustCompile(`(?s)^\s*(?:\$\$|\\\[|\\\(|\$)\s*(.*?)\s*(?:\$\$|\\\]|\\\)|\$)\s*$`)
)

// apply stores a transcription on its region by kind: HTML tables replace the
// layout engine's table HTML, formulas become LaTeX, the rest is region text.
func apply(r *parser.RawRegion, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	switch strings.ToLower(r.Class) {
	case "table":
		if t := reTable.FindString(text); t != "" {
			r.HTML = t
			return
		}
		r.Text = text
	case "formula":
		if m := reMathDel.FindStringSubmatch(text); m != nil {
			text = m[1]
		}
		r.LaTeX = text
	default:
		r.Text = text
	}
}

func firstErr(rs []RegionResult) string {
	for _, r := range rs {
		if r.Error != "" {
			return r.Error
		}
	}
	return ""
}
