// Package cache implements LoadingCache similar to hashicorp/golang-lru
//
// Support LRC, LRU and TTL-based eviction.
package cache

import (
	"sync"
	"time"

	"github.com/pkg/errors"
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

	sync.Mutex
	data      map[string]*cacheItem
	evictList *list
}

// noEvictionTTL - very long ttl to prevent eviction
const noEvictionTTL = time.Hour * 24 * 365 * 10

// NewLoadingCache returns a new cache, activates deleteExpired with PurgeEvery (0 to never deleteExpired).
// Default MaxKeys is unlimited (0).
// If MaxKeys and TTL are defined and PurgeEvery is zero, PurgeEvery will be set to 5 minutes.
func NewLoadingCache(options ...Option) (*LoadingCache, error) {
	res := LoadingCache{
		data:       map[string]*cacheItem{},
		evictList:  newList(),
		ttl:        noEvictionTTL,
		purgeEvery: 0,
		maxKeys:    0,
		done:       make(chan struct{}),
	}

	for _, opt := range options {
		if err := opt(&res); err != nil {
			return nil, errors.Wrap(err, "failed to set cache option")
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
					res.Lock()
					res.deleteExpired()
					res.Unlock()
				}
			}
		}(res.done)
	}
	return &res, nil
}

// Set key
func (c *LoadingCache) Set(key string, value interface{}) {
	c.Lock()
	defer c.Unlock()
	now := time.Now()

	// Check for existing item
	if ent, ok := c.data[key]; ok {
		c.evictList.MoveToFront(ent)
		ent.value = value
		ent.expiresAt = now.Add(c.ttl)
		return
	}

	// Add new item
	ent := &cacheItem{key: key, value: value, expiresAt: now.Add(c.ttl)}
	c.evictList.PushFront(ent)
	c.data[key] = ent

	// Verify size not exceeded
	if c.maxKeys > 0 && int64(len(c.data)) > c.maxKeys {
		c.removeOldest()
	}
}

// Get returns the key value
func (c *LoadingCache) Get(key string) (interface{}, bool) {
	c.Lock()
	defer c.Unlock()
	if ent, ok := c.data[key]; ok {
		// Expired item check
		if time.Now().After(ent.expiresAt) {
			return nil, false
		}
		if c.isLRU {
			c.evictList.MoveToFront(ent)
		}
		return ent.value, true
	}
	return nil, false
}

// Peek returns the key value (or undefined if not found) without updating the "recently used"-ness of the key.
func (c *LoadingCache) Peek(key string) (interface{}, bool) {
	c.Lock()
	defer c.Unlock()
	if ent, ok := c.data[key]; ok {
		// Expired item check
		if time.Now().After(ent.expiresAt) {
			return nil, false
		}
		return ent.value, true
	}
	return nil, false
}

// Invalidate key (item) from the cache
func (c *LoadingCache) Invalidate(key string) {
	c.Lock()
	defer c.Unlock()
	if ent, ok := c.data[key]; ok {
		c.removeElement(ent)
	}
}

// InvalidateFn deletes multiple keys if predicate is true
func (c *LoadingCache) InvalidateFn(fn func(key string) bool) {
	c.Lock()
	defer c.Unlock()
	for key, ent := range c.data {
		if fn(key) {
			c.removeElement(ent)
		}
	}
}

// RemoveOldest remove oldest element in the cache
func (c *LoadingCache) RemoveOldest() {
	c.Lock()
	defer c.Unlock()
	c.removeOldest()
}

// Keys returns a slice of the keys in the cache, from oldest to newest.
func (c *LoadingCache) Keys() []string {
	c.Lock()
	defer c.Unlock()
	return c.keys()
}

// Purge clears the cache completely.
func (c *LoadingCache) Purge() {
	c.Lock()
	defer c.Unlock()
	for k, v := range c.data {
		if c.onEvicted != nil {
			c.onEvicted(k, v.value)
		}
		delete(c.data, k)
	}
	c.evictList.Init()
}

// DeleteExpired clears cache of expired items
func (c *LoadingCache) DeleteExpired() {
	c.Lock()
	defer c.Unlock()
	c.deleteExpired()
}

// ItemCount return count of items in cache
func (c *LoadingCache) ItemCount() int {
	c.Lock()
	defer c.Unlock()
	return c.evictList.Len()
}

// Close cleans the cache and destroys running goroutines
func (c *LoadingCache) Close() {
	c.Lock()
	defer c.Unlock()
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
		keys = append(keys, ent.key)
	}
	return keys
}

// removeElement is used to remove a given list element from the cache. Has to be called with lock!
func (c *LoadingCache) removeElement(e *cacheItem) {
	c.evictList.Remove(e)
	delete(c.data, e.key)
	if c.onEvicted != nil {
		c.onEvicted(e.key, e.value)
	}
}

// deleteExpired deletes expired records. Has to be called with lock!
func (c *LoadingCache) deleteExpired() {
	for _, key := range c.keys() {
		if time.Now().After(c.data[key].expiresAt) {
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

	// Next and previous pointers in the doubly-linked list of elements.
	// To simplify the implementation, internally a list l is implemented
	// as a ring, such that &l.root is both the next element of the last
	// list element (l.Back()) and the previous element of the first list
	// element (l.Front()).
	next, prev *cacheItem

	// The list to which this element belongs.
	list *list
}
