package lcw

import (
	"fmt"
	"math"
	"sync/atomic"

	"github.com/go-pkgz/lcw/eventbus"
	"github.com/google/uuid"
	lru "github.com/hashicorp/golang-lru"
)

// LruCache wraps lru.LruCache with loading cache Get and size limits
type LruCache struct {
	options
	CacheStat
	backend     *lru.Cache
	currentSize int64
	id          string // uuid identifying cache instance
	loads       loadGroup
}

// NewLruCache makes LRU LoadingCache implementation, 1000 max keys by default
func NewLruCache(opts ...Option) (*LruCache, error) {
	res := LruCache{
		options: options{
			maxKeys:      1000,
			maxValueSize: 0,
			eventBus:     &eventbus.NopPubSub{},
		},
		id: uuid.New().String(),
	}
	for _, opt := range opts {
		if err := opt(&res.options); err != nil {
			return nil, fmt.Errorf("failed to set cache option: %w", err)
		}
	}

	err := res.init()
	return &res, err
}

func (c *LruCache) init() error {
	if err := c.eventBus.Subscribe(c.onBusEvent); err != nil {
		return fmt.Errorf("can't subscribe to event bus: %w", err)
	}

	onEvicted := func(key any, value any) {
		if c.onEvicted != nil {
			c.onEvicted(key.(string), value)
		}
		if size, ok := sizeOf(value); ok {
			atomic.AddInt64(&c.currentSize, -1*int64(size))
		}
		_ = c.eventBus.Publish(c.id, key.(string)) // signal invalidation to other nodes
	}

	var err error
	// OnEvicted called automatically for expired and manually deleted
	maxKeys := c.maxKeys
	if maxKeys <= 0 { // 0 means unlimited, lru backend requires a positive size
		maxKeys = math.MaxInt
	}
	if c.backend, err = lru.NewWithEvict(maxKeys, onEvicted); err != nil {
		return fmt.Errorf("failed to make lru cache backend: %w", err)
	}

	return nil
}

// Get gets value by key or load with fn if not found in cache
func (c *LruCache) Get(key string, fn func() (any, error)) (data any, err error) {
	if v, ok := c.backend.Get(key); ok {
		atomic.AddInt64(&c.Hits, 1)
		return v, nil
	}

	// concurrent callers for the same key wait for the first load instead of loading on their own,
	// otherwise each of them would add the value and count its size again
	return c.loads.do(key, func() (any, error) {
		if v, ok := c.backend.Get(key); ok { // filled by the load we were waiting for
			atomic.AddInt64(&c.Hits, 1)
			return v, nil
		}

		data, err := fn()
		if err != nil {
			atomic.AddInt64(&c.Errors, 1)
			return data, err
		}

		atomic.AddInt64(&c.Misses, 1)

		if !c.allowed(key, data) {
			return data, nil
		}

		c.backend.Add(key, data)

		if size, ok := sizeOf(data); ok {
			atomic.AddInt64(&c.currentSize, int64(size))
			for c.maxCacheSize > 0 && atomic.LoadInt64(&c.currentSize) > c.maxCacheSize {
				if _, _, ok := c.backend.RemoveOldest(); !ok { // nothing left to evict
					break
				}
			}
		}

		return data, nil
	})
}

// Peek returns the key value (or undefined if not found) without updating the "recently used"-ness of the key.
func (c *LruCache) Peek(key string) (any, bool) {
	return c.backend.Peek(key)
}

// Purge clears the cache completely.
func (c *LruCache) Purge() {
	c.backend.Purge()
	atomic.StoreInt64(&c.currentSize, 0)
}

// Invalidate removes keys with passed predicate fn, i.e. fn(key) should be true to get evicted
func (c *LruCache) Invalidate(fn func(key string) bool) {
	for _, k := range c.backend.Keys() { // Keys() returns copy of cache's key, safe to remove directly
		if key, ok := k.(string); ok && fn(key) {
			c.backend.Remove(key)
		}
	}
}

// Delete cache item by key
func (c *LruCache) Delete(key string) {
	c.backend.Remove(key)
}

// Keys returns cache keys
func (c *LruCache) Keys() (res []string) {
	keys := c.backend.Keys()
	res = make([]string, 0, len(keys))
	for _, key := range keys {
		res = append(res, key.(string))
	}
	return res
}

// Stat returns cache statistics
func (c *LruCache) Stat() CacheStat {
	return CacheStat{
		Hits:   c.Hits,
		Misses: c.Misses,
		Size:   c.size(),
		Keys:   c.keys(),
		Errors: c.Errors,
	}
}

// Close does nothing for this type of cache
func (c *LruCache) Close() error {
	return nil
}

// onBusEvent reacts on invalidation message triggered by event bus from another cache instance
func (c *LruCache) onBusEvent(id, key string) {
	if id != c.id && c.backend.Contains(key) { // prevent reaction on event from this cache
		c.backend.Remove(key)
	}
}

func (c *LruCache) size() int64 {
	return atomic.LoadInt64(&c.currentSize)
}

func (c *LruCache) keys() int {
	return c.backend.Len()
}

func (c *LruCache) allowed(key string, data any) bool {
	if c.maxKeySize > 0 && len(key) > c.maxKeySize {
		return false
	}
	if size, ok := sizeOf(data); ok {
		if c.maxValueSize > 0 && size >= c.maxValueSize {
			return false
		}
	}
	return true
}
