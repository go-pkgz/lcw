package lcw

// Value type wraps interface{}
type Value interface{}

// Sizer allows to perform size-based restrictions, optional.
// If not defined both maxValueSize and maxCacheSize checks will be ignored
type Sizer interface {
	Size() int
}

// LoadingCache defines guava-like cache with Get method returning cached value ao retriving it if not in cache
type LoadingCache interface {
	Get(key string, fn func() (Value, error)) (val Value, err error)
	Peek(key string) (Value, bool)
	Invalidate(fn func(key string) bool)
	Purge()

	size() int64 // cache size in bytes
	keys() int   // number of keys in cache
}

// Nop is do-nothing implementation of LoadingCache
type Nop struct{}

// Get calls fn without any caching
func (n *Nop) Get(key string, fn func() (Value, error)) (Value, error) { return fn() }

// Peek does nothing and always returns false
func (n *Nop) Peek(key string) (Value, bool) { return nil, false }

// Invalidate does nothing for nop cache
func (n *Nop) Invalidate(fn func(key string) bool) {}

// Purge does nothing for nop cache
func (n *Nop) Purge() {}

// Size is always 0 for nop cache
func (n *Nop) size() int64 { return 0 }

// Keys is always 0 for nop cache
func (n *Nop) keys() int { return 0 }
