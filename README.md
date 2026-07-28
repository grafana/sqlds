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


### Query diagnostics capture

Grafana's on-demand datasource diagnostics can show the HTTP traffic behind a
failing panel, because the plugin SDK wraps the datasource's
`http.RoundTripper`. SQL datasources have no such hop, so their diagnostic
bundle shows the frames the plugin returned with nothing to compare them
against — "the database returned the wrong rows" is indistinguishable from
"the plugin mangled correct rows".

The `diagnostics` package is the SQL counterpart of that middleware. A host
installs a `Recorder` on the query context, and `DBQuery.Run` reports the
statement it executed:

```go
type Recorder interface {
    Record(Interaction)
}

ctx = diagnostics.WithRecorder(ctx, rec)
```

Each `Interaction` carries the interpolated SQL, its bind arguments, the
datasource identity and refID, the duration, and either the row/frame counts
or the error. That is the missing upstream side of the comparison: the SQL the
database actually saw, next to what came back.

`diagnostics.NewSliceRecorder` is a bounded, concurrency-safe reference
implementation. Recorders are written to from several goroutines at once
(sqlds runs one per refID) and run inline on the query path, so they must be
safe for concurrent use and must not block.

Two properties are deliberate:

- **Capture is off unless a `Recorder` is in the context.** With none present,
  `Run` behaves exactly as it did before, and nothing about the response
  changes. Plugins need no code change to gain capture; they need only a
  version bump.
- **sqlds does not encode or return interactions.** Turning an `Interaction`
  into a HAR entry and attaching it to the `QueryDataResponse` is the host's
  job, so SQL and HTTP evidence can land in one document instead of competing
  for the SDK's reserved `__har__` refID.

`RecordingConnection` covers the paths that do not go through `DBQuery.Run` —
a plugin that takes a connection from `GetDBFromQuery` and runs its own schema
or completion queries.

Interactions carry SQL text, bind arguments and result summaries, all of which
can contain customer data and credentials. sqlds bounds their size
(`MaxStatementBytes`, `MaxArgsBytes`) but does not redact them; redaction and
retention are the `Recorder`'s responsibility.