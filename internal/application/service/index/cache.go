package index

import (
	"sync"
	"time"
)

// ttlCache is a small in-process cache of search responses keyed by the
// request and the generations of the documents in scope (§6.5).
type ttlCache struct {
	mu    sync.Mutex
	ttl   time.Duration
	max   int
	items map[string]cacheItem
}

type cacheItem struct {
	v   any
	exp time.Time
}

func newTTLCache(ttl time.Duration, max int) *ttlCache {
	return &ttlCache{ttl: ttl, max: max, items: map[string]cacheItem{}}
}

func (c *ttlCache) get(k string) (any, bool) {
	if c.ttl <= 0 {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	it, ok := c.items[k]
	if !ok || time.Now().After(it.exp) {
		delete(c.items, k)
		return nil, false
	}
	return it.v, true
}

func (c *ttlCache) put(k string, v any) {
	if c.ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.items) >= c.max {
		now := time.Now()
		for key, it := range c.items {
			if now.After(it.exp) || len(c.items) >= c.max {
				delete(c.items, key)
			}
		}
	}
	c.items[k] = cacheItem{v: v, exp: time.Now().Add(c.ttl)}
}
