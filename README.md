[![Build Status](https://drone.grafana.net/api/badges/grafana/sqlds/status.svg)](https://drone.grafana.net/grafana/sqlds)

# sqlds

`sqlds` stands for `SQL Datasource`.

Most SQL-driven datasources, like `Postgres`, `MySQL`, and `MSSQL` share extremely similar codebases.

The `sqlds` package is intended to remove the repetition of these datasources and centralize the datasource logic. The only thing that the datasources themselves should have to define is connecting to the database, and what driver to use, and the plugin frontend.

**Usage**

```go
if err := datasource.Manage("my-datasource", datasourceFactory, datasource.ManageOpts{}); err != nil {
  log.DefaultLogger.Error(err.Error())
  os.Exit(1)
}

func datasourceFactory(ctx context.Context, s backend.DataSourceInstanceSettings) (instancemgmt.Instance, error) {
  ds := sqlds.NewDatasource(&myDatasource{})
  return ds.NewDatasource(ctx, s)
}
```

## Standardization

### Macros

The `sqlds` package formerly defined a set of default macros, but those have been migrated to `grafana-plugin-sdk-go`: see [the code](https://github.com/grafana/grafana-plugin-sdk-go/blob/main/data/sqlutil/macros.go) for details.

### Pluggable interpolator

`SQLDatasource.Interpolator` is a func field that produces the SQL reaching
the driver:

```go
type Interpolator func(ctx context.Context, query *sqlutil.Query, rawJSON json.RawMessage) (string, error)
```

`NewDatasource` installs a default that delegates to `sqlutil.Interpolate`
over the driver's `Macros()` — byte-for-byte equivalent to the pre-extension
default. Override it by assigning your own func (for example an AST-aware
rewriter or a [`macropro`](https://github.com/grafana/macropro)-backed
handler):

```go
ds := sqlds.NewDatasource(driver)
ds.Interpolator = func(ctx context.Context, q *sqlutil.Query, rawJSON json.RawMessage) (string, error) {
    return myRewriter.Interpolate(ctx, q, rawJSON)
}
```

`rawJSON` carries the unparsed query JSON: `sqlutil.Query` keeps only its
fixed fields and drops the rest, so it's the channel for plugin-defined macro
context. A nil `Interpolator` resolves to the default, so a zero-value
`SQLDatasource` built without `NewDatasource` still interpolates.


### Pluggable connection cache

`SQLDatasource.ConnectionCacheFactory` accepts a factory function that
returns any implementation of the `ConnectionCache` interface:

```go
type ConnectionCache interface {
    Load(key string) (CachedConnection, bool)
    Store(key string, v CachedConnection)
    Range(f func(key string, v CachedConnection) bool)
    Dispose()
}
```

The factory is invoked once per `Connector` during datasource construction;
plugins capture their own configuration (TTL, size cap, dependencies) in
the closure. A nil factory falls back to `NewSyncMapCache()`, which is
behaviourally equivalent to the pre-extension `sync.Map`-backed storage
(no eviction, no background goroutines).

Cache implementations MUST return from `Load` the exact `CachedConnection`
value that was previously passed to `Store` — no wrapping or decoration.
`sqlds`-internal code type-asserts the returned value back to its concrete
type, and a wrapping implementation will panic at runtime.