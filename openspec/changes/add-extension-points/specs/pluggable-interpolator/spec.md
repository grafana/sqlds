## ADDED Requirements

### Requirement: `Interpolator` interface with default implementation

`sqlds` SHALL expose an `Interpolator` interface and a default implementation that wraps `sqlutil.Interpolate`.

```go
type Interpolator interface {
    Interpolate(ctx context.Context, ds *SQLDatasource, query *sqlutil.Query, rawJSON json.RawMessage) (sql string, err error)
}
```

A `DefaultInterpolator` SHALL be provided that delegates directly to `sqlutil.Interpolate` for legacy `sqlutil.MacroFunc` macros registered via the driver's `Macros()` method. Its body SHALL be byte-for-byte equivalent to the pre-extension default behaviour and SHALL NOT introduce any other branching or extension-point indirection.

#### Scenario: Default interpolator preserves legacy behavior

- **WHEN** a datasource has no custom `Interpolator`
- **AND** a query is executed
- **THEN** the SQL passed to the driver is identical to the SQL `sqlutil.Interpolate` would produce for the same input — byte-for-byte, for every fixture in the legacy test corpus

### Requirement: Per-datasource interpolator override

`SQLDatasource` SHALL expose a public `Interpolator` field. If non-nil, this implementation replaces the default for all queries on that datasource. A custom `Interpolator` SHALL own the entire interpolation call — including any per-query setup, macro dispatch (in whatever shape the plugin chooses), and post-rewrite mutation — without further hooks on `sqlds`.

#### Scenario: A plugin installs an AST-based interpolator

- **WHEN** a plugin sets `ds.Interpolator = myAstInterpolator` at datasource construction
- **AND** a query is executed
- **THEN** `myAstInterpolator.Interpolate` is invoked and `DefaultInterpolator` is not

#### Scenario: Nil falls back to default

- **WHEN** `ds.Interpolator` is `nil`
- **AND** a query is executed
- **THEN** `DefaultInterpolator` is invoked
