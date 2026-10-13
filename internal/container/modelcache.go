package container

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"

	"github.com/thanhenti/bepaylot/docpipe/parser/vlm"
	"github.com/thanhenti/bepaylot/internal/types"
	"github.com/thanhenti/bepaylot/internal/types/interfaces"
)

// modelCache stores the answers of model calls by request hash
// (postgres.ModelCacheRepo).
type modelCache interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Put(ctx context.Context, key, kind string, value []byte) error
}

// cacheKey hashes the parts of a request; a part's length is hashed with it
// so two requests never share a key by concatenation.
func cacheKey(parts ...[]byte) string {
	h := sha256.New()
	for _, p := range parts {
		_ = json.NewEncoder(h).Encode(len(p))
		h.Write(p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// cached returns the stored answer of key, or calls the model and stores
// what it answered. Only successful calls are stored; a cache failure is
// logged and the model is called as if there were no cache.
func cached[T any](ctx context.Context, c modelCache, log *slog.Logger, key, kind string, call func() (T, error)) (T, error) {
	var out T
	if b, ok, err := c.Get(ctx, key); err != nil {
		log.Warn("model cache: read", "kind", kind, "err", err)
	} else if ok && json.Unmarshal(b, &out) == nil {
		return out, nil
	}
	out, err := call()
	if err != nil {
		return out, err
	}
	if b, err := json.Marshal(out); err == nil {
		if err := c.Put(ctx, key, kind, b); err != nil {
			log.Warn("model cache: write", "kind", kind, "err", err)
		}
	}
	return out, nil
}

// cachedClassifier keeps the Decision API's answer for a page image: the
// same image asked about the same document types is answered from the cache.
type cachedClassifier struct {
	inner interfaces.PageClassifier
	cache modelCache
	scope string // model and instructions of the Decision API
	log   *slog.Logger
}

type pageType struct {
	Label      string  `json:"label"`
	Confidence float64 `json:"confidence"`
	FirstPage  bool    `json:"is_first_page"`
}

// The key names the questions asked ("decision+is_first_page"): answers
// stored for other questions (is_break) are not reused.
func (c cachedClassifier) ClassifyPage(ctx context.Context, labels []types.ClassLabel, image []byte) (string, float64, bool, error) {
	lb, _ := json.Marshal(labels)
	r, err := cached(ctx, c.cache, c.log, cacheKey([]byte("decision+is_first_page"), []byte(c.scope), lb, image), "decision", func() (pageType, error) {
		label, conf, isFirstPage, err := c.inner.ClassifyPage(ctx, labels, image)
		return pageType{label, conf, isFirstPage}, err
	})
	return r.Label, r.Confidence, r.FirstPage, err
}

// cachedVLM keeps the VLM's transcription of a page: the same image with the
// same prompt and model is answered from the cache. A transcription cut at
// max_tokens is not kept.
type cachedVLM struct {
	vlm.Transcriber
	cache modelCache
	log   *slog.Logger
}

func (c cachedVLM) Transcribe(ctx context.Context, req vlm.Request) (*vlm.Transcription, error) {
	key := cacheKey([]byte("vlm"), []byte(c.Model()), []byte(req.Prompt), req.JPEG)
	if b, ok, err := c.cache.Get(ctx, key); err == nil && ok {
		var t vlm.Transcription
		if json.Unmarshal(b, &t) == nil {
			return &t, nil
		}
	}
	t, err := c.Transcriber.Transcribe(ctx, req)
	if err != nil || t == nil || t.Truncated {
		return t, err
	}
	if b, err := json.Marshal(t); err == nil {
		if err := c.cache.Put(ctx, key, "vlm", b); err != nil {
			c.log.Warn("model cache: write", "kind", "vlm", "err", err)
		}
	}
	return t, nil
}
