# connection-cache Specification

## Purpose

`Connector` maintains a pool of `*sql.DB` instances keyed by `uid + hash(ConnectionArgs)`. This capability makes that pool pluggable: a datasource plugin can install its own cache implementation (e.g. one with TTL/LRU eviction that closes connections on eviction) while the default preserves the original unbounded `sync.Map` behaviour. The cached value is an exported concrete type so a plugin's cache can be as simple as a guarded `map[string]CachedConnection`.

## Requirements

### Requirement: `CachedConnection` exposes a cached connection's DB, settings, and close hook

`sqlds` SHALL expose an exported concrete struct `CachedConnection` whose fields are unexported and which exposes three value-receiver methods:

```go
type CachedConnection struct {
    db       *sql.DB
    settings backend.DataSourceInstanceSettings
}

func (c CachedConnection) DB() *sql.DB                                  { /* ... */ }
func (c CachedConnection) Settings() backend.DataSourceInstanceSettings { /* ... */ }
func (c CachedConnection) Close() error                                 { /* ... */ }
```

`DB()` SHALL return the underlying `*sql.DB`. `Settings()` SHALL return the `DataSourceInstanceSettings` captured when the connection was opened. `Close()` SHALL close the underlying `*sql.DB`.

The type SHALL NOT be an interface. Its fields SHALL remain unexported so that `sqlds` is the sole constructor of values and `ConnectionCache` implementations handle them as opaque entries (inspecting via the accessors, releasing via `Close()`). The name `CachedConnection` is used in preference to `Connection`, which is already taken by an unrelated exported interface in `driver.go`.

#### Scenario: A `CachedConnection` flows through the cache opaquely

- **WHEN** internal code creates a `CachedConnection{db, settings}` value
- **AND** that value is stored through a `ConnectionCache.Store(key, conn)` call and later returned by `Load(key)`
- **THEN** the returned value's `DB()` and `Settings()` report the original `*sql.DB` and settings, with no type assertion required at the call site

#### Scenario: `Close()` closes the underlying DB

- **WHEN** a `CachedConnection` is constructed from a live `*sql.DB`
- **AND** `Close()` is invoked
- **THEN** the underlying `*sql.DB.Close()` is called and any error from it is returned

### Requirement: `ConnectionCache` interface is the per-`Connector` cache contract

`sqlds` SHALL expose an exported `ConnectionCache` interface with four methods operating on the concrete `CachedConnection` value type:

```go
type ConnectionCache interface {
    Load(key string) (CachedConnection, bool)
    Store(key string, v CachedConnection)
    Range(f func(key string, v CachedConnection) bool)
    Dispose()
}
```

`Load` SHALL return `(value, true)` for a stored key and the zero value `(CachedConnection{}, false)` otherwise. `Store` SHALL associate the given value with the key, overwriting any prior value for that key. `Range` SHALL iterate every live entry exactly once and stop early if the callback returns `false`. `Dispose` SHALL release any background resources held by the cache implementation (sweep goroutines, eviction workers).

Implementations SHALL be safe for concurrent use by any number of goroutines. Because the value type is a concrete struct, a conforming implementation MAY be as simple as a mutex-guarded `map[string]CachedConnection`.

#### Scenario: Load and Store round-trip

- **WHEN** a `ConnectionCache` implementation has `Store(key, v)` called
- **AND** `Load(key)` is called subsequently
- **THEN** the call returns `(v, true)`

#### Scenario: Load of a missing key

- **WHEN** `Load(key)` is called for a key never passed to `Store`
- **THEN** the call returns `(CachedConnection{}, false)`

#### Scenario: Range iterates every live entry

- **WHEN** N keys have been stored
- **AND** `Range` is called with a callback that always returns `true`
- **THEN** the callback is invoked exactly N times, once per stored key, each with the value that was stored under that key

### Requirement: `Connector` applies a single nil-cache policy across all cache access

The `Connector` SHALL route every cache operation (`getDBConnection`, `storeDBConnection`, `Dispose`) through one private accessor that lazily installs `NewSyncMapCache()` when the `cache` field is nil and returns the non-nil cache. The three operations SHALL therefore behave consistently on a `Connector` literal constructed outside the package (e.g. `&sqlds.Connector{}` in test fixtures), where no constructor has run.

`NewConnector` SHALL continue to install a non-nil cache during construction, so the lazy path is only ever exercised by hand-built literals.

#### Scenario: Read against a nil-cache connector misses

- **WHEN** a `&Connector{}` literal with a nil `cache` field has `getDBConnection(key)` called
- **THEN** the accessor lazily installs the default cache and the call returns `(CachedConnection{}, false)`

#### Scenario: Store against a nil-cache connector lands

- **WHEN** a `&Connector{}` literal with a nil `cache` field has `storeDBConnection(key, conn)` called
- **AND** `getDBConnection(key)` is called subsequently
- **THEN** the value stored is returned

#### Scenario: Dispose against a nil-cache connector is a no-op

- **WHEN** a `&Connector{}` literal with a nil `cache` field has `Dispose()` called
- **THEN** the accessor installs an empty default cache, disposing it has no observable effect, and no panic occurs

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
