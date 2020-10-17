// Package cache implements LoadingCache similar to hashicorp/golang-lru
//
// Support LRC, LRU and TTL-based eviction.
package cache

import (
	"container/list"
	"fmt"
	"sync"
	"time"
)

// LoadingCache provides loading cache with on-demand loading, similar to Guava LoadingCache.
// Implements cache.LoadingCache. Cache is consistent, i.e. read guaranteed to get latest write,
// and no stale writes in any place. In order to provide such
// consistency LoadingCache locking on particular key, but no locks across multiple keys.
type LoadingCache struct {
	purgeEvery time.Duration
	ttl        time.Duration
	maxKeys    int64
	isLRU      bool
	done       chan struct{}
	onEvicted  func(key string, value interface{})

	mu        sync.Mutex
	data      map[string]*list.Element
	evictList *list.List
}

// noEvictionTTL - very long ttl to prevent eviction
const noEvictionTTL = time.Hour * 24 * 365 * 10

// NewLoadingCache returns a new cache, activates deleteExpired with PurgeEvery (0 to never deleteExpired).
// Default MaxKeys is unlimited (0).
// If MaxKeys and TTL are defined and PurgeEvery is zero, PurgeEvery will be set to 5 minutes.
func NewLoadingCache(options ...Option) (*LoadingCache, error) {
	res := LoadingCache{
		data:       map[string]*list.Element{},
		evictList:  list.New(),
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

	// enable deleteExpired() running in separate goroutine for cache
	// with non-zero TTL and maxKeys defined
	if res.ttl != noEvictionTTL && (res.maxKeys > 0 || res.purgeEvery > 0) {
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
					res.deleteExpired()
					res.mu.Unlock()
				}
			}
		}(res.done)
	}
	return &res, nil
}

// Set key
func (c *LoadingCache) Set(key string, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()

	// Check for existing item
	if ent, ok := c.data[key]; ok {
		c.evictList.MoveToFront(ent)
		ent.Value.(*cacheItem).value = value
		ent.Value.(*cacheItem).expiresAt = now.Add(c.ttl)
		return
	}

	// Add new item
	ent := &cacheItem{key: key, value: value, expiresAt: now.Add(c.ttl)}
	entry := c.evictList.PushFront(ent)
	c.data[key] = entry

	// Verify size not exceeded
	if c.maxKeys > 0 && int64(len(c.data)) > c.maxKeys {
		c.removeOldest()
	}
}

// Get returns the key value
func (c *LoadingCache) Get(key string) (interface{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ent, ok := c.data[key]; ok {
		// Expired item check
		if time.Now().After(ent.Value.(*cacheItem).expiresAt) {
			return nil, false
		}
		if c.isLRU {
			c.evictList.MoveToFront(ent)
		}
		return ent.Value.(*cacheItem).value, true
	}
	return nil, false
}

// Peek returns the key value (or undefined if not found) without updating the "recently used"-ness of the key.
func (c *LoadingCache) Peek(key string) (interface{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ent, ok := c.data[key]; ok {
		// Expired item check
		if time.Now().After(ent.Value.(*cacheItem).expiresAt) {
			return nil, false
		}
		return ent.Value.(*cacheItem).value, true
	}
	return nil, false
}

// Invalidate key (item) from the cache
func (c *LoadingCache) Invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ent, ok := c.data[key]; ok {
		c.removeElement(ent)
	}
}

// InvalidateFn deletes multiple keys if predicate is true
func (c *LoadingCache) InvalidateFn(fn func(key string) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, ent := range c.data {
		if fn(key) {
			c.removeElement(ent)
		}
	}
}

// RemoveOldest remove oldest element in the cache
func (c *LoadingCache) RemoveOldest() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeOldest()
}

// Keys returns a slice of the keys in the cache, from oldest to newest.
func (c *LoadingCache) Keys() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.keys()
}

// Purge clears the cache completely.
func (c *LoadingCache) Purge() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range c.data {
		if c.onEvicted != nil {
			c.onEvicted(k, v.Value.(*cacheItem).value)
		}
		delete(c.data, k)
	}
	c.evictList.Init()
}

// DeleteExpired clears cache of expired items
func (c *LoadingCache) DeleteExpired() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deleteExpired()
}

// ItemCount return count of items in cache
func (c *LoadingCache) ItemCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.evictList.Len()
}

// Close cleans the cache and destroys running goroutines
func (c *LoadingCache) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	close(c.done)
}

// removeOldest removes the oldest item from the cache. Has to be called with lock!
func (c *LoadingCache) removeOldest() {
	ent := c.evictList.Back()
	if ent != nil {
		c.removeElement(ent)
	}
}

// Keys returns a slice of the keys in the cache, from oldest to newest. Has to be called with lock!
func (c *LoadingCache) keys() []string {
	keys := make([]string, 0, len(c.data))
	for ent := c.evictList.Back(); ent != nil; ent = ent.Prev() {
		keys = append(keys, ent.Value.(*cacheItem).key)
	}
	return keys
}

// removeElement is used to remove a given list element from the cache. Has to be called with lock!
func (c *LoadingCache) removeElement(e *list.Element) {
	c.evictList.Remove(e)
	kv := e.Value.(*cacheItem)
	delete(c.data, kv.key)
	if c.onEvicted != nil {
		c.onEvicted(kv.key, kv.value)
	}
}

// deleteExpired deletes expired records. Has to be called with lock!
func (c *LoadingCache) deleteExpired() {
	for _, key := range c.keys() {
		if time.Now().After(c.data[key].Value.(*cacheItem).expiresAt) {
			c.removeElement(c.data[key])
			continue
		}
		// if cache is not LRU, keys() are sorted by expiresAt and there are no
		// more expired entries left at this point
		if !c.isLRU {
			return
		}
	}
}

// cacheItem is used to hold a value in the evictList
type cacheItem struct {
	expiresAt time.Time
	key       string
	value     interface{}
}
