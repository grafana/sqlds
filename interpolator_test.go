package sqlds

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
)

// macroDriver is a minimal Driver used to inject legacy macros into tests.
type macroDriver struct {
	SQLMock
	macros sqlutil.Macros
}

func (d *macroDriver) Macros() Macros { return d.macros }

func newDS(legacy sqlutil.Macros) *SQLDatasource {
	if legacy == nil {
		legacy = sqlutil.Macros{}
	}
	return &SQLDatasource{
		connector: &Connector{driver: &macroDriver{macros: legacy}, cache: NewSyncMapCache()},
	}
}

// TestDefaultInterpolator_LegacyParity asserts that the default interpolator
// produces byte-for-byte the same SQL as sqlutil.Interpolate for the legacy
// macro fixture corpus — the invariant the post-extension default must hold.
func TestDefaultInterpolator_LegacyParity(t *testing.T) {
	legacy := sqlutil.Macros{
		"upper": func(q *sqlutil.Query, args []string) (string, error) {
			return "UPPER(" + args[0] + ")", nil
		},
	}
	fixtures := []struct {
		sql  string
		want string
	}{
		{sql: "SELECT 1", want: "SELECT 1"},
		{sql: "SELECT $__upper(col) FROM t", want: "SELECT UPPER(col) FROM t"},
		{sql: "SELECT $__upper(col), $__upper(other) FROM t", want: "SELECT UPPER(col), UPPER(other) FROM t"},
	}
	ds := newDS(legacy)
	interp := defaultInterpolator(ds)
	for _, fx := range fixtures {
		q := &sqlutil.Query{RawSQL: fx.sql}
		got, err := interp(context.Background(), q, nil)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got != fx.want {
			t.Fatalf("sql=%q\n got: %s\nwant: %s", fx.sql, got, fx.want)
		}
		// Confirm equivalence with sqlutil.Interpolate directly.
		direct, err := sqlutil.Interpolate(q, legacy)
		if err != nil {
			t.Fatalf("direct sqlutil.Interpolate err: %v", err)
		}
		if direct != got {
			t.Fatalf("parity violated for %q:\n  default: %s\n  sqlutil: %s", fx.sql, got, direct)
		}
	}
}

// TestNilInterpolator_FallsBackToDefault asserts that a zero-value Interpolator
// field (e.g. an SQLDatasource built without NewDatasource) resolves to the
// default rather than panicking on a nil func call.
func TestNilInterpolator_FallsBackToDefault(t *testing.T) {
	legacy := sqlutil.Macros{
		"upper": func(q *sqlutil.Query, args []string) (string, error) {
			return "UPPER(" + args[0] + ")", nil
		},
	}
	ds := newDS(legacy) // does not set ds.Interpolator
	if ds.Interpolator != nil {
		t.Fatal("expected nil Interpolator on a struct-literal datasource")
	}
	got, err := ds.interpolate(context.Background(), &sqlutil.Query{RawSQL: "SELECT $__upper(col)"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "SELECT UPPER(col)" {
		t.Fatalf("got %q want SELECT UPPER(col)", got)
	}
}

// TestCustomInterpolator_ReplacesDefault asserts that assigning a custom
// Interpolator func suppresses the default code path.
func TestCustomInterpolator_ReplacesDefault(t *testing.T) {
	called := false
	ds := newDS(nil)
	ds.Interpolator = func(_ context.Context, _ *sqlutil.Query, _ json.RawMessage) (string, error) {
		called = true
		return "OVERRIDDEN", nil
	}
	got, err := ds.interpolate(context.Background(), &sqlutil.Query{RawSQL: "SELECT $__m()"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("custom interpolator not called")
	}
	if got != "OVERRIDDEN" {
		t.Fatalf("got %q want OVERRIDDEN", got)
	}
}
