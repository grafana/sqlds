package sqlds

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"

	"github.com/grafana/sqlds/v5/diagnostics"
)

// recordInteraction hands one diagnostics.Interaction to rec, describing a
// statement DBQuery.Run just executed. It is only called when capture is
// active. It must not alter the query's outcome, so every value it reports is
// derived defensively and any problem gathering one is reported in the
// Interaction instead of surfacing as an error.
func (q *DBQuery) recordInteraction(rec diagnostics.Recorder, start time.Time, query *Query, args []interface{}, frames data.Frames, results *diagnostics.ResultCapture, err error) {
	if rec == nil || query == nil {
		return
	}

	statement, statementTruncated := truncateStatement(query.RawSQL)
	renderedArgs, argsTruncated := renderArgs(args)
	rowCount, frameCount := summarizeFrames(frames)
	// A nil ResultCapture reads as "nothing was collected" rather than "no rows": the
	// query may have failed before the driver returned anything, and Result reports the
	// total as -1 so a consumer can tell the two apart.
	captured := results.Result()

	interaction := diagnostics.Interaction{
		Kind:                diagnostics.KindSQLQuery,
		StartedAt:           start,
		Duration:            time.Since(start),
		DatasourceUID:       q.Settings.UID,
		DatasourceType:      q.Settings.Type,
		DatasourceName:      q.DSName,
		RefID:               query.RefID,
		Statement:           statement,
		StatementTruncated:  statementTruncated,
		Args:                renderedArgs,
		ArgsTruncated:       argsTruncated,
		FrameCount:          frameCount,
		RowCount:            rowCount,
		ResultColumns:       captured.Columns,
		ResultRows:          captured.Rows,
		ResultRowsTruncated: captured.Truncated,
		ResultTotalRows:     captured.TotalRows,
	}
	if err != nil {
		interaction.Err = err.Error()
	}

	safeRecord(rec, interaction)
}

// safeRecord hands an Interaction to rec, absorbing a panic from it. A Recorder
// is supplied by the host, so treat a panic in it as the host's bug rather than
// the user's failed query: capture is a diagnostic side channel and must never
// be able to take down a panel. Recovering in a dedicated function keeps the
// recovery away from the query's own return values.
func safeRecord(rec diagnostics.Recorder, interaction diagnostics.Interaction) {
	defer func() {
		if r := recover(); r != nil {
			backend.Logger.Warn("sqlds diagnostics: recorder panicked; dropping interaction", "panic", fmt.Sprintf("%v", r))
		}
	}()
	rec.Record(interaction)
}

// truncateStatement bounds a statement to diagnostics.MaxStatementBytes,
// reporting whether it had to cut. Truncation is on a byte boundary; the
// statement is evidence to read, not to re-execute, so a split multi-byte rune
// at the tail is acceptable where a size guarantee is not.
func truncateStatement(sqlText string) (string, bool) {
	if len(sqlText) <= diagnostics.MaxStatementBytes {
		return sqlText, false
	}
	return sqlText[:diagnostics.MaxStatementBytes], true
}

// renderArgs renders bind arguments for display, stopping once the aggregate
// size would exceed diagnostics.MaxArgsBytes. Named arguments keep their name
// so that a capture can be matched against a statement using named parameters,
// which is how grafana-aws-sdk passes an Athena or Redshift queryID.
//
// Values are rendered, not redacted: bind arguments are exactly where a
// wrong-data investigation looks first. They are also where customer data
// lives, which is why the Recorder, not sqlds, owns redaction and retention.
func renderArgs(args []interface{}) ([]string, bool) {
	if len(args) == 0 {
		return nil, false
	}
	out := make([]string, 0, len(args))
	budget := diagnostics.MaxArgsBytes
	for _, a := range args {
		var rendered string
		if named, ok := a.(sql.NamedArg); ok {
			rendered = fmt.Sprintf("%s=%v", named.Name, named.Value)
		} else {
			rendered = fmt.Sprintf("%v", a)
		}
		if len(rendered) > budget {
			return out, true
		}
		budget -= len(rendered)
		out = append(out, rendered)
	}
	return out, false
}

// summarizeFrames counts the rows and frames the query produced. It reports a
// row count of -1 when the frames have inconsistent field lengths, since a
// partial total would misrepresent the response to someone comparing it against
// what the database returned.
func summarizeFrames(frames data.Frames) (rowCount, frameCount int) {
	frameCount = len(frames)
	for _, f := range frames {
		if f == nil {
			continue
		}
		n, err := f.RowLen()
		if err != nil {
			return -1, frameCount
		}
		rowCount += n
	}
	return rowCount, frameCount
}

// RecordingConnection wraps conn so that every statement executed through it is
// reported to the Recorder in ctx, using the given datasource settings for
// identity. When ctx carries no Recorder it returns conn unchanged, so callers
// can wrap unconditionally.
//
// DBQuery.Run already records the queries it runs, so this is for the paths
// that do not go through it: a plugin that takes a connection via
// SQLDatasource.GetDBFromQuery and runs its own SQL (schema and completion
// lookups, custom resource handlers) would otherwise be invisible in a capture.
func RecordingConnection(ctx context.Context, conn Connection, settings backend.DataSourceInstanceSettings) Connection {
	rec, ok := diagnostics.RecorderFromContext(ctx)
	if !ok || conn == nil {
		return conn
	}
	return &recordingConnection{Connection: conn, rec: rec, settings: settings}
}

// recordingConnection is the Connection counterpart of the SDK's capturing
// http.RoundTripper: same statement in, same rows out, with the exchange
// reported on the side.
type recordingConnection struct {
	Connection
	rec      diagnostics.Recorder
	settings backend.DataSourceInstanceSettings
}

func (c *recordingConnection) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	start := time.Now()
	rows, err := c.Connection.QueryContext(ctx, query, args...)

	statement, statementTruncated := truncateStatement(query)
	renderedArgs, argsTruncated := renderArgs(args)
	interaction := diagnostics.Interaction{
		Kind:               diagnostics.KindSQLQuery,
		StartedAt:          start,
		Duration:           time.Since(start),
		DatasourceUID:      c.settings.UID,
		DatasourceType:     c.settings.Type,
		DatasourceName:     c.settings.Name,
		Statement:          statement,
		StatementTruncated: statementTruncated,
		Args:               renderedArgs,
		ArgsTruncated:      argsTruncated,
		// Rows are streamed and not consumed here, so this capture point cannot
		// report how many came back, nor what they held, without changing what the
		// caller receives. -1 says "not determined"; zero would claim an empty result.
		FrameCount:      0,
		RowCount:        -1,
		ResultTotalRows: -1,
	}
	if err != nil {
		interaction.Err = err.Error()
	}

	safeRecord(c.rec, interaction)

	return rows, err
}
