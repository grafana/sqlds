## Why

The pluggable interpolator extension (added at `ef925e1`) shipped as an
`Interpolator` *interface* with a single
`Interpolate(ctx, *SQLDatasource, *sqlutil.Query, json.RawMessage)` method,
plus a `DefaultInterpolator` struct and an internal wrapper. Two things cut
against keeping that surface as thin as the job needs:

- The `Interpolate` method takes `*SQLDatasource`, but the interpolator is
  also a field *on* `SQLDatasource` — the datasource passes itself to its own
  field. The one real consumer (the Hydrolix plugin) ignores the parameter
  entirely, closing over the datasource and metadata it needs at construction.
- An interface + `DefaultInterpolator` struct + wrapper method is more surface
  than a single-method strategy needs. In Go a first-class `func` field does
  the same work with less to deprecate later.

Shaping the field as a `func` also makes it the migration bridge to
[`macropro`](https://github.com/grafana/macropro) (which ships SQL macro
handlers and supports overrides): a plugin assigns a `macropro`-backed `func`,
sqlds's own macro path becomes dead code, and a later major can delete it with
just the field remaining.

## What Changes

- **BREAKING** `Interpolator` becomes a func type —
  `func(ctx, *sqlutil.Query, json.RawMessage) (string, error)` — replacing the
  interface. The `*SQLDatasource` parameter is removed.
- **BREAKING** the exported `DefaultInterpolator` struct is removed; the
  default becomes an unexported closure installed by `NewDatasource`.
- `SQLDatasource.Interpolator` field type changes from interface to func; a
  nil field still resolves to the default, so a zero-value `SQLDatasource`
  built without `NewDatasource` still interpolates.
- `NewDatasource` installs the default interpolator on the field.
- `rawJSON` is retained in the signature as the channel for plugin-defined
  macro context — `sqlutil.Query` parses only its fixed fields and drops the
  rest.
- The README "Pluggable interpolator" section is rewritten for the func-field
  shape.
- Unit coverage: legacy-parity, nil-resolves-to-default, custom-override.

## Capabilities

- `interpolator`
