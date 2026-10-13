package storage

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
)

// TempFiles gives workers a local copy of a source file for as long as a
// task uses it (PDFium needs random access, §5.7). Nothing is kept: the
// object store is the only copy, a file is downloaded when a task asks for it
// and removed when the last task using it is done, so the disk holds only the
// files being processed right now.
type TempFiles struct {
	dir   string
	store ObjectStore

	mu      sync.Mutex
	entries map[string]*tempEntry
}

type tempEntry struct {
	path  string
	refs  int
	ready chan struct{} // closed when the download ended
	err   error
}

// NewTempFiles creates the directory the files are downloaded into.
func NewTempFiles(dir string, store ObjectStore) (*TempFiles, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &TempFiles{dir: dir, store: store, entries: map[string]*tempEntry{}}, nil
}

// Acquire returns a local path for key, downloading it unless another task
// has it right now. Call the returned release when done with the file.
func (c *TempFiles) Acquire(ctx context.Context, key string) (string, func(), error) {
	c.mu.Lock()
	e, ok := c.entries[key]
	if !ok {
		sum := sha1.Sum([]byte(key))
		e = &tempEntry{path: filepath.Join(c.dir, hex.EncodeToString(sum[:])+filepath.Ext(key)), ready: make(chan struct{})}
		c.entries[key] = e
	}
	e.refs++
	c.mu.Unlock()

	var once sync.Once
	release := func() { once.Do(func() { c.release(key, e) }) }
	if !ok {
		_, e.err = c.store.Download(ctx, key, e.path)
		close(e.ready)
	}
	select {
	case <-e.ready:
	case <-ctx.Done():
		release()
		return "", nil, ctx.Err()
	}
	if e.err != nil {
		release()
		return "", nil, e.err
	}
	return e.path, release, nil
}

// release drops one use of the file and removes it after the last one. A
// failed download leaves the entry too, so the next task downloads again.
func (c *TempFiles) release(key string, e *tempEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e.refs--
	if e.refs > 0 {
		return
	}
	if c.entries[key] == e {
		delete(c.entries, key)
	}
	os.Remove(e.path)
}
