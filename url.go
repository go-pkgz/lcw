package lcw

import (
	"github.com/go-redis/redis/v7"
	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
	"net/url"
	"strconv"
	"time"
)

// New parses uri and makes any of supported caches
// supported URIs:
//   - redis://<ip>:<port>?db=123&max_keys=10
//   - mem://lru?max_keys=10&max_cache_size=1024
//   - mem://expirable?ttl=30s&max_val_size=100
//   - nop://
func New(uri string) (LoadingCache, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, errors.Wrapf(err, "parse cache uri %s", uri)
	}

	query := u.Query()
	opts, err := optionsFromQuery(query)
	if err != nil {
		return nil, errors.Wrapf(err, "parse uri options %s", uri)
	}

	switch u.Scheme {
	case "redis":
		db, err := strconv.Atoi(query.Get("db"))
		if err != nil {
			return nil, err
		}
		client := redis.NewClient(&redis.Options{
			Addr: u.Host + ":" + u.Path,
			DB:   db,
			//TODO: add minimal list os useful params for redis client
		})
		res, err := NewRedisCache(client, opts...)
		return res, errors.Wrapf(err, "make redis for %s", uri)
	case "mem":
		switch u.Hostname() {
		case "lru":
			return NewLruCache(opts...)
		case "expirable":
			return NewExpirableCache(opts...)
		default:
			return nil, errors.Errorf("unsupported mem cache type %s", u.Hostname())
		}
	case "nop":
		return NewNopCache(), nil
	}
	return nil, errors.Errorf("unsupported cache type %s", u.Scheme)
}

func optionsFromQuery(q url.Values) (opts []Option, err error) {

	errs := new(multierror.Error)

	if v := q.Get("max_val_size"); v != "" {
		vv, e := strconv.Atoi(v)
		if e != nil {
			errs = multierror.Append(errs, errors.Wrapf(err, "max_val_size query param %s", v))
		} else {
			opts = append(opts, MaxValSize(vv))
		}
	}

	if v := q.Get("max_key_size"); v != "" {
		vv, e := strconv.Atoi(v)
		if e != nil {
			errs = multierror.Append(errs, errors.Wrapf(err, "max_key_size query param %s", v))
		} else {
			opts = append(opts, MaxKeySize(vv))
		}
	}

	if v := q.Get("max_keys"); v != "" {
		vv, e := strconv.Atoi(v)
		if e != nil {
			errs = multierror.Append(errs, errors.Wrapf(err, "max_keys query param %s", v))
		} else {
			opts = append(opts, MaxKeys(vv))
		}
	}

	if v := q.Get("max_cache_size"); v != "" {
		vv, e := strconv.ParseInt(v, 10, 64)
		if e != nil {
			errs = multierror.Append(errs, errors.Wrapf(err, "max_cache_size query param %s", v))
		} else {
			opts = append(opts, MaxCacheSize(vv))
		}
	}

	if v := q.Get("ttl"); v != "" {
		vv, e := time.ParseDuration(v)
		if e != nil {
			errs = multierror.Append(errs, errors.Wrapf(err, "ttl query param %s", v))
		} else {
			opts = append(opts, TTL(vv))
		}
	}

	return opts, errs.ErrorOrNil()
}
