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
	"time"
	"unicode/utf8"

	"golang.org/x/image/draw"

	"github.com/thanhenti/bepaylot/internal/parser"
)

// Name is the engine name used in config, the registry and documents.engine.
const Name = "turboocr_vlm"

// Error policies when the page call fails.
const (
	OnErrorFallback = "fallback" // keep the OCR text of the page
	OnErrorFail     = "fail"     // fail the page (the task retries it)
)

// Config configures the engine.
type Config struct {
	// Layout is the engine that reads lines, boxes and layout (TurboOCR).
	Layout parser.Engine
	Client Transcriber
	// MaxConcurrency bounds in-flight page calls across all pages of this
	// process.
	MaxConcurrency int
	// PagePrompt reads a page with its OCR text (OCRPlaceholder marks where
	// it goes); Prompt reads a page without OCR text (full_page).
	PagePrompt string
	Prompt     string
	// ContextMaxChars caps the OCR text sent with a page (runes).
	ContextMaxChars int
	// ContextLowConf marks OCR lines below this confidence with LowConfMark
	// (0 = no marks).
	ContextLowConf float64
	// MaxSide downscales page images whose longer side exceeds it (olmOCR is
	// trained on 1288 px pages); 0 keeps the size.
	MaxSide     int
	JPEGQuality int
	Retries     int
	OnError     string
	// FullPage sends the page to the VLM without OCR text when the layout
	// engine fails or finds nothing.
	FullPage bool
	Log      *slog.Logger
}

// Engine implements parser.Engine: step 1 reads the page with Config.Layout,
// step 2 asks the VLM for the page markdown with the OCR text as its check.
type Engine struct {
	cfg Config
	sem chan struct{}
}

// New returns the engine.
func New(cfg Config) (*Engine, error) {
	if cfg.Layout == nil || cfg.Client == nil {
		return nil, errors.New("vlm: layout engine and client are required")
	}
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = 4
	}
	if cfg.PagePrompt == "" {
		cfg.PagePrompt = DefaultPagePrompt
	}
	if cfg.Prompt == "" {
		cfg.Prompt = DefaultPrompt
	}
	if cfg.ContextMaxChars <= 0 {
		cfg.ContextMaxChars = 12000
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
	return &Engine{cfg: cfg, sem: make(chan struct{}, cfg.MaxConcurrency)}, nil
}

// Name implements parser.Engine.
func (e *Engine) Name() string { return Name }

// Health requires both the layout engine and the VLM.
func (e *Engine) Health(ctx context.Context) error {
	// With full_page the engine still works without the layout engine
	// (degraded: pages go to the VLM without OCR text), so only the VLM is
	// required.
	if err := e.cfg.Layout.Health(ctx); err != nil && !e.cfg.FullPage {
		return err
	}
	return e.cfg.Client.Health(ctx)
}

// CallResult records the page call.
type CallResult struct {
	Ms               int               `json:"ms"`
	PromptTokens     int               `json:"prompt_tokens,omitempty"`
	CompletionTokens int               `json:"completion_tokens,omitempty"`
	Truncated        bool              `json:"truncated,omitempty"`
	Meta             map[string]string `json:"meta,omitempty"`
	Error            string            `json:"error,omitempty"`
}

// rawEnvelope is what RawPage.Raw holds for this engine.
type rawEnvelope struct {
	Engine string `json:"engine"`
	Model  string `json:"model"`
	// Mode is "page" (TurboOCR + one VLM call with the OCR text) or
	// "full_page" (no OCR: the page went to the VLM alone).
	Mode        string          `json:"mode"`
	LayoutError string          `json:"layout_error,omitempty"`
	Layout      json.RawMessage `json:"layout,omitempty"`
	// ContextChars is the length of the OCR text sent with the page.
	ContextChars int          `json:"context_chars,omitempty"`
	Call         *CallResult  `json:"call,omitempty"`
	Markdown     string       `json:"markdown,omitempty"`
	Align        *AlignReport `json:"align,omitempty"`
	Ms           int          `json:"ms"`
}

// ParsePage runs the two steps: TurboOCR reads the page, then one VLM call
// returns the page markdown with the OCR text in the prompt as the check.
// The markdown is split and aligned onto the OCR regions (distribute).
//
// With FullPage, a page the layout engine cannot handle (error, circuit open,
// or nothing found) is sent to the VLM without OCR text and its markdown is
// split into blocks (fullPage). This also applies when Refine is off: a page
// the layout engine fails on has no other text source besides the VLM.
func (e *Engine) ParsePage(ctx context.Context, in parser.PageImage, opt parser.PageOptions) (*parser.RawPage, error) {
	img, err := io.ReadAll(io.LimitReader(in.Body, 256<<20))
	if err != nil {
		return nil, fmt.Errorf("vlm: read page image: %w", err)
	}
	if opt.Refine {
		opt.Layout = true // regions carry the markdown back to positions
	}
	in.Body, in.Size = bytes.NewReader(img), int64(len(img))
	page, err := e.cfg.Layout.ParsePage(ctx, in, opt)
	if err != nil {
		if !e.cfg.FullPage || ctx.Err() != nil {
			return nil, err
		}
		e.cfg.Log.Warn("vlm: layout engine failed, sending the page to the VLM without OCR text", "page", in.PageNo, "err", err)
		return e.fullPage(ctx, img, in, err.Error())
	}
	if !opt.Refine {
		return page, nil
	}
	if len(page.Lines) == 0 {
		if len(page.Regions) == 0 && e.cfg.FullPage {
			return e.fullPage(ctx, img, in, "layout engine found no region and no line")
		}
		return page, nil // nothing to check a transcription against
	}
	src, _, err := image.Decode(bytes.NewReader(img))
	if err != nil {
		return nil, fmt.Errorf("vlm: decode page image: %w", err)
	}

	t0 := time.Now()
	ocrText := e.ocrContext(page)
	prompt := e.cfg.PagePrompt
	if strings.Contains(prompt, OCRPlaceholder) {
		prompt = strings.Replace(prompt, OCRPlaceholder, ocrText, 1)
	} else {
		prompt += "\n\n<ocr>\n" + ocrText + "\n</ocr>"
	}
	tr, cr := e.call(ctx, in.PageNo, prompt, src)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	env := rawEnvelope{Mode: "page", ContextChars: utf8.RuneCountInString(ocrText), Call: &cr}
	if cr.Error != "" {
		e.envelope(page, env, int(time.Since(t0).Milliseconds()))
		e.cfg.Log.Warn("vlm: page call failed", "page", in.PageNo, "err", cr.Error)
		if e.cfg.OnError == OnErrorFail {
			return nil, fmt.Errorf("vlm: page call failed: %s", cr.Error)
		}
		return page, nil // OCR text only
	}
	if tr.Truncated {
		e.cfg.Log.Warn("vlm: page markdown truncated at max_tokens; the rest keeps OCR text", "page", in.PageNo)
	}
	env.Markdown = cleanMarkdown(tr.Text)
	rep := distribute(page, env.Markdown)
	env.Align = &rep
	if rep.OCRWords > 0 && rep.Coverage < 0.5 {
		e.cfg.Log.Warn("vlm: page markdown agrees with little of the OCR text", "page", in.PageNo,
			"coverage", rep.Coverage, "unmatched_segments", len(rep.Unmatched), "skipped", rep.Skipped)
	}
	e.envelope(page, env, int(time.Since(t0).Milliseconds()))
	return page, nil
}

func (e *Engine) envelope(page *parser.RawPage, env rawEnvelope, ms int) {
	env.Engine, env.Model, env.Ms = Name, e.cfg.Client.Model(), ms
	if json.Valid(page.Raw) {
		env.Layout = json.RawMessage(page.Raw)
	}
	if b, err := json.Marshal(env); err == nil {
		page.Raw = b
	}
}

// ocrContext renders the page's OCR lines in reading order, a blank line
// between layout regions, low-confidence lines marked, capped at
// ContextMaxChars runes.
func (e *Engine) ocrContext(page *parser.RawPage) string {
	regionOf := lineRegions(page)
	var sb strings.Builder
	n, prev := 0, -2
	for _, li := range readingOrder(page) {
		l := page.Lines[li]
		text := strings.TrimSpace(l.Text)
		if text == "" {
			continue
		}
		if e.cfg.ContextLowConf > 0 && l.Confidence > 0 && l.Confidence < e.cfg.ContextLowConf {
			text += " " + LowConfMark
		}
		sepr := "\n"
		if prev != -2 && regionOf[li] != prev {
			sepr = "\n\n"
		}
		if n == 0 {
			sepr = ""
		}
		k := utf8.RuneCountInString(sepr + text)
		if n+k > e.cfg.ContextMaxChars {
			sb.WriteString("\n…")
			break
		}
		sb.WriteString(sepr + text)
		n += k
		prev = regionOf[li]
	}
	return sb.String()
}

// cleanMarkdown drops low-confidence marks the model copied from the OCR text.
func cleanMarkdown(s string) string {
	s = strings.ReplaceAll(s, " "+LowConfMark, "")
	return strings.TrimSpace(strings.ReplaceAll(s, LowConfMark, ""))
}

// call sends one page image to the Transcriber under the process-wide
// semaphore, retrying transient errors.
func (e *Engine) call(ctx context.Context, pageNo int, prompt string, img image.Image) (*Transcription, CallResult) {
	var cr CallResult
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
		tr, err = e.cfg.Client.Transcribe(ctx, Request{Prompt: prompt, JPEG: jpg, PageNo: pageNo})
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
	cr.PromptTokens, cr.CompletionTokens, cr.Truncated, cr.Meta = tr.PromptTokens, tr.CompletionTokens, tr.Truncated, tr.Meta
	return tr, cr
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
