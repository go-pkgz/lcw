package lcw

import (
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/go-pkgz/lcw/eventbus"
)

func TestLruCache_MaxKeys(t *testing.T) {
	var coldCalls int32
	lc, err := NewLruCache(MaxKeys(5), MaxValSize(10))
	require.NoError(t, err)

	// put 5 keys to cache
	for i := 0; i < 5; i++ {
		i := i
		res, e := lc.Get(fmt.Sprintf("key-%d", i), func() (Value, error) {
			atomic.AddInt32(&coldCalls, 1)
			return fmt.Sprintf("result-%d", i), nil
		})
		assert.NoError(t, e)
		assert.Equal(t, fmt.Sprintf("result-%d", i), res.(string))
		assert.Equal(t, int32(i+1), atomic.LoadInt32(&coldCalls))
	}

	keys := lc.Keys()
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	assert.EqualValues(t, []string{"key-0", "key-1", "key-2", "key-3", "key-4"}, keys)

	// check if really cached
	res, err := lc.Get("key-3", func() (Value, error) {
		return "result-blah", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-3", res.(string), "should be cached")

	// try to cache after maxKeys reached
	res, err = lc.Get("key-X", func() (Value, error) {
		return "result-X", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-X", res.(string))
	assert.Equal(t, 5, lc.backend.Len())

	// put to cache and make sure it cached
	res, err = lc.Get("key-Z", func() (Value, error) {
		return "result-Z", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-Z", res.(string))

	res, err = lc.Get("key-Z", func() (Value, error) {
		return "result-Zzzz", nil
	})
	assert.NoError(t, err)
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

func TestLruCache_MaxKeysWithBus(t *testing.T) {
	ps := &mockPubSub{}

	var coldCalls int32
	lc1, err := NewLruCache(MaxKeys(5), MaxValSize(10), EventBus(ps))
	require.NoError(t, err)
	defer lc1.Close()

	lc2, err := NewLruCache(MaxKeys(50), MaxValSize(100), EventBus(ps))
	require.NoError(t, err)
	defer lc2.Close()

	// put 5 keys to cache1
	for i := 0; i < 5; i++ {
		i := i
		res, e := lc1.Get(fmt.Sprintf("key-%d", i), func() (Value, error) {
			atomic.AddInt32(&coldCalls, 1)
			return fmt.Sprintf("result-%d", i), nil
		})
		assert.NoError(t, e)
		assert.Equal(t, fmt.Sprintf("result-%d", i), res.(string))
		assert.Equal(t, int32(i+1), atomic.LoadInt32(&coldCalls))
	}
	// check if really cached
	res, err := lc1.Get("key-3", func() (Value, error) {
		return "result-blah", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-3", res.(string), "should be cached")

	assert.Equal(t, 0, len(ps.CalledKeys()), "no events")

	// put 1 key to cache2
	res, e := lc2.Get("key-1", func() (Value, error) {
		return "result-111", nil
	})
	assert.NoError(t, e)
	assert.Equal(t, "result-111", res.(string))

	// try to cache1 after maxKeys reached, will remove key-0
	res, err = lc1.Get("key-X", func() (Value, error) {
		return "result-X", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-X", res.(string))
	assert.Equal(t, 5, lc1.backend.Len())

	assert.Equal(t, 1, len(ps.CalledKeys()), "1 event, key-0 expired")

	assert.Equal(t, 1, lc2.backend.Len(), "cache2 still has key-1")

	// try to cache1 after maxKeys reached, will remove key-1
	res, err = lc1.Get("key-X2", func() (Value, error) {
		return "result-X", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-X", res.(string))

	assert.Equal(t, 2, len(ps.CalledKeys()), "2 events, key-1 expired")

	// wait for onBusEvent goroutines to finish
	ps.Wait()

	assert.Equal(t, 0, lc2.backend.Len(), "cache2 removed key-1")
}

func TestLruCache_MaxKeysWithRedis(t *testing.T) {
	if _, ok := os.LookupEnv("ENABLE_REDIS_TESTS"); !ok {
		t.Skip("ENABLE_REDIS_TESTS env variable is not set, not expecting Redis to be ready at 127.0.0.1:6379")
	}

	var coldCalls int32

	channel := "lcw-test-" + strconv.Itoa(rand.Intn(1000000))

	redisPubSub1, err := eventbus.NewRedisPubSub("127.0.0.1:6379", channel)
	require.NoError(t, err)
	lc1, err := NewLruCache(MaxKeys(5), MaxValSize(10), EventBus(redisPubSub1))
	require.NoError(t, err)
	defer lc1.Close()

	redisPubSub2, err := eventbus.NewRedisPubSub("127.0.0.1:6379", channel)
	require.NoError(t, err)
	lc2, err := NewLruCache(MaxKeys(50), MaxValSize(100), EventBus(redisPubSub2))
	require.NoError(t, err)
	defer lc2.Close()

	// put 5 keys to cache1
	for i := 0; i < 5; i++ {
		i := i
		res, e := lc1.Get(fmt.Sprintf("key-%d", i), func() (Value, error) {
			atomic.AddInt32(&coldCalls, 1)
			return fmt.Sprintf("result-%d", i), nil
		})
		assert.NoError(t, e)
		assert.Equal(t, fmt.Sprintf("result-%d", i), res.(string))
		assert.Equal(t, int32(i+1), atomic.LoadInt32(&coldCalls))
	}
	// check if really cached
	res, err := lc1.Get("key-3", func() (Value, error) {
		return "result-blah", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-3", res.(string), "should be cached")

	// put 1 key to cache2
	res, e := lc2.Get("key-1", func() (Value, error) {
		return "result-111", nil
	})
	assert.NoError(t, e)
	assert.Equal(t, "result-111", res.(string))

	// try to cache1 after maxKeys reached, will remove key-0
	res, err = lc1.Get("key-X", func() (Value, error) {
		return "result-X", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-X", res.(string))
	assert.Equal(t, 5, lc1.backend.Len())

	assert.Equal(t, 1, lc2.backend.Len(), "cache2 still has key-1")

	// try to cache1 after maxKeys reached, will remove key-1
	res, err = lc1.Get("key-X2", func() (Value, error) {
		return "result-X", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-X", res.(string))

	time.Sleep(time.Second)
	assert.Equal(t, 0, lc2.backend.Len(), "cache2 removed key-1")
	assert.NoError(t, redisPubSub1.Close())
	assert.NoError(t, redisPubSub2.Close())
}

// LruCache illustrates the use of LRU loading cache
func ExampleLruCache() {
	// set up test server for single response
	var hitCount int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.String() == "/post/42" && hitCount == 0 {
			_, _ = w.Write([]byte("<html><body>test response</body></html>"))
			return
		}
		w.WriteHeader(404)
	}))

	// load page function
	loadURL := func(url string) (string, error) {
		resp, err := http.Get(url) // nolint
		if err != nil {
			return "", err
		}
		b, err := ioutil.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return "", err
		}
		return string(b), nil
	}

	// fixed size LRU cache, 100 items, up to 10k in total size
	cache, err := NewLruCache(MaxKeys(100), MaxCacheSize(10*1024))
	if err != nil {
		log.Printf("can't make lru cache, %v", err)
	}

	// url not in cache, load data
	url := ts.URL + "/post/42"
	val, err := cache.Get(url, func() (val Value, err error) {
		return loadURL(url)
	})
	if err != nil {
		log.Fatalf("can't load url %s, %v", url, err)
	}
	fmt.Println(val.(string))

	// url not in cache, load data
	val, err = cache.Get(url, func() (val Value, err error) {
		return loadURL(url)
	})
	if err != nil {
		log.Fatalf("can't load url %s, %v", url, err)
	}
	fmt.Println(val.(string))

	// url cached, skip load and get from the cache
	val, err = cache.Get(url, func() (val Value, err error) {
		return loadURL(url)
	})
	if err != nil {
		log.Fatalf("can't load url %s, %v", url, err)
	}
	fmt.Println(val.(string))

	// get cache stats
	stats := cache.Stat()
	fmt.Printf("%+v\n", stats)

	// close test HTTP server after all log.Fatalf are passed
	ts.Close()

	// Output:
	// <html><body>test response</body></html>
	// <html><body>test response</body></html>
	// <html><body>test response</body></html>
	// {hits:2, misses:1, ratio:66.7%, keys:1, size:0, errors:0}
}
