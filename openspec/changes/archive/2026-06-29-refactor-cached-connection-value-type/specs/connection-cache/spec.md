## MODIFIED Requirements

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

## ADDED Requirements

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

## REMOVED Requirements

### Requirement: Cache implementations MUST NOT wrap or decorate stored values

**Reason:** The contract existed only so internal code could type-assert `Load`'s result back to the concrete `dbConnection`. With `CachedConnection` now a concrete value type, the cache boundary has no type assertion and no value to unwrap, so the contract — and the runtime panic that enforced it — is no longer meaningful.

**Migration:** None for conforming plugins. A cache that previously returned the exact value it was given (the only permitted behaviour) is unaffected; it now stores and returns a `CachedConnection` struct instead of an interface value.
