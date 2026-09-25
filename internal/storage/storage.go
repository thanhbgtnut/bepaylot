// Package storage is the object store for original files, rendered page
// images, raw parser output and full-document markdown (§9.1). Postgres only
// keeps the object keys.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when an object does not exist.
var ErrNotFound = errors.New("storage: object not found")

// ObjectInfo describes a stored object.
type ObjectInfo struct {
	Key         string
	Size        int64
	ETag        string
	ContentType string
}

// ObjectStore is the storage contract used by every module.
type ObjectStore interface {
	// Put streams r to key. size may be -1 when unknown.
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) (ObjectInfo, error)
	// Get opens key for streaming; the caller closes the reader.
	Get(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error)
	// Download writes key into the local file path (parallel ranged download).
	Download(ctx context.Context, key, path string) (int64, error)
	Stat(ctx context.Context, key string) (ObjectInfo, error)
	Delete(ctx context.Context, keys ...string) error
	// DeletePrefix removes every object under prefix and returns the count.
	DeletePrefix(ctx context.Context, prefix string) (int, error)
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// Keys builds object keys under a configured prefix (§9.1 layout).
type Keys struct{ Prefix string }

func (k Keys) join(parts ...string) string {
	all := append([]string{strings.Trim(k.Prefix, "/")}, parts...)
	return strings.TrimPrefix(path.Join(all...), "/")
}

// Doc is the root of one document's objects.
func (k Keys) Doc(kb, doc uuid.UUID) string {
	return k.join("kb", kb.String(), "doc", doc.String())
}

// Source is the original upload.
func (k Keys) Source(kb, doc uuid.UUID, ext string) string {
	ext = strings.TrimPrefix(ext, ".")
	if ext == "" {
		ext = "bin"
	}
	return k.join("kb", kb.String(), "doc", doc.String(), "source", "original."+ext)
}

// Gen is the root of one parse generation.
func (k Keys) Gen(kb, doc uuid.UUID, gen int) string {
	return k.join("kb", kb.String(), "doc", doc.String(), fmt.Sprintf("g%d", gen))
}

// PageImage is a rendered page.
func (k Keys) PageImage(kb, doc uuid.UUID, gen, page int) string {
	return k.Gen(kb, doc, gen) + fmt.Sprintf("/pages/%05d.jpg", page)
}

// OCRRaw is the engine's raw JSON for a page (gzip).
func (k Keys) OCRRaw(kb, doc uuid.UUID, gen, page int) string {
	return k.Gen(kb, doc, gen) + fmt.Sprintf("/ocr/%05d.json.gz", page)
}

// TextLayer is the page's raw text layer (gzip).
func (k Keys) TextLayer(kb, doc uuid.UUID, gen, page int) string {
	return k.Gen(kb, doc, gen) + fmt.Sprintf("/text/%05d.json.gz", page)
}

// Figure is a cropped figure image.
func (k Keys) Figure(kb, doc uuid.UUID, gen, page, block int) string {
	return k.Gen(kb, doc, gen) + fmt.Sprintf("/figures/p%d-b%d.jpg", page, block)
}

// Markdown is the full-document markdown.
func (k Keys) Markdown(kb, doc uuid.UUID, gen int) string {
	return k.Gen(kb, doc, gen) + "/document.md"
}
