## Why

When `EnableMultipleConnections` is set, `Connector` keys each cached `*sql.DB` by `uid + hash(ConnectionArgs)` and stores entries in an unexported `sync.Map`. The map is never pruned during a datasource's lifetime. Any caller that varies `ConnectionArgs` per request — most relevantly, plugins that key connections by an OAuth token that rotates — accumulates orphaned `*sql.DB` instances, each holding open file descriptors and server-side sessions. Today's only relief valve is `Dispose()`, which closes everything on datasource shutdown.

The connector-related fork at `hydrolix/sqlds@v5.0.1` swapped the `sync.Map` for a TTL cache that closes evicted connections, but did so by replacing the entire storage layer inline. That work could live outside `sqlds` if the library exposed a small extension surface for the cache: an interface a plugin can implement, a way to install it, and an interface that lets the plugin observe the cached value without naming the concrete connection struct.

## What Changes

- Add `CachedConnection` interface exposing read-only access to a cached connection's `*sql.DB`, settings, and a `Close()` lifecycle method. The existing unexported `dbConnection` gains adapter methods to satisfy it; its fields, layout, and internal access patterns are unchanged.
- Add `ConnectionCache` interface (`Load` / `Store` / `Range` / `Dispose`) operating on `CachedConnection` values. Non-generic — the cached value type is fixed.
- Add a default `NewSyncMapCache()` implementation that wraps `sync.Map` and preserves today's storage behavior byte-for-byte (no eviction, no extra work).
- Add `SQLDatasource.ConnectionCacheFactory func() ConnectionCache` field; nil falls back to the default. `Connector` calls the factory once during construction and routes all cache operations through the returned implementation.
- Replace the unexported `Connector.connections sync.Map` with an unexported `cache ConnectionCache` field. `storeDBConnection` / `getDBConnection` / `Dispose` delegate to the interface.
- Existing public methods (`Connect`, `Reconnect`, `GetConnectionFromQuery`) keep their signatures. Return types remain `dbConnection` / `*dbConnection`; the type is still unexported and unchanged.
- Plugins consume the surface by implementing `ConnectionCache` (e.g. a TTL-bounded cache that closes `*sql.DB` on eviction) and assigning a factory. Plugins that don't set the field see no behavior change.
- Backwards compatibility: every new symbol is opt-in. Datasources that do not set `ConnectionCacheFactory` get the existing `sync.Map`-backed storage and identical observable behavior.
- Tests: Go unit tests covering the default cache, factory wiring, nil-fallback behavior, and parity with the prior `sync.Map` path. GoDoc example for `ConnectionCacheFactory`.

## Capabilities

### New Capabilities

- `connection-cache`: Pluggable cache for the per-`ConnectionArgs` `*sql.DB` pool managed by `Connector`, with a default sync-map-backed implementation and a per-datasource override.

### Modified Capabilities

<!-- none — no existing connector spec in this repository -->

## Impact

- **Affected code (additive):** new `cache.go` (interface, default impl, factory plumbing); `connector.go` (replace `connections sync.Map` with `cache ConnectionCache` field, delegate three internal helpers, add three adapter methods on `dbConnection`); `datasource.go` (new `ConnectionCacheFactory` field).
- **Public API:** new exported interfaces (`CachedConnection`, `ConnectionCache`), new constructor (`NewSyncMapCache`), new struct field (`SQLDatasource.ConnectionCacheFactory`). No existing exported symbol changes signature. `dbConnection` remains unexported; its three new adapter methods are also unexported in effect (the struct is unnameable from outside the package).
- **Dependencies:** none added. The default cache uses `sync.Map`.
- **Consumers:** datasource plugins gain an optional pluggable cache. Plugins that don't opt in are unaffected. Downstream effect: the Hydrolix fork's TTL connection pool moves to an external `hdx-grafana-sqlds-ext` package that imports `grafana/sqlds` and supplies a factory — closing the catalog-review concern about forking a security-relevant library.
- **Risks:** type assertion at the cache boundary (`v.(dbConnection)`) assumes the plugin's cache implementation returns the same `CachedConnection` value it was given on `Store`, without wrapping or decoration. Documented in the spec; safe under standard cache patterns (sync.Map, ttlcache, LRU); mitigated by a unit test that exercises a wrapping cache and confirms the assertion path fails loudly.
