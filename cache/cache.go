// Package cache implements LoadingCache similar to Guava's cache.
//
// Usual way is to use Get(key, ttl, func loader), but direct access to values
// possible with GetValue. Cache also keeps stats and exposes it with Stat method.
// Supported size-based eviction with LRC and LRU, and TTL based eviction.
package cache

import (
	"fmt"
	"log"
	"sort"
	"sync"
	"time"
)

// LoadingCache defines loading cache interface
type LoadingCache interface {
	fmt.Stringer
	Get(key string, ttl time.Duration, fn func() (data interface{}, err error)) (interface{}, error)
	GetValue(key string) (interface{}, bool)
	Invalidate(key string)
	InvalidateFn(fn func(key string) bool)
	Stat() Stats
}

// loadingCacheImpl provides loading cache with on-demand loading, similar to Guava LoadingCache.
// Implements cache.LoadingCache. Cache is consistent, i.e. read guaranteed to get latest write,
// and no stale writes in any place. In order to provide such
// consistency loadingCacheImpl locking on particular key, but no locks across multiple keys.
type loadingCacheImpl struct {
	purgeEvery time.Duration
	maxKeys    int
	allowError bool
	isLRU      bool

	sync.Mutex
	stat Stats
	data map[string]*cacheItem
}

// Stats provides statistics for cache
type Stats struct {
	Size           int // number of records in cache
	Hits, Misses   int // cache effectiveness
	Added, Evicted int // number of added and auto-evicted records
}

const noEvictionTTL = time.Hour * 24 * 365 * 10

// NewLoadingCache returns a new cache, activates purge with purgeEvery (0 to never purge)
func NewLoadingCache(options ...Option) LoadingCache {
	res := loadingCacheImpl{
		data:       map[string]*cacheItem{},
		purgeEvery: 0,
		maxKeys:    0,
	}

	for _, opt := range options {
		if err := opt(&res); err != nil {
			log.Printf("[WARN] failed to set cache option, %v", err)
		}
	}

	if res.maxKeys > 0 || res.purgeEvery > 0 {
		if res.purgeEvery == 0 {
			res.purgeEvery = time.Minute * 5 // non-zero purge enforced because maxKeys defined
		}
		go func() {
			ticker := time.NewTicker(res.purgeEvery)
			for range ticker.C {
				res.Lock()
				res.purge(res.maxKeys)
				res.Unlock()
			}
		}()
	}
	log.Printf("[DEBUG] cache created. purge=%v, max=%d", res.purgeEvery, res.maxKeys)
	return &res
}

// Get by key if found, or get value and return
func (c *loadingCacheImpl) Get(key string, ttl time.Duration, f func() (interface{}, error)) (interface{}, error) {

	c.Lock()
	var ci *cacheItem
	if ci = c.data[key]; ci == nil {

		itemTTL := ttl
		if ttl == 0 {
			itemTTL = noEvictionTTL // very long ttl to prevent eviction
		}

		ci = &cacheItem{fun: f, ttl: itemTTL}
		c.data[key] = ci
		c.stat.Added++
	}
	c.Unlock()

	// we don't want to block the possible call as this may take time
	// and also cause dead-lock if one cached value calls (the same cache) for another
	data, called, err := ci.value(c.allowError)

	c.Lock()
	defer c.Unlock()
	c.stat.Size = len(c.data)
	if called {
		c.stat.Misses++
	} else {
		c.stat.Hits++
	}
	if c.maxKeys > 0 && c.stat.Size >= c.maxKeys*2 {
		c.purge(c.maxKeys)
	}
	return data, err
}

// GetValue by key from cache
func (c *loadingCacheImpl) GetValue(key string) (interface{}, bool) {
	c.Lock()
	defer c.Unlock()
	res, ok := c.data[key]
	if !ok {
		return nil, false
	}
	return res.data, ok
}

// Invalidate key (item) from the cache
func (c *loadingCacheImpl) Invalidate(key string) {
	c.Lock()
	delete(c.data, key)
	c.Unlock()
}

// InvalidateFn deletes multiple keys if predicate is true
func (c *loadingCacheImpl) InvalidateFn(fn func(key string) bool) {
	c.Lock()
	for key := range c.data {
		if fn(key) {
			delete(c.data, key)
		}
	}
	c.Unlock()
}

// Stat gets the current stats for cache
func (c *loadingCacheImpl) Stat() Stats {
	c.Lock()
	defer c.Unlock()
	return c.stat
}

func (c *loadingCacheImpl) String() string {
	return fmt.Sprintf("%+v (%0.1f%%)", c.Stat(), 100*float64(c.Stat().Hits)/float64(c.stat.Hits+c.stat.Misses))
}

// keysWithTs includes list of keys with ts. This is for sorting keys
// in order to provide least recently added sorting for size-based eviction
type keysWithTs []struct {
	key string
	ts  time.Time
}

// purge records > max.size. Has to be called with lock!
func (c *loadingCacheImpl) purge(maxKeys int) {

	kts := keysWithTs{}

	for key, cacheItem := range c.data {

		// ttl eviction
		if cacheItem.hasExpired() {
			delete(c.data, key)
			c.stat.Evicted++
		}

		// prepare list of keysWithTs for size eviction
		if maxKeys > 0 && len(c.data) > maxKeys {
			ts := cacheItem.expiresAt // for no-LRU sort by expiration time
			if c.isLRU {              // for LRU sort by read time
				ts = cacheItem.lastReadAt
			}

			kts = append(kts, struct {
				key string
				ts  time.Time
			}{key, ts})
		}
	}

	// size eviction
	size := len(c.data)
	if len(kts) > 0 {
		sort.Slice(kts, func(i int, j int) bool { return kts[i].ts.Before(kts[j].ts) })
		for d := 0; d < size-maxKeys; d++ {
			delete(c.data, kts[d].key)
			c.stat.Evicted++
		}
	}
	c.stat.Size = len(c.data)
}

type cacheItem struct {
	sync.Mutex
	fun        func() (data interface{}, err error)
	expiresAt  time.Time
	lastReadAt time.Time
	ttl        time.Duration
	data       interface{}
	err        error
}

func (ci *cacheItem) value(allowError bool) (data interface{}, called bool, err error) {
	ci.Lock()
	defer ci.Unlock()
	called = false
	now := time.Now()
	if ci.expiresAt.IsZero() || now.After(ci.expiresAt) {
		ci.data, ci.err = ci.fun()
		ci.expiresAt = now.Add(ci.ttl)

		if ci.err != nil && !allowError { // don't cache error calls
			ci.expiresAt = now
		}
		called = true
	}
	ci.lastReadAt = time.Now()
	return ci.data, called, ci.err
}

func (ci *cacheItem) hasExpired() (b bool) {
	ci.Lock()
	defer ci.Unlock()
	return !ci.expiresAt.IsZero() && time.Now().After(ci.expiresAt)
}
