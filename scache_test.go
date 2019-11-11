package lcw

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCache_Scopes(t *testing.T) {
	lru, err := NewLruCache()
	require.NoError(t, err)
	lc := NewScache(lru)

	res, err := lc.Get(NewKey("site").ID("key").Scopes("s1", "s2"), func() ([]byte, error) {
		return []byte("value"), nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "value", string(res))

	res, err = lc.Get(NewKey("site").ID("key2").Scopes("s2"), func() ([]byte, error) {
		return []byte("value2"), nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "value2", string(res))

	assert.Equal(t, 2, len(lc.lc.Keys()))
	lc.Flush(Flusher("site").Scopes("s1"))
	assert.Equal(t, 1, len(lc.lc.Keys()))

	_, err = lc.Get(NewKey("site").ID("key2").Scopes("s2"), func() ([]byte, error) {
		assert.Fail(t, "should stay")
		return nil, nil
	})
	assert.Nil(t, err)
	res, err = lc.Get(NewKey("site").ID("key").Scopes("s1", "s2"), func() ([]byte, error) {
		return []byte("value-upd"), nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "value-upd", string(res), "was deleted, update")

	assert.Equal(t, CacheStat{Hits: 1, Misses: 3, Keys: 2, Size: 0, Errors: 0}, lc.Stat())
}

func TestCache_Flush(t *testing.T) {
	lru, err := NewLruCache()
	require.NoError(t, err)
	lc := NewScache(lru)

	addToCache := func(id string, scopes ...string) {
		res, err := lc.Get(NewKey("site").ID(id).Scopes(scopes...), func() ([]byte, error) {
			return []byte("value" + id), nil
		})
		require.Nil(t, err)
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
		t.Run(tt.msg, func(t *testing.T) {
			init()
			lc.Flush(Flusher("site").Scopes(tt.scopes...))
			assert.Equal(t, tt.left, len(lc.lc.Keys()), "keys size, %s #%d", tt.msg, i)
		})
	}
}

func TestCache_FlushFailed(t *testing.T) {
	lru, err := NewLruCache()
	require.NoError(t, err)
	lc := NewScache(lru)

	val, err := lc.Get(NewKey("site").ID("invalid-composite"), func() ([]byte, error) {
		return []byte("value"), nil
	})
	assert.Nil(t, err)
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
