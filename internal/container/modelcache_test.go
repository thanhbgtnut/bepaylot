package container

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/thanhenti/bepaylot/docpipe/parser/vlm"
	"github.com/thanhenti/bepaylot/internal/types"
)

type memCache map[string][]byte

func (m memCache) Get(_ context.Context, k string) ([]byte, bool, error) {
	b, ok := m[k]
	return b, ok, nil
}
func (m memCache) Put(_ context.Context, k, _ string, v []byte) error { m[k] = v; return nil }

type countClassifier struct {
	calls int
	err   error
}

func (c *countClassifier) ClassifyPage(context.Context, []types.ClassLabel, []byte, string) (string, float64, bool, error) {
	c.calls++
	return "uy_nhiem_chi", 0.98, true, c.err
}

type countVLM struct {
	calls     int
	truncated bool
}

func (c *countVLM) Transcribe(context.Context, vlm.Request) (*vlm.Transcription, error) {
	c.calls++
	return &vlm.Transcription{Text: "# UNC", Truncated: c.truncated}, nil
}
func (c *countVLM) Health(context.Context) error { return nil }
func (c *countVLM) Model() string                { return "olmocr" }

// A page the model answered once is not sent again; another image, other
// labels, other OCR text, or a failed call are.
func TestCachedClassifier(t *testing.T) {
	ctx, inner := context.Background(), &countClassifier{}
	c := cachedClassifier{inner: inner, cache: memCache{}, scope: "m1", log: slog.Default()}
	labels := []types.ClassLabel{{Name: "uy_nhiem_chi", Title: "Uỷ nhiệm chi"}}
	for i := 0; i < 3; i++ {
		if l, conf, isFirstPage, err := c.ClassifyPage(ctx, labels, []byte("img-1"), "text-1"); err != nil || l != "uy_nhiem_chi" || conf != 0.98 || !isFirstPage {
			t.Fatalf("got %q %v %v", l, conf, err)
		}
	}
	if inner.calls != 1 {
		t.Fatalf("same page: %d calls, want 1", inner.calls)
	}
	c.ClassifyPage(ctx, labels, []byte("img-2"), "text-1")
	c.ClassifyPage(ctx, append(labels, types.ClassLabel{Name: "hop_dong"}), []byte("img-1"), "text-1")
	c.ClassifyPage(ctx, labels, []byte("img-1"), "text-2")
	if inner.calls != 4 {
		t.Fatalf("other image / other labels / other text: %d calls, want 4", inner.calls)
	}
	inner.err = errors.New("boom")
	for i := 0; i < 2; i++ {
		if _, _, _, err := c.ClassifyPage(ctx, labels, []byte("img-3"), ""); err == nil {
			t.Fatal("a failed call must fail")
		}
	}
	if inner.calls != 6 {
		t.Fatalf("a failed call must not be cached: %d calls, want 6", inner.calls)
	}
}

func TestCachedVLM(t *testing.T) {
	ctx, inner := context.Background(), &countVLM{}
	c := cachedVLM{Transcriber: inner, cache: memCache{}, log: slog.Default()}
	req := vlm.Request{Prompt: "p", JPEG: []byte("img")}
	for i := 0; i < 3; i++ {
		if tr, err := c.Transcribe(ctx, req); err != nil || tr.Text != "# UNC" {
			t.Fatalf("got %+v %v", tr, err)
		}
	}
	if inner.calls != 1 {
		t.Fatalf("same page: %d calls, want 1", inner.calls)
	}
	c.Transcribe(ctx, vlm.Request{Prompt: "other prompt", JPEG: []byte("img")})
	if inner.calls != 2 {
		t.Fatalf("other prompt: %d calls, want 2", inner.calls)
	}
	inner.truncated = true
	c.Transcribe(ctx, vlm.Request{Prompt: "p", JPEG: []byte("cut")})
	c.Transcribe(ctx, vlm.Request{Prompt: "p", JPEG: []byte("cut")})
	if inner.calls != 4 {
		t.Fatalf("a truncated answer must not be cached: %d calls, want 4", inner.calls)
	}
}
