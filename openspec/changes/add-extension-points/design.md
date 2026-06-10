## Context

`grafana/sqlds` v5.1.1 (revision `6c09016`) is a thin layer between Grafana datasource plugins and `grafana-plugin-sdk-go/data/sqlutil`. The current public surface for macros is a re-export of `sqlutil.MacroFunc` (`macros.go:16`), interpolation is `sqlutil.Interpolate` (`macros.go:28-30`), and the datasource struct already accepts a handful of hook fields (`PreCheckHealth`, `PostCheckHealth`, `ResourceMiddleware` at `datasource.go:66-70`). There is no place to substitute the interpolator with an AST-aware implementation (which also owns any per-query setup or post-rewrite mutation it needs).

The Hydrolix fork has been the workaround. It added a CTE-aware AST visitor in `interpolator.go` and a `MutateInterpolatedQuery` post-hook. The fork has been flagged in catalog review for moving security-relevant code (notably interpolation) out of the maintained, reviewed dependency.

This design promotes the seam the fork already proved out into a reusable extension point in `sqlds`, so the same behavior can be supplied from outside the library.

## Goals / Non-Goals

**Goals:**
- Let plugins replace the interpolator implementation per-datasource. A custom `Interpolator` owns the entire rewrite call — per-query setup, macro dispatch (including any plugin-defined macro shape, context type, or registry), and post-rewrite mutation.
- Preserve the existing public API; do not break datasources that don't opt in.
- Keep upstream `sqlutil` as the macro engine — `sqlds` adds the single pluggable seam around it.

**Non-Goals:**
- Move macros, interpolators, or filter-condition builders for any specific backend into `sqlds`. Those live in plugin packages.
- Change `sqlutil.MacroFunc` upstream.
- Add backend-specific knowledge (ClickHouse parsers, dollar-quoted literals, AdHoc filter shapes) to `sqlds`.
- Expose plugin-attached state (metadata providers, parsers, caches) or richer macro contexts as public sqlds API. Plugins host those on their own wrapper types, alongside the custom `Interpolator` that uses them.
- Driver-level parameter binding. Macros that need safe value interpolation emit single-quoted SQL literals with proper escaping (`'` → `\'`, `\` → `\\`) from inside their own implementation in the plugin package. Whether to move to bound parameters is a separate decision tracked outside this change.
- Generic backwards-compat shims for the existing fork shape. The HDX extension package is the consumer; it adapts to whatever upstream lands.

## Decisions

### Decision 1: `Interpolator` interface with default

```go
type Interpolator interface {
    Interpolate(ctx context.Context, ds *SQLDatasource, query *sqlutil.Query, rawJSON json.RawMessage) (string, error)
}
```

A default implementation (`DefaultInterpolator`) wraps `sqlutil.Interpolate` for legacy `sqlutil.MacroFunc` macros — byte-for-byte equivalent to the pre-extension default. Plugins assign their own via `SQLDatasource.Interpolator`.

The signature carries `ctx` (for cancellation and downstream calls), the parsed `*sqlutil.Query`, and the raw `json.RawMessage` from the original `backend.DataQuery.JSON` (so a plugin's interpolator can unmarshal its own query model — for example, an `HDXQuery` with extra filter fields — without `sqlds` knowing the shape). Returns the rewritten SQL plus any expansion error. The signature returns only `(string, error)` — no parameter list. Macros that need value interpolation emit escaped string literals themselves; `query.go` `Run` continues to call `db.QueryContext(ctx, sql, args...)` with whatever explicit `args` the caller passed, unchanged.

**Alternatives considered.**
- *Function pointer instead of interface.* Interface is more discoverable in Go code review and allows future composition (e.g. middleware-style wrapping).
- *Return `[]driver.NamedValue` for parameter binding.* Out of scope for this change — the working assumption is that macros escape values inline (see Non-Goals).

### Decision 2: Per-query setup, plugin-state access, and post-rewrite mutation all live inside the custom `Interpolator`

Plugins that need per-query setup (e.g. parse the SQL into an AST once and reuse it across macros), plugin-attached singletons (a metadata provider, a parser, a cache), or post-rewrite mutation (the `MutateInterpolatedQuery` design from fork commit `1738cf0`) implement those steps inside their own `Interpolator` rather than via separate hook fields, registries, or macro signatures exposed by `sqlds`.

**Why no separate hook fields or upstream-side registry.** A custom `Interpolator` already owns the full rewrite call; surrounding hooks would be parallel extension surface that does the same thing with less control over ordering, error handling, and composition. Plugin-attached singletons are ordinary Go fields on the plugin's own wrapper type — `sync.Map`-backed state is ~10 lines of plugin code, and it lives in the same package as the `Interpolator` that uses it, which is the natural home. Pulling those concerns into `sqlds`'s public API would add surface for a pattern that the only consumer (the Hydrolix plugin) hosts more cleanly on its own type.

If a future consumer wants to add behaviour around `DefaultInterpolator` without replacing it, an `Interpolator` middleware helper can be added then.

### Decision 3: Value escaping is the macro's responsibility

`sqlds` does not provide a parameter binder, a string escaper, or any other value-safety primitive. A macro that interpolates a user-supplied value MUST emit a safely-quoted SQL literal itself — typically a single-quoted string with `'` escaped as `\'` and `\` escaped as `\\` (the standard ClickHouse / MySQL convention). Escaping helpers live in the plugin package next to the macros that use them.

**Why this is OK.** Macros are already trusted to produce valid SQL fragments. Escaping is a small, well-understood pattern that the macro author owns end-to-end. Pulling it into `sqlds` would either require backend-specific knowledge (different escape rules per dialect) or a least-common-denominator helper that doesn't match any one backend cleanly.

**Why not parameter binding instead.** Considered, deliberately deferred. Adding it now would expand the API surface and the upstream-merge ask for a feature that the immediate consumers (the HDX extension package) can solve with inline escaping. Tracked as a possible follow-up; not blocking this change.

## Risks / Trade-offs

- [Plugin authors get escaping wrong inside their custom `Interpolator`] → Mitigation: the README's "Migrating off a fork" section and the plugin extension package both ship a worked example of the single-quoted escape pattern. Long-term safety net: a future change can add bound-parameter support without breaking what we ship here.
- [Backwards compatibility regressions if a datasource leaves `Interpolator` nil] → Mitigation: nil falls through to `DefaultInterpolator`, which is behaviorally equivalent to today's `sqlutil.Interpolate` path. A regression test asserts byte-for-byte parity against `sqlutil.Interpolate` for the legacy macro fixture corpus.
- [Increased API surface complicates upstream merges] → Mitigation: every new symbol is opt-in. No existing symbol changes signature. The surface is minimal — one interface, one default type, one field.

## Migration Plan

This change is additive within `sqlds`. No migration is needed for existing datasource plugins. For the Hydrolix fork specifically:

1. Land this change on `grafana/sqlds`.
2. Create `hdx-grafana-sqlds-ext` consuming upstream `sqlds`.
3. Move the fork's interpolator into the new package, rewritten against the `Interpolator` interface. Plugin-attached state (metadata provider, parser, cache) lives on the plugin's wrapper type. Per-query setup and post-rewrite mutation move inside the custom `Interpolator`. Replace the `$$…$$` dollar-quoted literals with safely-escaped single-quoted literals inside the plugin's macro implementations.
4. Switch the Hydrolix Grafana plugin's import from `github.com/hydrolix/sqlds/v5` to `github.com/grafana/sqlds/v5` + `github.com/hydrolix/hdx-grafana-sqlds-ext`.
5. Retire the fork.

(That migration is separate work, not part of this change — tracked in `hydrolix/grafana-datasource-plugin@openspec/changes/retire-sqlds-fork`.)

## Open Questions

- Does `Interpolator.Interpolate` need to return per-macro diagnostics (line/column of error)? Today's `sqlutil.Interpolate` doesn't; deferring unless a consumer needs it.
- Should `sqlds` ship a single canonical escape helper (e.g. `EscapeSingleQuoted(string) string`) for the common ClickHouse/MySQL dialect, even though escaping is the macro's responsibility? Defer to a separate change if a real consumer asks.
