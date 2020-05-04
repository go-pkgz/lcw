package lcw

import (
	"sync/atomic"

	"github.com/pkg/errors"

	"github.com/go-pkgz/lcw/internal/cache"
)

// LruCache wraps LoadingCache with loading cache Get and size limits
type LruCache struct {
	options
	CacheStat
	backend     *cache.LoadingCache
	currentSize int64
}

// NewLruCache makes LRU LoadingCache implementation, 1000 max keys by default
func NewLruCache(opts ...Option) (*LruCache, error) {
	res := LruCache{
		options: options{
			maxKeys:      1000,
			maxValueSize: 0,
		},
	}
	for _, opt := range opts {
		if err := opt(&res.options); err != nil {
			return nil, errors.Wrap(err, "failed to set cache option")
		}
	}

	backend, err := cache.NewLoadingCache(
		cache.LRU(),
		cache.MaxKeys(res.maxKeys),
		cache.OnEvicted(func(key string, value interface{}) {
			if res.onEvicted != nil {
				res.onEvicted(key, value)
			}
			if s, ok := value.(Sizer); ok {
				size := s.Size()
				atomic.AddInt64(&res.currentSize, -1*int64(size))
			}
		}),
	)
	if err != nil {
		return nil, errors.Wrap(err, "error creating backend")
	}
	res.backend = backend

	return &res, nil
}

// Get gets value by key or load with fn if not found in cache
func (c *LruCache) Get(key string, fn func() (Value, error)) (data Value, err error) {
	if v, ok := c.backend.Get(key); ok {
		atomic.AddInt64(&c.Hits, 1)
		return v, nil
	}

	if data, err = fn(); err != nil {
		atomic.AddInt64(&c.Errors, 1)
		return data, err
	}

	atomic.AddInt64(&c.Misses, 1)

	if c.allowed(key, data) {
		c.backend.Set(key, data)

		if s, ok := data.(Sizer); ok {
			atomic.AddInt64(&c.currentSize, int64(s.Size()))
			if c.maxCacheSize > 0 && atomic.LoadInt64(&c.currentSize) > c.maxCacheSize {
				for atomic.LoadInt64(&c.currentSize) > c.maxCacheSize {
					c.backend.RemoveOldest()
				}
			}
		}
	}
	return data, nil
}

// Peek returns the key value (or undefined if not found) without updating the "recently used"-ness of the key.
func (c *LruCache) Peek(key string) (Value, bool) {
	return c.backend.Peek(key)
}

// Purge clears the cache completely.
func (c *LruCache) Purge() {
	c.backend.Purge()
	atomic.StoreInt64(&c.currentSize, 0)
}

// Invalidate removes keys with passed predicate fn, i.e. fn(key) should be true to get evicted
func (c *LruCache) Invalidate(fn func(key string) bool) {
	c.backend.InvalidateFn(fn)
}

// Delete cache item by key
func (c *LruCache) Delete(key string) {
	c.backend.Invalidate(key)
}

// Keys returns cache keys
func (c *LruCache) Keys() (res []string) {
	return c.backend.Keys()
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

// Close kills cleanup goroutine
func (c *LruCache) Close() error {
	c.backend.Close()
	return nil
}

func (c *LruCache) size() int64 {
	return atomic.LoadInt64(&c.currentSize)
}

func (c *LruCache) keys() int {
	return c.backend.ItemCount()
}

func (c *LruCache) allowed(key string, data Value) bool {
	if c.maxKeySize > 0 && len(key) > c.maxKeySize {
		return false
	}
	if s, ok := data.(Sizer); ok {
		if c.maxValueSize > 0 && s.Size() >= c.maxValueSize {
			return false
		}
	}
	return true
}
