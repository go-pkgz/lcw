package cache

import (
	"errors"
	"fmt"
	"log"
	"math/rand"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadingCacheNoPurge(t *testing.T) {
	var lc LoadingCache
	lc = NewLoadingCache()

	called := 0
	v, err := lc.Get("key1", time.Minute, func() (data interface{}, err error) {
		called++
		return "val1", nil
	})

	assert.Nil(t, err)
	assert.Equal(t, "val1", v)
	assert.Equal(t, 1, called)

	v, err = lc.Get("key1", time.Minute, func() (data interface{}, err error) {
		called++
		return "val1", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "val1", v)
	assert.Equal(t, 1, called)

	v, err = lc.Get("key2", time.Minute, func() (data interface{}, err error) {
		called++
		return "val2", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "val2", v)
	assert.Equal(t, 2, called)
	assert.Equal(t, 2, lc.Stat().Size)
}

func TestLoadingCacheWithPurge(t *testing.T) {
	lc := NewLoadingCache(PurgeEvery(time.Millisecond * 50))
	called := 0

	v, err := lc.Get("key1", 150*time.Millisecond, func() (data interface{}, err error) {
		called++
		return "val1", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "val1", v)
	assert.Equal(t, 1, called)

	time.Sleep(100 * time.Millisecond) // not enougth to expire
	assert.Equal(t, 1, lc.Stat().Size)

	v, err = lc.Get("key1", 150*time.Millisecond, func() (data interface{}, err error) {
		called++
		return "val1", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "val1", v)
	assert.Equal(t, 1, called)

	time.Sleep(200 * time.Millisecond) //expire
	v, err = lc.Get("key1", 150*time.Millisecond, func() (data interface{}, err error) {
		called++
		return "val1", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "val1", v)
	assert.Equal(t, 2, called, "second call")

	assert.Equal(t, 1, lc.Stat().Size)
	t.Log(lc)
}

func TestLoadingCacheWithPurgeEnforcedBySize(t *testing.T) {
	lc := NewLoadingCache(PurgeEvery(time.Hour), MaxKeys(10))
	called := 0

	for i := 0; i < 100; i++ {
		v, err := lc.Get(fmt.Sprintf("key%d", i), 150*time.Millisecond, func() (data interface{}, err error) {
			called++
			return fmt.Sprintf("val%d", i), nil
		})
		assert.Nil(t, err)
		assert.Equal(t, fmt.Sprintf("val%d", i), v)
		assert.True(t, lc.Stat().Size < 20)
	}

	assert.Equal(t, 100, called)
	assert.Equal(t, 10, lc.Stat().Size)
	assert.Equal(t, 90, lc.Stat().Evicted)
}

func TestLoadingCacheDelete(t *testing.T) {
	lc := NewLoadingCache()

	called := 0
	v, err := lc.Get("key1", time.Minute, func() (data interface{}, err error) {
		called++
		return "val1", nil
	})

	assert.Nil(t, err)
	assert.Equal(t, "val1", v)
	assert.Equal(t, 1, called)

	lc.Invalidate("key1")

	v, err = lc.Get("key1", time.Minute, func() (data interface{}, err error) {
		called++
		return "val1", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "val1", v)
	assert.Equal(t, 2, called)
}

func TestLoadingCacheWithPurgeMax(t *testing.T) {
	lc := NewLoadingCache(PurgeEvery(time.Millisecond*50), MaxKeys(2))

	called := 0

	v, err := lc.Get("key1", time.Minute, func() (data interface{}, err error) {
		called++
		return "val1", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "val1", v)
	assert.Equal(t, 1, called)

	v, err = lc.Get("key2", time.Minute, func() (data interface{}, err error) {
		called++
		return "val2", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "val2", v)
	assert.Equal(t, 2, called)

	v, err = lc.Get("key3", time.Minute, func() (data interface{}, err error) {
		called++
		return "val3", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "val3", v)
	assert.Equal(t, 3, called)

	assert.Equal(t, 3, lc.Stat().Size)

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 2, lc.Stat().Size)

	_, found := lc.GetValue("key1")
	assert.False(t, found, "key1 should be deleted")
	t.Logf("%+v", lc.Stat())
}

func TestLoadingCacheWithPurgeMaxLru(t *testing.T) {
	lc := NewLoadingCache(PurgeEvery(time.Millisecond*50), MaxKeys(2), LRU())

	called := 0

	v, err := lc.Get("key1", time.Minute, func() (data interface{}, err error) {
		called++
		return "val1", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "val1", v)
	assert.Equal(t, 1, called)

	v, err = lc.Get("key2", time.Minute, func() (data interface{}, err error) {
		called++
		return "val2", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "val2", v)
	assert.Equal(t, 2, called)

	v, err = lc.Get("key3", time.Minute, func() (data interface{}, err error) {
		called++
		return "val3", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "val3", v)
	assert.Equal(t, 3, called)

	assert.Equal(t, 3, lc.Stat().Size)

	// read key1 again, changes LRU
	v, err = lc.Get("key1", time.Minute, func() (data interface{}, err error) {
		assert.Fail(t, "should be cached already")
		return "val1", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "val1", v)

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 2, lc.Stat().Size)

	_, found := lc.GetValue("key2")
	assert.False(t, found, "key2 should be deleted")
	t.Logf("%+v", lc.Stat())
}

func TestLoadingCacheStat(t *testing.T) {
	lc := NewLoadingCache(PurgeEvery(time.Millisecond*10), MaxKeys(50)) //50 records max

	// 10 distinct records, 20 hits for each one
	for i := 0; i < 200; i++ {
		v, err := lc.Get(fmt.Sprintf("key-%d", i%10), time.Millisecond*500, func() (data interface{}, err error) {
			return "val", nil
		})
		assert.Nil(t, err)
		assert.Equal(t, "val", v)
		time.Sleep(1 * time.Millisecond)
	}
	assert.Equal(t, Stats{Size: 10, Hits: 190, Misses: 10, Added: 10, Evicted: 0}, lc.Stat())
	t.Log(lc)

	time.Sleep(500 * time.Millisecond) // let records to expire
	assert.Equal(t, Stats{Size: 0, Hits: 190, Misses: 10, Added: 10, Evicted: 10}, lc.Stat())
}

func TestLoadingCacheConsistency(t *testing.T) {
	lc := NewLoadingCache()
	wg := sync.WaitGroup{}
	wg.Add(1000)
	for i := 0; i < 1000; i++ {
		go func(i int) {
			_, e := lc.Get(fmt.Sprintf("key-%d", i/10), time.Minute, func() (data interface{}, err error) {
				return fmt.Sprintf("val-%d", i/10), nil
			})
			assert.Nil(t, e)
			wg.Done()
		}(i)
	}
	wg.Wait()
	t.Log(lc)
	assert.Equal(t, 900, lc.Stat().Hits)
}

func TestLoadingCacheConcurrentUpdate(t *testing.T) {
	slowUpdate := func() (list []string) {
		for i := 0; i < 100; i++ {
			list = append(list, "string #"+strconv.Itoa(i))
			d := rand.Int31n(1000)
			if d > 800 {
				time.Sleep(10 * time.Millisecond)
			}
		}
		return list
	}

	lc := NewLoadingCache(MaxKeys(10), PurgeEvery(time.Millisecond*1))
	wg := sync.WaitGroup{}
	wg.Add(100)
	for i := 0; i < 100; i++ {
		go func(i int) {
			time.Sleep(10 * time.Millisecond)
			r, e := lc.Get(fmt.Sprintf("key"), time.Millisecond*100, func() (data interface{}, err error) {
				return slowUpdate(), nil
			})
			require.Nil(t, e)
			assert.Equal(t, 100, len(r.([]string)))
			wg.Done()
		}(i)
	}
	wg.Wait()
	t.Log(lc)
}

func TestLoadingCacheGetValue(t *testing.T) {
	lc := NewLoadingCache()
	v, err := lc.Get("key1", time.Minute, func() (data interface{}, err error) {
		return "val1", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "val1", v)

	val, ok := lc.GetValue("key1")
	assert.True(t, ok)
	assert.Equal(t, "val1", val)

	lc.Invalidate("key1")
	_, ok = lc.GetValue("key1")
	assert.False(t, ok)
}

func TestLoadingCacheWithErrorNotAllowed(t *testing.T) {

	lc := NewLoadingCache(PurgeEvery(time.Hour*10), MaxKeys(50))
	hits := 0
	for i := 0; i < 10; i++ {
		_, e := lc.Get("key", time.Hour, func() (data interface{}, err error) {
			hits++
			return nil, errors.New("errrr")
		})
		assert.NotNil(t, e)
	}
	assert.Equal(t, 10, hits, "all err hits not cached")

	assert.Equal(t, 1, lc.Stat().Size)
	assert.Equal(t, 0, lc.Stat().Hits)
	assert.Equal(t, 10, lc.Stat().Misses)
}

func TestLoadingCacheWithErrorAllowed(t *testing.T) {

	lc := NewLoadingCache(PurgeEvery(time.Hour*10), MaxKeys(50), AllowErrors())
	hits := 0
	for i := 0; i < 10; i++ {
		_, _ = lc.Get("key", time.Hour, func() (data interface{}, err error) {
			hits++
			return nil, errors.New("errrr")
		})
	}
	assert.Equal(t, 1, hits, "all err hits cached")

	assert.Equal(t, 1, lc.Stat().Size)
	assert.Equal(t, 9, lc.Stat().Hits)
	assert.Equal(t, 1, lc.Stat().Misses)
}

func ExampleLoadingCache() {
	// make cache with every 200ms purge and 3 max keys
	cache := NewLoadingCache(PurgeEvery(time.Millisecond*200), MaxKeys(3))

	// get value, store under key1 with TTL=1s
	r, err := cache.Get("key1", time.Second, func() (data interface{}, err error) {
		return "val1", nil
	})

	if err != nil { // check for nil before any type assertion
		log.Fatal(err)
	}

	rstr := r.(string) // convert cached value from interface{} to real type
	fmt.Print(rstr, " ")

	// get from key1, won't hit cache func, not expired yet
	r, _ = cache.Get("key1", time.Second, func() (data interface{}, err error) {
		return "val2", nil
	})
	fmt.Print(r, " ")
	fmt.Printf("%+v\n", cache.Stat())
	// Output: val1 val1 {Size:1 Hits:1 Misses:1 Added:1 Evicted:0}
}
