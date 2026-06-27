# Design

## Decisions

### D1: A `func` field, not an interface

`Interpolator` is now `type Interpolator func(ctx context.Context, query
*sqlutil.Query, rawJSON json.RawMessage) (string, error)`, assigned directly
to `SQLDatasource.Interpolator`.

Rationale: the extension is a single-method strategy. A func field is the
idiomatic Go shape for that — trivially stubbable in tests (assign a closure),
nothing to implement, and less to deprecate when `macropro` lands.

Alternative considered: keep the interface. Rejected — the only consumer
already ignores the receiver state the interface implies, so the interface
buys nothing over a func while costing a struct + method per implementation.

### D2: Drop the `*SQLDatasource` parameter

The method previously received `*SQLDatasource`. It is removed.

Rationale: it was redundant with the field it lives on, and consumers that
need datasource state close over it instead. The built-in default closes over
`ds` via `defaultInterpolator(ds)` so the func signature itself need not carry
the datasource.

### D3: Default installed by `NewDatasource`, with a nil-guard fallback

`NewDatasource` sets `ds.Interpolator = defaultInterpolator(ds)`. The internal
`ds.interpolate(...)` entry point also resolves a nil field to the default.

Rationale: installing the default on the field makes the built-in macro path
*replaceable* — assigning an override leaves sqlds's own macro code dead, which
is the `macropro` deprecation story. Keeping the nil-guard in `interpolate`
means a zero-value `SQLDatasource` (constructed without `NewDatasource`, e.g.
in a test) does not panic on a nil func call. Belt-and-suspenders, both cheap.

### D4: Keep `rawJSON` in the signature

Rationale: `sqlutil.Query` parses only its fixed fields and drops the rest, so
`rawJSON` is the only channel for carrying plugin-defined macro context (round
intervals, ad-hoc filters, ...) across the call. Removing it would make the
extension point unusable for the one consumer that exists.

## Risks

- [Plugins implementing the old `Interpolator` interface break at compile] →
  Mitigation: the surface is pre-release; the single known consumer (Hydrolix
  plugin) is updated in lockstep and carries a compile-time
  `var _ sqlds.Interpolator = (&HdxInterpolator{}).Interpolate` assertion that
  catches future signature drift at build time.
- [Zero-value `SQLDatasource` has a nil func → panic on call] → Mitigation:
  `interpolate` nil-guards to `defaultInterpolator(ds)`; proven by
  `TestNilInterpolator_FallsBackToDefault`.
- [Default no longer byte-for-byte matches `sqlutil.Interpolate`] → Mitigation:
  `TestDefaultInterpolator_LegacyParity` asserts equivalence against
  `sqlutil.Interpolate` over the legacy macro fixture corpus.
