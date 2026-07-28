// Package diagnostics defines the capture seam sqlds uses to record what it
// actually sent to, and received from, a SQL datasource.
//
// It exists to close a gap in Grafana's on-demand datasource diagnostics: HTTP
// datasources contribute a `traffic.har` to the diagnostic bundle because the
// plugin SDK can wrap their `http.RoundTripper`, but SQL datasources speak
// their own protocols over a `database/sql` driver and so contribute nothing.
// A bundle for a SQL datasource therefore shows the frames the plugin returned
// with nothing to compare them against, which makes "the database returned the
// wrong rows" indistinguishable from "the plugin mangled correct rows".
//
// The seam is deliberately shaped like the SDK's HTTP capture middleware:
//
//   - Capture is off unless a Recorder is present in the context, and a nil or
//     absent Recorder is a pure pass-through. Nothing about query execution or
//     its results changes when capture is off.
//   - The Recorder is installed by whoever activated capture (the plugin SDK's
//     QueryData middleware, when Grafana sets X-Grafana-HAR-Capture), not by
//     sqlds itself.
//   - sqlds only produces Interactions. It does not decide how they are
//     encoded or returned to Grafana. Mapping an Interaction onto a HAR entry
//     and attaching it to the QueryDataResponse is the consumer's job, so that
//     SQL evidence and HTTP evidence end up in one document instead of racing
//     for the same reserved refID.
//
// # Sensitive data
//
// Interactions carry the interpolated SQL statement, its bind arguments and a
// summary of the returned data. All three can contain customer data and
// credentials. sqlds bounds their size (see MaxStatementBytes and
// MaxArgsBytes) but does not redact them; redaction is the Recorder's
// responsibility, and a Recorder must not be installed on a path where the
// result is not treated as sensitive.
package diagnostics

import (
	"context"
	"time"
)

// Interaction is one recorded exchange with a datasource. It is intentionally
// protocol-neutral: Kind identifies what was captured so that non-SQL capture
// points (an HTTP round trip, a MongoDB command) can feed the same Recorder.
type Interaction struct {
	// Kind identifies the capture point, e.g. KindSQLQuery.
	Kind string
	// StartedAt is when the call to the datasource began.
	StartedAt time.Time
	// Duration is the wall-clock time spent on the call, including converting
	// rows into frames.
	Duration time.Duration

	// DatasourceUID, DatasourceType and DatasourceName identify the datasource
	// instance, so a consumer can correlate an Interaction with the panel query
	// that produced it in a multi-datasource capture.
	DatasourceUID  string
	DatasourceType string
	DatasourceName string
	// RefID is the query's refID, as set by the panel query editor.
	RefID string

	// Statement is the SQL as it was handed to the driver: after macro
	// interpolation, which is the form the database actually saw. Truncated to
	// MaxStatementBytes; StatementTruncated reports whether that happened.
	Statement string
	// StatementTruncated reports whether Statement was cut to fit
	// MaxStatementBytes.
	StatementTruncated bool
	// Args are the bind arguments, rendered for display. Named arguments are
	// rendered as "name=value". Truncated to MaxArgsBytes in aggregate;
	// ArgsTruncated reports whether that happened.
	Args []string
	// ArgsTruncated reports whether Args was cut to fit MaxArgsBytes. When
	// true, the arguments present are a prefix of those actually sent.
	ArgsTruncated bool

	// FrameCount and RowCount summarise what the plugin returned. RowCount is
	// -1 when it could not be determined (frames with inconsistent field
	// lengths).
	FrameCount int
	RowCount   int

	// Err is the error the query returned, or empty on success. A recorded
	// failure is the most valuable case for diagnostics, so an Interaction is
	// recorded on the error path too.
	Err string
}

// Kind values for the capture points that feed a Recorder. sqlds only emits
// KindSQLQuery; the others are declared so that consumers can switch on Kind
// across capture points without redefining the vocabulary.
const (
	// KindSQLQuery is a statement executed against a database/sql connection.
	KindSQLQuery = "sql.query"
	// KindHTTP is an HTTP round trip, as captured by the SDK's HAR middleware.
	KindHTTP = "http"
)

// Size bounds applied by sqlds before handing an Interaction to a Recorder. A
// single pathological query (a large IN clause, a bulk INSERT) must not be able
// to grow the capture without limit, and the whole QueryDataResponse the
// capture rides back on has to fit inside the SDK's gRPC message size.
//
// These bound one Interaction. A Recorder is still responsible for bounding the
// total across interactions.
const (
	// MaxStatementBytes caps Interaction.Statement.
	MaxStatementBytes = 64 * 1024
	// MaxArgsBytes caps the aggregate size of Interaction.Args.
	MaxArgsBytes = 16 * 1024
)

type recorderContextKey struct{}

// Recorder receives Interactions. Implementations must be safe for concurrent
// use: sqlds runs the queries of one QueryDataRequest in parallel, one
// goroutine per refID, so a single Recorder is written to from several
// goroutines at once.
//
// Record must not block for long and must not panic. It runs inline on the
// query path, so a slow Recorder slows the user's query.
type Recorder interface {
	Record(Interaction)
}

// WithRecorder returns a context that activates capture for every sqlds query
// executed under it. Passing a nil Recorder returns ctx unchanged, so a caller
// can wire capture unconditionally and decide later whether to enable it.
func WithRecorder(ctx context.Context, r Recorder) context.Context {
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, recorderContextKey{}, r)
}

// RecorderFromContext returns the Recorder activated for ctx, if any. The
// second result reports whether capture is on; when it is false the caller must
// behave exactly as it did before capture existed.
func RecorderFromContext(ctx context.Context) (Recorder, bool) {
	if ctx == nil {
		return nil, false
	}
	r, ok := ctx.Value(recorderContextKey{}).(Recorder)
	if !ok || r == nil {
		return nil, false
	}
	return r, true
}
