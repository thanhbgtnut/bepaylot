package storage

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// FileCache keeps downloaded source files on local disk for workers (PDFium
// needs random access, §5.7). Total size is bounded by maxBytes with LRU
// eviction; files in use are pinned and never evicted.
type FileCache struct {
	dir      string
	maxBytes int64
	store    ObjectStore

	mu      sync.Mutex
	entries map[string]*cacheEntry
	total   int64
	group   singleflight.Group
}

type cacheEntry struct {
	path    string
	size    int64
	refs    int
	lastUse time.Time
}

// NewFileCache creates the cache directory.
func NewFileCache(dir string, maxBytes int64, store ObjectStore) (*FileCache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &FileCache{dir: dir, maxBytes: maxBytes, store: store, entries: map[string]*cacheEntry{}}, nil
}

// Acquire returns a local path for key, downloading it if needed. Call the
// returned release when done with the file.
func (c *FileCache) Acquire(ctx context.Context, key string) (string, func(), error) {
	c.mu.Lock()
	if e, ok := c.entries[key]; ok {
		if _, err := os.Stat(e.path); err == nil {
			e.refs++
			e.lastUse = time.Now()
			c.mu.Unlock()
			return e.path, c.releaser(key), nil
		}
		c.total -= e.size
		delete(c.entries, key)
	}
	c.mu.Unlock()

	_, err, _ := c.group.Do(key, func() (any, error) {
		sum := sha1.Sum([]byte(key))
		path := filepath.Join(c.dir, hex.EncodeToString(sum[:])+filepath.Ext(key))
		size, err := c.store.Download(ctx, key, path)
		if err != nil {
			return nil, err
		}
		if size <= 0 {
			if st, err := os.Stat(path); err == nil {
				size = st.Size()
			}
		}
		c.mu.Lock()
		c.entries[key] = &cacheEntry{path: path, size: size, lastUse: time.Now()}
		c.total += size
		c.evictLocked()
		c.mu.Unlock()
		return nil, nil
	})
	if err != nil {
		return "", nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		// Evicted immediately (cache smaller than file); re-add pinned.
		return c.acquireAfterEvictLocked(ctx, key)
	}
	e.refs++
	e.lastUse = time.Now()
	return e.path, c.releaser(key), nil
}

func (c *FileCache) acquireAfterEvictLocked(ctx context.Context, key string) (string, func(), error) {
	sum := sha1.Sum([]byte(key))
	path := filepath.Join(c.dir, hex.EncodeToString(sum[:])+".pinned"+filepath.Ext(key))
	size, err := c.store.Download(ctx, key, path)
	if err != nil {
		return "", nil, err
	}
	c.entries[key] = &cacheEntry{path: path, size: size, refs: 1, lastUse: time.Now()}
	c.total += size
	return path, c.releaser(key), nil
}

func (c *FileCache) releaser(key string) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if e, ok := c.entries[key]; ok && e.refs > 0 {
				e.refs--
			}
			c.evictLocked()
		})
	}
}

// Forget removes key from the cache (document deleted).
func (c *FileCache) Forget(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok && e.refs == 0 {
		os.Remove(e.path)
		c.total -= e.size
		delete(c.entries, key)
	}
}

func (c *FileCache) evictLocked() {
	if c.maxBytes <= 0 || c.total <= c.maxBytes {
		return
	}
	type kv struct {
		key string
		e   *cacheEntry
	}
	var idle []kv
	for k, e := range c.entries {
		if e.refs == 0 {
			idle = append(idle, kv{k, e})
		}
	}
	sort.Slice(idle, func(i, j int) bool { return idle[i].e.lastUse.Before(idle[j].e.lastUse) })
	for _, it := range idle {
		if c.total <= c.maxBytes {
			return
		}
		os.Remove(it.e.path)
		c.total -= it.e.size
		delete(c.entries, it.key)
	}
}
