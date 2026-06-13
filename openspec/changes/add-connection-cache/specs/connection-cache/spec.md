## ADDED Requirements

### Requirement: `CachedConnection` interface exposes a cached connection's DB, settings, and close hook

`sqlds` SHALL expose an exported `CachedConnection` interface with three methods:

```go
type CachedConnection interface {
    DB() *sql.DB
    Settings() backend.DataSourceInstanceSettings
    Close() error
}
```

`DB()` SHALL return the underlying `*sql.DB`. `Settings()` SHALL return the `DataSourceInstanceSettings` captured when the connection was opened. `Close()` SHALL close the underlying `*sql.DB`.

The existing unexported `dbConnection` struct SHALL satisfy `CachedConnection` via adapter methods. Its fields, struct layout, and the internal function bodies that read `dbConn.db` / `dbConn.settings` directly SHALL remain unchanged.

#### Scenario: `dbConnection` satisfies `CachedConnection`

- **WHEN** internal code creates a `dbConnection{db, settings}` value
- **AND** that value is stored through a `ConnectionCache.Store(key, dbConn)` call
- **THEN** the value is accepted as a `CachedConnection` and the cache can call `DB()`, `Settings()`, and `Close()` on it

#### Scenario: `Close()` closes the underlying DB

- **WHEN** a `CachedConnection` is constructed from a live `*sql.DB`
- **AND** `Close()` is invoked
- **THEN** the underlying `*sql.DB.Close()` is called and any error from it is returned

### Requirement: `ConnectionCache` interface is the per-`Connector` cache contract

`sqlds` SHALL expose an exported `ConnectionCache` interface with four methods:

```go
type ConnectionCache interface {
    Load(key string) (CachedConnection, bool)
    Store(key string, v CachedConnection)
    Range(f func(key string, v CachedConnection) bool)
    Dispose()
}
```

`Load` SHALL return `(value, true)` for a stored key and `(nil, false)` otherwise. `Store` SHALL associate the given value with the key, overwriting any prior value for that key. `Range` SHALL iterate every live entry exactly once and stop early if the callback returns `false`. `Dispose` SHALL release any background resources held by the cache implementation (sweep goroutines, eviction workers).

Implementations SHALL be safe for concurrent use by any number of goroutines.

The interface is non-generic — the cached value type is `CachedConnection`.

#### Scenario: Load and Store round-trip

- **WHEN** a `ConnectionCache` implementation has `Store(key, v)` called
- **AND** `Load(key)` is called subsequently
- **THEN** the call returns `(v, true)` — the same `CachedConnection` value that was stored

#### Scenario: Load of a missing key

- **WHEN** `Load(key)` is called for a key never passed to `Store`
- **THEN** the call returns `(nil, false)`

#### Scenario: Range iterates every live entry

- **WHEN** N keys have been stored
- **AND** `Range` is called with a callback that always returns `true`
- **THEN** the callback is invoked exactly N times, once per stored key, each with the value that was stored under that key

### Requirement: Cache implementations MUST NOT wrap or decorate stored values

A `ConnectionCache` implementation SHALL return from `Load` the *exact* `CachedConnection` value that was previously passed to `Store` for the same key. Wrapping, copying, or decorating the value before returning it is not permitted.

This contract exists so `sqlds`-internal code can type-assert `Load`'s result back to the concrete `dbConnection` for field access. A wrapping cache breaks the type assertion and causes a runtime panic.

#### Scenario: Cache returns the stored value unchanged

- **WHEN** `Store(key, v)` is called where `v` is a `CachedConnection` whose dynamic type is `dbConnection`
- **AND** `Load(key)` is called
- **THEN** the returned interface value's dynamic type is still `dbConnection` and type-asserting it to `dbConnection` succeeds

### Requirement: `NewSyncMapCache()` is the default implementation

`sqlds` SHALL export a constructor `NewSyncMapCache() ConnectionCache` returning a `sync.Map`-backed implementation of `ConnectionCache`. The default SHALL NOT run any background goroutines, SHALL NOT evict entries, and SHALL be behaviourally equivalent to the pre-extension `Connector.connections sync.Map` field.

`Dispose()` on the default implementation SHALL iterate every live entry and call `Close()` on each, then clear the map.

#### Scenario: Default cache preserves pre-extension behaviour

- **WHEN** a datasource is constructed with no `ConnectionCacheFactory` set
- **AND** the same sequence of `Connect` / `GetConnectionFromQuery` / `Reconnect` calls is made as would have been made against the pre-extension code path
- **THEN** the cache contents at the end of the sequence are byte-equivalent to what the prior `sync.Map`-based implementation would have held

#### Scenario: Default Dispose closes every cached connection

- **WHEN** N entries are stored in a `NewSyncMapCache()` instance
- **AND** `Dispose()` is called
- **THEN** `Close()` is invoked on every stored `CachedConnection` and the cache becomes empty

### Requirement: `SQLDatasource.ConnectionCacheFactory` selects the cache implementation per datasource

`SQLDatasource` SHALL expose an exported field `ConnectionCacheFactory func() ConnectionCache`. The `Connector` SHALL call this factory once during construction and use the returned `ConnectionCache` for all subsequent storage. A nil factory SHALL resolve to `NewSyncMapCache()`.

The plugin SHALL capture any configuration (TTL, size cap, dependencies) it needs in the factory's closure; the factory takes no arguments.

#### Scenario: Nil factory resolves to default

- **WHEN** `ds.ConnectionCacheFactory` is `nil`
- **AND** `Connector` is constructed
- **THEN** the connector uses a `NewSyncMapCache()` instance

#### Scenario: Custom factory is invoked once at construction

- **WHEN** `ds.ConnectionCacheFactory` is set to a function that returns a custom `ConnectionCache`
- **AND** `Connector` is constructed
- **THEN** the factory is invoked exactly once and the returned cache is held by the connector for its lifetime

### Requirement: `NewConnector` accepts variadic `ConnectorOption` values

`sqlds` SHALL define an exported `ConnectorOption` function type and at least one option constructor `WithCache(ConnectionCache) ConnectorOption`. `NewConnector`'s signature SHALL gain a trailing variadic parameter `opts ...ConnectorOption`. Options SHALL be applied before the bootstrap connection is stored in the cache.

Existing callers (`NewConnector(ctx, driver, settings, enableMultipleConnections)`) SHALL continue to compile unchanged — the parameter is variadic and additive.

#### Scenario: Existing call sites compile unchanged

- **WHEN** a caller invokes `NewConnector(ctx, driver, settings, enableMultipleConnections)` with no options
- **THEN** the call compiles successfully and uses the default cache

#### Scenario: `WithCache` injects a custom cache before bootstrap

- **WHEN** a caller invokes `NewConnector(ctx, driver, settings, enableMultipleConnections, WithCache(customCache))`
- **AND** the bootstrap connection is created during construction
- **THEN** the bootstrap entry lands in `customCache`, not in a default `sync.Map`-backed cache
