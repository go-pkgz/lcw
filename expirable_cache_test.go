package lcw

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExpirableCache(t *testing.T) {
	lc, err := NewExpirableCache(MaxKeys(5), TTL(time.Millisecond*100))
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		i := i
		_, e := lc.Get(fmt.Sprintf("key-%d", i), func() (interface{}, error) {
			return fmt.Sprintf("result-%d", i), nil
		})
		assert.NoError(t, e)
		time.Sleep(10 * time.Millisecond)
	}

	assert.Equal(t, 5, lc.Stat().Keys)
	assert.Equal(t, int64(5), lc.Stat().Misses)

	keys := lc.Keys()
	assert.EqualValues(t, []string{"key-0", "key-1", "key-2", "key-3", "key-4"}, keys)

	_, e := lc.Get("key-xx", func() (interface{}, error) {
		return "result-xx", nil
	})
	assert.NoError(t, e)
	assert.Equal(t, 5, lc.Stat().Keys)
	assert.Equal(t, int64(6), lc.Stat().Misses)

	// let key-0 expire, GitHub Actions friendly way
	for lc.Stat().Keys > 4 {
		lc.backend.DeleteExpired() // enforce DeleteExpired for GitHub earlier than TTL/2
		time.Sleep(time.Millisecond * 10)
	}
	assert.Equal(t, 4, lc.Stat().Keys)

	time.Sleep(210 * time.Millisecond)
	assert.Equal(t, 0, lc.keys())
	assert.Equal(t, []string{}, lc.Keys())

	assert.NoError(t, lc.Close())
}

func TestExpirableCache_MaxKeys(t *testing.T) {
	var coldCalls int32
	lc, err := NewExpirableCache(MaxKeys(5), MaxValSize(10))
	require.NoError(t, err)

	// put 5 keys to cache
	for i := 0; i < 5; i++ {
		i := i
		res, e := lc.Get(fmt.Sprintf("key-%d", i), func() (interface{}, error) {
			atomic.AddInt32(&coldCalls, 1)
			return fmt.Sprintf("result-%d", i), nil
		})
		assert.NoError(t, e)
		assert.Equal(t, fmt.Sprintf("result-%d", i), res.(string))
		assert.Equal(t, int32(i+1), atomic.LoadInt32(&coldCalls))
	}

	// check if really cached
	res, err := lc.Get("key-3", func() (interface{}, error) {
		return "result-blah", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-3", res.(string), "should be cached")

	// try to cache after maxKeys reached
	res, err = lc.Get("key-X", func() (interface{}, error) {
		return "result-X", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-X", res.(string))
	assert.Equal(t, 5, lc.keys())

	// put to cache and make sure it cached
	res, err = lc.Get("key-Z", func() (interface{}, error) {
		return "result-Z", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-Z", res.(string))

	res, err = lc.Get("key-Z", func() (interface{}, error) {
		return "result-Zzzz", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-Zzzz", res.(string), "got non-cached value")
	assert.Equal(t, 5, lc.keys())

	assert.NoError(t, lc.Close())
}

func TestExpirableCache_BadOptions(t *testing.T) {
	_, err := NewExpirableCache(MaxCacheSize(-1))
	assert.EqualError(t, err, "failed to set cache option: negative max cache size")

	_, err = NewExpirableCache(MaxKeySize(-1))
	assert.EqualError(t, err, "failed to set cache option: negative max key size")

	_, err = NewExpirableCache(MaxKeys(-1))
	assert.EqualError(t, err, "failed to set cache option: negative max keys")

	_, err = NewExpirableCache(MaxValSize(-1))
	assert.EqualError(t, err, "failed to set cache option: negative max value size")

	_, err = NewExpirableCache(TTL(-1))
	assert.EqualError(t, err, "failed to set cache option: negative ttl")
}

func TestExpirableCacheWithBus(t *testing.T) {
	ps := &mockPubSub{}
	lc1, err := NewExpirableCache(MaxKeys(5), TTL(time.Millisecond*100), EventBus(ps))
	require.NoError(t, err)
	defer lc1.Close()

	lc2, err := NewExpirableCache(MaxKeys(50), TTL(time.Millisecond*5000), EventBus(ps))
	require.NoError(t, err)
	defer lc2.Close()

	// add 5 keys to the first node cache
	for i := 0; i < 5; i++ {
		i := i
		_, e := lc1.Get(fmt.Sprintf("key-%d", i), func() (interface{}, error) {
			return fmt.Sprintf("result-%d", i), nil
		})
		assert.NoError(t, e)
		time.Sleep(10 * time.Millisecond)
	}

	assert.Equal(t, 0, len(ps.CalledKeys()), "no events")
	assert.Equal(t, 5, lc1.Stat().Keys)
	assert.Equal(t, int64(5), lc1.Stat().Misses)

	// add key-1 key to the second node
	_, e := lc2.Get("key-1", func() (interface{}, error) {
		return "result-111", nil
	})
	assert.NoError(t, e)
	assert.Equal(t, 1, lc2.Stat().Keys)
	assert.Equal(t, int64(1), lc2.Stat().Misses, lc2.Stat())

	// let key-0 expire, GitHub Actions friendly way
	for lc1.Stat().Keys > 4 {
		lc1.backend.DeleteExpired() // enforce DeleteExpired for GitHub earlier than TTL/2
		ps.Wait()                   // wait for onBusEvent goroutines to finish
		time.Sleep(time.Millisecond * 10)
	}
	assert.Equal(t, 4, lc1.Stat().Keys)
	assert.Equal(t, 1, lc2.Stat().Keys, "key-1 still in cache2")
	assert.Equal(t, 1, len(ps.CalledKeys()))

	time.Sleep(210 * time.Millisecond) // let all keys expire
	ps.Wait()                          // wait for onBusEvent goroutines to finish
	assert.Equal(t, 6, len(ps.CalledKeys()), "6 events, key-1 expired %+v", ps.calledKeys)
	assert.Equal(t, 0, lc1.Stat().Keys)
	assert.Equal(t, 0, lc2.Stat().Keys, "key-1 removed from cache2")
}
