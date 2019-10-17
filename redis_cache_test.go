package lcw

import (
	"errors"
	"fmt"
	"log"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis"
	redis "github.com/go-redis/redis/v7"
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
	client := redis.NewClient(&redis.Options{
		Addr: server.Addr()})
	lc, err := NewRedisCache(client, MaxKeys(5), TTL(time.Second*6))
	if err != nil {
		log.Fatalf("can't make redis cache, %v", err)
	}
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		_, e := lc.Get(fmt.Sprintf("key-%d", i), func() (Value, error) {
			return fmt.Sprintf("result-%d", i), nil
		})
		assert.NoError(t, e)
		server.FastForward(1000 * time.Millisecond)
	}

	assert.Equal(t, 5, lc.Stat().Keys)
	assert.Equal(t, int64(5), lc.Stat().Misses)

	_, e := lc.Get("key-xx", func() (Value, error) {
		return "result-xx", nil
	})
	assert.NoError(t, e)
	assert.Equal(t, 5, lc.Stat().Keys)
	assert.Equal(t, int64(6), lc.Stat().Misses)

	server.FastForward(1000 * time.Millisecond)
	assert.Equal(t, 4, lc.Stat().Keys)

	server.FastForward(4000 * time.Millisecond)
	assert.Equal(t, 0, lc.keys())

}

func TestRedisCache(t *testing.T) {
	var coldCalls int32

	server := newTestRedisServer()
	client := redis.NewClient(&redis.Options{
		Addr: server.Addr()})
	lc, err := NewRedisCache(client, MaxKeys(5), MaxValSize(10), MaxKeySize(10))
	if err != nil {
		log.Fatalf("can't make redis cache, %v", err)
	}
	// put 5 keys to cache
	for i := 0; i < 5; i++ {
		res, e := lc.Get(fmt.Sprintf("key-%d", i), func() (Value, error) {
			atomic.AddInt32(&coldCalls, 1)
			return fmt.Sprintf("result-%d", i), nil
		})
		assert.Nil(t, e)
		assert.Equal(t, fmt.Sprintf("result-%d", i), res.(string))
		assert.Equal(t, int32(i+1), atomic.LoadInt32(&coldCalls))
	}

	// check if really cached
	res, err := lc.Get("key-3", func() (Value, error) {
		return "result-blah", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "result-3", res.(string), "should be cached")

	// try to cache after maxKeys reached
	res, err = lc.Get("key-X", func() (Value, error) {
		return "result-X", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "result-X", res.(string))
	assert.Equal(t, int64(5), lc.backend.DBSize().Val())

	// put to cache and make sure it cached
	res, err = lc.Get("key-Z", func() (Value, error) {
		return "result-Z", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "result-Z", res.(string))

	res, err = lc.Get("key-Z", func() (Value, error) {
		return "result-Zzzz", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "result-Zzzz", res.(string), "got non-cached value")
	assert.Equal(t, 5, lc.keys())

	res, err = lc.Get("key-Zzzzzzz", func() (Value, error) {
		return "result-Zzzz", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "result-Zzzz", res.(string), "got non-cached value")
	assert.Equal(t, 5, lc.keys())

	res, ok := lc.Peek("error-key-Z2")
	assert.False(t, ok)
	assert.Nil(t, res)
}

func TestRedisCacheErrors(t *testing.T) {

	server := newTestRedisServer()
	client := redis.NewClient(&redis.Options{
		Addr: server.Addr()})
	lc, err := NewRedisCache(client)
	if err != nil {
		log.Fatalf("can't make redis cache, %v", err)
	}

	res, err := lc.Get("error-key-Z", func() (Value, error) {
		return "error-result-Z", errors.New("some error")
	})
	assert.NotNil(t, err)
	assert.Equal(t, "error-result-Z", res.(string))
	assert.Equal(t, int64(1), lc.Stat().Errors)

	res, err = lc.Get("error-key-Z2", func() (Value, error) {
		return fakeString("error-result-Z2"), nil
	})
	assert.NotNil(t, err)
	assert.Equal(t, fakeString("error-result-Z2"), res.(fakeString))
	assert.Equal(t, int64(2), lc.Stat().Errors)

	server.Close()
	res, err = lc.Get("error-key-Z3", func() (Value, error) {
		return fakeString("error-result-Z3"), nil
	})
	assert.NotNil(t, err)
	assert.Equal(t, "", res.(string))
	assert.Equal(t, int64(3), lc.Stat().Errors)
}

func TestRedisCache_BadOptions(t *testing.T) {
	server := newTestRedisServer()
	client := redis.NewClient(&redis.Options{
		Addr: server.Addr()})

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
