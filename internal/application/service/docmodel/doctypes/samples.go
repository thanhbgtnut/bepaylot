package doctypes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/docpipe/parser"
	"github.com/thanhenti/bepaylot/docpipe/parser/assemble"
	"github.com/thanhenti/bepaylot/docpipe/parser/imagefile"
	"github.com/thanhenti/bepaylot/docpipe/parser/pdf"
	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/llm"
	"github.com/thanhenti/bepaylot/internal/queue"
	"github.com/thanhenti/bepaylot/internal/types"
)

// Renderer turns the pages of a PDF into images (pdf.Renderer).
type Renderer interface {
	Probe(ctx context.Context, path string) (*pdf.DocInfo, error)
	RenderBatch(ctx context.Context, path string, pages []int, opt pdf.RenderOptions, emit func(pdf.PageRender) error) error
}

// samplePrefix is where the files of a type's samples are kept.
func samplePrefix(owner uuid.UUID, code string) string {
	return fmt.Sprintf("doctypes/%s/%s/", owner, code)
}

func pageKey(storageKey string, n int) string {
	return fmt.Sprintf("%s.p%d.jpg", storageKey, n)
}

// SampleView is a sample as listed: no page content, whether the version in
// use ran on it and its score against the reviewed values.
type SampleView struct {
	types.DocTypeSample
	PageCount int      `json:"page_count"`
	Ran       bool     `json:"ran"`
	Score     *float64 `json:"score"`
}

// SampleDetail is a sample with its pages and every run on it.
type SampleDetail struct {
	types.DocTypeSample
	Runs []types.SampleRun `json:"runs"`
}

// maxSampleBytes caps one sample file.
const maxSampleBytes = 50 << 20

// Upload stores a sample file of a type and queues its reading.
func (s *Service) Upload(ctx context.Context, owner uuid.UUID, code, name string, r io.Reader, by uuid.UUID) (types.DocTypeSample, error) {
	if _, err := s.st.DocTypes.Get(ctx, owner, code); err != nil {
		return types.DocTypeSample{}, notFound(err)
	}
	body, err := io.ReadAll(io.LimitReader(r, maxSampleBytes+1))
	if err != nil {
		return types.DocTypeSample{}, err
	}
	if len(body) > maxSampleBytes {
		return types.DocTypeSample{}, fmt.Errorf("%w: %s is larger than 50 MB", ErrBad, name)
	}
	mime := sniff(body)
	if mime == "" {
		return types.DocTypeSample{}, fmt.Errorf("%w: %s is not a PDF or an image", ErrBad, name)
	}
	key := samplePrefix(owner, code) + uuid.NewString()
	if _, err := s.objects.Put(ctx, key, bytes.NewReader(body), int64(len(body)), mime); err != nil {
		return types.DocTypeSample{}, err
	}
	smp, err := s.st.DocTypes.CreateSample(ctx, owner, types.DocTypeSample{Code: code, FileName: filepath.Base(name), StorageKey: key, MimeType: mime}, by)
	if err != nil {
		return smp, err
	}
	if err := s.q.Enqueue(ctx, types.TaskDocTypeSample, samplePayload{SampleID: smp.ID, UserID: by}, queue.Opts{Interactive: true}); err != nil {
		_ = s.st.DocTypes.SetSampleRead(context.WithoutCancel(ctx), smp.ID, types.SampleFailed, nil, "could not queue: "+err.Error())
		return smp, err
	}
	return smp, nil
}

func sniff(head []byte) string {
	if bytes.HasPrefix(head, []byte("%PDF-")) {
		return "application/pdf"
	}
	if bytes.HasPrefix(head, []byte("II*\x00")) || bytes.HasPrefix(head, []byte("MM\x00*")) {
		return "image/tiff"
	}
	switch ct := http.DetectContentType(head); ct {
	case "image/jpeg", "image/png", "image/webp", "image/gif", "image/bmp":
		return ct
	}
	return ""
}

// Samples lists the samples of a type with their score by the version in use.
func (s *Service) Samples(ctx context.Context, owner uuid.UUID, code string) ([]SampleView, error) {
	t, err := s.st.DocTypes.Get(ctx, owner, code)
	if err != nil {
		return nil, notFound(err)
	}
	p, err := s.st.DocTypes.Prompt(ctx, owner, code, t.PromptVersion)
	if err != nil {
		return nil, notFound(err)
	}
	samples, runs, err := s.scoring(ctx, owner, code)
	if err != nil {
		return nil, err
	}
	out := make([]SampleView, len(samples))
	for i, smp := range samples {
		v := SampleView{DocTypeSample: smp, PageCount: len(smp.Pages)}
		v.Pages = []types.SamplePage{}
		r, ok := runs[smp.ID][p.Version]
		v.Ran = ok
		if ok && smp.Truth != nil {
			v.Score = accuracy(cellsOf(p.Fields, smp.Truth, r.Output, ""))
		}
		out[i] = v
	}
	return out, nil
}

// Sample returns a sample of a type with its pages and runs.
func (s *Service) Sample(ctx context.Context, owner uuid.UUID, code string, id uuid.UUID) (SampleDetail, error) {
	smp, err := s.st.DocTypes.Sample(ctx, owner, id)
	if err != nil || smp.Code != code {
		return SampleDetail{}, ErrNotFound
	}
	_, runs, err := s.scoring(ctx, owner, code)
	if err != nil {
		return SampleDetail{}, err
	}
	d := SampleDetail{DocTypeSample: smp, Runs: []types.SampleRun{}}
	for _, r := range runs[id] {
		d.Runs = append(d.Runs, r)
	}
	return d, nil
}

// PageImage opens the image of a page of a sample.
func (s *Service) PageImage(ctx context.Context, owner uuid.UUID, code string, id uuid.UUID, n int) (io.ReadCloser, error) {
	smp, err := s.st.DocTypes.Sample(ctx, owner, id)
	if err != nil || smp.Code != code || n < 1 || n > len(smp.Pages) {
		return nil, ErrNotFound
	}
	rc, _, err := s.objects.Get(ctx, pageKey(smp.StorageKey, n))
	if err != nil {
		return nil, ErrNotFound
	}
	return rc, nil
}

// DeleteSample removes a sample, its files and its runs.
func (s *Service) DeleteSample(ctx context.Context, owner uuid.UUID, code string, id uuid.UUID) error {
	smp, err := s.st.DocTypes.Sample(ctx, owner, id)
	if err != nil || smp.Code != code {
		return ErrNotFound
	}
	if err := s.st.DocTypes.DeleteSample(ctx, owner, id); err != nil {
		return notFound(err)
	}
	keys := []string{smp.StorageKey}
	for n := 1; n <= len(smp.Pages); n++ {
		keys = append(keys, pageKey(smp.StorageKey, n))
	}
	if err := s.objects.Delete(ctx, keys...); err != nil {
		s.log.Warn("doctypes: delete sample files", "sample", id, "err", err)
	}
	return nil
}

// SetTruth stores the reviewed values of a sample: one per field of the
// version in use, "" when the document does not have it.
func (s *Service) SetTruth(ctx context.Context, owner uuid.UUID, code string, id uuid.UUID, values map[string]string, by uuid.UUID) (types.DocTypeSample, error) {
	smp, err := s.st.DocTypes.Sample(ctx, owner, id)
	if err != nil || smp.Code != code {
		return types.DocTypeSample{}, ErrNotFound
	}
	if smp.Status != types.SampleReady {
		return types.DocTypeSample{}, fmt.Errorf("%w: the sample is not read yet", ErrBad)
	}
	t, err := s.st.DocTypes.Get(ctx, owner, code)
	if err != nil {
		return types.DocTypeSample{}, notFound(err)
	}
	truth := map[string]string{}
	for _, f := range t.Fields {
		truth[f.Key] = strings.TrimSpace(values[f.Key])
	}
	return s.st.DocTypes.SetTruth(ctx, owner, id, truth, by)
}

type samplePayload struct {
	SampleID uuid.UUID `json:"sample_id"`
	UserID   uuid.UUID `json:"user_id"`
}

// handleSample reads a sample once: render, then the OCR engine (and VLM)
// on every page. Per page it keeps the content the VLM read (the main
// source) and the lines with their confidence and box (U102).
func (s *Service) handleSample(ctx context.Context, payload []byte) error {
	var p samplePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("%w: %v", queue.ErrSkipRetry, err)
	}
	smp, _, err := s.st.DocTypes.SampleByID(ctx, p.SampleID)
	if errors.Is(err, postgres.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if smp.Status != types.SampleProcessing {
		return nil
	}
	ctx = llm.WithCaller(ctx, llm.Caller{UserID: &p.UserID, Kind: types.UsageParse})
	pages, err := s.read(ctx, smp)
	if err != nil {
		if errors.Is(err, queue.ErrSkipRetry) || queue.IsFinalAttempt(ctx) {
			s.log.Warn("doctypes: sample failed", "sample", smp.ID, "err", err)
			return s.st.DocTypes.SetSampleRead(context.WithoutCancel(ctx), smp.ID, types.SampleFailed, nil, "Không đọc được tài liệu này")
		}
		return err
	}
	return s.st.DocTypes.SetSampleRead(ctx, smp.ID, types.SampleReady, pages, "")
}

func (s *Service) read(ctx context.Context, smp types.DocTypeSample) ([]types.SamplePage, error) {
	if s.engines == nil || (s.renderer == nil && smp.MimeType == "application/pdf") {
		return nil, fmt.Errorf("%w: %v", queue.ErrSkipRetry, ErrNotReady)
	}
	engine, err := s.engines.Get("")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", queue.ErrSkipRetry, err)
	}
	tmp, err := os.CreateTemp("", "sample-*")
	if err != nil {
		return nil, err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	if _, err := s.objects.Download(ctx, smp.StorageKey, tmp.Name()); err != nil {
		return nil, err
	}
	rc := s.cfg.Parser.Render
	opt := pdf.RenderOptions{DPI: rc.DPI, MaxLongSide: rc.MaxLongSide, MaxPixels: rc.MaxPixels, JPEGQuality: rc.JPEGQuality}
	var renders []pdf.PageRender
	emit := func(r pdf.PageRender) error { renders = append(renders, r); return nil }
	if smp.MimeType == "application/pdf" {
		info, err := s.renderer.Probe(ctx, tmp.Name())
		if err != nil {
			return nil, fmt.Errorf("%w: %v", queue.ErrSkipRetry, err)
		}
		nos := make([]int, info.PageCount)
		for i := range nos {
			nos[i] = i + 1
		}
		if err := s.renderer.RenderBatch(ctx, tmp.Name(), nos, opt, emit); err != nil {
			return nil, err
		}
	} else {
		f, err := os.Open(tmp.Name())
		if err != nil {
			return nil, err
		}
		res, err := imagefile.Normalize(f, imagefile.Options{MaxPixels: opt.MaxPixels, JPEGQuality: opt.JPEGQuality})
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%w: %v", queue.ErrSkipRetry, err)
		}
		_ = emit(pdf.PageRender{PageNo: 1, JPEG: res.JPEG, Width: res.Width, Height: res.Height, DPI: 72})
	}

	o := s.cfg.Parser.Engines.TurboOCR.Options
	on := func(p *bool) bool { return p == nil || *p }
	popt := parser.PageOptions{Autorotate: on(o.Autorotate), Layout: on(o.Layout), ReadingOrder: on(o.ReadingOrder), Tables: on(o.Tables), Formulas: o.Formulas, Refine: true}
	pages := make([]types.SamplePage, len(renders))
	errs := make([]error, len(renders))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, r := range renders {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			if _, err := s.objects.Put(ctx, pageKey(smp.StorageKey, r.PageNo), bytes.NewReader(r.JPEG), int64(len(r.JPEG)), "image/jpeg"); err != nil {
				errs[i] = err
				return
			}
			raw, err := engine.ParsePage(ctx, parser.PageImage{PageNo: r.PageNo, Body: bytes.NewReader(r.JPEG), Size: int64(len(r.JPEG)), Width: r.Width, Height: r.Height}, popt)
			if err != nil {
				errs[i] = err
				return
			}
			pg := assemble.Build(raw, assemble.Options{PageNo: r.PageNo, DPI: r.DPI, Engine: engine.Name(), ClassMap: s.cfg.Parser.ClassMap,
				ReadingOrderFix: s.cfg.Parser.ReadingOrderFixEnabled(), LowConfThreshold: s.cfg.Parser.LowConfThreshold})
			assemble.Render(pg)
			md := strings.TrimSpace(pg.MarkdownVLM)
			if md == "" {
				md = strings.TrimSpace(pg.Markdown)
			}
			sp := types.SamplePage{N: r.PageNo, Width: float64(r.Width), Height: float64(r.Height), Markdown: md, Lines: []types.SampleLine{}}
			for _, l := range pg.Lines {
				text := l.TextOCR
				if text == "" {
					text = l.Text
				}
				if strings.TrimSpace(text) == "" {
					continue
				}
				sp.Lines = append(sp.Lines, types.SampleLine{N: l.LineNo, Text: text, Conf: l.Confidence, BBox: l.BBox.Array()})
			}
			pages[i] = sp
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return pages, ctx.Err()
}
