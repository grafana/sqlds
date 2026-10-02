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

### Converters

`Driver.Converters()` returns the `sqlutil.Converter` list used to map SQL
column types to dataframe field types. `sqlds` passes them straight to
`sqlutil.FrameFromRows`, which picks the first converter in slice order whose
matching criteria fit the column. A converter matches when any of these apply:

- `InputColumnName`: exact, case-sensitive column name match
- `InputTypeName`: exact, case-sensitive match on `sql.ColumnType.DatabaseTypeName()`
- `InputTypeRegex`: regex match on the database type name
- `InputTypeMatcher`: a `func(dbType string) bool` consulted with the database type name

For parameterised or nested types, prefer `InputTypeMatcher` over enumerating
regex permutations. A single matcher per base converter can unwrap wrapper
types recursively, so one `Float64` converter covers `Float64`,
`SimpleAggregateFunction(max, Float64)`,
`LowCardinality(Float64)` and any nested combination:

```go
{
    Name:          "Float64",
    InputScanType: reflect.TypeOf(float64(0)),
    InputTypeMatcher: func(dbType string) bool {
        return unwrapWrapperTypes(dbType) == "Float64" // your recursive unwrap helper
    },
    FrameConverter: sqlutil.FrameConverter{
        FieldType: data.FieldTypeFloat64,
        ConverterFunc: func(in interface{}) (interface{}, error) {
            return *(in.(*float64)), nil
        },
    },
}
```

Order converters from most to least specific: the first match wins, so a
broad matcher placed early can shadow later converters. The database driver
must be able to scan the wrapped type into the converter's `InputScanType`.

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

The cache traffics in `CachedConnection`, an exported concrete value type
that pairs the underlying `*sql.DB` with the captured
`DataSourceInstanceSettings`. Its fields are unexported; read them through
the `DB()`/`Settings()` accessors and release the connection with `Close()`.
Because it is a plain value, a plugin's TTL cache can be as simple as a
mutex-guarded `map[string]CachedConnection`.

The factory is invoked once per `Connector` during datasource construction;
plugins capture their own configuration (TTL, size cap, dependencies) in
the closure. A nil factory falls back to `NewSyncMapCache()`, which is
behaviourally equivalent to the pre-extension `sync.Map`-backed storage
(no eviction, no background goroutines).

### Connection pool limits

Every `*sql.DB` the `Connector` caches gets its pool bounded when it is
opened, on the bootstrap connect, the deferred default connect, `Reconnect`
and the per-connection-args path alike. Each of the three knobs is resolved
in this order:

1. `DriverSettings.MaxOpenConns`, `MaxIdleConns` or `ConnMaxLifetime`, when
   the driver sets it to a non-zero value. This is where a plugin exposes a
   per-data-source override read from its `jsonData`.
2. The Grafana `[sql_datasources]` defaults (`max_open_conns_default`,
   `max_idle_conns_default`, `max_conn_lifetime_default`), which Grafana
   injects into the plugin environment and the SDK exposes through
   `GrafanaCfg.SQL()`.
3. Otherwise the knob is left at its `database/sql` default.

A driver that bounds the pool itself inside `Connect` (a `SetMaxOpenConns`
call with `n > 0`) is left alone entirely, because `database/sql` gives no
way to tell which of the other knobs it also set. A driver that sets only
idle or lifetime there, or passes `n <= 0`, is treated as unbounded and
receives the resolved values for all three knobs. Move such settings to
`DriverSettings` instead.

Negative `DriverSettings` values reach `database/sql` unchanged: no limit
for `MaxOpenConns`, no idle connections kept for `MaxIdleConns`, and no
age limit for `ConnMaxLifetime`.
