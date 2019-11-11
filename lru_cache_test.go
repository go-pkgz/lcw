package lcw

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"sort"
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

	keys := lc.Keys()[:]
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	assert.EqualValues(t, []string{"key-0", "key-1", "key-2", "key-3", "key-4"}, keys)

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

	_, err = NewLruCache(MaxKeySize(-1))
	assert.EqualError(t, err, "failed to set cache option: negative max key size")

	_, err = NewLruCache(MaxKeys(-1))
	assert.EqualError(t, err, "failed to set cache option: negative max keys")

	_, err = NewLruCache(MaxValSize(-1))
	assert.EqualError(t, err, "failed to set cache option: negative max value size")

	_, err = NewLruCache(TTL(-1))
	assert.EqualError(t, err, "failed to set cache option: negative ttl")
}

// LruCache illustrates the use of LRU loading cache
func ExampleLruCache() {

	// load page function
	loadURL := func(url string) (string, error) {
		resp, err := http.Get(url) // nolint
		if err != nil {
			return "", err
		}
		_ = resp.Body.Close()
		b, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}

	// fixed size LRU cache, 100 items, up to 10k in total size
	cache, err := NewLruCache(MaxKeys(100), MaxCacheSize(10*1024))
	if err != nil {
		log.Fatalf("can't make lru cache, %v", err)
	}

	// url not in cache, load data
	url := "https://radio-t.com/online/"
	val, err := cache.Get(url, func() (val Value, err error) {
		return loadURL(url)
	})
	if err != nil {
		log.Fatalf("can't load url %s, %v", url, err)
	}
	log.Print(val.(string))

	// url not in cache, load data
	url = "https://radio-t.com/info/"
	val, err = cache.Get(url, func() (val Value, err error) {
		return loadURL(url)
	})
	if err != nil {
		log.Fatalf("can't load url %s, %v", url, err)
	}
	log.Print(val.(string))

	// url cached, skip load and get from the cache
	url = "https://radio-t.com/online/"
	val, err = cache.Get(url, func() (val Value, err error) {
		return loadURL(url)
	})
	if err != nil {
		log.Fatalf("can't load url %s, %v", url, err)
	}
	log.Print(val.(string))

}
