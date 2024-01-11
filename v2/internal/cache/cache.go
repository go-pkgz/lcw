// Package cache implements LoadingCache.
//
// Support LRC TTL-based eviction.
package cache

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// LoadingCache provides expirable loading cache with LRC eviction.
type LoadingCache[V any] struct {
	purgeEvery time.Duration
	ttl        time.Duration
	maxKeys    int64
	done       chan struct{}
	onEvicted  func(key string, value V)

	mu   sync.Mutex
	data map[string]*cacheItem[V]
}

// noEvictionTTL - very long ttl to prevent eviction
const noEvictionTTL = time.Hour * 24 * 365 * 10

// NewLoadingCache returns a new expirable LRC cache, activates purge with purgeEvery (0 to never purge).
// Default MaxKeys is unlimited (0).
func NewLoadingCache[V any](options ...Option[V]) (*LoadingCache[V], error) {
	res := LoadingCache[V]{
		data:       map[string]*cacheItem[V]{},
		ttl:        noEvictionTTL,
		purgeEvery: 0,
		maxKeys:    0,
		done:       make(chan struct{}),
	}

	for _, opt := range options {
		if err := opt(&res); err != nil {
			return nil, fmt.Errorf("failed to set cache option: %w", err)
		}
	}

	if res.maxKeys > 0 || res.purgeEvery > 0 {
		if res.purgeEvery == 0 {
			res.purgeEvery = time.Minute * 5 // non-zero purge enforced because maxKeys defined
		}
		go func(done <-chan struct{}) {
			ticker := time.NewTicker(res.purgeEvery)
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					res.mu.Lock()
					res.purge(res.maxKeys)
					res.mu.Unlock()
				}
			}
		}(res.done)
	}
	return &res, nil
}

// Set key
func (c *LoadingCache[V]) Set(key string, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	if _, ok := c.data[key]; !ok {
		c.data[key] = &cacheItem[V]{}
	}
	c.data[key].data = value
	c.data[key].expiresAt = now.Add(c.ttl)

	// Enforced purge call in addition the one from the ticker
	// to limit the worst-case scenario with a lot of sets in the
	// short period of time (between two timed purge calls)
	if c.maxKeys > 0 && int64(len(c.data)) >= c.maxKeys*2 {
		c.purge(c.maxKeys)
	}
}

// Get returns the key value
func (c *LoadingCache[V]) Get(key string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.getValue(key)
	if !ok {
		var emptyValue V
		return emptyValue, false
	}
	return value, ok
}

// Peek returns the key value (or undefined if not found) without updating the "recently used"-ness of the key.
func (c *LoadingCache[V]) Peek(key string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.getValue(key)
	if !ok {
		var emptyValue V
		return emptyValue, false
	}
	return value, ok
}

// Invalidate key (item) from the cache
func (c *LoadingCache[V]) Invalidate(key string) {
	c.mu.Lock()
	if value, ok := c.data[key]; ok {
		delete(c.data, key)
		if c.onEvicted != nil {
			c.onEvicted(key, value.data)
		}
	}
	c.mu.Unlock()
}

// InvalidateFn deletes multiple keys if predicate is true
func (c *LoadingCache[V]) InvalidateFn(fn func(key string) bool) {
	c.mu.Lock()
	for key, value := range c.data {
		if fn(key) {
			delete(c.data, key)
			if c.onEvicted != nil {
				c.onEvicted(key, value.data)
			}
		}
	}
	c.mu.Unlock()
}

// Keys return slice of current keys in the cache
func (c *LoadingCache[V]) Keys() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := make([]string, 0, len(c.data))
	for k := range c.data {
		keys = append(keys, k)
	}
	return keys
}

// get value respecting the expiration, should be called with lock
func (c *LoadingCache[V]) getValue(key string) (V, bool) {
	value, ok := c.data[key]
	if !ok {
		var emptyValue V
		return emptyValue, false
	}
	if time.Now().After(c.data[key].expiresAt) {
		var emptyValue V
		return emptyValue, false
	}
	return value.data, ok
}

// Purge clears the cache completely.
func (c *LoadingCache[V]) Purge() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range c.data {
		delete(c.data, k)
		if c.onEvicted != nil {
			c.onEvicted(k, v.data)
		}
	}
}

// DeleteExpired clears cache of expired items
func (c *LoadingCache[V]) DeleteExpired() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.purge(0)
}

// ItemCount return count of items in cache
func (c *LoadingCache[V]) ItemCount() int {
	c.mu.Lock()
	n := len(c.data)
	c.mu.Unlock()
	return n
}

// Close cleans the cache and destroys running goroutines
func (c *LoadingCache[V]) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	// don't panic in case service is already closed
	select {
	case <-c.done:
		return
	default:
	}
	close(c.done)
}

// keysWithTS includes list of keys with ts. This is for sorting keys
// in order to provide least recently added sorting for size-based eviction
type keysWithTS []struct {
	key string
	ts  time.Time
}

// purge records > maxKeys. Has to be called with lock!
// call with maxKeys 0 will only clear expired entries.
func (c *LoadingCache[V]) purge(maxKeys int64) {
	kts := keysWithTS{}

	for key, value := range c.data {
		// ttl eviction
		if time.Now().After(c.data[key].expiresAt) {
			delete(c.data, key)
			if c.onEvicted != nil {
				c.onEvicted(key, value.data)
			}
		}

		// prepare list of keysWithTS for size eviction
		if maxKeys > 0 && int64(len(c.data)) > maxKeys {
			ts := c.data[key].expiresAt

			kts = append(kts, struct {
				key string
				ts  time.Time
			}{key, ts})
		}
	}

	// size eviction
	size := int64(len(c.data))
	if len(kts) > 0 {
		sort.Slice(kts, func(i int, j int) bool { return kts[i].ts.Before(kts[j].ts) })
		for d := 0; int64(d) < size-maxKeys; d++ {
			key := kts[d].key
			value := c.data[key].data
			delete(c.data, key)
			if c.onEvicted != nil {
				c.onEvicted(key, value)
			}
		}
	}
}

type cacheItem[V any] struct {
	expiresAt time.Time
	data      V
}
