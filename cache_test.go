package lcw

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCache_Get(t *testing.T) {
	var coldCalls int32
	lc, err := NewCache()
	require.Nil(t, err)
	res, err := lc.Get("key", func() (Value, error) {
		atomic.AddInt32(&coldCalls, 1)
		return "result", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "result", res.(string))
	assert.Equal(t, int32(1), atomic.LoadInt32(&coldCalls))

	res, err = lc.Get("key", func() (Value, error) {
		atomic.AddInt32(&coldCalls, 1)
		return "result2", nil
	})

	assert.Nil(t, err)
	assert.Equal(t, "result", res.(string))
	assert.Equal(t, int32(1), atomic.LoadInt32(&coldCalls), "cache hit")

	_, err = lc.Get("key-2", func() (Value, error) {
		atomic.AddInt32(&coldCalls, 1)
		return "result2", errors.New("some error")
	})
	assert.NotNil(t, err)
	assert.Equal(t, int32(2), atomic.LoadInt32(&coldCalls), "cache hit")

	_, err = lc.Get("key-2", func() (Value, error) {
		atomic.AddInt32(&coldCalls, 1)
		return "result2", errors.New("some error")
	})
	assert.NotNil(t, err)
	assert.Equal(t, int32(3), atomic.LoadInt32(&coldCalls), "cache hit")
}

func TestCache_Peek(t *testing.T) {
	var coldCalls int32
	lc, err := NewCache()
	require.Nil(t, err)
	res, err := lc.Get("key", func() (Value, error) {
		atomic.AddInt32(&coldCalls, 1)
		return "result", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "result", res.(string))
	assert.Equal(t, int32(1), atomic.LoadInt32(&coldCalls))

	r, ok := lc.Peek("key")
	assert.True(t, ok)
	assert.Equal(t, "result", r.(string))
}

func TestCache_MaxKeys(t *testing.T) {
	var coldCalls int32
	lc, err := NewCache(MaxKeys(5), MaxValSize(10))
	require.Nil(t, err)

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
	assert.Equal(t, 5, lc.backend.Len())

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
	assert.Equal(t, "result-Z", res.(string), "got cached value")
	assert.Equal(t, 5, lc.backend.Len())
}

func TestCache_MaxValueSize(t *testing.T) {
	lc, err := NewCache(MaxKeys(5), MaxValSize(10))
	require.NoError(t, err)

	// put good size value to cache and make sure it cached
	res, err := lc.Get("key-Z", func() (Value, error) {
		return sizedString("result-Z"), nil
	})
	assert.NoError(t, err)
	assert.Equal(t, sizedString("result-Z"), res.(sizedString))

	res, err = lc.Get("key-Z", func() (Value, error) {
		return sizedString("result-Zzzz"), nil
	})
	assert.NoError(t, err)
	assert.Equal(t, sizedString("result-Z"), res.(sizedString), "got cached value")

	// put too big value to cache and make sure it is not cached
	res, err = lc.Get("key-Big", func() (Value, error) {
		return sizedString("1234567890"), nil
	})
	assert.NoError(t, err)
	assert.Equal(t, sizedString("1234567890"), res.(sizedString))

	res, err = lc.Get("key-Big", func() (Value, error) {
		return sizedString("result-big"), nil
	})
	assert.NoError(t, err)
	assert.Equal(t, sizedString("result-big"), res.(sizedString), "got not cached value")

	// put too big value to cache but not Sizer
	res, err = lc.Get("key-Big2", func() (Value, error) {
		return "1234567890", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "1234567890", res.(string))

	res, err = lc.Get("key-Big2", func() (Value, error) {
		return "xyz", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "1234567890", res.(string), "too long, but not Sizer. from cache")
}

func TestCache_MaxCacheSize(t *testing.T) {
	lc, err := NewCache(MaxKeys(50), MaxCacheSize(20))
	require.Nil(t, err)

	// put good size value to cache and make sure it cached
	res, err := lc.Get("key-Z", func() (Value, error) {
		return sizedString("result-Z"), nil
	})
	assert.NoError(t, err)
	assert.Equal(t, sizedString("result-Z"), res.(sizedString))

	res, err = lc.Get("key-Z", func() (Value, error) {
		return sizedString("result-Zzzz"), nil
	})
	assert.NoError(t, err)
	assert.Equal(t, sizedString("result-Z"), res.(sizedString), "got cached value")
	assert.Equal(t, int64(8), lc.currentSize)

	_, err = lc.Get("key-Z2", func() (Value, error) {
		return sizedString("result-Y"), nil
	})
	assert.Nil(t, err)
	assert.Equal(t, int64(16), lc.currentSize)

	// this will cause removal
	_, err = lc.Get("key-Z3", func() (Value, error) {
		return sizedString("result-Z"), nil
	})
	assert.Nil(t, err)
	assert.Equal(t, int64(16), lc.currentSize)
	assert.Equal(t, 2, lc.backend.Len())
}

func TestCache_MaxCacheSizeParallel(t *testing.T) {
	lc, err := NewCache(MaxCacheSize(123), MaxKeys(10000))
	require.Nil(t, err)

	wg := sync.WaitGroup{}
	for i := 0; i < 1000; i++ {
		wg.Add(1)
		i := i
		go func() {
			time.Sleep(time.Duration(rand.Intn(100)) * time.Nanosecond)
			defer wg.Done()
			res, err := lc.Get(fmt.Sprintf("key-%d", i), func() (Value, error) {
				return sizedString(fmt.Sprintf("result-%d", i)), nil
			})
			require.Nil(t, err)
			require.Equal(t, sizedString(fmt.Sprintf("result-%d", i)), res.(sizedString))
			size := atomic.LoadInt64(&lc.currentSize)
			require.True(t, size < 200 && size >= 0, "unexpected size=%d", size) // won't be exactly 123 due parallel
		}()
	}
	wg.Wait()
	assert.True(t, lc.currentSize < 123 && lc.currentSize >= 0)
	t.Log("size", lc.currentSize)
}

func TestCache_MaxKeySize(t *testing.T) {
	lc, err := NewCache(MaxKeySize(5))
	require.Nil(t, err)

	res, err := lc.Get("key", func() (Value, error) {
		return "value", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "value", res.(string))

	res, err = lc.Get("key", func() (Value, error) {
		return "valueXXX", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "value", res.(string), "cached")

	res, err = lc.Get("key1234", func() (Value, error) {
		return "value", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "value", res.(string))

	res, err = lc.Get("key1234", func() (Value, error) {
		return "valueXYZ", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "valueXYZ", res.(string), "not cached")
}
func TestCache_Parallel(t *testing.T) {
	var coldCalls int32
	lc, err := NewCache()
	require.Nil(t, err)

	res, err := lc.Get("key", func() (Value, error) {
		return "value", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "value", res.(string))

	wg := sync.WaitGroup{}
	for i := 0; i < 1000; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			res, err := lc.Get("key", func() (Value, error) {
				atomic.AddInt32(&coldCalls, 1)
				return fmt.Sprintf("result-%d", i), nil
			})
			require.Nil(t, err)
			require.Equal(t, "value", res.(string))
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(0), atomic.LoadInt32(&coldCalls))
}

func TestCache_Invalidate(t *testing.T) {
	var coldCalls int32
	lc, err := NewCache()
	require.Nil(t, err)

	// fill cache
	for i := 0; i < 1000; i++ {
		_, err := lc.Get(fmt.Sprintf("key-%d", i), func() (Value, error) {
			atomic.AddInt32(&coldCalls, 1)
			return fmt.Sprintf("result-%d", i), nil
		})
		require.Nil(t, err)
	}
	assert.Equal(t, int32(1000), atomic.LoadInt32(&coldCalls))
	assert.Equal(t, 1000, lc.backend.Len())

	lc.Invalidate(func(key string) bool {
		return strings.HasSuffix(key, "0")
	})

	assert.Equal(t, 900, lc.backend.Len(), "100 keys removed")
	res, err := lc.Get("key-1", func() (Value, error) {
		atomic.AddInt32(&coldCalls, 1)
		return "result-xxx", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "result-1", res.(string), "from the cache")

	res, err = lc.Get("key-10", func() (Value, error) {
		atomic.AddInt32(&coldCalls, 1)
		return "result-xxx", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "result-xxx", res.(string), "not from the cache")
}

func TestCache_Purge(t *testing.T) {
	var coldCalls int32
	lc, err := NewCache()
	require.Nil(t, err)

	// fill cache
	for i := 0; i < 1000; i++ {
		_, err := lc.Get(fmt.Sprintf("key-%d", i), func() (Value, error) {
			atomic.AddInt32(&coldCalls, 1)
			return fmt.Sprintf("result-%d", i), nil
		})
		require.Nil(t, err)
	}
	assert.Equal(t, int32(1000), atomic.LoadInt32(&coldCalls))
	assert.Equal(t, 1000, lc.backend.Len())

	lc.Purge()
	assert.Equal(t, 0, lc.backend.Len(), "all keys removed")
}
func TestCache_BadOptions(t *testing.T) {
	_, err := NewCache(MaxCacheSize(-1))
	assert.EqualError(t, err, "failed to set cache option: negative max cache size")

	_, err = NewCache(MaxCacheSize(-1))
	assert.EqualError(t, err, "failed to set cache option: negative max cache size")

	_, err = NewCache(MaxKeys(-1))
	assert.EqualError(t, err, "failed to set cache option: negative max keys")

	_, err = NewCache(MaxValSize(-1))
	assert.EqualError(t, err, "failed to set cache option: negative max value size")
}

type sizedString string

func (s sizedString) Size() int { return len(s) }
