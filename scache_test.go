package lcw

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScache_Get(t *testing.T) {
	lru, err := NewLruCache()
	require.NoError(t, err)
	lc := NewScache(lru)

	var coldCalls int32

	res, err := lc.Get(NewKey("site").ID("key"), func() ([]byte, error) {
		atomic.AddInt32(&coldCalls, 1)
		return []byte("result"), nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result", string(res))
	assert.Equal(t, int32(1), atomic.LoadInt32(&coldCalls))

	res, err = lc.Get(NewKey("site").ID("key"), func() ([]byte, error) {
		atomic.AddInt32(&coldCalls, 1)
		return []byte("result"), nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "result", string(res))
	assert.Equal(t, int32(1), atomic.LoadInt32(&coldCalls))

	lc.Flush(Flusher("site"))
	time.Sleep(100 * time.Millisecond) // let postFn to do its thing

	_, err = lc.Get(NewKey("site").ID("key"), func() ([]byte, error) {
		return nil, errors.New("err")
	})
	assert.Error(t, err)
}

func TestScache_Scopes(t *testing.T) {
	lru, err := NewLruCache()
	require.NoError(t, err)
	lc := NewScache(lru)

	res, err := lc.Get(NewKey("site").ID("key").Scopes("s1", "s2"), func() ([]byte, error) {
		return []byte("value"), nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "value", string(res))

	res, err = lc.Get(NewKey("site").ID("key2").Scopes("s2"), func() ([]byte, error) {
		return []byte("value2"), nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "value2", string(res))

	assert.Equal(t, 2, len(lc.lc.Keys()))
	lc.Flush(Flusher("site").Scopes("s1"))
	assert.Equal(t, 1, len(lc.lc.Keys()))

	_, err = lc.Get(NewKey("site").ID("key2").Scopes("s2"), func() ([]byte, error) {
		assert.Fail(t, "should stay")
		return nil, nil
	})
	assert.NoError(t, err)
	res, err = lc.Get(NewKey("site").ID("key").Scopes("s1", "s2"), func() ([]byte, error) {
		return []byte("value-upd"), nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "value-upd", string(res), "was deleted, update")

	assert.Equal(t, CacheStat{Hits: 1, Misses: 3, Keys: 2, Size: 0, Errors: 0}, lc.Stat())
}

func TestScache_Flush(t *testing.T) {
	lru, err := NewLruCache()
	require.NoError(t, err)
	lc := NewScache(lru)

	addToCache := func(id string, scopes ...string) {
		res, err := lc.Get(NewKey("site").ID(id).Scopes(scopes...), func() ([]byte, error) {
			return []byte("value" + id), nil
		})
		require.NoError(t, err)
		require.Equal(t, "value"+id, string(res))
	}

	init := func() {
		lc.Flush(Flusher("site"))
		addToCache("key1", "s1", "s2")
		addToCache("key2", "s1", "s2", "s3")
		addToCache("key3", "s1", "s2", "s3")
		addToCache("key4", "s2", "s3")
		addToCache("key5", "s2")
		addToCache("key6")
		addToCache("key7", "s4", "s3")
		require.Equal(t, 7, len(lc.lc.Keys()), "cache init")
	}

	tbl := []struct {
		scopes []string
		left   int
		msg    string
	}{
		{[]string{}, 0, "full flush, no scopes"},
		{[]string{"s0"}, 7, "flush wrong scope"},
		{[]string{"s1"}, 4, "flush s1 scope"},
		{[]string{"s2", "s1"}, 2, "flush s2+s1 scope"},
		{[]string{"s1", "s2"}, 2, "flush s1+s2 scope"},
		{[]string{"s1", "s2", "s4"}, 1, "flush s1+s2+s4 scope"},
		{[]string{"s1", "s2", "s3"}, 1, "flush s1+s2+s3 scope"},
		{[]string{"s1", "s2", "ss"}, 2, "flush s1+s2+wrong scope"},
	}

	for i, tt := range tbl {
		tt := tt
		i := i
		t.Run(tt.msg, func(t *testing.T) {
			init()
			lc.Flush(Flusher("site").Scopes(tt.scopes...))
			assert.Equal(t, tt.left, len(lc.lc.Keys()), "keys size, %s #%d", tt.msg, i)
		})
	}
}

func TestScache_FlushFailed(t *testing.T) {
	lru, err := NewLruCache()
	require.NoError(t, err)
	lc := NewScache(lru)

	val, err := lc.Get(NewKey("site").ID("invalid-composite"), func() ([]byte, error) {
		return []byte("value"), nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "value", string(val))
	assert.Equal(t, 1, len(lc.lc.Keys()))

	lc.Flush(Flusher("site").Scopes("invalid-composite"))
	assert.Equal(t, 1, len(lc.lc.Keys()))
}

func TestScope_Key(t *testing.T) {
	tbl := []struct {
		key       string
		partition string
		scopes    []string
		full      string
	}{
		{"key1", "p1", []string{"s1"}, "p1@@key1@@s1"},
		{"key2", "p2", []string{"s11", "s2"}, "p2@@key2@@s11$$s2"},
		{"key3", "", []string{}, "@@key3@@"},
		{"key3", "", []string{"xx", "yyy"}, "@@key3@@xx$$yyy"},
	}

	for _, tt := range tbl {
		tt := tt
		t.Run(tt.full, func(t *testing.T) {
			k := NewKey(tt.partition).ID(tt.key).Scopes(tt.scopes...)
			assert.Equal(t, tt.full, k.String())
			k, err := parseKey(tt.full)
			require.NoError(t, err)
			assert.Equal(t, tt.partition, k.partition)
			assert.Equal(t, tt.key, k.id)
			assert.Equal(t, tt.scopes, k.scopes)
		})
	}

	// without partition
	k := NewKey().ID("id1").Scopes("s1", "s2")
	assert.Equal(t, "@@id1@@s1$$s2", k.String())

	// parse invalid key strings
	_, err := parseKey("abc")
	assert.Error(t, err)
	_, err = parseKey("")
	assert.Error(t, err)
}

func TestScache_Parallel(t *testing.T) {

	var coldCalls int32
	lru, err := NewLruCache()
	require.NoError(t, err)
	lc := NewScache(lru)

	res, err := lc.Get(NewKey("site").ID("key"), func() ([]byte, error) {
		return []byte("value"), nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "value", string(res))

	wg := sync.WaitGroup{}
	for i := 0; i < 1000; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			res, err := lc.Get(NewKey("site").ID("key"), func() ([]byte, error) {
				atomic.AddInt32(&coldCalls, 1)
				return []byte(fmt.Sprintf("result-%d", i)), nil
			})
			require.NoError(t, err)
			require.Equal(t, "value", string(res))
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(0), atomic.LoadInt32(&coldCalls))
}

// LruCache illustrates the use of LRU loading cache
func ExampleScache() {

	// load page function
	loadURL := func(url string) ([]byte, error) {
		resp, err := http.Get(url) // nolint
		if err != nil {
			return nil, err
		}
		b, err := ioutil.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}
		return b, nil
	}

	// fixed size LRU cache, 100 items, up to 10k in total size
	backend, err := NewLruCache(MaxKeys(100), MaxCacheSize(10*1024))
	if err != nil {
		log.Fatalf("can't make lru cache, %v", err)
	}

	cache := NewScache(backend)

	// url not in cache, load data
	url := "https://radio-t.com/online/"
	key := NewKey().ID(url).Scopes("test")
	val, err := cache.Get(key, func() (val []byte, err error) {
		return loadURL(url)
	})
	if err != nil {
		log.Fatalf("can't load url %s, %v", url, err)
	}
	log.Print(string(val))

	// url not in cache, load data
	url = "https://radio-t.com/info/"
	key = NewKey().ID(url).Scopes("test")
	val, err = cache.Get(key, func() (val []byte, err error) {
		return loadURL(url)
	})
	if err != nil {
		log.Fatalf("can't load url %s, %v", url, err)
	}
	log.Print(string(val))

	// url cached, skip load and get from the cache
	url = "https://radio-t.com/online/"
	key = NewKey().ID(url).Scopes("test")
	val, err = cache.Get(key, func() (val []byte, err error) {
		return loadURL(url)
	})
	if err != nil {
		log.Fatalf("can't load url %s, %v", url, err)
	}
	log.Print(string(val))

	// get cache stats
	stats := cache.Stat()
	log.Printf("%+v", stats)
}
