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
	"sort"
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

// DefaultGroups batch similar classes: the regions of one page whose classes
// share a group are read in one call. Classes outside every group (titles,
// tables) are read one region per call.
var DefaultGroups = map[string][]string{
	"text":      {"text", "abstract", "content", "reference", "aside_text", "algorithm"},
	"caption":   {"figure_title", "table_title", "chart_title"},
	"furniture": {"header", "footer", "footnote"},
	"formula":   {"formula"},
}

// DefaultTagClasses are tagged from the layout alone, without a VLM call.
var DefaultTagClasses = []string{"seal"}

// Error policies when a region cannot be transcribed.
const (
	OnErrorFallback = "fallback" // keep the OCR text of that region
	OnErrorFail     = "fail"     // fail the page (the task retries it)
)

// Config configures the engine.
type Config struct {
	// Layout is the engine that finds regions and lines (TurboOCR).
	Layout parser.Engine
	Client Transcriber
	// MaxConcurrency bounds in-flight VLM calls across all pages of this
	// process; the per-page fan-out waits on it.
	MaxConcurrency int
	Classes        []string
	// Groups maps a group name to the classes it batches (DefaultGroups when
	// nil; an empty map turns batching off).
	Groups     map[string][]string
	TagClasses []string
	// Prompt reads one region, BatchPrompt a stitched batch (with %d).
	Prompt      string
	BatchPrompt string
	// BatchMaxRegions and BatchMaxHeight (px of the stitched image before
	// downscaling) cut a group into several calls.
	BatchMaxRegions int
	BatchMaxHeight  int
	// Padding in pixels added around each region before cropping.
	Padding int
	// MaxSide downscales images whose longer side exceeds it (olmOCR is
	// trained on 1288 px pages); 0 keeps the size.
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
	groupOf map[string]string
	tagged  map[string]bool
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
	if cfg.Groups == nil {
		cfg.Groups = DefaultGroups
	}
	if cfg.TagClasses == nil {
		cfg.TagClasses = DefaultTagClasses
	}
	if cfg.Prompt == "" {
		cfg.Prompt = DefaultPrompt
	}
	if cfg.BatchPrompt == "" {
		cfg.BatchPrompt = DefaultBatchPrompt
	}
	if cfg.BatchMaxRegions <= 0 {
		cfg.BatchMaxRegions = 20
	}
	if cfg.BatchMaxHeight <= 0 {
		cfg.BatchMaxHeight = 2400
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
	e := &Engine{cfg: cfg, classes: map[string]bool{}, groupOf: map[string]string{}, tagged: map[string]bool{},
		sem: make(chan struct{}, cfg.MaxConcurrency)}
	for _, c := range cfg.Classes {
		e.classes[norm(c)] = true
	}
	for g, cs := range cfg.Groups {
		for _, c := range cs {
			e.groupOf[norm(c)] = g
		}
	}
	for _, c := range cfg.TagClasses {
		e.tagged[norm(c)] = true
	}
	return e, nil
}

func norm(c string) string { return strings.ToLower(strings.TrimSpace(c)) }

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

// RegionResult records one region (kept in RawPage.Raw).
type RegionResult struct {
	LayoutID int    `json:"layout_id"`
	Class    string `json:"class"`
	BBox     [4]int `json:"bbox"`
	// Call is the index of the call that read the region in rawEnvelope.Calls
	// (-1 for a tagged region).
	Call      int               `json:"call"`
	Tagged    bool              `json:"tagged,omitempty"`
	Text      string            `json:"text,omitempty"`
	Meta      map[string]string `json:"meta,omitempty"`
	Truncated bool              `json:"truncated,omitempty"`
	Error     string            `json:"error,omitempty"`
}

// CallResult records one VLM call: a batch of one group or a single region.
type CallResult struct {
	Group            string `json:"group,omitempty"` // "" = single region
	Regions          []int  `json:"regions"`
	Ms               int    `json:"ms"`
	PromptTokens     int    `json:"prompt_tokens,omitempty"`
	CompletionTokens int    `json:"completion_tokens,omitempty"`
	Truncated        bool   `json:"truncated,omitempty"`
	Error            string `json:"error,omitempty"`
}

// rawEnvelope is what RawPage.Raw holds for this engine.
type rawEnvelope struct {
	Engine string `json:"engine"`
	Model  string `json:"model"`
	// Mode is "regions" (layout + grouped VLM calls) or "full_page" (no
	// layout: the whole page went to the VLM).
	Mode        string          `json:"mode"`
	LayoutError string          `json:"layout_error,omitempty"`
	Layout      json.RawMessage `json:"layout,omitempty"`
	Regions     []RegionResult  `json:"regions"`
	Calls       []CallResult    `json:"calls"`
	Ms          int             `json:"ms"`
}

// ParsePage runs the layout engine, tags seals, reads the selected regions
// with as few VLM calls as possible (one per group batch, one per title or
// table), and attaches the text to the regions.
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
	var results []RegionResult
	for _, r := range page.Regions {
		if e.tagged[norm(r.Class)] {
			b := r.Quad.BBox()
			results = append(results, RegionResult{LayoutID: r.ID, Class: r.Class, Call: -1, Tagged: true,
				BBox: [4]int{int(b.X0), int(b.Y0), int(b.X1 + 0.5), int(b.Y1 + 0.5)}})
		}
	}
	if len(targets) == 0 {
		e.envelope(page, "regions", "", results, nil, 0)
		return page, nil
	}
	src, _, err := image.Decode(bytes.NewReader(img))
	if err != nil {
		return nil, fmt.Errorf("vlm: decode page image: %w", err)
	}

	t0 := time.Now()
	batches := e.plan(src, page, targets)
	out := make([][]RegionResult, len(batches))
	calls := make([][]CallResult, len(batches))
	var wg sync.WaitGroup
	for i, b := range batches {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i], calls[i] = e.read(ctx, src, page, b)
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	byID := map[int]int{}
	for i, r := range page.Regions {
		byID[r.ID] = i
	}
	var allCalls []CallResult
	failed, total := 0, 0
	for i := range batches {
		for _, r := range out[i] {
			if r.Call >= 0 {
				r.Call += len(allCalls)
			}
			total++
			if r.Error != "" {
				failed++
			} else {
				apply(&page.Regions[byID[r.LayoutID]], r.Text)
			}
			results = append(results, r)
		}
		allCalls = append(allCalls, calls[i]...)
	}
	e.envelope(page, "regions", "", results, allCalls, int(time.Since(t0).Milliseconds()))
	if failed > 0 {
		e.cfg.Log.Warn("vlm: regions failed", "page", in.PageNo, "failed", failed, "total", total, "first_err", firstErr(results))
		if e.cfg.OnError == OnErrorFail {
			return nil, fmt.Errorf("vlm: %d/%d regions failed: %s", failed, total, firstErr(results))
		}
	}
	return page, nil
}

func (e *Engine) envelope(page *parser.RawPage, mode, layoutErr string, regions []RegionResult, calls []CallResult, ms int) {
	env := rawEnvelope{Engine: Name, Model: e.cfg.Client.Model(), Mode: mode, LayoutError: layoutErr,
		Regions: regions, Calls: calls, Ms: ms}
	if json.Valid(page.Raw) {
		env.Layout = json.RawMessage(page.Raw)
	}
	if b, err := json.Marshal(env); err == nil {
		page.Raw = b
	}
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
		if !e.classes[norm(r.Class)] || b.Width() < float64(e.cfg.MinSide) || b.Height() < float64(e.cfg.MinSide) {
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

// batch is one planned call: a group's regions (in top-to-bottom order) or a
// single region (group "").
type batch struct {
	group   string
	regions []int // indexes into page.Regions
}

// plan groups the targets: regions of a grouped class are batched per group
// in reading position, cut by BatchMaxRegions and BatchMaxHeight; every
// other region is a batch of its own.
func (e *Engine) plan(src image.Image, page *parser.RawPage, targets []int) []batch {
	var out []batch
	byGroup := map[string][]int{}
	var groups []string
	for _, ri := range targets {
		g := e.groupOf[norm(page.Regions[ri].Class)]
		if g == "" {
			out = append(out, batch{regions: []int{ri}})
			continue
		}
		if _, ok := byGroup[g]; !ok {
			groups = append(groups, g)
		}
		byGroup[g] = append(byGroup[g], ri)
	}
	for _, g := range groups {
		rs := byGroup[g]
		sort.SliceStable(rs, func(a, b int) bool {
			ba, bb := page.Regions[rs[a]].Quad.BBox(), page.Regions[rs[b]].Quad.BBox()
			if ba.Y0 != bb.Y0 {
				return ba.Y0 < bb.Y0
			}
			return ba.X0 < bb.X0
		})
		cur := batch{group: g}
		h := 0
		for _, ri := range rs {
			rh := e.cropRect(src, page.Regions[ri]).Dy() + barHeight + gap
			if len(cur.regions) > 0 && (len(cur.regions) >= e.cfg.BatchMaxRegions || h+rh > e.cfg.BatchMaxHeight) {
				out = append(out, cur)
				cur, h = batch{group: g}, 0
			}
			cur.regions = append(cur.regions, ri)
			h += rh
		}
		if len(cur.regions) > 0 {
			out = append(out, cur)
		}
	}
	return out
}

func (e *Engine) cropRect(src image.Image, r parser.RawRegion) image.Rectangle {
	b := r.Quad.BBox()
	pad := float64(e.cfg.Padding)
	return image.Rect(int(b.X0-pad), int(b.Y0-pad), int(b.X1+pad+0.5), int(b.Y1+pad+0.5)).Intersect(src.Bounds())
}

// read performs one planned batch. A group batch is stitched into one image;
// the regions its answer does not mark are read again one by one, so a model
// that ignores the markers costs extra calls, never text.
func (e *Engine) read(ctx context.Context, src image.Image, page *parser.RawPage, b batch) ([]RegionResult, []CallResult) {
	res := make([]RegionResult, len(b.regions))
	var rects []image.Rectangle
	var ids []int
	var keep []int // positions in b.regions with a usable crop
	for i, ri := range b.regions {
		r := page.Regions[ri]
		rect := e.cropRect(src, r)
		res[i] = RegionResult{LayoutID: r.ID, Class: r.Class, Call: -1, BBox: [4]int{rect.Min.X, rect.Min.Y, rect.Max.X, rect.Max.Y}}
		if rect.Empty() {
			res[i].Error = "empty crop"
			continue
		}
		rects, ids, keep = append(rects, rect), append(ids, r.ID), append(keep, i)
	}
	if len(keep) == 0 {
		return res, nil
	}
	if b.group == "" || len(keep) == 1 {
		var calls []CallResult
		for k, i := range keep {
			tr, cr := e.call(ctx, b.group, ids[k:k+1], e.cfg.Prompt, crop(src, rects[k]))
			res[i].Call = len(calls)
			calls = append(calls, cr)
			fill(&res[i], tr, cr.Error)
		}
		return res, calls
	}

	tr, cr := e.call(ctx, b.group, ids, fmt.Sprintf(e.cfg.BatchPrompt, len(keep)), stitch(src, rects))
	calls := []CallResult{cr}
	var parts map[int]string
	if cr.Error == "" {
		parts = splitBatch(tr.Text)
	}
	for k, i := range keep {
		text, ok := parts[k+1]
		if ok {
			res[i].Call = 0
			_, text = SplitFrontMatter(text)
			res[i].Text, res[i].Truncated = text, tr.Truncated
			continue
		}
		if cr.Error != "" {
			// The call itself failed (already retried): every region of the
			// batch follows on_error rather than multiplying failing calls.
			res[i].Error = cr.Error
			continue
		}
		// Missing from the batch answer: read this region on its own.
		one, ocr := e.call(ctx, "", ids[k:k+1], e.cfg.Prompt, crop(src, rects[k]))
		res[i].Call = len(calls)
		calls = append(calls, ocr)
		fill(&res[i], one, ocr.Error)
	}
	return res, calls
}

func fill(r *RegionResult, tr *Transcription, errMsg string) {
	if errMsg != "" {
		r.Error = errMsg
		return
	}
	r.Text, r.Meta, r.Truncated = tr.Text, tr.Meta, tr.Truncated
}

// call sends one image to the Transcriber under the process-wide semaphore,
// retrying transient errors.
func (e *Engine) call(ctx context.Context, group string, ids []int, prompt string, img image.Image) (*Transcription, CallResult) {
	cr := CallResult{Group: group, Regions: ids}
	jpg, err := e.encode(img)
	if err != nil {
		cr.Error = err.Error()
		return nil, cr
	}
	select {
	case e.sem <- struct{}{}:
	case <-ctx.Done():
		cr.Error = ctx.Err().Error()
		return nil, cr
	}
	defer func() { <-e.sem }()

	t0 := time.Now()
	var tr *Transcription
	for attempt := 0; ; attempt++ {
		tr, err = e.cfg.Client.Transcribe(ctx, Request{Prompt: prompt, JPEG: jpg, Regions: ids})
		if err == nil || attempt >= e.cfg.Retries || !Retryable(err) || ctx.Err() != nil {
			break
		}
		select {
		case <-time.After(time.Duration(attempt+1) * time.Second):
		case <-ctx.Done():
		}
	}
	cr.Ms = int(time.Since(t0).Milliseconds())
	if err != nil {
		cr.Error = err.Error()
		return nil, cr
	}
	cr.PromptTokens, cr.CompletionTokens, cr.Truncated = tr.PromptTokens, tr.CompletionTokens, tr.Truncated
	return tr, cr
}

type subImager interface {
	SubImage(r image.Rectangle) image.Image
}

func crop(src image.Image, rect image.Rectangle) image.Image {
	if si, ok := src.(subImager); ok {
		return si.SubImage(rect)
	}
	dst := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	draw.Draw(dst, dst.Bounds(), src, rect.Min, draw.Src)
	return dst
}

// encode downscales to MaxSide and encodes JPEG.
func (e *Engine) encode(img image.Image) ([]byte, error) {
	b := img.Bounds()
	if long := max(b.Dx(), b.Dy()); e.cfg.MaxSide > 0 && long > e.cfg.MaxSide {
		f := float64(e.cfg.MaxSide) / float64(long)
		w, h := max(1, int(float64(b.Dx())*f+0.5)), max(1, int(float64(b.Dy())*f+0.5))
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
		img = dst
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: e.cfg.JPEGQuality}); err != nil {
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
