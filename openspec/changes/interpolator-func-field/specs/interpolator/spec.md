# interpolator

## ADDED Requirements

### Requirement: `Interpolator` is a func type

`sqlds` SHALL define `Interpolator` as a func type
`func(ctx context.Context, query *sqlutil.Query, rawJSON json.RawMessage) (string, error)`.
It SHALL NOT be an interface, and the signature SHALL NOT carry a
`*SQLDatasource` parameter. Implementations SHALL be safe for concurrent use
across queries.

#### Scenario: Closure conforms to the func type

- **GIVEN** a closure `func(ctx context.Context, q *sqlutil.Query, raw json.RawMessage) (string, error)`
- **WHEN** assigned to a `sqlds.Interpolator` variable or to `SQLDatasource.Interpolator`
- **THEN** the assignment SHALL compile without error

### Requirement: `NewDatasource` installs the default interpolator

`NewDatasource` SHALL set `SQLDatasource.Interpolator` to the default
interpolator. The default SHALL delegate to `sqlutil.Interpolate` over the
macros returned by the driver's `Macros()` method, byte-for-byte equivalent to
the pre-extension behaviour.

#### Scenario: Default produces legacy-equivalent SQL

- **GIVEN** a datasource whose driver registers a legacy macro `upper`
- **WHEN** the default interpolator runs on `SELECT $__upper(col) FROM t`
- **THEN** the output SHALL equal `SELECT UPPER(col) FROM t`
- **AND** the output SHALL equal `sqlutil.Interpolate` invoked directly with the same macros

### Requirement: A nil `Interpolator` field resolves to the default

The datasource's internal interpolation entry point SHALL resolve a nil
`SQLDatasource.Interpolator` field to the default interpolator rather than
calling a nil func, so a zero-value `SQLDatasource` constructed without
`NewDatasource` still interpolates.

#### Scenario: Zero-value datasource still interpolates

- **GIVEN** an `SQLDatasource` built as a struct literal (no `NewDatasource`) with a driver exposing macro `upper`
- **WHEN** the datasource interpolates `SELECT $__upper(col)`
- **THEN** the call SHALL NOT panic
- **AND** the output SHALL equal `SELECT UPPER(col)`

### Requirement: An assigned `Interpolator` replaces the default

The datasource's interpolation entry point SHALL invoke a non-nil
`Interpolator` assigned to `SQLDatasource.Interpolator` and SHALL NOT invoke
the default macro path when one is assigned.

#### Scenario: Custom func suppresses the default

- **GIVEN** an `SQLDatasource` with `Interpolator` assigned to a func returning `"OVERRIDDEN"`
- **WHEN** the datasource interpolates any query
- **THEN** the returned SQL SHALL be `"OVERRIDDEN"`
- **AND** the assigned func SHALL have been invoked exactly once

### Requirement: `rawJSON` is passed through to the interpolator

The datasource's interpolation entry point SHALL pass the request's unparsed
query JSON to the `Interpolator` as the `rawJSON` argument, so a custom
interpolator can recover plugin-defined fields that `sqlutil.Query` does not
parse.

#### Scenario: Interpolator receives the raw query JSON

- **GIVEN** a custom `Interpolator` that records its `rawJSON` argument
- **WHEN** the datasource interpolates a request carrying a JSON body
- **THEN** the recorded `rawJSON` SHALL equal the request's query JSON

### Requirement: The exported `DefaultInterpolator` struct is removed

`sqlds` SHALL NOT export a `DefaultInterpolator` type. The default behaviour
SHALL be reachable only by leaving `SQLDatasource.Interpolator` nil or by
using the value `NewDatasource` installs.

#### Scenario: No exported default type

- **GIVEN** the `sqlds` package API
- **WHEN** a consumer references `sqlds.DefaultInterpolator`
- **THEN** the reference SHALL fail to compile (the identifier does not exist)
