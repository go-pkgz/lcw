# Loading Cache Wrapper [![Build Status](https://github.com/go-pkgz/lcw/workflows/build/badge.svg)](https://github.com/go-pkgz/lcw/actions) [![Coverage Status](https://coveralls.io/repos/github/go-pkgz/lcw/badge.svg?branch=master)](https://coveralls.io/github/go-pkgz/lcw?branch=master) [![godoc](https://godoc.org/github.com/go-pkgz/lcw?status.svg)](https://godoc.org/github.com/go-pkgz/lcw/v2)

The library adds a thin layer on top of the LRU and expirable caches of [hashicorp/golang-lru](https://github.com/hashicorp/golang-lru) (v1 uses its own expirable cache).

| Cache name     | Constructor                           | Defaults          | Description             |
|----------------|---------------------------------------|-------------------|-------------------------|
| LruCache       | `lcw.NewLruCache(opts...)`            | keys=1000         | LRU cache with limits   |
| ExpirableCache | `lcw.NewExpirableCache(opts...)`      | keys=1000, ttl=5m | TTL cache with limits   |
| RedisCache     | `lcw.NewRedisCache(client, opts...)`  | ttl=5m            | Redis cache with limits |
| Nop            | `lcw.NewNopCache[string]()`           |                   | Do-nothing cache        |

`client` is a `redis.UniversalClient`. In v2 the value type is inferred from the options, so `NewNopCache`, which
has none, takes it explicitly.

Main features:

- LoadingCache (guava style)
- Limit maximum cache size (in bytes)
- Limit maximum key size
- Limit maximum size of a value
- Limit number of keys
- TTL support (`ExpirableCache` and `RedisCache`)
- Callback on eviction event (not supported in `RedisCache`; with v2 `ExpirableCache` the `OnEvicted` handler
  must not call the same cache, or it deadlocks)
- Functional style invalidation
- Functional options
- Sane defaults

## Install and update

`go get -u github.com/go-pkgz/lcw/v2`

v1 (`github.com/go-pkgz/lcw`) has the same options, except `StrToV`, as package functions,
`lcw.NewLruCache(lcw.MaxKeys(500))`, with `any` values.

## Usage

```go
package main

import (
	"fmt"

	"github.com/go-pkgz/lcw/v2"
)

func main() {
	o := lcw.NewOpts[string]()
	cache, err := lcw.NewLruCache(o.MaxKeys(500), o.MaxCacheSize(65536), o.MaxValSize(200), o.MaxKeySize(32))
	if err != nil {
		panic("failed to create cache")
	}
	defer cache.Close()

	val, err := cache.Get("key123", func() (string, error) {
		return "value123", nil // load the value from the actual source here
	})

	if err != nil {
		panic("failed to get data")
	}

	fmt.Println(val) // cached value
}
```

### Cache with URI

`lcw.New[V](uri)` creates a cache from a URI (`lcw.New(uri)` in v1):

- `mem://lru?max_key_size=10&max_val_size=1024&max_keys=50&max_cache_size=64000` - creates LRU cache with given limits
- `mem://expirable?ttl=30s&max_key_size=10&max_val_size=1024&max_keys=50&max_cache_size=64000` - create expirable cache
- `redis://10.0.0.1:1234?db=1&password=qwerty&network=tcp4&redis_key_prefix=lcw:` - create redis cache, the port and
  `db` are required; also accepts `dial_timeout`, `read_timeout` and `write_timeout`
- `nop://` - create Nop cache

## Scoped cache

`Scache` provides a wrapper on top of all implementations of `LoadingCache` with a number of special features:

1. Key is not a string, but a composed type made from partition, key-id and list of scopes (tags).
1. Value type is generic in v2 and limited to `[]byte` in v1.
1. Added `Flush` method for scoped/tagged invalidation of multiple records in a given partition.
   It only touches keys of the requested partition, with no scopes set the whole partition is dropped.
1. A simplified interface with Get, Stat, Flush and Close only.

```go
o := lcw.NewOpts[string]()
lc, err := lcw.NewExpirableCache(o.MaxKeys(1000), o.TTL(time.Minute))
if err != nil {
	panic("failed to create cache")
}
sc := lcw.NewScache[string](lc)
defer sc.Close()

key := lcw.NewKey("site1").ID("post42").Scopes("posts", "user7")
val, err := sc.Get(key, func() (string, error) {
	return "post body", nil // load the value from the actual source here
})
if err != nil {
	panic("failed to get data")
}
fmt.Println(val)

sc.Flush(lcw.Flusher("site1").Scopes("user7")) // drops every key of site1 with the user7 scope
```

`Flush` with several scopes removes the keys that have any of them. The underlying cache stores the composed key
`<partition>@@<id>@@<scope1>$$<scope2>`, with `%`, `@` and `$` escaped inside the parts, so `MaxKeySize` applies
to that string, not to the id alone.

Note that `RedisCache` in v2 stores string-based values only, so it can't back a `Scache[[]byte]`.
In v1 `Scache` over `RedisCache` works, values come back as bytes. v1 `RedisCache` itself returns cached values
as `string`; store strings, byte slices or numbers only.

## Distributed invalidation

The `EventBus` option keeps several `LruCache` or `ExpirableCache` instances, usually in different processes,
consistent: each of them publishes the keys it evicts, deletes or lets expire, and removes the keys the others
publish. `eventbus.NewRedisPubSub(addr, channel)` provides such a bus over Redis pub/sub, and another implementation
of the `eventbus.PubSub` interface can be used instead. Its `Publish` must not wait for the subscribers to handle
the message, as a v2 `ExpirableCache` publishes while it holds the lock of its backend.

```go
bus, err := eventbus.NewRedisPubSub("127.0.0.1:6379", "lcw-events")
if err != nil {
	panic("failed to connect to redis")
}
defer bus.Close()

o := lcw.NewOpts[string]()
cache, err := lcw.NewExpirableCache(o.TTL(time.Minute), o.EventBus(bus))
if err != nil {
	panic("failed to create cache")
}
defer cache.Close()
```

- `RedisCache` ignores the option, its instances share the data in Redis already.
- Give each cache its own `RedisPubSub`. A second `Subscribe` on the same one starts a competing reader, and each
  message reaches only one of the two caches.
- An `ExpirableCache` that replaces an expired entry, not yet cleaned up, with a newly loaded value does not
  publish the key.
- `Close` of a v2 `ExpirableCache` removes its entries without publishing them, so the other instances keep theirs.

## Development

The repository holds two modules, the root one for v1 and `v2` for the generics-based version. Both have to be
tested:

```
go test -race ./... && (cd v2 && go test -race ./...)
```

## Details

- In all cache types other than Redis (e.g. LRU and Expirable at the moment) values are stored as-is which means
  that mutable values can be changed outside of cache. `Example_loadingCacheMutability` illustrates that.
- All byte-size limits (MaxCacheSize and MaxValSize) work for values implementing the `lcw.Sizer` interface,
  as well as for `[]byte` and `string` values sized by their length. Values of any other type are not limited,
  named types such as `json.RawMessage` included; implement `Sizer` for them.
- `MaxCacheSize` is not supported by `RedisCache`, the option is accepted but ignored and `Stat` reports size 0.
- `MaxKeys(0)` means unlimited, as do all other limits set to 0. `TTL(0)` means entries do not expire.
- Limits are inclusive: a key of exactly `MaxKeySize` bytes and a value of exactly `MaxValSize` bytes are cached,
  and a cache holds up to `MaxKeys` keys and `MaxCacheSize` bytes.
- At `MaxKeys` or `MaxCacheSize`, `LruCache` evicts the least recently used entries. `ExpirableCache`, and
  `RedisCache` at `MaxKeys`, do not cache the new value, which is still returned, until older entries expire or
  are removed; with `TTL(0)` they never expire.
- `Stat` returns `CacheStat` with `Hits`, `Misses`, `Keys`, `Size` and `Errors`, all zero for `Nop`. A failed load
  counts in `Errors`, not in `Misses`, and callers that waited for another caller's load of the same key count in
  neither. For `ExpirableCache`, `Keys` and `Keys()` include expired entries that are not cleaned up yet.
- A v2 `ExpirableCache` with a TTL other than 0 keeps one goroutine running after `Close`, so create caches once,
  not per request or test.
- `RedisCache` counts keys on the server before each write, so concurrent writers, including callers sharing
  one `RedisCache`, can push it past `MaxKeys`.
- Negative limits (max options) rejected
- Concurrent `Get` calls for the same missing key run the loader once, the rest wait for its result.
  A loader must not call `Get` for the same key on the same cache, it would wait for itself.
- By default `RedisCache` assumes exclusive ownership of the selected redis database, i.e. `Purge` flushes it
  and `Keys`, `Stat` and `MaxKeys` count every key in it. Set `RedisKeyPrefix` to keep the cache in its own
  namespace and leave unrelated keys alone.
- The implementation started as a part of [remark42](https://github.com/umputun/remark42)
  and later on moved to [go-pkgz/rest](https://github.com/go-pkgz/rest)
  library and finally generalized to become `lcw`.
