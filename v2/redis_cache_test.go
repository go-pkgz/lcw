package lcw

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestRedis returns a redis.Cmdable.
func newTestRedisServer() *miniredis.Miniredis {
	mr, err := miniredis.Run()
	if err != nil {
		panic(err)
	}

	return mr
}

func TestExpirableRedisCache(t *testing.T) {
	server := newTestRedisServer()
	defer server.Close()
	client := redis.NewClient(&redis.Options{
		Addr: server.Addr()})
	defer client.Close()
	o := NewOpts[string]()
	rc, err := NewRedisCache(client, o.MaxKeys(5), o.TTL(time.Second*6))
	require.NoError(t, err)
	defer rc.Close()
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		i := i
		_, e := rc.Get(fmt.Sprintf("key-%d", i), func() (string, error) {
			return fmt.Sprintf("result-%d", i), nil
		})
		assert.NoError(t, e)
		server.FastForward(1000 * time.Millisecond)
	}

	assert.Equal(t, 5, rc.Stat().Keys)
	assert.Equal(t, int64(5), rc.Stat().Misses)

	keys := rc.Keys()
	slices.Sort(keys)
	assert.EqualValues(t, []string{"key-0", "key-1", "key-2", "key-3", "key-4"}, keys)

	_, e := rc.Get("key-xx", func() (string, error) {
		return "result-xx", nil
	})
	assert.NoError(t, e)
	assert.Equal(t, 5, rc.Stat().Keys)
	assert.Equal(t, int64(6), rc.Stat().Misses)

	server.FastForward(1000 * time.Millisecond)
	assert.Equal(t, 4, rc.Stat().Keys)

	server.FastForward(4000 * time.Millisecond)
	assert.Equal(t, 0, rc.keys())

}

func TestRedisCache(t *testing.T) {
	var coldCalls int32

	server := newTestRedisServer()
	defer server.Close()
	client := redis.NewClient(&redis.Options{
		Addr: server.Addr()})
	defer client.Close()
	o := NewOpts[string]()
	rc, err := NewRedisCache(client, o.MaxKeys(5), o.MaxValSize(10), o.MaxKeySize(10))
	require.NoError(t, err)
	defer rc.Close()
	// put 5 keys to cache
	for i := 0; i < 5; i++ {
		i := i
		res, e := rc.Get(fmt.Sprintf("key-%d", i), func() (string, error) {
			atomic.AddInt32(&coldCalls, 1)
			return fmt.Sprintf("result-%d", i), nil
		})
		assert.NoError(t, e)
		assert.Equal(t, fmt.Sprintf("result-%d", i), res)
		assert.Equal(t, int32(i+1), atomic.LoadInt32(&coldCalls))
	}

	// check if really cached
	res, err := rc.Get("key-3", func() (string, error) {
		return "result-blah", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-3", res, "should be cached")

	// try to cache after maxKeys reached
	res, err = rc.Get("key-X", func() (string, error) {
		return "result-X", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-X", res)
	assert.Equal(t, int64(5), rc.backend.DBSize(context.Background()).Val())

	// put to cache and make sure it cached
	res, err = rc.Get("key-Z", func() (string, error) {
		return "result-Z", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-Z", res)

	res, err = rc.Get("key-Z", func() (string, error) {
		return "result-Zzzz", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-Zzzz", res, "got non-cached value")
	assert.Equal(t, 5, rc.keys())

	res, err = rc.Get("key-Zzzzzzz", func() (string, error) {
		return "result-Zzzz", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-Zzzz", res, "got non-cached value")
	assert.Equal(t, 5, rc.keys())

	res, ok := rc.Peek("error-key-Z2")
	assert.False(t, ok)
	assert.Empty(t, res)
}

func TestRedisCacheErrors(t *testing.T) {
	server := newTestRedisServer()
	defer server.Close()
	client := redis.NewClient(&redis.Options{
		Addr: server.Addr()})
	defer client.Close()
	rc, err := NewRedisCache[string](client)
	require.NoError(t, err)
	defer rc.Close()

	res, err := rc.Get("error-key-Z", func() (string, error) {
		return "error-result-Z", fmt.Errorf("some error")
	})
	assert.Error(t, err)
	assert.Equal(t, "error-result-Z", res)
	assert.Equal(t, int64(1), rc.Stat().Errors)
}

// should not work with non-string types
func TestRedisCacheCreationErrors(t *testing.T) {
	// string case, no error
	// no close is needed as it will call client.Close(), which will cause panic
	rcString, err := NewRedisCache[string](nil)
	require.NoError(t, err)
	assert.NotNil(t, rcString)
	// string-based type but no StrToV option, error expected
	rcSizedString, err := NewRedisCache[sizedString](nil)
	require.EqualError(t, err, "StrToV option should be set for string-like type")
	assert.Nil(t, rcSizedString)
	// string-based type with StrToV option, no error
	// no close is needed as it will call client.Close(), which will cause panic
	o := NewOpts[sizedString]()
	rcSizedString, err = NewRedisCache[sizedString](nil, o.StrToV(func(s string) sizedString { return sizedString(s) }))
	require.NoError(t, err)
	assert.NotNil(t, rcSizedString)
	// non-string based type, error expected
	rcInt, err := NewRedisCache[int](nil)
	require.EqualError(t, err, "can't store non-string types in Redis cache")
	assert.Nil(t, rcInt)
}

func TestRedisCache_BadOptions(t *testing.T) {
	server := newTestRedisServer()
	defer server.Close()
	client := redis.NewClient(&redis.Options{
		Addr: server.Addr()})
	defer client.Close()

	o := NewOpts[string]()
	_, err := NewRedisCache(client, o.MaxCacheSize(-1))
	assert.EqualError(t, err, "failed to set cache option: negative max cache size")

	_, err = NewRedisCache(client, o.MaxCacheSize(-1))
	assert.EqualError(t, err, "failed to set cache option: negative max cache size")

	_, err = NewRedisCache(client, o.MaxKeys(-1))
	assert.EqualError(t, err, "failed to set cache option: negative max keys")

	_, err = NewRedisCache(client, o.MaxValSize(-1))
	assert.EqualError(t, err, "failed to set cache option: negative max value size")

	_, err = NewRedisCache(client, o.TTL(-1))
	assert.EqualError(t, err, "failed to set cache option: negative ttl")

	_, err = NewRedisCache(client, o.MaxKeySize(-1))
	assert.EqualError(t, err, "failed to set cache option: negative max key size")

}

func TestRedisCache_PeekStringBasedType(t *testing.T) {
	server := newTestRedisServer()
	defer server.Close()
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()

	o := NewOpts[sizedString]()
	rc, err := NewRedisCache(client, o.StrToV(func(s string) sizedString { return sizedString(s) }))
	require.NoError(t, err)

	_, err = rc.Get("key", func() (sizedString, error) { return "value", nil })
	require.NoError(t, err)

	// Peek asserted the redis string to V directly, which panics for a string-based type
	res, ok := rc.Peek("key")
	require.True(t, ok)
	assert.Equal(t, sizedString("value"), res)
}

func TestRedisCache_KeyPrefix(t *testing.T) {
	server := newTestRedisServer()
	defer server.Close()
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	ctx := context.Background()

	// data belonging to somebody else in the same redis db
	require.NoError(t, client.Set(ctx, "foreign-key", "foreign-value", 0).Err())

	o := NewOpts[string]()
	rc, err := NewRedisCache(client, o.RedisKeyPrefix("lcw:"))
	require.NoError(t, err)

	_, err = rc.Get("key", func() (string, error) { return "value", nil })
	require.NoError(t, err)

	stored, err := client.Get(ctx, "lcw:key").Result()
	require.NoError(t, err)
	assert.Equal(t, "value", stored, "stored under the prefix")

	assert.Equal(t, []string{"key"}, rc.Keys(), "keys reported without the prefix")
	assert.Equal(t, 1, rc.Stat().Keys, "foreign key not counted")

	// a key colliding with foreign data is a miss, not a hit on somebody else's value
	var coldCalls int32
	res, err := rc.Get("foreign-key", func() (string, error) {
		atomic.AddInt32(&coldCalls, 1)
		return "own-value", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "own-value", res)
	assert.Equal(t, int32(1), atomic.LoadInt32(&coldCalls))
	foreign, err := client.Get(ctx, "foreign-key").Result()
	require.NoError(t, err)
	assert.Equal(t, "foreign-value", foreign, "foreign value untouched")

	// invalidate gets logical keys and removes only prefixed ones
	rc.Invalidate(func(key string) bool {
		assert.NotContains(t, key, "lcw:", "predicate gets the logical key")
		return key == "key"
	})
	assert.Equal(t, 1, rc.Stat().Keys)
	require.NoError(t, client.Get(ctx, "foreign-key").Err(), "foreign key kept")

	// purge clears own namespace only, no FlushDB
	rc.Purge()
	assert.Equal(t, 0, rc.Stat().Keys)
	assert.Empty(t, rc.Keys())
	foreign, err = client.Get(ctx, "foreign-key").Result()
	require.NoError(t, err, "foreign key survives purge")
	assert.Equal(t, "foreign-value", foreign)
}

func TestRedisCache_KeyPrefixGlobChars(t *testing.T) {
	server := newTestRedisServer()
	defer server.Close()
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	ctx := context.Background()

	o := NewOpts[string]()
	rc, err := NewRedisCache(client, o.RedisKeyPrefix("lcw[1]:"))
	require.NoError(t, err)

	_, err = rc.Get("key", func() (string, error) { return "value", nil })
	require.NoError(t, err)

	// a key that a naive, unescaped glob would also match
	require.NoError(t, client.Set(ctx, "lcw1:other", "other-value", 0).Err())

	assert.Equal(t, []string{"key"}, rc.Keys(), "glob metacharacters matched literally")
	assert.Equal(t, 1, rc.Stat().Keys)

	rc.Purge()
	require.NoError(t, client.Get(ctx, "lcw1:other").Err(), "key matching the unescaped glob kept")
}

func TestRedisCache_NoPrefixOwnsDatabase(t *testing.T) {
	server := newTestRedisServer()
	defer server.Close()
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	ctx := context.Background()

	require.NoError(t, client.Set(ctx, "foreign-key", "foreign-value", 0).Err())

	o := NewOpts[string]()
	rc, err := NewRedisCache(client, o.MaxKeys(100))
	require.NoError(t, err)

	_, err = rc.Get("key", func() (string, error) { return "value", nil })
	require.NoError(t, err)

	// documented behavior without a prefix, the whole db belongs to the cache
	assert.Equal(t, 2, rc.Stat().Keys, "foreign key counted")
	rc.Purge()
	assert.Equal(t, redis.Nil, client.Get(ctx, "foreign-key").Err(), "purge flushes the db")
}
