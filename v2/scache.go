package lcw

import (
	"fmt"
	"slices"
	"strings"
	"sync"
)

// Scache wraps LoadingCache with partitions (sub-system), and scopes.
// Simplified interface with just 4 funcs - Get, Flush, Stat and Close
type Scache[V any] struct {
	lc LoadingCache[V]

	mu    sync.Mutex
	loads map[*scacheLoad]struct{} // loads in progress, for Flush to mark
}

// scacheLoad is a load in progress; flushed is set by a Flush matching its key while the load runs
type scacheLoad struct {
	key     string
	flushed bool
}

// NewScache creates Scache on top of LoadingCache
func NewScache[V any](lc LoadingCache[V]) *Scache[V] {
	return &Scache[V]{lc: lc, loads: map[*scacheLoad]struct{}{}}
}

// Get retrieves a key from underlying backend.
// A value loaded while a Flush matching its key ran may have been read before the change the Flush is for,
// so it is returned to this caller and removed from the cache instead of being kept until the next Flush.
func (m *Scache[V]) Get(key Key, fn func() (V, error)) (data V, err error) {
	keyStr := key.String()
	var ld *scacheLoad
	defer func() { m.endLoad(ld) }()
	return m.lc.Get(keyStr, func() (value V, e error) {
		ld = m.startLoad(keyStr)
		return fn()
	})
}

func (m *Scache[V]) startLoad(key string) *scacheLoad {
	ld := &scacheLoad{key: key}
	m.mu.Lock()
	m.loads[ld] = struct{}{}
	m.mu.Unlock()
	return ld
}

// endLoad forgets a load once the backend has stored its value, and removes the value if a Flush marked it.
// ld is nil when the value came from the cache or from another caller's load.
func (m *Scache[V]) endLoad(ld *scacheLoad) {
	if ld == nil {
		return
	}
	m.mu.Lock()
	delete(m.loads, ld)
	flushed := ld.flushed
	m.mu.Unlock()
	if flushed {
		m.lc.Delete(ld.key)
	}
}

// Stat delegates the call to the underlying cache backend
func (m *Scache[V]) Stat() CacheStat {
	return m.lc.Stat()
}

// Close calls Close function of the underlying cache
func (m *Scache[V]) Close() error {
	return m.lc.Close()
}

// Flush clears keys of the requested partition, matching the requested scopes.
// With no scopes set every key of the partition is removed, keys of other partitions are kept.
func (m *Scache[V]) Flush(req FlusherRequest) {
	// check if fullKey belongs to the requested partition and has matching scopes
	inScope := func(fullKey string) bool {
		key, err := parseKey(fullKey)
		if err != nil {
			return false
		}
		if key.partition != req.partition {
			return false
		}
		if len(req.scopes) == 0 { // no scopes means the whole partition
			return true
		}
		for _, s := range req.scopes {
			if slices.Contains(key.scopes, s) {
				return true
			}
		}
		return false
	}

	// marked first, so a load that ends after this point drops its value even if it is stored after the keys
	// below were removed
	m.mu.Lock()
	for ld := range m.loads {
		if inScope(ld.key) {
			ld.flushed = true
		}
	}
	m.mu.Unlock()

	for _, k := range m.lc.Keys() {
		if inScope(k) {
			m.lc.Delete(k) // Keys() returns copy of cache's key, safe to remove directly
		}
	}
}

// Key for scoped cache. Created foe given partition (can be empty) and set with ID and Scopes.
// example: k := NewKey("sys1").ID(postID).Scopes("last_posts", customer_id)
type Key struct {
	id        string   // the primary part of the key, i.e. usual cache's key
	partition string   // optional id for a subsystem or cache partition
	scopes    []string // list of scopes to use in invalidation
}

// NewKey makes base key for given partition. Partition can be omitted.
func NewKey(partition ...string) Key {
	if len(partition) == 0 {
		return Key{partition: ""}
	}
	return Key{partition: partition[0]}
}

// ID sets key id
func (k Key) ID(id string) Key {
	k.id = id
	return k
}

// Scopes of the key
func (k Key) Scopes(scopes ...string) Key {
	k.scopes = scopes
	return k
}

// String makes full string key from primary key, partition and scopes
// key string made as <partition>@@<id>@@<scope1>$$<scope2>....
// Each part is escaped, so an id or scope containing @@ or $$ (a URL, for example) keeps the key parseable.
func (k Key) String() string {
	bld := strings.Builder{}
	_, _ = bld.WriteString(keyEscaper.Replace(k.partition))
	_, _ = bld.WriteString("@@")
	_, _ = bld.WriteString(keyEscaper.Replace(k.id))
	_, _ = bld.WriteString("@@")
	for i, s := range k.scopes {
		if i > 0 {
			_, _ = bld.WriteString("$$")
		}
		_, _ = bld.WriteString(keyEscaper.Replace(s))
	}
	return bld.String()
}

// keyEscaper replaces the characters of the key separators, and the escape character itself,
// so escaped parts never contain @ or $; keyUnescaper reverses it
var (
	keyEscaper   = strings.NewReplacer("%", "%25", "@", "%40", "$", "%24")
	keyUnescaper = strings.NewReplacer("%25", "%", "%40", "@", "%24", "$")
)

// parseKey gets compound key string created by Key func and split it to the actual key, partition and scopes
// key string made as <partition>@@<id>@@<scope1>$$<scope2>....
func parseKey(keyStr string) (Key, error) {
	elems := strings.Split(keyStr, "@@")
	if len(elems) != 3 {
		return Key{}, fmt.Errorf("can't parse cache key %s, invalid number of segments %d", keyStr, len(elems))
	}

	scopes := strings.Split(elems[2], "$$")
	if len(scopes) == 1 && scopes[0] == "" {
		scopes = []string{}
	}
	for i := range scopes {
		scopes[i] = keyUnescaper.Replace(scopes[i])
	}
	key := Key{
		partition: keyUnescaper.Replace(elems[0]),
		id:        keyUnescaper.Replace(elems[1]),
		scopes:    scopes,
	}

	return key, nil
}

// FlusherRequest used as input for cache.Flush
type FlusherRequest struct {
	partition string
	scopes    []string
}

// Flusher makes new FlusherRequest with empty scopes
func Flusher(partition string) FlusherRequest {
	res := FlusherRequest{partition: partition}
	return res
}

// Scopes adds scopes to FlusherRequest
func (f FlusherRequest) Scopes(scopes ...string) FlusherRequest {
	f.scopes = scopes
	return f
}
