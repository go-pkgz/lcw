package cache

import "time"

// Option func type
type Option func(lc *loadingCacheImpl) error

// PurgeEvery functional option defines purge interval
// by default it is 0, i.e. never. If MaxKeys set to any non-zero this default will be 5minutes
func PurgeEvery(interval time.Duration) Option {
	return func(lc *loadingCacheImpl) error {
		lc.purgeEvery = interval
		return nil
	}
}

// MaxKeys functional option defines how many keys to keep.
// By default it is 0, which means unlimited.
// If any non-zero MaxKeys set, default PurgeEvery will be set to 5 minutes
func MaxKeys(max int) Option {
	return func(lc *loadingCacheImpl) error {
		lc.maxKeys = max
		return nil
	}
}

// AllowErrors option to cache results (and errors) even if error != nil.
// by default such failed calls won't be cached
func AllowErrors() Option {
	return func(lc *loadingCacheImpl) error {
		lc.allowError = true
		return nil
	}
}

// LRU sets cache to LRU (Least Recently Used) eviction mode. Affects size-based purge only.
func LRU() Option {
	return func(lc *loadingCacheImpl) error {
		lc.isLRU = true
		return nil
	}
}
