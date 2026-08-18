package lcw

import (
	"fmt"
	"math/rand"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNop_Get(t *testing.T) {
	var coldCalls int32
	var c LoadingCache = NewNopCache()
	res, err := c.Get("key1", func() (any, error) {
		atomic.AddInt32(&coldCalls, 1)
		return "result", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result", res.(string))
	assert.Equal(t, int32(1), atomic.LoadInt32(&coldCalls))

	res, err = c.Get("key1", func() (any, error) {
		atomic.AddInt32(&coldCalls, 1)
		return "result2", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result2", res.(string))
	assert.Equal(t, int32(2), atomic.LoadInt32(&coldCalls))

	assert.Equal(t, CacheStat{}, c.Stat())
}

func TestNop_Peek(t *testing.T) {
	var coldCalls int32
	c := NewNopCache()
	res, err := c.Get("key1", func() (any, error) {
		atomic.AddInt32(&coldCalls, 1)
		return "result", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result", res.(string))
	assert.Equal(t, int32(1), atomic.LoadInt32(&coldCalls))

	_, ok := c.Peek("key1")
	assert.False(t, ok)
}

func TestStat_String(t *testing.T) {
	s := CacheStat{Keys: 100, Hits: 60, Misses: 10, Size: 12345, Errors: 5}
	assert.Equal(t, "{hits:60, misses:10, ratio:0.86, keys:100, size:12345, errors:5}", s.String())
}

func TestCache_Get(t *testing.T) {
	caches, teardown := cachesTestList(t)
	defer teardown()

	for _, c := range caches {
		c := c
		t.Run(strings.Replace(fmt.Sprintf("%T", c), "*lcw.", "", 1), func(t *testing.T) {
			var coldCalls int32
			res, err := c.Get("key", func() (any, error) {
				atomic.AddInt32(&coldCalls, 1)
				return "result", nil
			})
			assert.NoError(t, err)
			assert.Equal(t, "result", res.(string))
			assert.Equal(t, int32(1), atomic.LoadInt32(&coldCalls))

			res, err = c.Get("key", func() (any, error) {
				atomic.AddInt32(&coldCalls, 1)
				return "result2", nil
			})

			assert.NoError(t, err)
			assert.Equal(t, "result", res.(string))
			assert.Equal(t, int32(1), atomic.LoadInt32(&coldCalls), "cache hit")

			_, err = c.Get("key-2", func() (any, error) {
				atomic.AddInt32(&coldCalls, 1)
				return "result2", fmt.Errorf("some error")
			})
			assert.Error(t, err)
			assert.Equal(t, int32(2), atomic.LoadInt32(&coldCalls), "cache hit")

			_, err = c.Get("key-2", func() (any, error) {
				atomic.AddInt32(&coldCalls, 1)
				return "result2", fmt.Errorf("some error")
			})
			assert.Error(t, err)
			assert.Equal(t, int32(3), atomic.LoadInt32(&coldCalls), "cache hit")
		})
	}
}

func TestCache_MaxValueSize(t *testing.T) {
	caches, teardown := cachesTestList(t, MaxKeys(5), MaxValSize(10))
	defer teardown()

	for _, c := range caches {
		c := c
		t.Run(strings.Replace(fmt.Sprintf("%T", c), "*lcw.", "", 1), func(t *testing.T) {
			// put good size value to cache and make sure it cached
			res, err := c.Get("key-Z", func() (any, error) {
				return sizedString("result-Z"), nil
			})
			assert.NoError(t, err)
			assert.Equal(t, sizedString("result-Z"), res.(sizedString))

			res, err = c.Get("key-Z", func() (any, error) {
				return sizedString("result-Zzzz"), nil
			})
			if s, ok := res.(string); ok {
				res = sizedString(s)
			}
			assert.NoError(t, err)
			assert.Equal(t, sizedString("result-Z"), res.(sizedString), "got cached value")

			// put too big value to cache and make sure it is not cached
			res, err = c.Get("key-Big", func() (any, error) {
				return sizedString("1234567890"), nil
			})
			if s, ok := res.(string); ok {
				res = sizedString(s)
			}
			assert.NoError(t, err)
			assert.Equal(t, sizedString("1234567890"), res.(sizedString))

			res, err = c.Get("key-Big", func() (any, error) {
				return sizedString("result-big"), nil
			})
			if s, ok := res.(string); ok {
				res = sizedString(s)
			}
			assert.NoError(t, err)
			assert.Equal(t, sizedString("result-big"), res.(sizedString), "got not cached value")

			// put too big plain string to cache, sized by length even without Sizer
			res, err = c.Get("key-Big2", func() (any, error) {
				return "1234567890", nil
			})
			assert.NoError(t, err)
			assert.Equal(t, "1234567890", res.(string))

			res, err = c.Get("key-Big2", func() (any, error) {
				return "xyz", nil
			})
			assert.NoError(t, err)
			assert.Equal(t, "xyz", res.(string), "too long string, not cached")
		})
	}
}

func TestCache_MaxCacheSize(t *testing.T) {
	caches, teardown := cachesTestList(t, MaxKeys(50), MaxCacheSize(20))
	defer teardown()

	for _, c := range caches {
		c := c
		t.Run(strings.Replace(fmt.Sprintf("%T", c), "*lcw.", "", 1), func(t *testing.T) {
			// put good size value to cache and make sure it cached
			res, err := c.Get("key-Z", func() (any, error) {
				return sizedString("result-Z"), nil
			})
			assert.NoError(t, err)
			if s, ok := res.(string); ok {
				res = sizedString(s)
			}
			assert.Equal(t, sizedString("result-Z"), res.(sizedString))
			res, err = c.Get("key-Z", func() (any, error) {
				return sizedString("result-Zzzz"), nil
			})
			if s, ok := res.(string); ok {
				res = sizedString(s)
			}
			assert.NoError(t, err)
			assert.Equal(t, sizedString("result-Z"), res.(sizedString), "got cached value")
			if _, ok := c.(*RedisCache); !ok {
				assert.Equal(t, int64(8), c.size())
			}
			_, err = c.Get("key-Z2", func() (any, error) {
				return sizedString("result-Y"), nil
			})
			assert.NoError(t, err)
			if _, ok := c.(*RedisCache); !ok {
				assert.Equal(t, int64(16), c.size())
			}

			// this will cause removal
			_, err = c.Get("key-Z3", func() (any, error) {
				return sizedString("result-Z"), nil
			})
			assert.NoError(t, err)
			if _, ok := c.(*RedisCache); !ok {
				assert.Equal(t, int64(16), c.size())
				// Due RedisCache does not support MaxCacheSize this assert should be skipped
				assert.Equal(t, 2, c.keys())
			}
		})
	}
}

func TestCache_MaxCacheSizeParallel(t *testing.T) {
	caches, teardown := cachesTestList(t, MaxCacheSize(123), MaxKeys(10000))
	defer teardown()

	for _, c := range caches {
		c := c
		t.Run(strings.Replace(fmt.Sprintf("%T", c), "*lcw.", "", 1), func(t *testing.T) {
			wg := sync.WaitGroup{}
			for i := 0; i < 1000; i++ {
				wg.Add(1)
				i := i
				go func() {
					//nolint:gosec // not used for security	purpose
					time.Sleep(time.Duration(rand.Intn(100)) * time.Nanosecond)
					defer wg.Done()
					res, err := c.Get(fmt.Sprintf("key-%d", i), func() (any, error) {
						return sizedString(fmt.Sprintf("result-%d", i)), nil
					})
					require.NoError(t, err)
					require.Equal(t, sizedString(fmt.Sprintf("result-%d", i)), res.(sizedString))
				}()
			}
			wg.Wait()
			assert.True(t, c.size() < 123 && c.size() >= 0)
			t.Log("size", c.size())
		})
	}

}

func TestCache_MaxKeySize(t *testing.T) {
	caches, teardown := cachesTestList(t, MaxKeySize(5))
	defer teardown()

	for _, c := range caches {
		c := c
		t.Run(strings.Replace(fmt.Sprintf("%T", c), "*lcw.", "", 1), func(t *testing.T) {
			res, err := c.Get("key", func() (any, error) {
				return "value", nil
			})
			assert.NoError(t, err)
			assert.Equal(t, "value", res.(string))

			res, err = c.Get("key", func() (any, error) {
				return "valueXXX", nil
			})
			assert.NoError(t, err)
			assert.Equal(t, "value", res.(string), "cached")

			res, err = c.Get("key1234", func() (any, error) {
				return "value", nil
			})
			assert.NoError(t, err)
			assert.Equal(t, "value", res.(string))

			res, err = c.Get("key1234", func() (any, error) {
				return "valueXYZ", nil
			})
			assert.NoError(t, err)
			assert.Equal(t, "valueXYZ", res.(string), "not cached")
		})
	}
}

func TestCache_Peek(t *testing.T) {
	caches, teardown := cachesTestList(t)
	defer teardown()

	for _, c := range caches {
		c := c
		t.Run(strings.Replace(fmt.Sprintf("%T", c), "*lcw.", "", 1), func(t *testing.T) {
			var coldCalls int32
			res, err := c.Get("key", func() (any, error) {
				atomic.AddInt32(&coldCalls, 1)
				return "result", nil
			})
			assert.NoError(t, err)
			assert.Equal(t, "result", res.(string))
			assert.Equal(t, int32(1), atomic.LoadInt32(&coldCalls))

			r, ok := c.Peek("key")
			assert.True(t, ok)
			assert.Equal(t, "result", r.(string))
		})
	}
}

func TestLruCache_ParallelHits(t *testing.T) {
	caches, teardown := cachesTestList(t)
	defer teardown()

	for _, c := range caches {
		c := c
		t.Run(strings.Replace(fmt.Sprintf("%T", c), "*lcw.", "", 1), func(t *testing.T) {
			var coldCalls int32

			res, err := c.Get("key", func() (any, error) {
				return "value", nil
			})
			assert.NoError(t, err)
			assert.Equal(t, "value", res.(string))

			wg := sync.WaitGroup{}
			for i := 0; i < 1000; i++ {
				wg.Add(1)
				i := i
				go func() {
					defer wg.Done()
					res, err := c.Get("key", func() (any, error) {
						atomic.AddInt32(&coldCalls, 1)
						return fmt.Sprintf("result-%d", i), nil
					})
					require.NoError(t, err)
					require.Equal(t, "value", res.(string))
				}()
			}
			wg.Wait()
			assert.Equal(t, int32(0), atomic.LoadInt32(&coldCalls))
		})
	}
}

func TestCache_Purge(t *testing.T) {
	caches, teardown := cachesTestList(t)
	defer teardown()

	for _, c := range caches {
		c := c
		t.Run(strings.Replace(fmt.Sprintf("%T", c), "*lcw.", "", 1), func(t *testing.T) {
			var coldCalls int32
			// fill cache
			for i := 0; i < 1000; i++ {
				i := i
				_, err := c.Get(fmt.Sprintf("key-%d", i), func() (any, error) {
					atomic.AddInt32(&coldCalls, 1)
					return fmt.Sprintf("result-%d", i), nil
				})
				require.NoError(t, err)
			}
			assert.Equal(t, int32(1000), atomic.LoadInt32(&coldCalls))
			assert.Equal(t, 1000, c.keys())

			c.Purge()
			assert.Equal(t, 0, c.keys(), "all keys removed")
		})
	}
}

func TestCache_Invalidate(t *testing.T) {
	caches, teardown := cachesTestList(t)
	defer teardown()

	for _, c := range caches {
		c := c
		t.Run(strings.Replace(fmt.Sprintf("%T", c), "*lcw.", "", 1), func(t *testing.T) {
			var coldCalls int32

			// fill cache
			for i := 0; i < 1000; i++ {
				i := i
				_, err := c.Get(fmt.Sprintf("key-%d", i), func() (any, error) {
					atomic.AddInt32(&coldCalls, 1)
					return fmt.Sprintf("result-%d", i), nil
				})
				require.NoError(t, err)
			}
			assert.Equal(t, int32(1000), atomic.LoadInt32(&coldCalls))
			assert.Equal(t, 1000, c.keys())

			c.Invalidate(func(key string) bool {
				return strings.HasSuffix(key, "0")
			})

			assert.Equal(t, 900, c.keys(), "100 keys removed")
			res, err := c.Get("key-1", func() (any, error) {
				atomic.AddInt32(&coldCalls, 1)
				return "result-xxx", nil
			})
			require.NoError(t, err)
			assert.Equal(t, "result-1", res.(string), "from the cache")

			res, err = c.Get("key-10", func() (any, error) {
				atomic.AddInt32(&coldCalls, 1)
				return "result-xxx", nil
			})
			require.NoError(t, err)
			assert.Equal(t, "result-xxx", res.(string), "not from the cache")
		})
	}
}

func TestCache_Delete(t *testing.T) {
	caches, teardown := cachesTestList(t)
	defer teardown()

	for _, c := range caches {
		c := c
		t.Run(strings.Replace(fmt.Sprintf("%T", c), "*lcw.", "", 1), func(t *testing.T) {
			// fill cache
			for i := 0; i < 1000; i++ {
				i := i
				_, err := c.Get(fmt.Sprintf("key-%d", i), func() (any, error) {
					return sizedString(fmt.Sprintf("result-%d", i)), nil
				})
				require.NoError(t, err)
			}
			assert.Equal(t, 1000, c.Stat().Keys)
			if _, ok := c.(*RedisCache); !ok {
				assert.Equal(t, int64(9890), c.Stat().Size)
			}
			c.Delete("key-2")
			assert.Equal(t, 999, c.Stat().Keys)
			if _, ok := c.(*RedisCache); !ok {
				assert.Equal(t, int64(9890-8), c.Stat().Size)
			}
		})
	}
}

func TestCache_DeleteWithEvent(t *testing.T) {
	var evKey string
	var evVal any
	var evCount int
	onEvict := func(key string, value any) {
		evKey = key
		evVal = value
		evCount++
	}

	caches, teardown := cachesTestList(t, OnEvicted(onEvict))
	defer teardown()

	for _, c := range caches {
		c := c

		evKey, evVal, evCount = "", "", 0
		t.Run(strings.Replace(fmt.Sprintf("%T", c), "*lcw.", "", 1), func(t *testing.T) {
			if _, ok := c.(*RedisCache); ok {
				t.Skip("RedisCache doesn't support delete events")
			}
			// fill cache
			for i := 0; i < 1000; i++ {
				i := i
				_, err := c.Get(fmt.Sprintf("key-%d", i), func() (any, error) {
					return sizedString(fmt.Sprintf("result-%d", i)), nil
				})
				require.NoError(t, err)
			}
			assert.Equal(t, 1000, c.Stat().Keys)
			assert.Equal(t, int64(9890), c.Stat().Size)

			c.Delete("key-2")
			assert.Equal(t, 999, c.Stat().Keys)
			assert.Equal(t, "key-2", evKey)
			assert.Equal(t, sizedString("result-2"), evVal)
			assert.Equal(t, 1, evCount)
		})
	}
}

func TestCache_Stats(t *testing.T) {
	caches, teardown := cachesTestList(t)
	defer teardown()

	for _, c := range caches {
		c := c
		t.Run(strings.Replace(fmt.Sprintf("%T", c), "*lcw.", "", 1), func(t *testing.T) {
			// fill cache
			for i := 0; i < 100; i++ {
				i := i
				_, err := c.Get(fmt.Sprintf("key-%d", i), func() (any, error) {
					return sizedString(fmt.Sprintf("result-%d", i)), nil
				})
				require.NoError(t, err)
			}
			stats := c.Stat()
			switch c.(type) {
			case *RedisCache:
				assert.Equal(t, CacheStat{Hits: 0, Misses: 100, Keys: 100, Size: 0}, stats)
			default:
				assert.Equal(t, CacheStat{Hits: 0, Misses: 100, Keys: 100, Size: 890}, stats)
			}

			_, err := c.Get("key-1", func() (any, error) {
				return "xyz", nil
			})
			require.NoError(t, err)
			switch c.(type) {
			case *RedisCache:
				assert.Equal(t, CacheStat{Hits: 1, Misses: 100, Keys: 100, Size: 0}, c.Stat())
			default:
				assert.Equal(t, CacheStat{Hits: 1, Misses: 100, Keys: 100, Size: 890}, c.Stat())
			}

			_, err = c.Get("key-1123", func() (any, error) {
				return sizedString("xyz"), nil
			})
			require.NoError(t, err)
			switch c.(type) {
			case *RedisCache:
				assert.Equal(t, CacheStat{Hits: 1, Misses: 101, Keys: 101, Size: 0}, c.Stat())
			default:
				assert.Equal(t, CacheStat{Hits: 1, Misses: 101, Keys: 101, Size: 893}, c.Stat())
			}

			_, err = c.Get("key-9999", func() (any, error) {
				return nil, fmt.Errorf("err")
			})
			require.Error(t, err)
			switch c.(type) {
			case *RedisCache:
				assert.Equal(t, CacheStat{Hits: 1, Misses: 101, Keys: 101, Size: 0, Errors: 1}, c.Stat())
			default:
				assert.Equal(t, CacheStat{Hits: 1, Misses: 101, Keys: 101, Size: 893, Errors: 1}, c.Stat())
			}
		})
	}
}

// ExampleLoadingCache_Get illustrates creation of a cache and loading value from it
func ExampleLoadingCache_Get() {
	c, err := NewExpirableCache(MaxKeys(10), TTL(time.Minute*30)) // make expirable cache (30m TTL) with up to 10 keys
	if err != nil {
		panic("can' make cache")
	}
	defer c.Close()

	// try to get from cache and because mykey is not in will put it
	_, _ = c.Get("mykey", func() (any, error) {
		fmt.Println("cache miss 1")
		return "myval-1", nil
	})

	// get from cache, func won't run because mykey in
	v, err := c.Get("mykey", func() (any, error) {
		fmt.Println("cache miss 2")
		return "myval-2", nil
	})

	if err != nil {
		panic("can't get from cache")
	}
	fmt.Printf("got %s from cache, stats: %s", v.(string), c.Stat())
	// Output: cache miss 1
	// got myval-1 from cache, stats: {hits:1, misses:1, ratio:0.50, keys:1, size:7, errors:0}
}

// ExampleLoadingCache_Delete illustrates cache value eviction and OnEvicted function usage.
func ExampleLoadingCache_Delete() {
	// make expirable cache (30m TTL) with up to 10 keys. Set callback on eviction event
	c, err := NewExpirableCache(MaxKeys(10), TTL(time.Minute*30), OnEvicted(func(key string, _ any) {
		fmt.Println("key " + key + " evicted")
	}))
	if err != nil {
		panic("can' make cache")
	}
	defer c.Close()

	// try to get from cache and because mykey is not in will put it
	_, _ = c.Get("mykey", func() (any, error) {
		return "myval-1", nil
	})

	c.Delete("mykey")
	fmt.Println("stats: " + c.Stat().String())
	// Output: key mykey evicted
	// stats: {hits:0, misses:1, ratio:0.00, keys:0, size:0, errors:0}
}

// nolint:govet //false positive due to example name
// ExampleLoadingCacheMutability illustrates changing mutable stored item outside of cache, works only for non-Redis cache.
func Example_loadingCacheMutability() {
	c, err := NewExpirableCache(MaxKeys(10), TTL(time.Minute*30)) // make expirable cache (30m TTL) with up to 10 keys
	if err != nil {
		panic("can' make cache")
	}
	defer c.Close()

	mutableSlice := []string{"key1", "key2"}

	// put mutableSlice in "mutableSlice" cache key
	_, _ = c.Get("mutableSlice", func() (any, error) {
		return mutableSlice, nil
	})

	// get from cache, func won't run because mutableSlice is cached
	// value is original now
	v, _ := c.Get("mutableSlice", func() (any, error) {
		return nil, nil
	})
	fmt.Printf("got %v slice from cache\n", v)

	mutableSlice[0] = "another_key_1"
	mutableSlice[1] = "another_key_2"

	// get from cache, func won't run because mutableSlice is cached
	// value is changed inside the cache now because mutableSlice stored as-is, in mutable state
	v, _ = c.Get("mutableSlice", func() (any, error) {
		return nil, nil
	})
	fmt.Printf("got %v slice from cache after it's change outside of cache\n", v)

	// Output:
	// got [key1 key2] slice from cache
	// got [another_key_1 another_key_2] slice from cache after it's change outside of cache
}

type counts interface {
	size() int64 // cache size in bytes
	keys() int   // number of keys in cache
}

type countedCache interface {
	LoadingCache
	counts
}

func cachesTestList(t *testing.T, opts ...Option) (c []countedCache, teardown func()) {
	var caches []countedCache
	ec, err := NewExpirableCache(opts...)
	require.NoError(t, err, "can't make exp cache")
	caches = append(caches, ec)
	lc, err := NewLruCache(opts...)
	require.NoError(t, err, "can't make lru cache")
	caches = append(caches, lc)

	server := newTestRedisServer()
	client := redis.NewClient(&redis.Options{
		Addr: server.Addr()})
	rc, err := NewRedisCache(client, opts...)
	require.NoError(t, err, "can't make redis cache")
	caches = append(caches, rc)

	return caches, func() {
		_ = client.Close()
		_ = ec.Close()
		_ = lc.Close()
		_ = rc.Close()
		server.Close()
	}
}

type sizedString string

func (s sizedString) Size() int { return len(s) }

func (s sizedString) MarshalBinary() (data []byte, err error) {
	return []byte(s), nil
}

type mockPubSub struct {
	calledKeys []string
	fns        []func(fromID, key string)
	sync.Mutex
	sync.WaitGroup
}

func (m *mockPubSub) CalledKeys() []string {
	m.Lock()
	defer m.Unlock()
	return m.calledKeys
}

func (m *mockPubSub) Subscribe(fn func(fromID, key string)) error {
	m.Lock()
	defer m.Unlock()
	m.fns = append(m.fns, fn)
	return nil
}

func (m *mockPubSub) Publish(fromID, key string) error {
	m.Lock()
	defer m.Unlock()
	m.calledKeys = append(m.calledKeys, key)
	for _, fn := range m.fns {
		fn := fn
		m.Add(1)
		// run in goroutine to prevent deadlock
		go func() {
			fn(fromID, key)
			m.Done()
		}()
	}
	return nil
}

func TestCache_ConcurrentSameKeyLoad(t *testing.T) {
	caches, teardown := cachesTestList(t, MaxKeys(50), MaxCacheSize(1000))
	defer teardown()

	for _, c := range caches {
		c := c
		t.Run(strings.Replace(fmt.Sprintf("%T", c), "*lcw.", "", 1), func(t *testing.T) {
			var coldCalls int32
			var wg sync.WaitGroup
			start := make(chan struct{})

			for i := 0; i < 10; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					res, err := c.Get("key", func() (any, error) {
						atomic.AddInt32(&coldCalls, 1)
						time.Sleep(10 * time.Millisecond) // make the overlap wide enough
						return sizedString("result"), nil
					})
					assert.NoError(t, err)
					if s, ok := res.(string); ok {
						res = sizedString(s)
					}
					assert.Equal(t, sizedString("result"), res)
				}()
			}
			close(start)
			wg.Wait()

			assert.Equal(t, int32(1), atomic.LoadInt32(&coldCalls), "loaded once for all callers")
			assert.Equal(t, 1, c.Stat().Keys)
			assert.Equal(t, int64(1), c.Stat().Misses, "one miss counted")
		})
	}
}

func TestCache_ConcurrentSameKeySizeAccounting(t *testing.T) {
	// concurrent cold loads used to add the value size once per caller, overflowing maxCacheSize
	// and leaving the lru eviction loop spinning over an empty cache
	lc, err := NewLruCache(MaxKeys(50), MaxCacheSize(10))
	require.NoError(t, err)
	defer lc.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, e := lc.Get("key", func() (any, error) {
					time.Sleep(10 * time.Millisecond)
					return sizedString("123456"), nil
				})
				assert.NoError(t, e)
			}()
		}
		wg.Wait()
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		require.Fail(t, "concurrent loads hang, size accounting overflowed")
	}

	assert.Equal(t, int64(6), lc.Stat().Size, "size counted once")
	assert.Equal(t, 1, lc.Stat().Keys)
}

func TestCache_MaxKeysZeroUnlimited(t *testing.T) {
	// MaxKeys(0) is documented as unlimited, it used to reject every insert in the expirable cache
	// and fail the lru cache construction
	ec, err := NewExpirableCache(MaxKeys(0))
	require.NoError(t, err)
	defer ec.Close()

	lc, err := NewLruCache(MaxKeys(0))
	require.NoError(t, err)
	defer lc.Close()

	for _, c := range []countedCache{ec, lc} {
		c := c
		t.Run(strings.Replace(fmt.Sprintf("%T", c), "*lcw.", "", 1), func(t *testing.T) {
			var coldCalls int32
			for i := 0; i < 2; i++ {
				_, err := c.Get("key", func() (any, error) {
					atomic.AddInt32(&coldCalls, 1)
					return "value", nil
				})
				require.NoError(t, err)
			}
			assert.Equal(t, int32(1), atomic.LoadInt32(&coldCalls), "second get from cache")
			assert.Equal(t, 1, c.Stat().Keys)

			for i := 0; i < 100; i++ {
				_, err := c.Get(fmt.Sprintf("key-%d", i), func() (any, error) {
					return "value", nil
				})
				require.NoError(t, err)
			}
			assert.Equal(t, 101, c.Stat().Keys, "no limit applied")
		})
	}
}

func TestCache_LoaderPanicReleasesWaiters(t *testing.T) {
	// a panicking loader used to release waiters with a zero value and no error,
	// so they silently treated it as a successful load
	lc, err := NewLruCache(MaxKeys(50))
	require.NoError(t, err)
	defer lc.Close()

	leaderIn, release := make(chan struct{}), make(chan struct{})
	var waiterEntered, waiterLoaded int32
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			assert.NotNil(t, recover(), "panic still propagates to the caller that loaded")
		}()
		_, _ = lc.Get("key", func() (any, error) {
			close(leaderIn)
			<-release // held until the waiter is in, so it joins this load instead of starting its own
			panic("loader blew up")
		})
	}()

	<-leaderIn
	done := make(chan struct{})
	var waiterVal any
	var waiterErr error
	go func() {
		defer close(done)
		atomic.StoreInt32(&waiterEntered, 1)
		waiterVal, waiterErr = lc.Get("key", func() (any, error) {
			atomic.StoreInt32(&waiterLoaded, 1)
			return "should not be called", nil
		})
	}()

	require.Eventually(t, func() bool { return atomic.LoadInt32(&waiterEntered) == 1 },
		5*time.Second, time.Millisecond, "waiter goroutine did not start")
	runtime.Gosched()
	time.Sleep(50 * time.Millisecond) // let the waiter reach the load group
	close(release)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		require.Fail(t, "waiter not released after the loader panicked")
	}
	wg.Wait()

	require.Zero(t, atomic.LoadInt32(&waiterLoaded), "waiter joined the in-flight load")
	require.ErrorIs(t, waiterErr, ErrLoaderPanic, "waiter gets an error, not a silent zero value")
	assert.Nil(t, waiterVal)
	assert.Equal(t, 0, lc.Stat().Keys, "nothing cached")
}
