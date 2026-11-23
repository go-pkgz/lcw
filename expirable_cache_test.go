package lcw

import (
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/go-pkgz/lcw/internal/cache"
)

func TestExpirableCache(t *testing.T) {
	lc, err := NewExpirableCache(MaxKeys(5), TTL(time.Millisecond*100))
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		i := i
		_, e := lc.Get(fmt.Sprintf("key-%d", i), func() (any, error) {
			return fmt.Sprintf("result-%d", i), nil
		})
		assert.NoError(t, e)
		time.Sleep(10 * time.Millisecond)
	}

	assert.Equal(t, 5, lc.Stat().Keys)
	assert.Equal(t, int64(5), lc.Stat().Misses)

	keys := lc.Keys()
	slices.Sort(keys)
	assert.EqualValues(t, []string{"key-0", "key-1", "key-2", "key-3", "key-4"}, keys)

	_, e := lc.Get("key-xx", func() (any, error) {
		return "result-xx", nil
	})
	assert.NoError(t, e)
	assert.Equal(t, 5, lc.Stat().Keys)
	assert.Equal(t, int64(6), lc.Stat().Misses)

	// let key-0 expire, GitHub Actions friendly way
	require.Eventually(t, func() bool {
		lc.backend.DeleteExpired() // enforce DeleteExpired for GitHub earlier than TTL/2
		return lc.Stat().Keys <= 4 // keys expire in sequence, an exact count races with the next one going
	}, 5*time.Second, 10*time.Millisecond, "key-0 should expire")

	require.Eventually(t, func() bool {
		lc.backend.DeleteExpired()
		return lc.keys() == 0
	}, 5*time.Second, 10*time.Millisecond, "all keys should expire")
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
		res, e := lc.Get(fmt.Sprintf("key-%d", i), func() (any, error) {
			atomic.AddInt32(&coldCalls, 1)
			return fmt.Sprintf("result-%d", i), nil
		})
		assert.NoError(t, e)
		assert.Equal(t, fmt.Sprintf("result-%d", i), res.(string))
		assert.Equal(t, int32(i+1), atomic.LoadInt32(&coldCalls))
	}

	// check if really cached
	res, err := lc.Get("key-3", func() (any, error) {
		return "result-blah", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-3", res.(string), "should be cached")

	// try to cache after maxKeys reached
	res, err = lc.Get("key-X", func() (any, error) {
		return "result-X", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-X", res.(string))
	assert.Equal(t, 5, lc.keys())

	// put to cache and make sure it cached
	res, err = lc.Get("key-Z", func() (any, error) {
		return "result-Z", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result-Z", res.(string))

	res, err = lc.Get("key-Z", func() (any, error) {
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

	// cache2 keeps its keys for the whole test, so only an event from cache1 can remove key-1
	lc2, err := NewExpirableCache(MaxKeys(50), TTL(time.Hour), EventBus(ps))
	require.NoError(t, err)
	defer lc2.Close()

	// add 5 keys to the first node cache
	for i := 0; i < 5; i++ {
		i := i
		_, e := lc1.Get(fmt.Sprintf("key-%d", i), func() (any, error) {
			return fmt.Sprintf("result-%d", i), nil
		})
		assert.NoError(t, e)
		time.Sleep(10 * time.Millisecond)
	}

	assert.Equal(t, 0, len(ps.CalledKeys()), "no events")
	assert.Equal(t, 5, lc1.Stat().Keys)
	assert.Equal(t, int64(5), lc1.Stat().Misses)

	// add key-1 key to the second node
	_, e := lc2.Get("key-1", func() (any, error) {
		return "result-111", nil
	})
	assert.NoError(t, e)
	assert.Equal(t, 1, lc2.Stat().Keys)
	assert.Equal(t, int64(1), lc2.Stat().Misses, lc2.Stat())

	// let key-0 expire, GitHub Actions friendly way. keys expire 10ms apart, so by the time this
	// sees it more of them may be gone, and only the first expiry is certain. the expiry is
	// published after the backend unlocks, so the event can trail the key count
	require.Eventually(t, func() bool {
		lc1.backend.DeleteExpired() // enforce DeleteExpired for GitHub earlier than TTL/2
		ps.Wait()                   // wait for onBusEvent goroutines to finish
		return lc1.Stat().Keys <= 4 && slices.Contains(ps.CalledKeys(), "key-0")
	}, 5*time.Second, 10*time.Millisecond, "key-0 should expire and be published")

	require.Eventually(t, func() bool {
		lc1.backend.DeleteExpired()
		ps.Wait() // wait for onBusEvent goroutines to finish
		return lc1.Stat().Keys == 0 && lc2.Stat().Keys == 0 && len(ps.CalledKeys()) >= 6
	}, 5*time.Second, 10*time.Millisecond, "all keys should expire, key-1 removed from cache2")
	// five expiries in cache1, and cache2 publishes once more when it removes key-1
	assert.Equal(t, 6, len(ps.CalledKeys()), "6 events, key-1 expired %+v", ps.CalledKeys())
}

func TestExpirableCache_ConcurrentAdmission(t *testing.T) {
	loadAll := func(t *testing.T, c *ExpirableCache, n int, val func(i int) sizedString) {
		t.Helper()
		var arrived, done sync.WaitGroup
		start := make(chan struct{})
		arrived.Add(n)
		done.Add(n)
		res := make([]any, n)
		for i := 0; i < n; i++ {
			go func(i int) {
				defer done.Done()
				res[i], _ = c.Get(fmt.Sprintf("key-%d", i), func() (any, error) {
					arrived.Done()
					<-start
					return val(i), nil
				})
			}(i)
		}
		arrived.Wait()
		close(start)
		done.Wait()
		for i := 0; i < n; i++ {
			assert.Equal(t, val(i), res[i])
		}
	}

	t.Run("max cache size", func(t *testing.T) {
		for round := 0; round < 200; round++ {
			c, err := NewExpirableCache(MaxCacheSize(100), MaxKeys(1000))
			require.NoError(t, err)
			loadAll(t, c, 100, func(i int) sizedString { return sizedString(fmt.Sprintf("%060d", i)) })
			require.Equal(t, int64(60), c.size(), "round %d", round)
			require.Equal(t, 1, c.keys(), "round %d", round)
			require.NoError(t, c.Close())
		}
	})

	t.Run("max keys", func(t *testing.T) {
		for round := 0; round < 200; round++ {
			var evicted int32
			c, err := NewExpirableCache(MaxKeys(5), TTL(time.Hour),
				OnEvicted(func(string, any) { atomic.AddInt32(&evicted, 1) }))
			require.NoError(t, err)
			loadAll(t, c, 50, func(i int) sizedString { return sizedString(fmt.Sprintf("value-%d", i)) })
			require.Equal(t, 5, c.keys(), "round %d", round)
			require.Equal(t, int32(0), atomic.LoadInt32(&evicted), "round %d", round)
			require.NoError(t, c.Close())
		}
	})
}

// a Get that misses on an expired key still held by the backend reloads it and stores it again
func TestExpirableCache_ReloadExpiredKey(t *testing.T) {
	t.Run("size of the expired value is subtracted", func(t *testing.T) {
		var evicted []any
		c, err := NewExpirableCache(OnEvicted(func(_ string, v any) { evicted = append(evicted, v) }))
		require.NoError(t, err)
		expireSoon(t, c)

		_, err = c.Get("a", func() (any, error) { return "0123456789", nil })
		require.NoError(t, err)
		waitExpired(t, c, "a")
		v, err := c.Get("a", func() (any, error) { return "abcdefghij", nil })
		require.NoError(t, err)
		assert.Equal(t, "abcdefghij", v)
		assert.Equal(t, int64(10), c.Stat().Size)
		assert.Equal(t, 1, c.Stat().Keys)
		assert.Equal(t, []any{"0123456789"}, evicted, "expired value reported as evicted")

		c.Delete("a")
		assert.Equal(t, int64(0), c.Stat().Size)
	})

	for name, opt := range map[string]Option{"max keys": MaxKeys(1), "max cache size": MaxCacheSize(15)} {
		t.Run("full cache takes the reloaded value, "+name, func(t *testing.T) {
			c, err := NewExpirableCache(opt)
			require.NoError(t, err)
			expireSoon(t, c)

			_, err = c.Get("a", func() (any, error) { return "0123456789", nil })
			require.NoError(t, err)
			waitExpired(t, c, "a")
			_, err = c.Get("a", func() (any, error) { return "abcdefghij", nil })
			require.NoError(t, err)
			v, ok := c.Peek("a")
			require.True(t, ok)
			assert.Equal(t, "abcdefghij", v)
			assert.Equal(t, 1, c.Stat().Keys)
			assert.Equal(t, int64(10), c.Stat().Size)
		})
	}

	t.Run("OnEvicted may get the same key", func(t *testing.T) {
		var c *ExpirableCache
		var err error
		var inner []any
		c, err = NewExpirableCache(OnEvicted(func(key string, _ any) {
			v, e := c.Get(key, func() (any, error) { return "from OnEvicted", nil })
			assert.NoError(t, e)
			inner = append(inner, v)
		}))
		require.NoError(t, err)
		expireSoon(t, c)

		_, err = c.Get("a", func() (any, error) { return "old", nil })
		require.NoError(t, err)
		waitExpired(t, c, "a")
		done := make(chan any)
		go func() {
			v, e := c.Get("a", func() (any, error) { return "new", nil })
			assert.NoError(t, e)
			done <- v
		}()
		select {
		case v := <-done:
			assert.Equal(t, "new", v)
		case <-time.After(5 * time.Second):
			require.FailNow(t, "Get of a key whose OnEvicted gets the same key did not return")
		}
		assert.Equal(t, []any{"new"}, inner, "OnEvicted runs after the new value is stored")
	})

	t.Run("OnEvicted may load the same key", func(t *testing.T) {
		var c *ExpirableCache
		var err error
		c, err = NewExpirableCache(MaxValSize(5), OnEvicted(func(key string, _ any) {
			_, e := c.Get(key, func() (any, error) { return "abc", nil })
			assert.NoError(t, e)
		}))
		require.NoError(t, err)
		expireSoon(t, c)

		_, err = c.Get("a", func() (any, error) { return "old", nil })
		require.NoError(t, err)
		waitExpired(t, c, "a")
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, e := c.Get("a", func() (any, error) { return "too long", nil }) // not cached, over MaxValSize
			assert.NoError(t, e)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			require.FailNow(t, "Get of a key whose OnEvicted loads the same key did not return")
		}
		v, ok := c.Peek("a")
		assert.True(t, ok)
		assert.Equal(t, "abc", v, "the value OnEvicted loaded is cached")
	})

	t.Run("OnEvicted of an entry removed for room may get the key being loaded", func(t *testing.T) {
		var c *ExpirableCache
		var err error
		c, err = NewExpirableCache(MaxKeys(2), MaxCacheSize(2), OnEvicted(func(key string, _ any) {
			if key == "item" {
				_, e := c.Get("index", func() (any, error) { return "x", nil })
				assert.NoError(t, e)
			}
		}))
		require.NoError(t, err)
		expireSoon(t, c)

		for _, k := range []string{"index", "item"} {
			_, err = c.Get(k, func() (any, error) { return "1", nil })
			require.NoError(t, err)
		}
		waitExpired(t, c, "index")
		waitExpired(t, c, "item")
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, e := c.Get("index", func() (any, error) { return "22", nil }) // over MaxCacheSize with item in place
			assert.NoError(t, e)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			require.FailNow(t, "Get that makes room for its value did not return")
		}
		v, ok := c.Peek("index")
		assert.True(t, ok)
		assert.Equal(t, "x", v, "the value OnEvicted loaded is cached")
		assert.Equal(t, int64(1), c.Stat().Size)
	})

	t.Run("other instances keep the key", func(t *testing.T) {
		ps := &mockPubSub{}
		a, err := NewExpirableCache(EventBus(ps))
		require.NoError(t, err)
		expireSoon(t, a)
		b, err := NewExpirableCache(EventBus(ps))
		require.NoError(t, err)
		defer b.Close()

		for _, c := range []*ExpirableCache{a, b} {
			_, err = c.Get("k", func() (any, error) { return "v", nil })
			require.NoError(t, err)
		}
		waitExpired(t, a, "k")
		_, err = a.Get("k", func() (any, error) { return "v2", nil })
		require.NoError(t, err)
		ps.Wait()

		assert.Empty(t, ps.CalledKeys(), "reloading an expired key publishes nothing")
		v, ok := a.Peek("k")
		assert.True(t, ok)
		assert.Equal(t, "v2", v)
		v, ok = b.Peek("k")
		assert.True(t, ok)
		assert.Equal(t, "v", v)
	})
}

// expireSoon replaces the backend of c with one that expires entries after 200ms and never purges them,
// so an expired entry stays in it until something removes it
func expireSoon(t *testing.T, c *ExpirableCache) {
	t.Helper()
	require.NoError(t, c.Close())
	backend, err := cache.NewLoadingCache(cache.TTL(200*time.Millisecond), cache.OnEvicted(c.onBackendEvicted))
	require.NoError(t, err)
	c.backend = backend
}

// waitExpired waits until the entry of key has expired, and checks it is still in the backend
func waitExpired(t *testing.T, c *ExpirableCache, key string) {
	t.Helper()
	require.Eventually(t, func() bool { _, ok := c.Peek(key); return !ok }, 5*time.Second, time.Millisecond,
		"entry of %q did not expire", key)
	require.Contains(t, c.Keys(), key, "expired entry is still in the backend")
}
