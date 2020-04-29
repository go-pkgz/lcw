package lcw

import (
	"sync/atomic"
	"time"

	"github.com/bluele/gcache"
	"github.com/pkg/errors"
)

// ExpirableCache implements LoadingCache with TTL.
type ExpirableCache struct {
	options
	CacheStat
	currentSize int64
	backend     gcache.Cache
}

// NewExpirableCache makes expirable LoadingCache implementation, 1000 max keys by default and 5s TTL
func NewExpirableCache(opts ...Option) (*ExpirableCache, error) {
	res := ExpirableCache{
		options: options{
			maxKeys:      1000,
			maxValueSize: 0,
			ttl:          5 * time.Minute,
		},
	}

	for _, opt := range opts {
		if err := opt(&res.options); err != nil {
			return nil, errors.Wrap(err, "failed to set cache option")
		}
	}

	c := gcache.New(res.maxKeys).LRU().EvictedFunc(
		// EvictedFunc called automatically for expired and manually deleted
		func(key, value interface{}) {
			if res.onEvicted != nil {
				res.onEvicted(key.(string), value)
			}
			if s, ok := value.(Sizer); ok {
				size := s.Size()
				atomic.AddInt64(&res.currentSize, -1*int64(size))
			}
		}).Build()

	res.backend = c

	return &res, nil
}

// Get gets value by key or load with fn if not found in cache
func (c *ExpirableCache) Get(key string, fn func() (Value, error)) (data Value, err error) {
	if v, e := c.backend.Get(key); e == nil {
		atomic.AddInt64(&c.Hits, 1)
		return v, nil
	}

	if data, err = fn(); err != nil {
		atomic.AddInt64(&c.Errors, 1)
		return data, err
	}
	atomic.AddInt64(&c.Misses, 1)

	if c.allowed(key, data) {
		if s, ok := data.(Sizer); ok {
			if c.maxCacheSize > 0 && atomic.LoadInt64(&c.currentSize)+int64(s.Size()) >= c.maxCacheSize {
				return data, nil
			}
			atomic.AddInt64(&c.currentSize, int64(s.Size()))
		}
		err = c.backend.SetWithExpire(key, data, c.ttl)
	}

	return data, err
}

// Invalidate removes keys with passed predicate fn, i.e. fn(key) should be true to get evicted
func (c *ExpirableCache) Invalidate(fn func(key string) bool) {
	for _, key := range c.backend.Keys(true) { // Keys() returns copy of cache's key, safe to remove directly
		if fn(key.(string)) {
			c.backend.Remove(key)
		}
	}
}

// Peek returns the key value (or undefined if not found) without updating the "recently used"-ness of the key.
func (c *ExpirableCache) Peek(key string) (Value, bool) {
	v, err := c.backend.Get(key)
	if err != nil {
		return nil, false
	}
	return v, true
}

// Purge clears the cache completely.
func (c *ExpirableCache) Purge() {
	c.backend.Purge()
	atomic.StoreInt64(&c.currentSize, 0)
}

// Delete cache item by key
func (c *ExpirableCache) Delete(key string) {
	c.backend.Remove(key)
}

// Keys returns cache keys
func (c *ExpirableCache) Keys() (res []string) {
	items := c.backend.Keys(true)
	res = make([]string, 0, len(items))
	for _, key := range items {
		res = append(res, key.(string))
	}
	return res
}

// Stat returns cache statistics
func (c *ExpirableCache) Stat() CacheStat {
	return CacheStat{
		Hits:   c.Hits,
		Misses: c.Misses,
		Size:   c.size(),
		Keys:   c.keys(),
		Errors: c.Errors,
	}
}

// Close does nothing for this type of cache
func (c *ExpirableCache) Close() error {
	return nil
}

func (c *ExpirableCache) size() int64 {
	return atomic.LoadInt64(&c.currentSize)
}

func (c *ExpirableCache) keys() int {
	return len(c.backend.Keys(true))
}

func (c *ExpirableCache) allowed(key string, data Value) bool {
	if len(c.backend.Keys(true)) >= c.maxKeys {
		return false
	}
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
