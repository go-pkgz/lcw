package lcw

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisValueSizeLimit is maximum allowed value size in Redis
const RedisValueSizeLimit = 512 * 1024 * 1024

// RedisCache implements LoadingCache for Redis.
type RedisCache struct {
	options
	CacheStat
	backend redis.UniversalClient
	loads   loadGroup
}

// NewRedisCache makes Redis LoadingCache implementation.
// Without RedisKeyPrefix the cache assumes exclusive ownership of the selected Redis database,
// i.e. Purge flushes it entirely and Keys, Stat and MaxKeys count every key in it.
// MaxCacheSize is not supported by this backend, it is accepted but ignored, and Stat reports size 0.
func NewRedisCache(backend redis.UniversalClient, opts ...Option) (*RedisCache, error) {
	res := RedisCache{
		options: options{
			ttl: 5 * time.Minute,
		},
	}
	for _, opt := range opts {
		if err := opt(&res.options); err != nil {
			return nil, fmt.Errorf("failed to set cache option: %w", err)
		}
	}

	if res.maxValueSize <= 0 || res.maxValueSize > RedisValueSizeLimit {
		res.maxValueSize = RedisValueSizeLimit
	}

	res.backend = backend

	return &res, nil
}

// fullKey makes the physical redis key for the given logical key
func (c *RedisCache) fullKey(key string) string {
	return c.redisKeyPrefix + key
}

// scanKeys returns all physical keys belonging to this cache
func (c *RedisCache) scanKeys() []string {
	return c.backend.Keys(context.Background(), escapeGlob(c.redisKeyPrefix)+"*").Val()
}

// Get gets value by key or load with fn if not found in cache
func (c *RedisCache) Get(key string, fn func() (any, error)) (data any, err error) {
	v, getErr := c.backend.Get(context.Background(), c.fullKey(key)).Result()
	switch {
	// RedisClient returns nil when find a key in DB
	case getErr == nil:
		atomic.AddInt64(&c.Hits, 1)
		return v, nil
		// RedisClient returns redis.Nil when doesn't find a key in DB, load it below
	case errors.Is(getErr, redis.Nil):
		// RedisClient returns !nil when something goes wrong while get data
	default:
		atomic.AddInt64(&c.Errors, 1)
		return v, getErr
	}

	// concurrent callers for the same key wait for the first load instead of loading on their own
	return c.loads.do(key, func() (any, error) {
		if cached, e := c.backend.Get(context.Background(), c.fullKey(key)).Result(); e == nil {
			atomic.AddInt64(&c.Hits, 1) // filled by the load we were waiting for
			return cached, nil
		}

		data, err := fn()
		if err != nil {
			atomic.AddInt64(&c.Errors, 1)
			return data, err
		}
		atomic.AddInt64(&c.Misses, 1)

		if !c.allowed(key, data) {
			return data, nil
		}

		if _, setErr := c.backend.Set(context.Background(), c.fullKey(key), data, c.ttl).Result(); setErr != nil {
			atomic.AddInt64(&c.Errors, 1)
			return data, setErr
		}

		return data, nil
	})
}

// Invalidate removes keys with passed predicate fn, i.e. fn(key) should be true to get evicted
func (c *RedisCache) Invalidate(fn func(key string) bool) {
	for _, key := range c.scanKeys() { // Keys() returns copy of cache's key, safe to remove directly
		if fn(strings.TrimPrefix(key, c.redisKeyPrefix)) {
			c.backend.Del(context.Background(), key)
		}
	}
}

// Peek returns the key value (or undefined if not found) without updating the "recently used"-ness of the key.
func (c *RedisCache) Peek(key string) (any, bool) {
	ret, err := c.backend.Get(context.Background(), c.fullKey(key)).Result()
	if err != nil {
		return nil, false
	}
	return ret, true
}

// Purge clears the cache completely. Without RedisKeyPrefix set it flushes the whole redis database.
func (c *RedisCache) Purge() {
	if c.redisKeyPrefix == "" {
		c.backend.FlushDB(context.Background())
		return
	}
	// deleted one by one, a multi-key Del is routed by the first key's slot
	// and fails across slots on a cluster client
	for _, key := range c.scanKeys() {
		c.backend.Del(context.Background(), key)
	}
}

// Delete cache item by key
func (c *RedisCache) Delete(key string) {
	c.backend.Del(context.Background(), c.fullKey(key))
}

// Keys gets all keys for the cache
func (c *RedisCache) Keys() (res []string) {
	keys := c.scanKeys()
	if c.redisKeyPrefix == "" {
		return keys
	}
	res = make([]string, 0, len(keys))
	for _, key := range keys {
		res = append(res, strings.TrimPrefix(key, c.redisKeyPrefix))
	}
	return res
}

// Stat returns cache statistics
func (c *RedisCache) Stat() CacheStat {
	return CacheStat{
		Hits:   c.Hits,
		Misses: c.Misses,
		Size:   c.size(),
		Keys:   c.keys(),
		Errors: c.Errors,
	}
}

// Close closes underlying connections
func (c *RedisCache) Close() error {
	return c.backend.Close()
}

func (c *RedisCache) size() int64 {
	return 0
}

func (c *RedisCache) keys() int {
	if c.redisKeyPrefix == "" {
		return int(c.backend.DBSize(context.Background()).Val())
	}
	return len(c.scanKeys())
}

func (c *RedisCache) allowed(key string, data any) bool {
	if c.maxKeys > 0 && c.keys() >= c.maxKeys {
		return false
	}
	if c.maxKeySize > 0 && len(key) > c.maxKeySize {
		return false
	}
	if size, ok := sizeOf(data); ok {
		if c.maxValueSize > 0 && size >= c.maxValueSize {
			return false
		}
	}
	return true
}

// escapeGlob escapes redis glob-style pattern metacharacters, so a key prefix
// containing them still matches literally
func escapeGlob(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, "*", `\*`, "?", `\?`, "[", `\[`, "]", `\]`)
	return replacer.Replace(s)
}
