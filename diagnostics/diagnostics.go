// Package diagnostics is the capture seam sqlds uses to record what it actually
// sent to, and received from, a SQL datasource.
//
// It exists to close a gap in Grafana's on-demand datasource diagnostics: HTTP
// datasources contribute a `traffic.har` to the diagnostic bundle because the
// plugin SDK can wrap their `http.RoundTripper`, but SQL datasources speak their
// own protocols over a `database/sql` driver and so contribute nothing. A bundle
// for a SQL datasource therefore shows the frames the plugin returned with
// nothing to compare them against, which makes "the database returned the wrong
// rows" indistinguishable from "the plugin mangled correct rows".
//
// # Where the vocabulary lives
//
// The types below are the SDK's, re-exported here as aliases: an Interaction
// recorded by sqlds is the same value the SDK's HAR capture middleware installs
// a Recorder to collect, and the context they travel in is the SDK's context
// key. That is what makes the seam work end to end without sqlds and the SDK
// importing each other -- the SDK cannot import sqlds, so a vocabulary defined
// here could never be read back by the host that activates capture.
//
// The aliases are kept so that a plugin importing sqlds does not have to reach
// for a second module to implement a Recorder, and so this package remains the
// documented entry point for the SQL side of the seam.
//
// The seam is deliberately shaped like the SDK's HTTP capture middleware:
//
//   - Capture is off unless a Recorder is present in the context, and a nil or
//     absent Recorder is a pure pass-through. Nothing about query execution or
//     its results changes when capture is off.
//   - The Recorder is installed by whoever activated capture (the plugin SDK's
//     QueryData middleware, when Grafana sets X-Grafana-HAR-Capture), not by
//     sqlds itself.
//   - sqlds only produces Interactions. It does not decide how they are encoded
//     or returned to Grafana. Mapping an Interaction onto a HAR entry and
//     attaching it to the QueryDataResponse is the consumer's job, so that SQL
//     evidence and HTTP evidence end up in one document instead of racing for
//     the same reserved refID.
//
// # Sensitive data
//
// Interactions carry the interpolated SQL statement, its bind arguments and a
// summary of the returned data. All three can contain customer data and
// credentials. sqlds bounds their size (see MaxStatementBytes and MaxArgsBytes)
// but does not redact them; redaction is the Recorder's responsibility, and a
// Recorder must not be installed on a path where the result is not treated as
// sensitive.
package diagnostics

import (
	"context"

	"github.com/grafana/grafana-plugin-sdk-go/backend/querycapture"
)

// Interaction is one recorded exchange with a datasource. See
// querycapture.Interaction for the field documentation.
type Interaction = querycapture.Interaction

// Recorder receives Interactions. Implementations must be safe for concurrent
// use: sqlds runs the queries of one QueryDataRequest in parallel, one goroutine
// per refID, so a single Recorder is written to from several goroutines at once.
//
// Record must not block for long and must not panic. It runs inline on the query
// path, so a slow Recorder slows the user's query.
type Recorder = querycapture.Recorder

// Kind values for the capture points that feed a Recorder. sqlds only emits
// KindSQLQuery; the others are re-exported so consumers can switch on Kind
// across capture points without importing a second package.
const (
	// KindSQLQuery is a statement executed against a database/sql connection.
	KindSQLQuery = querycapture.KindSQLQuery
	// KindHTTP is an HTTP round trip, as captured by the SDK's HAR middleware.
	KindHTTP = querycapture.KindHTTP
)

// Size bounds applied by sqlds before handing an Interaction to a Recorder. A
// single pathological query (a large IN clause, a bulk INSERT) must not be able
// to grow the capture without limit, and the whole QueryDataResponse the capture
// rides back on has to fit inside the SDK's gRPC message size.
//
// These bound one Interaction. A Recorder is still responsible for bounding the
// total across interactions.
const (
	// MaxStatementBytes caps Interaction.Statement.
	MaxStatementBytes = querycapture.MaxStatementBytes
	// MaxArgsBytes caps the aggregate size of Interaction.Args.
	MaxArgsBytes = querycapture.MaxArgsBytes
)

// WithRecorder returns a context that activates capture for every sqlds query
// executed under it. Passing a nil Recorder returns ctx unchanged, so a caller
// can wire capture unconditionally and decide later whether to enable it.
//
// Hosts do not normally call this: the SDK's HAR capture middleware already
// installs a Recorder when Grafana asks for a capture. It is exported for tests
// and for a host that drives sqlds outside that middleware.
func WithRecorder(ctx context.Context, r Recorder) context.Context {
	return querycapture.WithRecorder(ctx, r)
}

// RecorderFromContext returns the Recorder activated for ctx, if any. The second
// result reports whether capture is on; when it is false the caller must behave
// exactly as it did before capture existed.
func RecorderFromContext(ctx context.Context) (Recorder, bool) {
	return querycapture.RecorderFromContext(ctx)
}
