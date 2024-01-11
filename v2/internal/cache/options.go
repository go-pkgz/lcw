package cache

import "time"

// Option func type
type Option[V any] func(lc *LoadingCache[V]) error

// WorkerOptions holds the option setting methods
type WorkerOptions[T any] struct{}

// NewOpts creates a new WorkerOptions instance
func NewOpts[T any]() *WorkerOptions[T] {
	return &WorkerOptions[T]{}
}

// OnEvicted called automatically for expired and manually deleted entries
func (o *WorkerOptions[V]) OnEvicted(fn func(key string, value V)) Option[V] {
	return func(lc *LoadingCache[V]) error {
		lc.onEvicted = fn
		return nil
	}
}

// PurgeEvery functional option defines purge interval
// by default it is 0, i.e. never. If MaxKeys set to any non-zero this default will be 5minutes
func (o *WorkerOptions[V]) PurgeEvery(interval time.Duration) Option[V] {
	return func(lc *LoadingCache[V]) error {
		lc.purgeEvery = interval
		return nil
	}
}

// MaxKeys functional option defines how many keys to keep.
// By default, it is 0, which means unlimited.
// If any non-zero MaxKeys set, default PurgeEvery will be set to 5 minutes
func (o *WorkerOptions[V]) MaxKeys(max int) Option[V] {
	return func(lc *LoadingCache[V]) error {
		lc.maxKeys = int64(max)
		return nil
	}
}

// TTL functional option defines TTL for all cache entries.
// By default, it is set to 10 years, sane option for expirable cache might be 5 minutes.
func (o *WorkerOptions[V]) TTL(ttl time.Duration) Option[V] {
	return func(lc *LoadingCache[V]) error {
		lc.ttl = ttl
		return nil
	}
}
