package sqlds

import (
	"sync"
)

// ConnectionCache is the per-Connector cache contract for the
// *sql.DB instances keyed by (datasource UID + ConnectionArgs hash) that
// Connector manages. Plugins install a custom implementation (e.g. one with
// TTL eviction) via SQLDatasource.ConnectionCacheFactory. The cache traffics
// in the exported CachedConnection value type, so a plugin's TTL cache can be as
// simple as a guarded map[string]CachedConnection.
//
// Implementations MUST be safe for concurrent use from any number of
// goroutines.
type ConnectionCache interface {
	// Load returns the CachedConnection stored under key, or (zero, false) if no
	// entry exists for the key.
	Load(key string) (CachedConnection, bool)
	// Store associates v with key, overwriting any prior value for key.
	Store(key string, v CachedConnection)
	// Range invokes f for every live entry. Iteration stops early if f
	// returns false. The iteration order is implementation-defined.
	Range(f func(key string, v CachedConnection) bool)
	// Dispose releases any background resources held by the implementation
	// (sweep goroutines, evict workers, …) and is invoked from
	// Connector.Dispose at datasource shutdown. Implementations SHOULD also
	// call Close on every live entry as part of Dispose.
	Dispose()
}

// NewSyncMapCache returns the default ConnectionCache implementation,
// backed by sync.Map. It runs no background goroutines, performs no
// eviction, and is behaviourally equivalent to the pre-extension
// Connector.connections sync.Map field.
//
// Dispose iterates every live entry, calls Close on each, then clears
// the underlying map.
func NewSyncMapCache() ConnectionCache {
	return &syncMapCache{}
}

// syncMapCache is the default sync.Map-backed ConnectionCache.
type syncMapCache struct {
	m sync.Map
}

func (c *syncMapCache) Load(key string) (CachedConnection, bool) {
	v, ok := c.m.Load(key)
	if !ok {
		return CachedConnection{}, false
	}
	// Safe: this cache is the only writer of c.m, and Store only ever puts
	// CachedConnection values in, so the assertion never fails.
	return v.(CachedConnection), true
}

func (c *syncMapCache) Store(key string, v CachedConnection) {
	c.m.Store(key, v)
}

func (c *syncMapCache) Range(f func(key string, v CachedConnection) bool) {
	c.m.Range(func(k, v any) bool {
		return f(k.(string), v.(CachedConnection))
	})
}

func (c *syncMapCache) Dispose() {
	c.m.Range(func(_, v any) bool {
		_ = v.(CachedConnection).Close()
		return true
	})
	c.m.Clear()
}
