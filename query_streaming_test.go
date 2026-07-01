package sqlds

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
	"github.com/stretchr/testify/require"
)

// The fake driver below yields a *sql.Rows whose Next fails, simulating the
// connection pool being torn down (see Connector.Dispose / Reconnect) while a
// query is being streamed. Unlike a failure from QueryContext, this error
// surfaces during row iteration in convertRowsToFrames / getFrames.
//
// DBQuery.Run does not special-case the closed pool on this path: it relies on
// sqlutil.FrameFromRows wrapping rows.Err() in backend.DownstreamError, which
// convertRowsToFrames then honours via backend.IsDownstreamHTTPError. This test
// pins that behaviour so a future change to either layer can't silently start
// counting a torn-down pool against the plugin's error budget.

type streamErrConnector struct{ rowErr error }

func (c streamErrConnector) Connect(context.Context) (driver.Conn, error) {
	return &streamErrConn{c.rowErr}, nil
}
func (c streamErrConnector) Driver() driver.Driver { return streamErrDriver{c.rowErr} }

type streamErrDriver struct{ rowErr error }

func (d streamErrDriver) Open(string) (driver.Conn, error) { return &streamErrConn{d.rowErr}, nil }

type streamErrConn struct{ rowErr error }

func (c *streamErrConn) Prepare(string) (driver.Stmt, error) { return &streamErrStmt{c.rowErr}, nil }
func (c *streamErrConn) Close() error                        { return nil }
func (c *streamErrConn) Begin() (driver.Tx, error)           { return nil, errors.New("not supported") }
func (c *streamErrConn) QueryContext(_ context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	return &streamErrRows{rowErr: c.rowErr}, nil
}

type streamErrStmt struct{ rowErr error }

func (s *streamErrStmt) Close() error  { return nil }
func (s *streamErrStmt) NumInput() int { return -1 }
func (s *streamErrStmt) Exec([]driver.Value) (driver.Result, error) {
	return nil, errors.New("not supported")
}
func (s *streamErrStmt) Query([]driver.Value) (driver.Rows, error) {
	return &streamErrRows{rowErr: s.rowErr}, nil
}

type streamErrRows struct{ rowErr error }

func (r *streamErrRows) Columns() []string           { return []string{"col"} }
func (r *streamErrRows) Close() error                { return nil }
func (r *streamErrRows) Next(_ []driver.Value) error { return r.rowErr } // pool closed mid-stream

func TestRun_ConnectionClosedDuringStreamingIsDownstream(t *testing.T) {
	settings := backend.DataSourceInstanceSettings{Name: "test"}
	query := &Query{RawSQL: "SELECT * FROM test", RefID: "A"}

	cases := []struct {
		name string
		err  error
	}{
		{"sql: database is closed", errors.New("sql: database is closed")},
		{"sql.ErrConnDone", sql.ErrConnDone},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := sql.OpenDB(streamErrConnector{rowErr: tc.err})
			defer db.Close()

			dbQuery := NewQuery(db, settings, []sqlutil.Converter{}, nil, defaultRowLimit)

			// A driver mutator that would otherwise misclassify as plugin must
			// not flip the classification: the streaming path is already
			// downstream before the mutator is consulted.
			_, err := dbQuery.Run(context.Background(), query, &mockErrorMutator{shouldMutate: false})

			require.Error(t, err)
			var errWithSource backend.ErrorWithSource
			require.True(t, errors.As(err, &errWithSource), "should implement ErrorWithSource")
			require.Equal(t, backend.ErrorSourceDownstream, errWithSource.ErrorSource(),
				"pool closed during streaming must be classified as downstream")
		})
	}
}
