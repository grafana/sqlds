## 1. `CachedConnection` interface + `dbConnection` adapters

- [ ] 1.1 Define the `CachedConnection` interface in a new `cache.go` (methods: `DB() *sql.DB`, `Settings() backend.DataSourceInstanceSettings`, `Close() error`)
- [ ] 1.2 Add three adapter methods to the existing unexported `dbConnection` (in `connector.go`): `DB()`, `Settings()`, `Close()` — pure accessors, no behaviour changes. `dbConnection` fields and struct layout remain untouched
- [ ] 1.3 Unit-test that `dbConnection` satisfies `CachedConnection` (compile-time assertion `var _ CachedConnection = dbConnection{}` plus a positive assertion that `Close()` calls the underlying `db.Close()`)

## 2. `ConnectionCache` interface

- [ ] 2.1 Define the `ConnectionCache` interface in `cache.go` (methods: `Load(string) (CachedConnection, bool)`, `Store(string, CachedConnection)`, `Range(func(string, CachedConnection) bool)`, `Dispose()`)
- [ ] 2.2 GoDoc on `ConnectionCache` documents the no-wrapping / no-decoration contract: `Load` MUST return the exact value passed to `Store`. Include a comment that `sqlds`-internal code type-asserts the returned value back to the concrete connection struct, so wrapping panics at runtime

## 3. Default `sync.Map`-backed implementation

- [ ] 3.1 Implement an unexported `syncMapCache` struct backing the interface with `sync.Map` semantics
- [ ] 3.2 Export `NewSyncMapCache() ConnectionCache` as the constructor
- [ ] 3.3 `Dispose()` on the default iterates every live entry, calls `Close()` on each, then clears the map
- [ ] 3.4 No background goroutines, no eviction — behaviourally identical to the pre-extension `Connector.connections sync.Map` field

## 4. `Connector` wiring

- [ ] 4.1 Define exported `ConnectorOption` function type (`func(*Connector)`) and exported `WithCache(ConnectionCache) ConnectorOption` constructor
- [ ] 4.2 Add a trailing `opts ...ConnectorOption` parameter to `NewConnector`. Apply options before the bootstrap connection is stored, so `WithCache` controls where the bootstrap entry lands
- [ ] 4.3 Replace `Connector.connections sync.Map` with an unexported `cache ConnectionCache` field. Default the field to `NewSyncMapCache()` when no `WithCache` option is supplied
- [ ] 4.4 Update `Connector.storeDBConnection(key, dbConn)` to call `c.cache.Store(key, dbConn)` — `dbConn` satisfies `CachedConnection` via the adapter methods from 1.2
- [ ] 4.5 Update `Connector.getDBConnection(key)` to `Load` from the cache and type-assert the result back to `dbConnection`. The assertion is safe under the contract in 2.2 — document this at the call site
- [ ] 4.6 Update `Connector.Dispose()` to delegate to `c.cache.Dispose()`

## 5. `SQLDatasource` integration

- [ ] 5.1 Add the exported field `ConnectionCacheFactory func() ConnectionCache` to `SQLDatasource` in `datasource.go`. GoDoc explains nil-fallback behaviour and that the factory is invoked once per `Connector`
- [ ] 5.2 In `SQLDatasource.NewDatasource(ctx, settings)`, resolve the factory: if non-nil, call it and pass the result via `WithCache(...)` to `NewConnector`. Nil → `NewConnector` defaults to `NewSyncMapCache()` internally
- [ ] 5.3 Confirm the second constructor (`NewDatasource(c Driver) *SQLDatasource`, which creates a stub `Connector`) does not need touching — the stub never stores entries before the instance-method constructor replaces it

## 6. Tests

- [ ] 6.1 Unit-test `syncMapCache` `Load`/`Store`/`Range`/`Dispose` round-trip; include a wrap-detection test that constructs a custom cache returning a wrapped value and confirms the internal `getDBConnection` assertion panics (the documented failure mode)
- [ ] 6.2 Unit-test factory wiring: with `ConnectionCacheFactory = nil` the connector's cache is `*syncMapCache`; with a custom factory the returned instance is the one held
- [ ] 6.3 Unit-test bootstrap entry placement: when `WithCache(custom)` is passed to `NewConnector`, the bootstrap connection ends up in `custom`, not in the default cache
- [ ] 6.4 Backwards-compat parity test: drive a `Connector` through `Connect`, `GetConnectionFromQuery` (with `EnableMultipleConnections=true` and varying `ConnectionArgs`), and `Reconnect`; assert the cache contents match what the pre-change `sync.Map` implementation would have held
- [ ] 6.5 Concurrent-access test: many goroutines calling `Store`/`Load` against `syncMapCache`; run with `-race`
- [ ] 6.6 `Dispose` test: stores N entries via `syncMapCache.Store`, calls `Dispose`, asserts every stored `CachedConnection`'s `Close()` was called and the cache reports empty afterwards

## 7. Documentation

- [ ] 7.1 Update `README.md` "Extension points" to add a "Pluggable connection cache" subsection alongside "Pluggable interpolator". One paragraph: factory field, default behaviour, link to GoDoc example
- [ ] 7.2 Add a runnable GoDoc example `ExampleSQLDatasource_ConnectionCacheFactory` showing a plugin installing a custom cache (a thin wrapper around the default impl is sufficient — the example demonstrates the wiring, not a real TTL policy)
- [ ] 7.3 GoDoc on `CachedConnection` documents the lifetime: the value is valid as long as the cache holds it; `Close()` is called once at eviction or `Dispose`

## 8. Validation gates

- [ ] 8.1 Run `openspec validate add-connection-cache --strict` — passes
- [ ] 8.2 Run `go vet ./...` — clean
- [ ] 8.3 Run `go test ./... -race` — all tests pass; backwards-compat parity test confirms no diff in observable cache behaviour for datasources without `ConnectionCacheFactory`
- [ ] 8.4 Symbol grep: confirm `Connector.connections` is no longer referenced anywhere in `*.go` (`grep -RnE 'c\.connections\b|connections\s+sync\.Map' --include='*.go' .` returns nothing)
