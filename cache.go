package lcw

import (
	"sync/atomic"

	lru "github.com/hashicorp/golang-lru"
	"github.com/pkg/errors"
)

// Cache wraps lru.Cache with laoding cache Get and size limits
type Cache struct {
	backend      *lru.Cache
	maxKeys      int
	maxValueSize int
	maxKeySize   int
	maxCacheSize int64
	currentSize  int64
}

// NewCache makes LoadingCache (lru) implementation, 1000 max keys by default
func NewCache(options ...Option) (*Cache, error) {

	res := Cache{
		maxKeys:      1000,
		maxValueSize: 0,
	}
	for _, opt := range options {
		if err := opt(&res); err != nil {
			return nil, errors.Wrap(err, "failed to set cache option")
		}
	}

	onEvicted := func(key interface{}, value interface{}) {
		if s, ok := value.(Sizer); ok {
			size := s.Size()
			atomic.AddInt64(&res.currentSize, -1*int64(size))
		}
	}

	var err error
	// OnEvicted called automatically for expired and manually deleted
	if res.backend, err = lru.NewWithEvict(res.maxKeys, onEvicted); err != nil {
		return nil, errors.Wrap(err, "failed to make lru cache backend")
	}

	return &res, nil
}

// Get gets value by key or load with fn if not found in cache
func (c *Cache) Get(key string, fn func() (Value, error)) (data Value, err error) {

	if v, ok := c.backend.Get(key); ok {
		return v, nil
	}

	if data, err = fn(); err != nil {
		return data, err
	}

	if c.allowed(key, data) {
		c.backend.Add(key, data)

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
func (c *Cache) Peek(key string) (Value, bool) {
	return c.backend.Peek(key)
}

// Purge clears the cache completely.
func (c *Cache) Purge() {
	c.backend.Purge()
	atomic.StoreInt64(&c.currentSize, 0)
}

// Invalidate removes keys with passed predicate fn, i.e. fn(key) should be true to get evicted
func (c *Cache) Invalidate(fn func(key string) bool) {
	for _, k := range c.backend.Keys() { // Keys() returns copy of cache's key, safe to remove directly
		if key, ok := k.(string); ok && fn(key) {
			c.backend.Remove(key)
		}
	}
}

func (c *Cache) allowed(key string, data Value) bool {
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
