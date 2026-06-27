# Tasks

## 1. Convert the extension surface to a func field

- [x] 1.1 Replace the `Interpolator` interface with the func type
  `func(ctx context.Context, query *sqlutil.Query, rawJSON json.RawMessage) (string, error)` in `interpolator.go`
- [x] 1.2 Remove the exported `DefaultInterpolator` struct and its method
- [x] 1.3 Add `defaultInterpolator(ds *SQLDatasource) Interpolator` — a closure
  that delegates to `sqlutil.Interpolate` over `ds.driver().Macros()`
- [x] 1.4 Update `(*SQLDatasource).interpolate` to resolve a nil field to
  `defaultInterpolator(ds)` and call the func directly (no `*SQLDatasource` arg)

## 2. Wire the default and document

- [x] 2.1 Set `ds.Interpolator = defaultInterpolator(ds)` in `NewDatasource`
- [x] 2.2 Update the `SQLDatasource.Interpolator` field doc comment in `datasource.go`
- [x] 2.3 Rewrite the README "Pluggable interpolator" section for the func-field
  shape (default install, override pattern, why `rawJSON` is retained)

## 3. Tests

- [x] 3.1 `TestDefaultInterpolator_LegacyParity` — default equals
  `sqlutil.Interpolate` byte-for-byte over the legacy macro corpus
- [x] 3.2 `TestNilInterpolator_FallsBackToDefault` — a zero-value datasource
  interpolates without panicking
- [x] 3.3 `TestCustomInterpolator_ReplacesDefault` — an assigned func suppresses
  the default path and is invoked exactly once
- [x] 3.4 `go build ./...`, `go vet ./...`, `go test ./...` all green
