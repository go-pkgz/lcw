package lcw

import (
	"strconv"
	"sync/atomic"
	"time"

	"github.com/dgraph-io/ristretto"
	"github.com/pkg/errors"
)

// ExpirableCache implements LoadingCache with TTL.
type ExpirableCache struct {
	options
	CacheStat
	currentSize int64
	keysStorage map[string]struct{}
	backend     *ristretto.Cache
}

// NewExpirableCache makes expirable LoadingCache implementation, 1000 max keys by default and 5s TTL
func NewExpirableCache(opts ...Option) (*ExpirableCache, error) {
	res := ExpirableCache{
		options: options{
			maxKeys:      1000,
			maxValueSize: 0,
			ttl:          5 * time.Minute,
		},
		keysStorage: map[string]struct{}{},
	}

	for _, opt := range opts {
		if err := opt(&res.options); err != nil {
			return nil, errors.Wrap(err, "failed to set cache option")
		}
	}

	// TODO how to delete key from res.keysStorage here?
	onEvict := func(key, _ uint64, value interface{}, _ int64) {
		if res.onEvicted != nil {
			res.onEvicted(strconv.FormatUint(key, 10), value)
		}
		if s, ok := value.(Sizer); ok {
			size := s.Size()
			atomic.AddInt64(&res.currentSize, -1*int64(size))
		}
	}

	cache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: int64(res.options.maxKeys * 10), // number of keys to track frequency of (10x max keys).
		MaxCost:     int64(res.options.maxKeys),      // maximum cost of cache (equal to amount of keys).
		BufferItems: 64,                              // number of keys per Get buffer.
		OnEvict:     onEvict,                         // OnEvict called automatically for expired and manually deleted
		Metrics:     true,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to set cache option")
	}
	res.backend = cache

	return &res, nil
}

// Get gets value by key or load with fn if not found in cache
func (c *ExpirableCache) Get(key string, fn func() (Value, error)) (data Value, err error) {
	if v, ok := c.backend.Get(key); ok {
		atomic.AddInt64(&c.Hits, 1)
		return v, nil
	}

	if data, err = fn(); err != nil {
		atomic.AddInt64(&c.Errors, 1)
		return data, err
	}
	atomic.AddInt64(&c.Misses, 1)

	if s, ok := data.(Sizer); ok {
		atomic.AddInt64(&c.currentSize, int64(s.Size()))
	}
	c.backend.SetWithTTL(key, data, 1, c.ttl)
	c.keysStorage[key] = struct{}{}

	return data, nil
}

// Invalidate removes keys with passed predicate fn, i.e. fn(key) should be true to get evicted
func (c *ExpirableCache) Invalidate(fn func(key string) bool) {
	for key := range c.keysStorage {
		if fn(key) {
			c.backend.Del(key)
		}
	}
}

// Peek returns the key value (or undefined if not found) without updating the "recently used"-ness of the key.
func (c *ExpirableCache) Peek(key string) (Value, bool) {
	return c.backend.Get(key)
}

// Purge clears the cache completely.
func (c *ExpirableCache) Purge() {
	c.backend.Clear()
	atomic.StoreInt64(&c.currentSize, 0)
}

// Delete cache item by key
func (c *ExpirableCache) Delete(key string) {
	c.backend.Del(key)
}

// Keys returns cache keys
func (c *ExpirableCache) Keys() (res []string) {
	res = make([]string, 0, len(c.keysStorage))
	for key := range c.keysStorage {
		res = append(res, key)
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
	return int(c.backend.Metrics.KeysAdded() - c.backend.Metrics.KeysEvicted())
}
