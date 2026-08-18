# Loading Cache Wrapper [![Build Status](https://github.com/go-pkgz/lcw/workflows/build/badge.svg)](https://github.com/go-pkgz/lcw/actions) [![Coverage Status](https://coveralls.io/repos/github/go-pkgz/lcw/badge.svg?branch=master)](https://coveralls.io/github/go-pkgz/lcw?branch=master) [![godoc](https://godoc.org/github.com/go-pkgz/lcw?status.svg)](https://godoc.org/github.com/go-pkgz/lcw/v2)

The library adds a thin layer on top of [lru\expirable cache](https://github.com/hashicorp/golang-lru).

| Cache name     | Constructor           | Defaults          | Description             |
|----------------|-----------------------|-------------------|-------------------------|
| LruCache       | lcw.NewLruCache       | keys=1000         | LRU cache with limits   |
| ExpirableCache | lcw.NewExpirableCache | keys=1000, ttl=5m | TTL cache with limits   |
| RedisCache     | lcw.NewRedisCache     | ttl=5m            | Redis cache with limits |
| Nop            | lcw.NewNopCache       |                   | Do-nothing cache        |

Main features:

- LoadingCache (guava style)
- Limit maximum cache size (in bytes)
- Limit maximum key size
- Limit maximum size of a value
- Limit number of keys
- TTL support (`ExpirableCache` and `RedisCache`)
- Callback on eviction event (not supported in `RedisCache`)
- Functional style invalidation
- Functional options
- Sane defaults

## Install and update

`go get -u github.com/go-pkgz/lcw/v2`

## Usage

```go
package main

import (
	"fmt"

	"github.com/go-pkgz/lcw/v2"
)

func main() {
	o := lcw.NewOpts[int]()
	cache, err := lcw.NewLruCache(o.MaxKeys(500), o.MaxCacheSize(65536), o.MaxValSize(200), o.MaxKeySize(32))
	if err != nil {
		panic("failed to create cache")
	}
	defer cache.Close()

	val, err := cache.Get("key123", func() (int, error) {
		return 123, nil // load the value from the actual source here
	})

	if err != nil {
		panic("failed to get data")
	}

	fmt.Println(val) // cached value
}
```

### Cache with URI

Cache can be created with URIs:

- `mem://lru?max_key_size=10&max_val_size=1024&max_keys=50&max_cache_size=64000` - creates LRU cache with given limits
- `mem://expirable?ttl=30s&max_key_size=10&max_val_size=1024&max_keys=50&max_cache_size=64000` - create expirable cache
- `redis://10.0.0.1:1234?db=16&password=qwerty&network=tcp4&redis_key_prefix=lcw:` - create redis cache, also
  accepts `dial_timeout`, `read_timeout` and `write_timeout`
- `nop://` - create Nop cache

## Scoped cache

`Scache` provides a wrapper on top of all implementations of `LoadingCache` with a number of special features:

1. Key is not a string, but a composed type made from partition, key-id and list of scopes (tags).
1. Value type is generic in v2 and limited to `[]byte` in v1.
1. Added `Flush` method for scoped/tagged invalidation of multiple records in a given partition.
   It only touches keys of the requested partition, with no scopes set the whole partition is dropped.
1. A simplified interface with Get, Stat, Flush and Close only.

Note that `RedisCache` in v2 stores string-based values only, so it can't back a `Scache[[]byte]`.
In v1 `Scache` over `RedisCache` works, values come back as bytes.

## Development

The repository holds two modules, the root one for v1 and `v2` for the generics-based version. Both have to be
tested:

```
go test -race ./... && (cd v2 && go test -race ./...)
```

## Details

- In all cache types other than Redis (e.g. LRU and Expirable at the moment) values are stored as-is which means
  that mutable values can be changed outside of cache. `ExampleLoadingCache_Mutability` illustrates that.
- All byte-size limits (MaxCacheSize and MaxValSize) work for values implementing the `lcw.Sizer` interface,
  as well as for `[]byte` and `string` values sized by their length. Values of any other type are not limited.
- `MaxCacheSize` is not supported by `RedisCache`, the option is accepted but ignored and `Stat` reports size 0.
- `MaxKeys(0)` means unlimited, as do all other limits set to 0.
- Negative limits (max options) rejected
- Concurrent `Get` calls for the same missing key run the loader once, the rest wait for its result.
  A loader must not call `Get` for the same key on the same cache, it would wait for itself.
- By default `RedisCache` assumes exclusive ownership of the selected redis database, i.e. `Purge` flushes it
  and `Keys`, `Stat` and `MaxKeys` count every key in it. Set `RedisKeyPrefix` to keep the cache in its own
  namespace and leave unrelated keys alone.
- The implementation started as a part of [remark42](https://github.com/umputun/remark)
  and later on moved to [go-pkgz/rest](https://github.com/go-pkgz/rest/tree/master/cache)
  library and finally generalized to become `lcw`.
