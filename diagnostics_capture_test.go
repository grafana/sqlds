package sqlds

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
	"github.com/stretchr/testify/require"

	"github.com/grafana/sqlds/v5/diagnostics"
)

// panicRecorder is a Recorder that always panics, standing in for a buggy host
// implementation.
type panicRecorder struct{}

func (panicRecorder) Record(diagnostics.Interaction) { panic("boom") }

func TestRun_CaptureOff_IsPassthrough(t *testing.T) {
	// A context with no Recorder must produce exactly the result Run produced
	// before capture existed, and must not touch the connection more than once.
	conn := &testConnection{}
	settings := backend.DataSourceInstanceSettings{Name: "test"}
	query := &Query{RawSQL: "SELECT * FROM test", RefID: "A"}

	frames, err := NewQuery(conn, settings, []sqlutil.Converter{}, nil, defaultRowLimit).
		Run(context.Background(), query, nil)

	require.Error(t, err)
	require.True(t, errorsIsErrorQuery(err))
	require.NotNil(t, frames)
	require.Equal(t, 1, conn.QueryRunCount)
}

func TestRun_RecordsFailedQuery(t *testing.T) {
	// The failing query is the case a diagnostics bundle exists for, so it must
	// be recorded, with the error attached rather than swallowed.
	rec := diagnostics.NewSliceRecorder(0)
	ctx := diagnostics.WithRecorder(context.Background(), rec)

	conn := &testConnection{}
	settings := backend.DataSourceInstanceSettings{
		UID:  "abc123",
		Name: "my-postgres",
		Type: "grafana-postgresql-datasource",
	}
	query := &Query{RawSQL: "SELECT * FROM missing_table", RefID: "B"}

	_, err := NewQuery(conn, settings, []sqlutil.Converter{}, nil, defaultRowLimit).
		Run(ctx, query, nil)
	require.Error(t, err)

	got := rec.Interactions()
	require.Len(t, got, 1)
	require.Equal(t, diagnostics.KindSQLQuery, got[0].Kind)
	require.Equal(t, "SELECT * FROM missing_table", got[0].Statement)
	require.Equal(t, "B", got[0].RefID)
	require.Equal(t, "abc123", got[0].DatasourceUID)
	require.Equal(t, "my-postgres", got[0].DatasourceName)
	require.Equal(t, "grafana-postgresql-datasource", got[0].DatasourceType)
	require.NotEmpty(t, got[0].Err, "the query error must be recorded")
	require.False(t, got[0].StartedAt.IsZero())
}

func TestRun_RecordsNamedArgs(t *testing.T) {
	// grafana-aws-sdk fetches Athena and Redshift results by calling
	// NewQuery(...).Run(...) directly, passing the async queryID as a named
	// argument. Recording in Run is what makes that path visible, so assert the
	// shape it actually uses.
	rec := diagnostics.NewSliceRecorder(0)
	ctx := diagnostics.WithRecorder(context.Background(), rec)

	conn := &testConnection{}
	settings := backend.DataSourceInstanceSettings{Name: "athena"}
	query := &Query{RawSQL: "SELECT * FROM logs", RefID: "A"}

	_, err := NewQuery(conn, settings, []sqlutil.Converter{}, nil, defaultRowLimit).
		Run(ctx, query, nil, sql.NamedArg{Name: "queryID", Value: "q-4711"})
	require.Error(t, err)

	got := rec.Interactions()
	require.Len(t, got, 1)
	require.Equal(t, []string{"queryID=q-4711"}, got[0].Args)
	require.False(t, got[0].ArgsTruncated)
}

func TestRun_RecorderPanic_DoesNotAffectTheQuery(t *testing.T) {
	// Capture is a side channel. A broken host Recorder must not change what the
	// panel receives, and must not escape as a panic.
	ctx := diagnostics.WithRecorder(context.Background(), panicRecorder{})

	conn := &testConnection{}
	settings := backend.DataSourceInstanceSettings{Name: "test"}
	query := &Query{RawSQL: "SELECT 1", RefID: "A"}

	require.NotPanics(t, func() {
		frames, err := NewQuery(conn, settings, []sqlutil.Converter{}, nil, defaultRowLimit).
			Run(ctx, query, nil)
		require.Error(t, err)
		require.True(t, errorsIsErrorQuery(err))
		require.NotNil(t, frames)
	})
}

func TestTruncateStatement(t *testing.T) {
	t.Run("statement under the cap is untouched", func(t *testing.T) {
		got, truncated := truncateStatement("SELECT 1")
		require.Equal(t, "SELECT 1", got)
		require.False(t, truncated)
	})

	t.Run("statement over the cap is cut and flagged", func(t *testing.T) {
		long := strings.Repeat("x", diagnostics.MaxStatementBytes+100)
		got, truncated := truncateStatement(long)
		require.Len(t, got, diagnostics.MaxStatementBytes)
		require.True(t, truncated)
	})
}

func TestRenderArgs(t *testing.T) {
	t.Run("no args renders as nil", func(t *testing.T) {
		got, truncated := renderArgs(nil)
		require.Nil(t, got)
		require.False(t, truncated)
	})

	t.Run("positional args render by value", func(t *testing.T) {
		got, truncated := renderArgs([]interface{}{1, "two", 3.5, nil})
		require.Equal(t, []string{"1", "two", "3.5", "<nil>"}, got)
		require.False(t, truncated)
	})

	t.Run("args over the budget yield a flagged prefix", func(t *testing.T) {
		// Two args that each nearly fill the budget: the first fits, the second
		// cannot, so the result is a prefix and the caller is told so.
		big := strings.Repeat("y", diagnostics.MaxArgsBytes-10)
		got, truncated := renderArgs([]interface{}{big, big})
		require.Len(t, got, 1)
		require.True(t, truncated)
	})
}

func TestSummarizeFrames(t *testing.T) {
	t.Run("counts rows across frames", func(t *testing.T) {
		f1 := data.NewFrame("a", data.NewField("v", nil, []int64{1, 2, 3}))
		f2 := data.NewFrame("b", data.NewField("v", nil, []int64{4, 5}))
		rows, frames := summarizeFrames(data.Frames{f1, f2})
		require.Equal(t, 5, rows)
		require.Equal(t, 2, frames)
	})

	t.Run("nil frames are skipped", func(t *testing.T) {
		f1 := data.NewFrame("a", data.NewField("v", nil, []int64{1}))
		rows, frames := summarizeFrames(data.Frames{f1, nil})
		require.Equal(t, 1, rows)
		require.Equal(t, 2, frames, "frame count reflects the response as returned")
	})

	t.Run("inconsistent field lengths report an unknown row count", func(t *testing.T) {
		// A partial total would misrepresent the response to someone comparing
		// it against what the database returned, so report -1 instead.
		bad := data.NewFrame("bad",
			data.NewField("a", nil, []int64{1, 2}),
			data.NewField("b", nil, []int64{1}),
		)
		rows, frames := summarizeFrames(data.Frames{bad})
		require.Equal(t, -1, rows)
		require.Equal(t, 1, frames)
	})

	t.Run("no frames", func(t *testing.T) {
		rows, frames := summarizeFrames(nil)
		require.Equal(t, 0, rows)
		require.Equal(t, 0, frames)
	})
}

func TestRecordingConnection(t *testing.T) {
	settings := backend.DataSourceInstanceSettings{UID: "u1", Name: "n1", Type: "t1"}

	t.Run("without a recorder the connection is returned unchanged", func(t *testing.T) {
		conn := &testConnection{}
		got := RecordingConnection(context.Background(), conn, settings)
		require.Same(t, conn, got)
	})

	t.Run("a nil connection stays nil", func(t *testing.T) {
		ctx := diagnostics.WithRecorder(context.Background(), diagnostics.NewSliceRecorder(0))
		require.Nil(t, RecordingConnection(ctx, nil, settings))
	})

	t.Run("records the statement and preserves the result", func(t *testing.T) {
		rec := diagnostics.NewSliceRecorder(0)
		ctx := diagnostics.WithRecorder(context.Background(), rec)
		conn := &testConnection{}

		wrapped := RecordingConnection(ctx, conn, settings)
		rows, err := wrapped.QueryContext(ctx, "SELECT table_name FROM information_schema.tables")

		require.Error(t, err, "the wrapper must return the underlying error verbatim")
		require.Nil(t, rows)
		require.Equal(t, 1, conn.QueryRunCount, "the wrapper must not re-run the query")

		got := rec.Interactions()
		require.Len(t, got, 1)
		require.Equal(t, "SELECT table_name FROM information_schema.tables", got[0].Statement)
		require.Equal(t, "u1", got[0].DatasourceUID)
		require.NotEmpty(t, got[0].Err)
		require.Equal(t, -1, got[0].RowCount, "rows are streamed, so the row count is unknown here")
	})
}

// errorsIsErrorQuery keeps the passthrough assertions readable; Run wraps query
// failures in ErrorQuery so the datasource retry logic can detect them.
func errorsIsErrorQuery(err error) bool {
	return err != nil && strings.Contains(err.Error(), ErrorQuery.Error())
}
