package sqlds

import (
	"context"
	"encoding/json"

	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
)

// Interpolator owns the SQL rewriting pipeline for a datasource and produces
// the SQL that reaches the driver. Plugins replace the default by assigning
// a custom value to SQLDatasource.Interpolator.
//
// Implementations MUST be safe for concurrent use across queries.
type Interpolator interface {
	Interpolate(ctx context.Context, ds *SQLDatasource, query *sqlutil.Query, rawJSON json.RawMessage) (string, error)
}

// DefaultInterpolator is the implementation used when SQLDatasource.Interpolator
// is nil. It delegates directly to sqlutil.Interpolate for the legacy
// sqlutil.MacroFunc macros registered via the driver's Macros() method —
// byte-for-byte equivalent to the pre-extension default behaviour.
type DefaultInterpolator struct{}

// Interpolate implements Interpolator.
func (DefaultInterpolator) Interpolate(_ context.Context, ds *SQLDatasource, query *sqlutil.Query, _ json.RawMessage) (string, error) {
	var macros sqlutil.Macros
	if ds != nil {
		macros = ds.driver().Macros()
	}
	return sqlutil.Interpolate(query, macros)
}

// interpolate is the datasource-level entry point that resolves a nil
// Interpolator field to DefaultInterpolator and forwards the call. The
// internal QueryData path uses this; plugins that have already migrated
// can also call ds.Interpolator directly.
func (ds *SQLDatasource) interpolate(ctx context.Context, query *sqlutil.Query, rawJSON json.RawMessage) (string, error) {
	interp := ds.Interpolator
	if interp == nil {
		interp = DefaultInterpolator{}
	}
	return interp.Interpolate(ctx, ds, query, rawJSON)
}
