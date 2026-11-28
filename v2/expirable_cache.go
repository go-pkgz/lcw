package lcw

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/golang-lru/v2/expirable"

	"github.com/go-pkgz/lcw/v2/eventbus"
)

// ExpirableCache implements LoadingCache with TTL.
type ExpirableCache[V any] struct {
	Workers[V]
	CacheStat
	currentSize atomic.Int64
	id          string
	backend     *expirable.LRU[string, V]
	loads       loadGroup[V]
	storeMu     sync.Mutex
	closed      atomic.Bool            // set by Close, evictions reported after it are not published on the event bus
	storing     atomic.Pointer[string] // key whose expired entry store is removing, its removal is not published
}

// NewExpirableCache makes expirable LoadingCache implementation, 1000 max keys by default and 5m TTL.
// Note that the underlying hashicorp/golang-lru expirable backend calls eviction callbacks while
// holding its own lock, so an OnEvicted handler must not call back into the same cache, it would deadlock.
// The same handler is safe to use with LruCache and with the v1 ExpirableCache.
// Reported upstream as https://github.com/hashicorp/golang-lru/issues/230
func NewExpirableCache[V any](opts ...Option[V]) (*ExpirableCache[V], error) {
	res := ExpirableCache[V]{
		Workers: Workers[V]{
			maxKeys:      1000,
			maxValueSize: 0,
			ttl:          5 * time.Minute,
			eventBus:     &eventbus.NopPubSub{},
		},
		id: uuid.New().String(),
	}

	for _, opt := range opts {
		if err := opt(&res.Workers); err != nil {
			return nil, fmt.Errorf("failed to set cache option: %w", err)
		}
	}

	if err := res.eventBus.Subscribe(res.onBusEvent); err != nil {
		return nil, fmt.Errorf("can't subscribe to event bus: %w", err)
	}

	res.backend = expirable.NewLRU[string, V](res.maxKeys, func(key string, value V) {
		// read before OnEvicted: Close sets the flag without the backend lock, and a Close that starts while the
		// handler runs must not stop this removal from being published. The callbacks of Close's own purge read true
		closed := res.closed.Load()
		if res.onEvicted != nil {
			res.onEvicted(key, value)
		}
		if size, ok := sizeOf(value); ok {
			res.currentSize.Add(-1 * int64(size))
		}
		if closed {
			return // a closed cache drops its entries locally, other instances keep their copies
		}
		if k := res.storing.Load(); k != nil && *k == key && res.storing.CompareAndSwap(k, nil) {
			return // the expired entry store removes, see there
		}
		// ignore the error on Publish as we don't have log inside the module and
		// there is no other way to handle it: we publish the cache invalidation
		// and hope for the best
		_ = res.eventBus.Publish(res.id, key)
	}, res.ttl)

	return &res, nil
}

// Get gets value by key or load with fn if not found in cache
func (c *ExpirableCache[V]) Get(key string, fn func() (V, error)) (data V, err error) {
	if v, ok := c.backend.Get(key); ok {
		atomic.AddInt64(&c.Hits, 1)
		return v, nil
	}

	// concurrent callers for the same key wait for the first load instead of loading on their own,
	// otherwise each of them would add the value and count its size again
	return c.loads.do(key, func() (V, error) {
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

		c.store(key, data)
		return data, nil
	})
}

// store adds data to the backend if the limits allow it.
// the checks and the insert run under one lock, otherwise concurrent loads of different keys
// all pass the checks and overshoot MaxCacheSize, or evict entries to stay within MaxKeys.
func (c *ExpirableCache[V]) store(key string, data V) {
	c.storeMu.Lock()
	defer c.storeMu.Unlock()

	// an entry of this key still in the backend has expired, a live one would have been served instead
	// of the load. It stays until the backend cleans it up, counting against MaxKeys, and Add would
	// replace it without calling the eviction callback, so its size would never be subtracted. It is
	// removed here, through the callback, which does not publish the key on the event bus, as this key is
	// being reloaded, even if the limits refuse the new value. The callback takes the marker off for the
	// first removal of the key it sees; when that one was not this Remove, another one (Delete, cleanup)
	// took the entry and was not published, so it is published here.
	marker := &key
	c.storing.Store(marker)
	removed := c.backend.Remove(key)
	if !c.storing.CompareAndSwap(marker, nil) && !removed {
		_ = c.eventBus.Publish(c.id, key)
	}

	if !c.allowed(key, data) {
		return
	}

	if size, ok := sizeOf(data); ok {
		if c.maxCacheSize > 0 && c.currentSize.Load()+int64(size) > c.maxCacheSize {
			return
		}
		c.currentSize.Add(int64(size))
	}

	c.backend.Add(key, data)
}

// Invalidate removes keys with passed predicate fn, i.e. fn(key) should be true to get evicted
func (c *ExpirableCache[V]) Invalidate(fn func(key string) bool) {
	for _, key := range c.backend.Keys() {
		if fn(key) {
			c.backend.Remove(key)
		}
	}
}

// Peek returns the key value (or undefined if not found) without updating the "recently used"-ness of the key.
func (c *ExpirableCache[V]) Peek(key string) (V, bool) {
	return c.backend.Peek(key)
}

// Purge clears the cache completely.
func (c *ExpirableCache[V]) Purge() {
	c.backend.Purge()
	c.currentSize.Store(0)
}

// Delete cache item by key
func (c *ExpirableCache[V]) Delete(key string) {
	c.backend.Remove(key)
}

// Keys returns cache keys
func (c *ExpirableCache[V]) Keys() (res []string) {
	return c.backend.Keys()
}

// Stat returns cache statistics
func (c *ExpirableCache[V]) Stat() CacheStat {
	return CacheStat{
		Hits:   c.Hits,
		Misses: c.Misses,
		Size:   c.size(),
		Keys:   c.keys(),
		Errors: c.Errors,
	}
}

// Close removes all entries, calling OnEvicted for each of them, and publishes none of them on the event bus,
// so closing one instance does not invalidate these keys in other instances. Removals the backend reports
// after Close starts are not published either. A removal by Delete, Invalidate, Purge, expiry or an
// invalidation from another instance that the backend reported before Close started is still published,
// even if its OnEvicted handler returns after Close; if a reload of the same key was storing its value at
// that time, the reload may publish the removal in its place. The expired entry a reload replaces is never
// published.
// With a TTL other than 0 the backend runs a cleanup goroutine, which Close can't stop before
// https://github.com/hashicorp/golang-lru/issues/159 is solved, so that goroutine keeps running.
func (c *ExpirableCache[V]) Close() error {
	c.closed.Store(true)
	c.backend.Purge()
	c.currentSize.Store(0)
	return nil
}

// onBusEvent reacts on invalidation message triggered by event bus from another cache instance
func (c *ExpirableCache[V]) onBusEvent(id, key string) {
	if id != c.id {
		c.backend.Remove(key)
	}
}

func (c *ExpirableCache[V]) size() int64 {
	return c.currentSize.Load()
}

func (c *ExpirableCache[V]) keys() int {
	return c.backend.Len()
}

func (c *ExpirableCache[V]) allowed(key string, data V) bool {
	if c.maxKeys > 0 && c.backend.Len() >= c.maxKeys {
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
