## 1. `Interpolator` interface and default

- [x] 1.1 Define the `Interpolator` interface in a new `interpolator.go` (signature: `Interpolate(ctx, ds, query, rawJSON) (sql, err)`)
- [x] 1.2 Implement `DefaultInterpolator` that delegates to `sqlutil.Interpolate` for legacy `sqlutil.MacroFunc` macros — byte-for-byte equivalent to the pre-extension default
- [x] 1.3 Add the public `Interpolator` field on `SQLDatasource`; resolve nil to `DefaultInterpolator` at call sites
- [x] 1.4 Unit-test default behavior parity with `sqlutil.Interpolate` (golden SQL outputs for a set of legacy fixtures)
- [x] 1.5 Unit-test custom-interpolator override (default not invoked when field is set)

## 2. Backwards-compatibility guarantees

- [x] 2.1 Verify the existing `sqlutil.MacroFunc` re-export in `macros.go:16` continues to work unchanged
- [x] 2.2 Run the existing test suite (`go test ./...`) and confirm zero diffs in output SQL for legacy fixtures

## 3. Documentation

- [x] 3.1 Update `README.md` with an "Extension points" section pointing at the `Interpolator` interface and field
- [x] 3.2 Add a GoDoc example (`ExampleSQLDatasource_Interpolator`) showing a plugin installing a custom `Interpolator`
- [x] 3.3 Add a short migration note for plugin authors who today fork `sqlds` to add behaviour, pointing them at the `Interpolator` seam (plugin-attached state and any plugin-defined macro shape live on the plugin's own wrapper type)

## 4. Validation

- [x] 4.1 Run `openspec validate add-extension-points --strict` and resolve any issues
- [x] 4.2 Run `go vet ./...` and `go test ./...`; both must pass
- [x] 4.3 Confirm the parity test in `interpolator_test.go` asserts byte-for-byte equivalence between `DefaultInterpolator` and a direct `sqlutil.Interpolate` call for every legacy fixture
