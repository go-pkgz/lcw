package lcw

import (
	"context"
	"fmt"
	"sort"
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
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
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
