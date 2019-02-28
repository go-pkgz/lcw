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

func TestNop_Get(t *testing.T) {
	var coldCalls int32
	var c LoadingCache = NewNopCache()
	res, err := c.Get("key1", func() (Value, error) {
		atomic.AddInt32(&coldCalls, 1)
		return "result", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "result", res.(string))
	assert.Equal(t, int32(1), atomic.LoadInt32(&coldCalls))

	res, err = c.Get("key1", func() (Value, error) {
		atomic.AddInt32(&coldCalls, 1)
		return "result2", nil
	})
	assert.Nil(t, err)
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
	assert.Nil(t, err)
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

	caches := cachesTestList(t)

	for _, c := range caches {
		t.Run(strings.Replace(fmt.Sprintf("%T", c), "*lcw.", "", 1), func(t *testing.T) {
			var coldCalls int32
			res, err := c.Get("key", func() (Value, error) {
				atomic.AddInt32(&coldCalls, 1)
				return "result", nil
			})
			assert.Nil(t, err)
			assert.Equal(t, "result", res.(string))
			assert.Equal(t, int32(1), atomic.LoadInt32(&coldCalls))

			res, err = c.Get("key", func() (Value, error) {
				atomic.AddInt32(&coldCalls, 1)
				return "result2", nil
			})

			assert.Nil(t, err)
			assert.Equal(t, "result", res.(string))
			assert.Equal(t, int32(1), atomic.LoadInt32(&coldCalls), "cache hit")

			_, err = c.Get("key-2", func() (Value, error) {
				atomic.AddInt32(&coldCalls, 1)
				return "result2", errors.New("some error")
			})
			assert.NotNil(t, err)
			assert.Equal(t, int32(2), atomic.LoadInt32(&coldCalls), "cache hit")

			_, err = c.Get("key-2", func() (Value, error) {
				atomic.AddInt32(&coldCalls, 1)
				return "result2", errors.New("some error")
			})
			assert.NotNil(t, err)
			assert.Equal(t, int32(3), atomic.LoadInt32(&coldCalls), "cache hit")
		})
	}
}

func TestCache_MaxValueSize(t *testing.T) {
	caches := cachesTestList(t, MaxKeys(5), MaxValSize(10))

	for _, c := range caches {
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
			assert.NoError(t, err)
			assert.Equal(t, sizedString("result-Z"), res.(sizedString), "got cached value")

			// put too big value to cache and make sure it is not cached
			res, err = c.Get("key-Big", func() (Value, error) {
				return sizedString("1234567890"), nil
			})
			assert.NoError(t, err)
			assert.Equal(t, sizedString("1234567890"), res.(sizedString))

			res, err = c.Get("key-Big", func() (Value, error) {
				return sizedString("result-big"), nil
			})
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
	caches := cachesTestList(t, MaxKeys(50), MaxCacheSize(20))

	for _, c := range caches {
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
			assert.NoError(t, err)
			assert.Equal(t, sizedString("result-Z"), res.(sizedString), "got cached value")
			assert.Equal(t, int64(8), c.size())

			_, err = c.Get("key-Z2", func() (Value, error) {
				return sizedString("result-Y"), nil
			})
			assert.Nil(t, err)
			assert.Equal(t, int64(16), c.size())

			// this will cause removal
			_, err = c.Get("key-Z3", func() (Value, error) {
				return sizedString("result-Z"), nil
			})
			assert.Nil(t, err)
			assert.Equal(t, int64(16), c.size())
			assert.Equal(t, 2, c.keys())
		})
	}
}

func TestCache_MaxCacheSizeParallel(t *testing.T) {

	caches := cachesTestList(t, MaxCacheSize(123), MaxKeys(10000))

	for _, c := range caches {
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
					require.Nil(t, err)
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
	caches := cachesTestList(t, MaxKeySize(5))

	for _, c := range caches {
		t.Run(strings.Replace(fmt.Sprintf("%T", c), "*lcw.", "", 1), func(t *testing.T) {
			res, err := c.Get("key", func() (Value, error) {
				return "value", nil
			})
			assert.Nil(t, err)
			assert.Equal(t, "value", res.(string))

			res, err = c.Get("key", func() (Value, error) {
				return "valueXXX", nil
			})
			assert.Nil(t, err)
			assert.Equal(t, "value", res.(string), "cached")

			res, err = c.Get("key1234", func() (Value, error) {
				return "value", nil
			})
			assert.Nil(t, err)
			assert.Equal(t, "value", res.(string))

			res, err = c.Get("key1234", func() (Value, error) {
				return "valueXYZ", nil
			})
			assert.Nil(t, err)
			assert.Equal(t, "valueXYZ", res.(string), "not cached")
		})
	}
}

func TestCache_Peek(t *testing.T) {
	caches := cachesTestList(t)
	for _, c := range caches {
		t.Run(strings.Replace(fmt.Sprintf("%T", c), "*lcw.", "", 1), func(t *testing.T) {
			var coldCalls int32
			res, err := c.Get("key", func() (Value, error) {
				atomic.AddInt32(&coldCalls, 1)
				return "result", nil
			})
			assert.Nil(t, err)
			assert.Equal(t, "result", res.(string))
			assert.Equal(t, int32(1), atomic.LoadInt32(&coldCalls))

			r, ok := c.Peek("key")
			assert.True(t, ok)
			assert.Equal(t, "result", r.(string))
		})
	}

}

func TestLruCache_ParallelHits(t *testing.T) {
	caches := cachesTestList(t)
	for _, c := range caches {
		t.Run(strings.Replace(fmt.Sprintf("%T", c), "*lcw.", "", 1), func(t *testing.T) {
			var coldCalls int32

			res, err := c.Get("key", func() (Value, error) {
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
					res, err := c.Get("key", func() (Value, error) {
						atomic.AddInt32(&coldCalls, 1)
						return fmt.Sprintf("result-%d", i), nil
					})
					require.Nil(t, err)
					require.Equal(t, "value", res.(string))
				}()
			}
			wg.Wait()
			assert.Equal(t, int32(0), atomic.LoadInt32(&coldCalls))
		})
	}
}

func TestCache_Purge(t *testing.T) {

	caches := cachesTestList(t)
	for _, c := range caches {
		t.Run(strings.Replace(fmt.Sprintf("%T", c), "*lcw.", "", 1), func(t *testing.T) {
			var coldCalls int32
			// fill cache
			for i := 0; i < 1000; i++ {
				_, err := c.Get(fmt.Sprintf("key-%d", i), func() (Value, error) {
					atomic.AddInt32(&coldCalls, 1)
					return fmt.Sprintf("result-%d", i), nil
				})
				require.Nil(t, err)
			}
			assert.Equal(t, int32(1000), atomic.LoadInt32(&coldCalls))
			assert.Equal(t, 1000, c.keys())

			c.Purge()
			assert.Equal(t, 0, c.keys(), "all keys removed")
		})
	}
}

func TestCache_Invalidate(t *testing.T) {

	caches := cachesTestList(t)
	for _, c := range caches {
		t.Run(strings.Replace(fmt.Sprintf("%T", c), "*lcw.", "", 1), func(t *testing.T) {
			var coldCalls int32

			// fill cache
			for i := 0; i < 1000; i++ {
				_, err := c.Get(fmt.Sprintf("key-%d", i), func() (Value, error) {
					atomic.AddInt32(&coldCalls, 1)
					return fmt.Sprintf("result-%d", i), nil
				})
				require.Nil(t, err)
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

	caches := cachesTestList(t)
	for _, c := range caches {
		t.Run(strings.Replace(fmt.Sprintf("%T", c), "*lcw.", "", 1), func(t *testing.T) {
			// fill cache
			for i := 0; i < 1000; i++ {
				_, err := c.Get(fmt.Sprintf("key-%d", i), func() (Value, error) {
					return sizedString(fmt.Sprintf("result-%d", i)), nil
				})
				require.Nil(t, err)
			}
			assert.Equal(t, 1000, c.Stat().Keys)
			assert.Equal(t, int64(9890), c.Stat().Size)

			c.Delete("key-2")
			assert.Equal(t, 999, c.Stat().Keys)
			assert.Equal(t, int64(9890-8), c.Stat().Size)
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

	caches := cachesTestList(t, OnEvicted(onEvict))
	for _, c := range caches {
		evKey, evVal, evCount = "", "", 0
		t.Run(strings.Replace(fmt.Sprintf("%T", c), "*lcw.", "", 1), func(t *testing.T) {
			// fill cache
			for i := 0; i < 1000; i++ {
				_, err := c.Get(fmt.Sprintf("key-%d", i), func() (Value, error) {
					return sizedString(fmt.Sprintf("result-%d", i)), nil
				})
				require.Nil(t, err)
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
	caches := cachesTestList(t)
	for _, c := range caches {
		t.Run(strings.Replace(fmt.Sprintf("%T", c), "*lcw.", "", 1), func(t *testing.T) {
			// fill cache
			for i := 0; i < 100; i++ {
				_, err := c.Get(fmt.Sprintf("key-%d", i), func() (Value, error) {
					return sizedString(fmt.Sprintf("result-%d", i)), nil
				})
				require.Nil(t, err)
			}
			stats := c.Stat()
			assert.Equal(t, CacheStat{Hits: 0, Misses: 100, Keys: 100, Size: 890}, stats)

			_, err := c.Get("key-1", func() (Value, error) {
				return "xyz", nil
			})
			require.NoError(t, err)
			assert.Equal(t, CacheStat{Hits: 1, Misses: 100, Keys: 100, Size: 890}, c.Stat())

			_, err = c.Get("key-1123", func() (Value, error) {
				return sizedString("xyz"), nil
			})
			require.NoError(t, err)
			assert.Equal(t, CacheStat{Hits: 1, Misses: 101, Keys: 101, Size: 893}, c.Stat())

			_, err = c.Get("key-9999", func() (Value, error) {
				return nil, errors.New("err")
			})
			require.NotNil(t, err)
			assert.Equal(t, CacheStat{Hits: 1, Misses: 101, Keys: 101, Size: 893, Errors: 1}, c.Stat())
		})

	}
}

type counts interface {
	size() int64 // cache size in bytes
	keys() int   // number of keys in cache
}

type countedCache interface {
	LoadingCache
	counts
}

func cachesTestList(t *testing.T, opts ...Option) []countedCache {
	var caches []countedCache
	ec, err := NewExpirableCache(opts...)
	require.NoError(t, err, "can't make exp cache")
	caches = append(caches, ec)
	lc, err := NewLruCache(opts...)
	require.NoError(t, err, "can't make lru cache")
	caches = append(caches, lc)
	return caches
}

type sizedString string

func (s sizedString) Size() int { return len(s) }
