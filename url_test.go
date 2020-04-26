package lcw

import (
	"fmt"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/go-redis/redis/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUrl_optionsFromQuery(t *testing.T) {
	tbl := []struct {
		url  string
		num  int
		fail bool
	}{
		{"mem://lru?ttl=26s&max_keys=100&max_val_size=1024&max_key_size=64&max_cache_size=111", 5, false},
		{"mem://lru?ttl=26s&max_keys=100&foo=bar", 2, false},
		{"mem://lru?ttl=xx26s&max_keys=100&foo=bar", 0, true},
		{"mem://lru?foo=bar", 0, false},
		{"mem://lru?foo=bar&max_keys=abcd", 0, true},
		{"mem://lru?foo=bar&max_val_size=abcd", 0, true},
		{"mem://lru?foo=bar&max_cache_size=abcd", 0, true},
		{"mem://lru?foo=bar&max_key_size=abcd", 0, true},
	}

	for i, tt := range tbl {
		tt := tt
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			u, err := url.Parse(tt.url)
			require.NoError(t, err)
			r, err := optionsFromQuery(u.Query())
			if tt.fail {
				require.Error(t, err)
				return
			}
			assert.Equal(t, tt.num, len(r))
		})
	}
}

func TestUrl_redisOptionsFromURL(t *testing.T) {
	tbl := []struct {
		url  string
		fail bool
		opts redis.Options
	}{
		{"redis://127.0.0.1:12345?db=xa19", true, redis.Options{}},
		{"redis://127.0.0.1:12345?foo=bar&max_keys=abcd&db=19", false, redis.Options{Addr: "127.0.0.1:12345", DB: 19}},
		{
			"redis://127.0.0.1:12345?db=19&password=xyz&network=tcp4&dial_timeout=1s&read_timeout=2s&write_timeout=3m",
			false, redis.Options{Addr: "127.0.0.1:12345", DB: 19, Password: "xyz", Network: "tcp4",
				DialTimeout: 1 * time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 3 * time.Minute},
		},
	}

	for i, tt := range tbl {
		tt := tt
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			u, err := url.Parse(tt.url)
			require.NoError(t, err)
			r, err := redisOptionsFromURL(u)
			if tt.fail {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.opts, *r)
		})
	}
}

func TestUrl_NewLru(t *testing.T) {
	u := "mem://lru?max_keys=10"
	res, err := New(u)
	require.NoError(t, err)
	r, ok := res.(*LruCache)
	require.True(t, ok)
	assert.Equal(t, 10, r.maxKeys)
}

func TestUrl_NewExpirable(t *testing.T) {
	u := "mem://expirable?max_keys=10&ttl=30m"
	res, err := New(u)
	require.NoError(t, err)
	r, ok := res.(*ExpirableCache)
	require.True(t, ok)
	assert.Equal(t, 10, r.maxKeys)
	assert.Equal(t, 30*time.Minute, r.ttl)
}

func TestUrl_NewNop(t *testing.T) {
	u := "nop://"
	res, err := New(u)
	require.NoError(t, err)
	_, ok := res.(*Nop)
	require.True(t, ok)
}

func TestUrl_NewRedis(t *testing.T) {
	srv := newTestRedisServer()
	u := fmt.Sprintf("redis://%s?db=1&ttl=10s", srv.Addr())
	res, err := New(u)
	require.NoError(t, err)
	r, ok := res.(*RedisCache)
	require.True(t, ok)
	assert.Equal(t, 10*time.Second, r.ttl)

	u = fmt.Sprintf("redis://%s?db=1&ttl=zz10s", srv.Addr())
	_, err = New(u)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ttl query param zz10s: time: invalid duration zz10s")

	_, err = New("redis://localhost:xxx?db=1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse cache uri redis://localhost:xxx?db=1: parse")
	assert.Contains(t, err.Error(), "redis://localhost:xxx?db=1")
	assert.Contains(t, err.Error(), "invalid port \":xxx\" after host")
}

func TestUrl_NewFailed(t *testing.T) {
	u := "blah://ip?foo=bar"
	_, err := New(u)
	require.EqualError(t, err, "unsupported cache type blah")

	u = "mem://blah?foo=bar"
	_, err = New(u)
	require.EqualError(t, err, "unsupported mem cache type blah")

	u = "mem://lru?max_keys=xyz"
	_, err = New(u)
	require.EqualError(t, err, "parse uri options mem://lru?max_keys=xyz: 1 error occurred:\n\t* max_keys query param xyz: strconv.Atoi: parsing \"xyz\": invalid syntax\n\n")

}
