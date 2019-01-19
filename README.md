# Loading Cache Wrapper

The library adds a thin level on top of [lru cache](github.com/hashicorp/golang-lru).

- LoadingCache (guava style)
- Limit maximum cache size (in bytes)
- Limit maximum key size
- Limit maximum size of a value 
- Functional style invalidation
- Nop cache provided
  
## Install

`go install github.com/go-pkgz/auth`

## Usage

```
cache := lcw.NewCache(lcw.MaxKeys(500), lcw.MaxCacheSize(65536), lcw.MaxValSize(200), lcw.MaxKeySize(32))

val, err := cache.Get("key123", func() (lcw.Value, error) {
    res, err := getDateFromSomeSource(params)
    return res, err
})

if err != nil {
    panic("failed to get data")
}
```

## Details

