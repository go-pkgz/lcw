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

	"github.com/go-redis/redis/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNop_Get(t *testing.T) {
	var coldCalls int32
	var c LoadingCache = NewNopCache()
	res, err := c.Get("key1", func() (Value, error) {
		atomic.AddInt32(&coldCalls, 1)
		return "result", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result", res.(string))
	assert.Equal(t, int32(1), atomic.LoadInt32(&coldCalls))

	res, err = c.Get("key1", func() (Value, error) {
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
	res, err := c.Get("key1", func() (Value, error) {
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
	assert.Equal(t, "{hits:60, misses:10, ratio:85.7%, keys:100, size:12345, errors:5}", s.String())
}

func TestCache_Get(t *testing.T) {
	caches, teardown := cachesTestList(t)
	defer teardown()

	for _, c := range caches {
		c := c
		t.Run(strings.Replace(fmt.Sprintf("%T", c), "*lcw.", "", 1), func(t *testing.T) {
			var coldCalls int32
			res, err := c.Get("key", func() (Value, error) {
				atomic.AddInt32(&coldCalls, 1)
				return "result", nil
			})
			assert.NoError(t, err)
			assert.Equal(t, "result", res.(string))
			assert.Equal(t, int32(1), atomic.LoadInt32(&coldCalls))

			res, err = c.Get("key", func() (Value, error) {
				atomic.AddInt32(&coldCalls, 1)
				return "result2", nil
			})

			assert.NoError(t, err)
			assert.Equal(t, "result", res.(string))
			assert.Equal(t, int32(1), atomic.LoadInt32(&coldCalls), "cache hit")

			_, err = c.Get("key-2", func() (Value, error) {
				atomic.AddInt32(&coldCalls, 1)
				return "result2", errors.New("some error")
			})
			assert.Error(t, err)
			assert.Equal(t, int32(2), atomic.LoadInt32(&coldCalls), "cache hit")

			_, err = c.Get("key-2", func() (Value, error) {
				atomic.AddInt32(&coldCalls, 1)
				return "result2", errors.New("some error")
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
			res, err := c.Get("key-Z", func() (Value, error) {
				return sizedString("result-Z"), nil
			})
			assert.NoError(t, err)
			assert.Equal(t, sizedString("result-Z"), res.(sizedString))

			res, err = c.Get("key-Z", func() (Value, error) {
				return sizedString("result-Zzzz"), nil
			})
			if s, ok := res.(string); ok {
				res = sizedString(s)
			}
			assert.NoError(t, err)
			assert.Equal(t, sizedString("result-Z"), res.(sizedString), "got cached value")

			// put too big value to cache and make sure it is not cached
			res, err = c.Get("key-Big", func() (Value, error) {
				return sizedString("1234567890"), nil
			})
			if s, ok := res.(string); ok {
				res = sizedString(s)
			}
			assert.NoError(t, err)
			assert.Equal(t, sizedString("1234567890"), res.(sizedString))

			res, err = c.Get("key-Big", func() (Value, error) {
				return sizedString("result-big"), nil
			})
			if s, ok := res.(string); ok {
				res = sizedString(s)
			}
			assert.NoError(t, err)
			assert.Equal(t, sizedString("result-big"), res.(sizedString), "got not cached value")

			// put too big value to cache but not Sizer
			res, err = c.Get("key-Big2", func() (Value, error) {
				return "1234567890", nil
			})
			assert.NoError(t, err)
			assert.Equal(t, "1234567890", res.(string))

			res, err = c.Get("key-Big2", func() (Value, error) {
				return "xyz", nil
			})
			assert.NoError(t, err)
			assert.Equal(t, "1234567890", res.(string), "too long, but not Sizer. from cache")
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
			res, err := c.Get("key-Z", func() (Value, error) {
				return sizedString("result-Z"), nil
			})
			assert.NoError(t, err)
			if s, ok := res.(string); ok {
				res = sizedString(s)
			}
			assert.Equal(t, sizedString("result-Z"), res.(sizedString))
			res, err = c.Get("key-Z", func() (Value, error) {
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
			_, err = c.Get("key-Z2", func() (Value, error) {
				return sizedString("result-Y"), nil
			})
			assert.NoError(t, err)
			if _, ok := c.(*RedisCache); !ok {
				assert.Equal(t, int64(16), c.size())
			}

			// this will cause removal
			_, err = c.Get("key-Z3", func() (Value, error) {
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
					time.Sleep(time.Duration(rand.Intn(100)) * time.Nanosecond)
					defer wg.Done()
					res, err := c.Get(fmt.Sprintf("key-%d", i), func() (Value, error) {
						return sizedString(fmt.Sprintf("result-%d", i)), nil
					})
					require.NoError(t, err)
					require.Equal(t, sizedString(fmt.Sprintf("result-%d", i)), res.(sizedString))
					size := c.size()
					require.True(t, size < 200 && size >= 0, "unexpected size=%d", size) // won't be exactly 123 due parallel
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
			res, err := c.Get("key", func() (Value, error) {
				return "value", nil
			})
			assert.NoError(t, err)
			assert.Equal(t, "value", res.(string))

			res, err = c.Get("key", func() (Value, error) {
				return "valueXXX", nil
			})
			assert.NoError(t, err)
			assert.Equal(t, "value", res.(string), "cached")

			res, err = c.Get("key1234", func() (Value, error) {
				return "value", nil
			})
			assert.NoError(t, err)
			assert.Equal(t, "value", res.(string))

			res, err = c.Get("key1234", func() (Value, error) {
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
			res, err := c.Get("key", func() (Value, error) {
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

			res, err := c.Get("key", func() (Value, error) {
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
					res, err := c.Get("key", func() (Value, error) {
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
				_, err := c.Get(fmt.Sprintf("key-%d", i), func() (Value, error) {
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
				_, err := c.Get(fmt.Sprintf("key-%d", i), func() (Value, error) {
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
			res, err := c.Get("key-1", func() (Value, error) {
				atomic.AddInt32(&coldCalls, 1)
				return "result-xxx", nil
			})
			require.NoError(t, err)
			assert.Equal(t, "result-1", res.(string), "from the cache")

			res, err = c.Get("key-10", func() (Value, error) {
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
				_, err := c.Get(fmt.Sprintf("key-%d", i), func() (Value, error) {
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
	var evVal Value
	var evCount int
	onEvict := func(key string, value Value) {
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
				_, err := c.Get(fmt.Sprintf("key-%d", i), func() (Value, error) {
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
				_, err := c.Get(fmt.Sprintf("key-%d", i), func() (Value, error) {
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

			_, err := c.Get("key-1", func() (Value, error) {
				return "xyz", nil
			})
			require.NoError(t, err)
			switch c.(type) {
			case *RedisCache:
				assert.Equal(t, CacheStat{Hits: 1, Misses: 100, Keys: 100, Size: 0}, c.Stat())
			default:
				assert.Equal(t, CacheStat{Hits: 1, Misses: 100, Keys: 100, Size: 890}, c.Stat())
			}

			_, err = c.Get("key-1123", func() (Value, error) {
				return sizedString("xyz"), nil
			})
			require.NoError(t, err)
			switch c.(type) {
			case *RedisCache:
				assert.Equal(t, CacheStat{Hits: 1, Misses: 101, Keys: 101, Size: 0}, c.Stat())
			default:
				assert.Equal(t, CacheStat{Hits: 1, Misses: 101, Keys: 101, Size: 893}, c.Stat())
			}

			_, err = c.Get("key-9999", func() (Value, error) {
				return nil, errors.New("err")
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

// LoadingCache illustrates creation of a cache and load
func ExampleLoadingCache_Get() {
	c, err := NewExpirableCache(MaxKeys(10), TTL(time.Minute*30)) // make expirable cache (30m TTL) with up to 10 keys
	if err != nil {
		panic("can' make cache")
	}
	defer c.Close()

	// try to get from cache and because mykey is not in will put it
	_, _ = c.Get("mykey", func() (Value, error) {
		fmt.Println("cache miss 1")
		return "myval-1", nil
	})

	// get from cache, func won't run because mykey in
	v, err := c.Get("mykey", func() (Value, error) {
		fmt.Println("cache miss 2")
		return "myval-2", nil
	})

	if err != nil {
		panic("can't get from cache")
	}
	fmt.Printf("got %s from cache, stats: %s", v.(string), c.Stat())
	// Output: cache miss 1
	// got myval-1 from cache, stats: {hits:1, misses:1, ratio:50.0%, keys:1, size:0, errors:0}
}

func ExampleLoadingCache_Delete() {
	// make expirable cache (30m TTL) with up to 10 keys. Set callback on eviction event
	c, err := NewExpirableCache(MaxKeys(10), TTL(time.Minute*30), OnEvicted(func(key string, value Value) {
		fmt.Println("key " + key + " evicted")
	}))
	if err != nil {
		panic("can' make cache")
	}
	defer c.Close()

	// try to get from cache and because mykey is not in will put it
	_, _ = c.Get("mykey", func() (Value, error) {
		return "myval-1", nil
	})

	c.Delete("mykey")
	fmt.Println("stats: " + c.Stat().String())
	// Output: key mykey evicted
	// stats: {hits:0, misses:1, ratio:0.0%, keys:0, size:0, errors:0}
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
