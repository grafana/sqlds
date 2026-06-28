## Why

The `add-connection-cache` change shipped `CachedConnection` as an **interface** and had the unexported `dbConnection` struct satisfy it via adapter methods. In review this abstraction proved to be cost without benefit:

- The interface is never used polymorphically. `Connector.getDBConnection` immediately type-asserts the value back to the concrete `dbConnection` (`v.(dbConnection)`), and every internal call site reads `.db` / `.settings` directly rather than through `DB()` / `Settings()`.
- To make that assertion safe, the cache had to document a "Load MUST return the exact value, no wrapping or decoration" contract, enforced by a runtime panic. That rules out the one reason an interface would exist here — letting a plugin supply its own implementation of the value.

The eviction use case the original change targeted needs the **cache** to be pluggable, not the **value**. The value is inert data (`*sql.DB` + settings); a plugin's TTL cache only ever stores it and hands it back. The opacity the interface appeared to provide is already supplied by the value type's unexported fields.

Separately, the nil-`cache` handling added by the original change is asymmetric across the three call sites: `getDBConnection` treats nil as "empty", `storeDBConnection` lazily creates a cache, and `Dispose` early-returns. The constructors already guarantee a non-nil cache, so the three should agree on one policy.

## What Changes

- **Replace the `CachedConnection` interface with an exported concrete struct.** `dbConnection` is renamed and exported as `CachedConnection`; its fields (`db`, `settings`) stay unexported. The `DB()` / `Settings()` / `Close()` accessors hang off the concrete type. The name `CachedConnection` is retained (rather than the originally-sketched `Connection`) to avoid a conflict with the existing `Connection` interface in `driver.go`.
- **`ConnectionCache` traffics in the concrete `CachedConnection` value.** `Load` / `Store` / `Range` signatures use the struct, not the interface.
- **Remove the no-wrapping/no-decoration contract and its runtime panic.** With a concrete value type there is no type assertion at the cache boundary, so nothing can be wrapped. `getDBConnection` returns the value straight from `Load`.
- **Unify the nil-cache policy.** A single private `Connector.connCache()` helper lazily installs `NewSyncMapCache()` when `cache == nil`; `getDBConnection`, `storeDBConnection`, and `Dispose` all route through it. Behaviour on a hand-built `&Connector{}` literal is now uniform (read misses, stores land, Dispose is a no-op on an empty cache).
- **Exported method signatures now use the exported type.** `Connect`, `Reconnect`, and `GetConnectionFromQuery` previously returned/accepted the unexported `dbConnection` from exported methods; they now use `CachedConnection`. Source-compatible — external callers could not name the old type.

## Capabilities

### Modified Capabilities

- `connection-cache`: the cached value becomes an exported concrete type instead of an interface; the no-wrapping contract is removed; the nil-cache policy is unified.

## Impact

- **Affected code:** `cache.go` (interface signatures, `syncMapCache` methods, drop interface + contract doc), `connector.go` (`connCache()` helper, delegate three helpers, type rename across signatures), `datasource.go` (move the struct + accessors here, update GoDoc), `README.md` (rewrite the "Pluggable connection cache" prose).
- **Public API:** `CachedConnection` changes from an interface to a struct with the same method set. `ConnectionCache` method signatures change their value type from interface to struct. `Connect` / `Reconnect` / `GetConnectionFromQuery` now name the exported type. `NewSyncMapCache`, `WithCache`, `ConnectionCacheFactory` are unchanged.
- **Consumers:** a plugin's cache becomes a guarded `map[string]CachedConnection`; no `Load` contract to honour. Plugins that only set `ConnectionCacheFactory` and store/return values opaquely need no changes beyond the type swap.
- **Net surface:** smaller — the diff removes more than it adds (interface declaration, three adapter methods, contract paragraph, runtime panic, and the boundary cast all deleted).
- **Risks:** none functional. The `syncMapCache` still type-asserts internally (`sync.Map` stores `any`), but it is the sole writer of its own map, so the assertion is safe by construction rather than by external contract.
