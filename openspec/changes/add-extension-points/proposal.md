## Why

Today, plugins that need behavior beyond what `grafana/sqlds` provides (AST-aware interpolation, per-query setup, post-rewrite mutation) have no clean way to inject it. The practical workaround has been to fork the library, which moves security-relevant code (query interpolation, OAuth header construction, driver wiring) out of the maintained, reviewed dependency. The Hydrolix fork is a concrete example: it diverged to add a CTE-aware interpolator and a `MutateInterpolatedQuery` post-hook. That work could live outside `sqlds` if the library exposed a single seam.

This change adds that seam so a plugin can supply backend-specific behavior from its own package while keeping `sqlds` as the upstream, audited dependency.

## What Changes

- **Pluggable `Interpolator` interface.** Promote the current regex-based interpolator into a default implementation behind an `Interpolator` interface; allow datasources to install a custom implementation (e.g. an AST-based one). Plugins that need per-query setup, plugin-state access, or post-rewrite mutation own those steps inside their custom `Interpolator` — the interpolator owns the entire rewrite call, so no separate hook fields are needed.
- **Backwards compatibility.** The existing `sqlutil.MacroFunc` registration via the driver's `Macros()` method continues to work unchanged. A nil `Interpolator` field falls back to a default that delegates to `sqlutil.Interpolate` byte-for-byte.
- **Out of scope: parameterized queries.** This change does not introduce a driver-arg binder. Macros that need safe value interpolation emit single-quoted SQL literals with proper escaping (`'` → `\'`, `\` → `\\`) from inside their own implementation in the plugin package. Whether to move to driver-level parameter binding is a separate decision tracked outside this change.

## Capabilities

### New Capabilities

- `pluggable-interpolator`: `Interpolator` interface with a default implementation that wraps `sqlutil.Interpolate`; per-datasource override via the `SQLDatasource.Interpolator` field. A custom `Interpolator` owns the full interpolation pipeline (per-query setup, macro dispatch with plugin-defined signatures, post-rewrite mutation) without further hooks on `sqlds`.

### Modified Capabilities

<!-- none — no existing specs in this repository -->

## Impact

- **Affected code (additive):** new `interpolator.go` (`Interpolator` interface and `DefaultInterpolator`), `datasource.go` (interpolator field).
- **Public API:** new `Interpolator` interface, new `DefaultInterpolator` type, new `SQLDatasource.Interpolator` field. No existing exported symbol changes signature.
- **Dependencies:** none added.
- **Consumers:** datasource plugins gain a single optional integration surface; plugins that don't set `Interpolator` are unaffected. Downstream effect: the Hydrolix fork's interpolator becomes movable to an external `hdx-grafana-sqlds-ext` package that imports `grafana/sqlds` and assigns its own `Interpolator` — closing the catalog-review concern about forking a security-relevant library.
- **Risks:** none meaningful beyond the addition itself. The default-path parity with `sqlutil.Interpolate` is preserved and asserted by a regression test.
