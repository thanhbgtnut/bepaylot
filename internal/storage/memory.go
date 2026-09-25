package storage

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Memory is an in-process ObjectStore for tests and local runs without S3.
type Memory struct {
	mu   sync.RWMutex
	objs map[string]memObj
}

type memObj struct {
	data        []byte
	contentType string
}

// NewMemory returns an empty store.
func NewMemory() *Memory { return &Memory{objs: map[string]memObj{}} }

// Put implements ObjectStore.
func (m *Memory) Put(_ context.Context, key string, r io.Reader, _ int64, contentType string) (ObjectInfo, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return ObjectInfo{}, err
	}
	m.mu.Lock()
	m.objs[key] = memObj{data: b, contentType: contentType}
	m.mu.Unlock()
	sum := md5.Sum(b)
	return ObjectInfo{Key: key, Size: int64(len(b)), ETag: hex.EncodeToString(sum[:]), ContentType: contentType}, nil
}

// Get implements ObjectStore.
func (m *Memory) Get(_ context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	m.mu.RLock()
	o, ok := m.objs[key]
	m.mu.RUnlock()
	if !ok {
		return nil, ObjectInfo{}, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	return io.NopCloser(bytes.NewReader(o.data)), ObjectInfo{Key: key, Size: int64(len(o.data)), ContentType: o.contentType}, nil
}

// Download implements ObjectStore.
func (m *Memory) Download(ctx context.Context, key, path string) (int64, error) {
	rc, _, err := m.Get(ctx, key)
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	return int64(len(b)), os.WriteFile(path, b, 0o600)
}

// Stat implements ObjectStore.
func (m *Memory) Stat(_ context.Context, key string) (ObjectInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	o, ok := m.objs[key]
	if !ok {
		return ObjectInfo{}, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	return ObjectInfo{Key: key, Size: int64(len(o.data)), ContentType: o.contentType}, nil
}

// Delete implements ObjectStore.
func (m *Memory) Delete(_ context.Context, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		delete(m.objs, k)
	}
	return nil
}

// DeletePrefix implements ObjectStore.
func (m *Memory) DeletePrefix(_ context.Context, prefix string) (int, error) {
	prefix = strings.TrimSuffix(prefix, "/") + "/"
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for k := range m.objs {
		if strings.HasPrefix(k, prefix) {
			delete(m.objs, k)
			n++
		}
	}
	return n, nil
}

// PresignGet returns a fake memory:// URL.
func (m *Memory) PresignGet(_ context.Context, key string, _ time.Duration) (string, error) {
	return "memory://" + key, nil
}

// Keys lists stored keys (tests).
func (m *Memory) Keys() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.objs))
	for k := range m.objs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
