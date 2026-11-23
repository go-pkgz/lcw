package lcw

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-pkgz/lcw/eventbus"
	"github.com/go-pkgz/lcw/internal/cache"
	"github.com/google/uuid"
)

// ExpirableCache implements LoadingCache with TTL.
type ExpirableCache struct {
	options
	CacheStat
	currentSize int64
	id          string
	backend     *cache.LoadingCache
	loads       loadGroup
	storeMu     sync.Mutex
}

// NewExpirableCache makes expirable LoadingCache implementation, 1000 max keys by default and 5m TTL
func NewExpirableCache(opts ...Option) (*ExpirableCache, error) {
	res := ExpirableCache{
		options: options{
			maxKeys:      1000,
			maxValueSize: 0,
			ttl:          5 * time.Minute,
			eventBus:     &eventbus.NopPubSub{},
		},
		id: uuid.New().String(),
	}

	for _, opt := range opts {
		if err := opt(&res.options); err != nil {
			return nil, fmt.Errorf("failed to set cache option: %w", err)
		}
	}

	if err := res.eventBus.Subscribe(res.onBusEvent); err != nil {
		return nil, fmt.Errorf("can't subscribe to event bus: %w", err)
	}

	backend, err := cache.NewLoadingCache(
		cache.MaxKeys(res.maxKeys),
		cache.TTL(res.ttl),
		cache.PurgeEvery(res.ttl/2),
		cache.OnEvicted(res.onBackendEvicted),
	)
	if err != nil {
		return nil, fmt.Errorf("error creating backend: %w", err)
	}
	res.backend = backend

	return &res, nil
}

// onBackendEvicted is called by the backend for every entry it removes
func (c *ExpirableCache) onBackendEvicted(key string, value any) {
	if c.onEvicted != nil {
		c.onEvicted(key, value)
	}
	if size, ok := sizeOf(value); ok {
		atomic.AddInt64(&c.currentSize, -1*int64(size))
	}
	// ignore the error on Publish as we don't have log inside the module and
	// there is no other way to handle it: we publish the cache invalidation
	// and hope for the best
	_ = c.eventBus.Publish(c.id, key)
}

// Get gets value by key or load with fn if not found in cache
func (c *ExpirableCache) Get(key string, fn func() (any, error)) (data any, err error) {
	if v, ok := c.backend.Get(key); ok {
		atomic.AddInt64(&c.Hits, 1)
		return v, nil
	}

	// concurrent callers for the same key wait for the first load instead of loading on their own,
	// otherwise each of them would set the value and count its size again
	var expired any
	var replaced, full bool
	data, err = c.loads.do(key, func() (any, error) {
		if v, ok := c.backend.Get(key); ok { // filled by the load we were waiting for
			atomic.AddInt64(&c.Hits, 1)
			return v, nil
		}

		loaded, loadErr := fn()
		if loadErr != nil {
			atomic.AddInt64(&c.Errors, 1)
			return loaded, loadErr
		}
		atomic.AddInt64(&c.Misses, 1)

		expired, replaced, full = c.store(key, loaded)
		return loaded, nil
	})

	// OnEvicted is called only after the load of this key is over, as it may call back into the cache,
	// for this key too: for the expired entry store removed, and for the entries DeleteExpired removes
	if replaced && c.onEvicted != nil {
		c.onEvicted(key, expired)
	}
	if full {
		c.backend.DeleteExpired()
	}
	return data, err
}

// store adds data to the backend if the limits allow it and reports whether MaxCacheSize refused it.
// the checks and the insert run under one lock, otherwise concurrent loads of different keys
// all pass the checks and overshoot MaxKeys and MaxCacheSize.
// It also returns the expired entry of the key it removed, for the caller to report to OnEvicted.
func (c *ExpirableCache) store(key string, data any) (expired any, replaced, full bool) {
	c.storeMu.Lock()
	defer c.storeMu.Unlock()

	// an entry of this key still in the backend has expired, a live one would have been served instead
	// of the load. It stays until the next purge, counting against MaxKeys, and Set would replace it
	// without subtracting its size, so it is removed here and its size subtracted. Its key is not
	// published on the event bus, as this key is being reloaded, even if the limits refuse the new value.
	if expired, replaced = c.backend.Remove(key); replaced {
		if size, ok := sizeOf(expired); ok {
			atomic.AddInt64(&c.currentSize, -1*int64(size))
		}
	}

	if !c.allowed(key, data) {
		return expired, replaced, false
	}

	if size, ok := sizeOf(data); ok {
		if c.maxCacheSize > 0 && atomic.LoadInt64(&c.currentSize)+int64(size) > c.maxCacheSize {
			return expired, replaced, true
		}
		atomic.AddInt64(&c.currentSize, int64(size))
	}

	c.backend.Set(key, data)
	return expired, replaced, false
}

// Invalidate removes keys with passed predicate fn, i.e. fn(key) should be true to get evicted
func (c *ExpirableCache) Invalidate(fn func(key string) bool) {
	c.backend.InvalidateFn(fn)
}

// Peek returns the key value (or undefined if not found) without updating the "recently used"-ness of the key.
func (c *ExpirableCache) Peek(key string) (any, bool) {
	return c.backend.Peek(key)
}

// Purge clears the cache completely.
func (c *ExpirableCache) Purge() {
	c.backend.Purge()
	atomic.StoreInt64(&c.currentSize, 0)
}

// Delete cache item by key
func (c *ExpirableCache) Delete(key string) {
	c.backend.Invalidate(key)
}

// Keys returns cache keys
func (c *ExpirableCache) Keys() (res []string) {
	return c.backend.Keys()
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

// Close kills cleanup goroutine
func (c *ExpirableCache) Close() error {
	c.backend.Close()
	return nil
}

// onBusEvent reacts on invalidation message triggered by event bus from another cache instance
func (c *ExpirableCache) onBusEvent(id, key string) {
	if id != c.id {
		c.backend.Invalidate(key)
	}
}

func (c *ExpirableCache) size() int64 {
	return atomic.LoadInt64(&c.currentSize)
}

func (c *ExpirableCache) keys() int {
	return c.backend.ItemCount()
}

func (c *ExpirableCache) allowed(key string, data any) bool {
	if c.maxKeys > 0 && c.backend.ItemCount() >= c.maxKeys {
		return false
	}
	if c.maxKeySize > 0 && len(key) > c.maxKeySize {
		return false
	}
	if size, ok := sizeOf(data); ok {
		if c.maxValueSize > 0 && size > c.maxValueSize {
			return false
		}
	}
	return true
}
