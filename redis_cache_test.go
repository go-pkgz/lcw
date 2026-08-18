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

type fakeString string

func TestExpirableRedisCache(t *testing.T) {
	server := newTestRedisServer()
	defer server.Close()
	client := redis.NewClient(&redis.Options{
		Addr: server.Addr()})
	defer client.Close()
	rc, err := NewRedisCache(client, MaxKeys(5), TTL(time.Second*6))
	require.NoError(t, err)
	defer rc.Close()
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		i := i
		_, e := rc.Get(fmt.Sprintf("key-%d", i), func() (any, error) {
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

	_, e := rc.Get("key-xx", func() (any, error) {
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
	rc, err := NewRedisCache(client, MaxKeys(5), MaxValSize(10), MaxKeySize(10))
	require.NoError(t, err)
	defer rc.Close()
	// put 5 keys to cache
	for i := 0; i < 5; i++ {
		i := i
		res, e := rc.Get(fmt.Sprintf("key-%d", i), func() (any, error) {
			atomic.AddInt32(&coldCalls, 1)
			return fmt.Sprintf("result-%d", i), nil
		})
		assert.NoError(t, e)
		assert.Equal(t, fmt.Sprintf("result-%d", i), res.(string))
		assert.Equal(t, int32(i+1), atomic.LoadInt32(&coldCalls))
	}

	// check if really cached
	res, err := rc.Get("key-3", func() (any, error) {
		return "result-blah", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-3", res.(string), "should be cached")

	// try to cache after maxKeys reached
	res, err = rc.Get("key-X", func() (any, error) {
		return "result-X", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-X", res.(string))
	assert.Equal(t, int64(5), rc.backend.DBSize(context.Background()).Val())

	// put to cache and make sure it cached
	res, err = rc.Get("key-Z", func() (any, error) {
		return "result-Z", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-Z", res.(string))

	res, err = rc.Get("key-Z", func() (any, error) {
		return "result-Zzzz", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-Zzzz", res.(string), "got non-cached value")
	assert.Equal(t, 5, rc.keys())

	res, err = rc.Get("key-Zzzzzzz", func() (any, error) {
		return "result-Zzzz", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-Zzzz", res.(string), "got non-cached value")
	assert.Equal(t, 5, rc.keys())

	res, ok := rc.Peek("error-key-Z2")
	assert.False(t, ok)
	assert.Nil(t, res)
}

func TestRedisCacheErrors(t *testing.T) {
	server := newTestRedisServer()
	defer server.Close()
	client := redis.NewClient(&redis.Options{
		Addr: server.Addr()})
	defer client.Close()
	rc, err := NewRedisCache(client)
	require.NoError(t, err)
	defer rc.Close()

	res, err := rc.Get("error-key-Z", func() (any, error) {
		return "error-result-Z", fmt.Errorf("some error")
	})
	assert.Error(t, err)
	assert.Equal(t, "error-result-Z", res.(string))
	assert.Equal(t, int64(1), rc.Stat().Errors)

	res, err = rc.Get("error-key-Z2", func() (any, error) {
		return fakeString("error-result-Z2"), nil
	})
	assert.Error(t, err)
	assert.Equal(t, fakeString("error-result-Z2"), res.(fakeString))
	assert.Equal(t, int64(2), rc.Stat().Errors)

	server.Close()
	res, err = rc.Get("error-key-Z3", func() (any, error) {
		return fakeString("error-result-Z3"), nil
	})
	assert.Error(t, err)
	assert.Equal(t, "", res.(string))
	assert.Equal(t, int64(3), rc.Stat().Errors)
}

func TestRedisCache_BadOptions(t *testing.T) {
	server := newTestRedisServer()
	defer server.Close()
	client := redis.NewClient(&redis.Options{
		Addr: server.Addr()})
	defer client.Close()

	_, err := NewRedisCache(client, MaxCacheSize(-1))
	assert.EqualError(t, err, "failed to set cache option: negative max cache size")

	_, err = NewRedisCache(client, MaxCacheSize(-1))
	assert.EqualError(t, err, "failed to set cache option: negative max cache size")

	_, err = NewRedisCache(client, MaxKeys(-1))
	assert.EqualError(t, err, "failed to set cache option: negative max keys")

	_, err = NewRedisCache(client, MaxValSize(-1))
	assert.EqualError(t, err, "failed to set cache option: negative max value size")

	_, err = NewRedisCache(client, TTL(-1))
	assert.EqualError(t, err, "failed to set cache option: negative ttl")

	_, err = NewRedisCache(client, MaxKeySize(-1))
	assert.EqualError(t, err, "failed to set cache option: negative max key size")

}

func TestRedisCache_KeyPrefix(t *testing.T) {
	server := newTestRedisServer()
	defer server.Close()
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	ctx := context.Background()

	// data belonging to somebody else in the same redis db
	require.NoError(t, client.Set(ctx, "foreign-key", "foreign-value", 0).Err())

	rc, err := NewRedisCache(client, RedisKeyPrefix("lcw:"))
	require.NoError(t, err)

	_, err = rc.Get("key", func() (any, error) { return "value", nil })
	require.NoError(t, err)

	stored, err := client.Get(ctx, "lcw:key").Result()
	require.NoError(t, err)
	assert.Equal(t, "value", stored, "stored under the prefix")

	assert.Equal(t, []string{"key"}, rc.Keys(), "keys reported without the prefix")
	assert.Equal(t, 1, rc.Stat().Keys, "foreign key not counted")

	// a key colliding with foreign data is a miss, not a hit on somebody else's value
	var coldCalls int32
	res, err := rc.Get("foreign-key", func() (any, error) {
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

	// several keys, purge used to issue one multi-key Del which a cluster client
	// routes by the first key's slot and rejects across slots
	for _, k := range []string{"k1", "k2", "k3"} {
		_, err = rc.Get(k, func() (any, error) { return "value", nil })
		require.NoError(t, err)
	}

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

	rc, err := NewRedisCache(client, RedisKeyPrefix("lcw[1]:"))
	require.NoError(t, err)

	_, err = rc.Get("key", func() (any, error) { return "value", nil })
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

	rc, err := NewRedisCache(client)
	require.NoError(t, err)

	_, err = rc.Get("key", func() (any, error) { return "value", nil })
	require.NoError(t, err)

	// documented behavior without a prefix, the whole db belongs to the cache
	assert.Equal(t, 2, rc.Stat().Keys, "foreign key counted")
	rc.Purge()
	assert.Equal(t, redis.Nil, client.Get(ctx, "foreign-key").Err(), "purge flushes the db")
}
