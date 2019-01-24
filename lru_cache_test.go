package lcw

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLruCache_MaxKeys(t *testing.T) {
	var coldCalls int32
	lc, err := NewLruCache(MaxKeys(5), MaxValSize(10))
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

func TestLruCache_BadOptions(t *testing.T) {
	_, err := NewLruCache(MaxCacheSize(-1))
	assert.EqualError(t, err, "failed to set cache option: negative max cache size")

	_, err = NewLruCache(MaxCacheSize(-1))
	assert.EqualError(t, err, "failed to set cache option: negative max cache size")

	_, err = NewLruCache(MaxKeys(-1))
	assert.EqualError(t, err, "failed to set cache option: negative max keys")

	_, err = NewLruCache(MaxValSize(-1))
	assert.EqualError(t, err, "failed to set cache option: negative max value size")

	_, err = NewLruCache(TTL(-1))
	assert.EqualError(t, err, "failed to set cache option: negative ttl")
}
