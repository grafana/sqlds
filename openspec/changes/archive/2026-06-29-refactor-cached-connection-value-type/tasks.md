## 1. Concrete `CachedConnection` value type

- [x] 1.1 Rename the unexported `dbConnection` struct to the exported `CachedConnection` (in `datasource.go`); keep `db` / `settings` unexported
- [x] 1.2 Move/keep the `DB()` / `Settings()` / `Close()` accessors as value-receiver methods on `CachedConnection`; update their GoDoc to describe a concrete opaque value, not an interface
- [x] 1.3 Drop the standalone `CachedConnection` interface declaration from `cache.go`

## 2. `ConnectionCache` traffics in the concrete type

- [x] 2.1 Change `Load` / `Store` / `Range` signatures to use `CachedConnection` (struct) instead of the interface; `Load` miss returns `(CachedConnection{}, false)`
- [x] 2.2 Update `syncMapCache` methods to assert `v.(CachedConnection)` internally (safe: it is the sole writer of its own `sync.Map`); document that the assertion is by-construction, not by external contract
- [x] 2.3 Remove the no-wrapping / no-decoration contract paragraph and the runtime-panic rationale from the `ConnectionCache` GoDoc

## 3. `Connector` wiring + unified nil policy

- [x] 3.1 Add a private `connCache()` accessor that lazily installs `NewSyncMapCache()` when `cache == nil` and returns the cache
- [x] 3.2 Reduce `getDBConnection` to `return c.connCache().Load(key)` — no type assertion, no nil branch
- [x] 3.3 Reduce `storeDBConnection` to `c.connCache().Store(key, conn)`
- [x] 3.4 Reduce `Dispose` to `c.connCache().Dispose()`
- [x] 3.5 Update `Connect` / `Reconnect` / `GetConnectionFromQuery` and internal helpers (`connectWithRetries`, `connect`, `ping`) to name `CachedConnection` in place of `dbConnection`

## 4. Tests

- [x] 4.1 Update `cache_test.go`: replace the interface-implementing `stubConn` with real (closeable) `*sql.DB` values (via the existing `noopConnector`); `Dispose` test asserts each DB is actually closed
- [x] 4.2 Drop the `var _ CachedConnection = dbConnection{}` compile-time interface assertion (no longer an interface)
- [x] 4.3 Update `connector_cache_test.go` `recordingCache.Store` and `datasource_connect_test.go` Range callback / literals to the concrete type
- [x] 4.4 Update `bench_test.go` connection literal to the concrete type
- [x] 4.5 Add nil-cache policy coverage: a `&Connector{}` literal misses on read, lands on store, and disposes without panic

## 5. Documentation

- [x] 5.1 Rewrite the README "Pluggable connection cache" prose: describe `CachedConnection` as an exported concrete value with `DB()`/`Settings()`/`Close()` accessors and a `map[string]CachedConnection`-simple cache; delete the "MUST return the exact value / panics" paragraph

## 6. Validation gates

- [x] 6.1 `go build ./...` — clean
- [x] 6.2 `go vet ./...` — clean
- [x] 6.3 `gofmt -l *.go` — no files listed
- [x] 6.4 `go test ./... -race` — all tests pass
- [x] 6.5 Symbol grep: `grep -rnw dbConnection --include='*.go' .` returns nothing; bare `Connection` only resolves to the unrelated `driver.go` interface
