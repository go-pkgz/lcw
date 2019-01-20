package lcw

import "errors"

// Option func type
type Option func(c *Cache) error

// MaxValSize functional option defines the largest value's size allowed to be cached
// By default it is 0, which means unlimited.
func MaxValSize(max int) Option {
	return func(c *Cache) error {
		if max < 0 {
			return errors.New("negative max value size")
		}
		c.maxValueSize = max
		return nil
	}
}

// MaxKeySize functional option defines the largest key's size allowed to be used in cache
// By default it is 0, which means unlimited.
func MaxKeySize(max int) Option {
	return func(c *Cache) error {
		if max < 0 {
			return errors.New("negative max key size")
		}
		c.maxKeySize = max
		return nil
	}
}

// MaxKeys functional option defines how many keys to keep.
// By default it is 0, which means unlimited.
func MaxKeys(max int) Option {
	return func(c *Cache) error {
		if max < 0 {
			return errors.New("negative max keys")
		}
		c.maxKeys = max
		return nil
	}
}

// MaxCacheSize functional option defines the total size of cached data.
// By default it is 0, which means unlimited.
func MaxCacheSize(max int64) Option {
	return func(c *Cache) error {
		if max < 0 {
			return errors.New("negative max cache size")
		}
		c.maxCacheSize = max
		return nil
	}
}
