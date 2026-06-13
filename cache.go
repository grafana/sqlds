package sqlds

import (
	"database/sql"
	"sync"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

// CachedConnection is the value type held by a ConnectionCache. It exposes
// read-only access to the underlying *sql.DB and the DataSourceInstanceSettings
// captured when the connection was opened, plus a Close lifecycle method that
// the cache calls on eviction or Dispose.
//
// The Connector's internal value (an unexported dbConnection struct) satisfies
// this interface via three adapter methods. Plugins typically do not construct
// CachedConnection values themselves — they only handle them as opaque values
// flowing through their ConnectionCache implementation.
type CachedConnection interface {
	// DB returns the underlying *sql.DB.
	DB() *sql.DB
	// Settings returns the DataSourceInstanceSettings captured when the
	// connection was opened.
	Settings() backend.DataSourceInstanceSettings
	// Close closes the underlying *sql.DB. It is safe to call multiple times;
	// subsequent calls return the same error database/sql would return.
	Close() error
}

// ConnectionCache is the per-Connector cache contract for the
// *sql.DB instances keyed by (datasource UID + ConnectionArgs hash) that
// Connector manages. Plugins install a custom implementation (e.g. one with
// TTL eviction) via SQLDatasource.ConnectionCacheFactory.
//
// Implementations MUST be safe for concurrent use from any number of
// goroutines.
//
// Contract: Load MUST return the exact CachedConnection value that was
// previously passed to Store for the same key. Implementations MUST NOT
// wrap, copy, or decorate the value before returning it. sqlds-internal
// code type-asserts the returned value back to the concrete connection
// struct for field access; a wrapping implementation causes a runtime
// type-assertion panic at the call site.
type ConnectionCache interface {
	// Load returns the CachedConnection stored under key, or (nil, false)
	// if no entry exists for the key.
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
		return nil, false
	}
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
