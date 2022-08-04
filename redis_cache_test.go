package lcw

import (
	"context"
	"fmt"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
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
		_, e := rc.Get(fmt.Sprintf("key-%d", i), func() (interface{}, error) {
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

	_, e := rc.Get("key-xx", func() (interface{}, error) {
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
		res, e := rc.Get(fmt.Sprintf("key-%d", i), func() (interface{}, error) {
			atomic.AddInt32(&coldCalls, 1)
			return fmt.Sprintf("result-%d", i), nil
		})
		assert.NoError(t, e)
		assert.Equal(t, fmt.Sprintf("result-%d", i), res.(string))
		assert.Equal(t, int32(i+1), atomic.LoadInt32(&coldCalls))
	}

	// check if really cached
	res, err := rc.Get("key-3", func() (interface{}, error) {
		return "result-blah", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-3", res.(string), "should be cached")

	// try to cache after maxKeys reached
	res, err = rc.Get("key-X", func() (interface{}, error) {
		return "result-X", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-X", res.(string))
	assert.Equal(t, int64(5), rc.backend.DBSize(context.Background()).Val())

	// put to cache and make sure it cached
	res, err = rc.Get("key-Z", func() (interface{}, error) {
		return "result-Z", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-Z", res.(string))

	res, err = rc.Get("key-Z", func() (interface{}, error) {
		return "result-Zzzz", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-Zzzz", res.(string), "got non-cached value")
	assert.Equal(t, 5, rc.keys())

	res, err = rc.Get("key-Zzzzzzz", func() (interface{}, error) {
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

	res, err := rc.Get("error-key-Z", func() (interface{}, error) {
		return "error-result-Z", fmt.Errorf("some error")
	})
	assert.Error(t, err)
	assert.Equal(t, "error-result-Z", res.(string))
	assert.Equal(t, int64(1), rc.Stat().Errors)

	res, err = rc.Get("error-key-Z2", func() (interface{}, error) {
		return fakeString("error-result-Z2"), nil
	})
	assert.Error(t, err)
	assert.Equal(t, fakeString("error-result-Z2"), res.(fakeString))
	assert.Equal(t, int64(2), rc.Stat().Errors)

	server.Close()
	res, err = rc.Get("error-key-Z3", func() (interface{}, error) {
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
