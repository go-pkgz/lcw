# Loading Cache Wrapper [![Build Status](https://travis-ci.org/go-pkgz/lcw.svg?branch=master)](https://travis-ci.org/go-pkgz/lcw) [![Coverage Status](https://coveralls.io/repos/github/go-pkgz/lcw/badge.svg?branch=master)](https://coveralls.io/github/go-pkgz/lcw?branch=master) [![godoc](https://godoc.org/github.com/go-pkgz/lcw?status.svg)](https://godoc.org/github.com/go-pkgz/lcw)


The library adds a thin layer on top of [lru cache](https://github.com/hashicorp/golang-lru).

- LoadingCache (guava style)
- Limit maximum cache size (in bytes)
- Limit maximum key size
- Limit maximum size of a value 
- Functional style invalidation
- Nop cache provided
  
## Install and update

`go get -u github.com/go-pkgz/lcw`

## Usage

```
cache := lcw.NewCache(lcw.MaxKeys(500), lcw.MaxCacheSize(65536), lcw.MaxValSize(200), lcw.MaxKeySize(32))

val, err := cache.Get("key123", func() (lcw.Value, error) {
    res, err := getDataFromSomeSource(params) // returns string
    return res, err
})

if err != nil {
    panic("failed to get data")
}

s := val.(string) // cached values

```

## Details

- All byte-size limits work for values implementing `lcw.Sizer` interface
- Negative limits (max options) rejected
- `lgr.Value` wraps `interface{}` and should be converted back to the concrete type.
- The implementation started as a part of [remark42](https://github.com/umputun/remark) and later on moved to [go-pkgz/rest](https://github.com/go-pkgz/rest/tree/master/cache) library.
