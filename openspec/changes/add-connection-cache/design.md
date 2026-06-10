## Context

`grafana/sqlds` v5.1.1 (revision `6c09016`) routes per-query connections through `Connector` (`connector.go:15-24`). When `EnableMultipleConnections` is set, `GetConnectionFromQuery` (`connector.go:152-185`) keys each `*sql.DB` by `keyWithConnectionArgs(uid, q.ConnectionArgs)` and stores it in an unexported `sync.Map` field. Entries are never pruned during the datasource's lifetime; the only release path is `Connector.Dispose()` (`connector.go:144-150`), which runs on datasource shutdown.

For plugins that vary `ConnectionArgs` per request — most relevantly, those that key connections by an OAuth token that rotates per user and re-issues on TTL — this behavior produces an unbounded `sync.Map` of orphaned `*sql.DB` instances. Each entry holds open file descriptors and server-side sessions for a key that will never be looked up again.

The Hydrolix fork has been the workaround. Its `connector.go` swapped `sync.Map` for `jellydator/ttlcache/v3` with a 1-hour TTL and an `OnEviction` callback that closes the evicted `*sql.DB`. That work could live outside `sqlds` if the library exposed a small extension surface for the cache: an interface a plugin can implement, a way to install it, and a value type that lets the plugin observe cached entries without naming the unexported `dbConnection` struct.

This design promotes that pattern into a reusable extension point.

## Goals / Non-Goals

**Goals:**
- Let plugins supply their own cache implementation (TTL, LRU, hybrid) for the per-`ConnectionArgs` `*sql.DB` pool managed by `Connector`.
- Keep `dbConnection` unexported and structurally unchanged. Internal connector code that accesses `dbConn.db` and `dbConn.settings` by field continues to work.
- Preserve the existing public API and behavior for datasources that do not opt in.

**Non-Goals:**
- Ship a built-in eviction policy (TTL, LRU) in `sqlds`. Plugins own the policy via their `ConnectionCache` implementation.
- Expose the `Connector`'s storage layout, sweep timing, or internal types as public API.
- Add new functionality to the connection pool itself (priming, warm-up, health-pinging cached entries, multi-DB sharding). Those are out of scope here and would be separate changes.
- Per-user health checks. `Connect()` and `CheckHealth` continue to use the datasource-default key. A custom `ConnectionCache` doesn't change health semantics.
- Refactor the existing fork's connector shape into upstream. The HDX extension package adapts to whatever upstream lands.

## Decisions

### Decision 1: `CachedConnection` interface (method-set, exported)

A new exported interface exposes read-only access to a cached connection's `*sql.DB`, its `DataSourceInstanceSettings`, and a `Close()` lifecycle method. The existing unexported `dbConnection` struct gains three adapter methods that satisfy the interface. The struct's fields, layout, and the existing internal field-access patterns (`dbConn.db`, `dbConn.settings`) are unchanged.

```go
type CachedConnection interface {
    DB() *sql.DB
    Settings() backend.DataSourceInstanceSettings
    Close() error
}
```

**Why interface, not exported struct.** Exporting `DBConnection` would expose the field layout as public API and freeze it. An interface is forward-compatible: future fields on `dbConnection` (e.g. a per-entry creation timestamp) don't break consumers as long as the method contract holds.

**Why method-set, not type-set.** A type-set interface (`interface { *dbConnection }`) can only be used as a generic constraint, not as a runtime value type — the cache needs to hold values of the type, so the type-set variant is unusable. Method-set interfaces are usable everywhere a normal type can go.

**Alternatives considered.**
- *Export `dbConnection` as `DBConnection`.* Smaller diff (rename + capitalize fields) but exposes layout; requires fields to be capitalized (renaming = breaking field access at every call site, i.e. a connector refactor).
- *Embed the concrete type in a type-set constraint interface.* Rejected — type-set interfaces can only appear in generic constraint positions, not as runtime types. See "Why method-set" above.

### Decision 2: `ConnectionCache` interface (non-generic)

```go
type ConnectionCache interface {
    Load(key string) (CachedConnection, bool)
    Store(key string, v CachedConnection)
    Range(f func(key string, v CachedConnection) bool)
    Dispose()
}
```

Shape matches `sync.Map` access patterns minus what `Connector` doesn't use (no `Delete` — `Connector` never deletes, only stores and disposes). `Range` is required so `Dispose()` can iterate and close every entry. `Dispose()` on the interface lets cache implementations clean up their own background state (sweep goroutines, evict workers) alongside closing the entries.

**Why non-generic.** The cached value type is fixed at the interface level (`CachedConnection`). A `ConnectionCache[V any]` would be ceremony without flexibility — every consumer instantiates with V = `CachedConnection` anyway. Plugin implementations can still parameterize their *underlying* storage generically (e.g. `ttlcache.Cache[string, sqlds.CachedConnection]`) while presenting the non-generic interface to sqlds.

**Alternatives considered.**
- *Generic `ConnectionCache[V any]`.* Rejected as above — adds type parameters without adding expressiveness for this surface.
- *Eviction callbacks on the interface (`OnEvict`).* Rejected — the cache implementation already owns its eviction timing; an `OnEvict` hook on the interface would let `Connector` observe evictions, but `Connector` has no use for that signal (it never holds external references to a cached entry).

### Decision 3: Factory field on `SQLDatasource`, not an instance field

```go
type SQLDatasource struct {
    // ...
    ConnectionCacheFactory func() ConnectionCache  // nil → NewSyncMapCache()
}
```

`Connector` calls the factory once during construction and holds the returned `ConnectionCache` for its lifetime. Plugin captures its configuration (TTL, size cap, dependencies) in the closure.

**Why factory, not an instance.** A factory binds the cache's lifetime to the `Connector`'s. If `Connector` is ever reconstructed (today it isn't; the door stays open), each instance gets a fresh cache instead of sharing a stale one constructed at plugin init. A factory also avoids the awkwardness of cache state existing before its owner.

**Why no factory parameters.** The factory takes no arguments. If the plugin needs configuration (TTL value, size cap, dependencies), it captures them in the closure:

```go
ds.ConnectionCacheFactory = func() ConnectionCache {
    return NewTTLConnectionCache(pluginConfig.CacheTTL)
}
```

This keeps `sqlds` out of the plugin's configuration schema.

**Alternatives considered.**
- *Instance field (`ConnectionCache ConnectionCache`).* Simpler for single-Connector cases but couples cache lifetime to plugin-init timing rather than Connector-init timing.
- *Factory takes `DriverSettings` or similar context.* Rejected — couples sqlds to caring about cache config field names; closure capture is cleaner.

### Decision 4: Default `NewSyncMapCache()` preserves today's behavior byte-for-byte

```go
func NewSyncMapCache() ConnectionCache
```

Wraps a `sync.Map`. `Load` / `Store` / `Range` delegate directly. `Dispose()` iterates and calls `Close()` on every cached value. No background goroutines, no eviction, no extra allocations beyond the boxing required to hold values as `CachedConnection` interface values.

When `SQLDatasource.ConnectionCacheFactory` is nil, `Connector` constructs `NewSyncMapCache()` automatically. The legacy code path — datasources that never set the field — sees identical behavior to today.

### Decision 5: `dbConnection` stays unexported; adapter methods only

`dbConnection` gains three method declarations and nothing else. Fields, struct layout, and the function bodies that read `dbConn.db` / `dbConn.settings` are untouched.

```go
func (d dbConnection) DB() *sql.DB                                  { return d.db }
func (d dbConnection) Settings() backend.DataSourceInstanceSettings { return d.settings }
func (d dbConnection) Close() error                                 { return d.db.Close() }
```

The methods are mechanical adapters that expose existing internal state through the public interface. They do not change any behavior or any return value.

### Decision 6: Type assertion at the internal cache boundary

Inside `Connector`, `getDBConnection` reads from the cache and type-asserts the returned `CachedConnection` back to the concrete `dbConnection`:

```go
func (c *Connector) getDBConnection(key string) (dbConnection, bool) {
    v, ok := c.cache.Load(key)
    if !ok {
        return dbConnection{}, false
    }
    return v.(dbConnection), true
}
```

This is safe under the contract that the cache returns the same value passed to `Store`, without wrapping or decoration. That contract is documented on `ConnectionCache` and is satisfied by standard cache implementations (`sync.Map`, `ttlcache`, hashicorp `lru`). A cache that wraps values would break the assertion; this fails loudly (panic with a clear runtime type-assertion error) rather than silently misbehaving.

Switching `Connector`'s internal callers to use the interface methods (`v.DB()`, `v.Settings()`) would remove the assertion. That is a refactor of connector bodies, not strictly required for correctness, and is deliberately out of scope to keep the diff focused on the extension surface.

**Alternatives considered.**
- *Wrap the cache in a `Connector`-internal adapter that does the assertion once.* Same effect, more layers; net negative readability.
- *Pass a "downcast hook" through `ConnectionCache`.* Conceptually cleaner but adds public surface for an internal concern.

### Decision 7: Variadic `ConnectorOption` for cache injection

`NewConnector` gains a trailing variadic parameter:

```go
type ConnectorOption func(*Connector)
func WithCache(cache ConnectionCache) ConnectorOption

func NewConnector(
    ctx context.Context,
    driver Driver,
    settings backend.DataSourceInstanceSettings,
    enableMultipleConnections bool,
    opts ...ConnectorOption,
) (*Connector, error)
```

Options are applied before the bootstrap connection is stored, so `WithCache` controls where the bootstrap entry lands. `SQLDatasource.NewDatasource` resolves `ds.ConnectionCacheFactory` (calling it if non-nil) and passes the result via `WithCache`.

**Why variadic options.** Additive: existing callers `NewConnector(a, b, c, d)` keep compiling unchanged. Leaves room for future Connector-level options without further signature churn.

**Alternatives considered.**
- *New constructor `NewConnectorWithCache(...)`.* Doubles the constructor surface; variadic options absorb future extension cleanly.
- *Setter after construction (`conn.SetCache(...)`).* Rejected because the bootstrap connection is stored inside `NewConnector`; a post-hoc setter either loses the bootstrap entry or has to re-store, both of which complicate the contract.

### Decision 8: No built-in eviction policy in sqlds

`sqlds` does not ship `DriverSettings.ConnectionMaxIdle`, `MaxConnections`, or similar policy knobs. Plugins own the policy entirely via their `ConnectionCache` implementation.

**Why.** Owning a policy upstream means owning the policy's machinery: sweep goroutines, lastUsed tracking, LRU lists, configuration schema, edge cases (concurrent eviction during reconnect, etc.). The factory pattern moves all of that cost to the plugin and removes the temptation to grow a policy schema in `DriverSettings` over time. A plugin that just wants "idle for N minutes" writes ~30 LOC; one that wants something exotic isn't limited by sqlds's policy menu.

**Alternatives considered.**
- *Ship built-in TTL + LRU behind `DriverSettings` flags.* Simpler one-config-line UX for the common case, but: imposes background goroutines on every datasource, locks the policy schema, and still leaves custom-policy plugins needing the factory escape hatch. Net more upstream code for less flexibility.

## Risks / Trade-offs

- [Plugin cache implementations that wrap values break the type-assertion in `getDBConnection`] → Mitigation: documented contract on `ConnectionCache` ("Load MUST return the exact value passed to Store; implementations MUST NOT wrap or decorate"). Default impl exercises the path. The failure mode is a runtime panic with a clear type-assertion message — loud, not silent.
- [Default impl drifts from `sync.Map` behavior over time, regressing the legacy path] → Mitigation: parity unit test asserts behavior identical to a direct `sync.Map` for the operations `Connector` performs. Add to the test suite as a guard for future refactors.
- [API surface grows] → Mitigation: every new symbol is opt-in (`ConnectionCacheFactory` nil → default; `WithCache` not passed → default). No existing symbol changes signature. The variadic options pattern is the only signature change and it is additive by language rule.
- [`Dispose()` on the cache interface is called from `Connector.Dispose()`, which datasource shutdown may invoke concurrently with in-flight queries on bad client code] → Mitigation: same risk as today's `sync.Map.Range` + `Close` pattern. No regression introduced by this change; documented but not solved here.

## Migration Plan

This change is additive within `sqlds`. No migration is needed for existing datasource plugins.

For the Hydrolix consumer specifically (and applicable to any plugin currently maintaining a fork to get a TTL connection cache):

1. Land this change on `grafana/sqlds`.
2. In the plugin extension package (`hdx-grafana-sqlds-ext`, alongside the interpolator move from `add-extension-points`), implement `sqlds.ConnectionCache` over `jellydator/ttlcache/v3` with a 1-hour default TTL and an `OnEviction` callback that calls `item.Value().Close()`.
3. In the Hydrolix plugin's datasource construction, set `ds.ConnectionCacheFactory = NewTTLConnectionCache`.
4. Retire the fork's `connector.go`.

The OAuth-keying half of the fork's connector lives in `MutateQueryData` (a hook that already exists in `sqlds`), not in this change. The two pieces compose: `MutateQueryData` writes `connectionArgs = {"oauthToken": "<token>"}` into each query; `keyWithConnectionArgs` (existing) produces a per-user key; the plugin's `ConnectionCache` evicts idle entries.

## Open Questions

- Should the cache contract require deterministic ordering on `Range`? Today's `sync.Map.Range` is unordered. Plugin implementations have no reason to be ordered. Default: not required.
- Should `NewSyncMapCache()` be exported as the *only* default, or also be made the canonical constructor for plugins that want to extend it (e.g. wrap with metrics)? Leaning yes for now — small surface, simple to use as a base.
- Should `Connector` expose its cache via a getter (`ds.Connector().Cache()`) so plugin code can introspect at runtime? Out of scope here; the factory model means plugin code already holds a reference to its cache if it wants to introspect. Add a getter only if a concrete need surfaces.
