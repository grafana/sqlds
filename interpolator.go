package sqlds

import (
	"context"
	"encoding/json"

	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
)

// Interpolator produces the SQL that reaches the driver for a given query.
// NewDatasource installs a default that delegates to sqlutil.Interpolate over
// the driver's Macros(); plugins replace the pipeline by assigning their own
// func to SQLDatasource.Interpolator (for example an AST-aware rewriter or a
// macropro-backed handler).
//
// rawJSON is the unparsed query JSON from the request. sqlutil.Query only
// parses its fixed fields and drops the rest, so rawJSON is the channel for
// carrying plugin-defined macro context across the call.
//
// Implementations MUST be safe for concurrent use across queries.
type Interpolator func(ctx context.Context, query *sqlutil.Query, rawJSON json.RawMessage) (string, error)

// defaultInterpolator returns the Interpolator installed by NewDatasource. It
// delegates to sqlutil.Interpolate for the legacy sqlutil.MacroFunc macros
// registered via the driver's Macros() method — byte-for-byte equivalent to
// the pre-extension default behaviour. It closes over ds so the func signature
// itself need not expose the datasource.
func defaultInterpolator(ds *SQLDatasource) Interpolator {
	return func(_ context.Context, query *sqlutil.Query, _ json.RawMessage) (string, error) {
		var macros sqlutil.Macros
		if ds != nil {
			macros = ds.driver().Macros()
		}
		return sqlutil.Interpolate(query, macros)
	}
}

// interpolate is the datasource-level entry point used by the QueryData path.
// NewDatasource always sets ds.Interpolator, but interpolate also resolves a
// nil field (e.g. a zero-value SQLDatasource constructed without NewDatasource)
// to the default so the field stays safely optional.
func (ds *SQLDatasource) interpolate(ctx context.Context, query *sqlutil.Query, rawJSON json.RawMessage) (string, error) {
	interp := ds.Interpolator
	if interp == nil {
		interp = defaultInterpolator(ds)
	}
	return interp(ctx, query, rawJSON)
}
