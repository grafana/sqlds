package sqlds

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// longResultDriver is a minimal database/sql driver that serves one
// time-sorted long result (time, label, value) so getFrames can be exercised
// with real *sql.Rows.
type longResultDriver struct {
	timestamps, series int
}

func (d *longResultDriver) Open(string) (driver.Conn, error) { return &longResultConn{d: d}, nil }

type longResultConn struct{ d *longResultDriver }

func (c *longResultConn) Prepare(string) (driver.Stmt, error) { return &longResultStmt{d: c.d}, nil }
func (c *longResultConn) Close() error                        { return nil }
func (c *longResultConn) Begin() (driver.Tx, error)           { return nil, errors.New("not implemented") }

type longResultStmt struct{ d *longResultDriver }

func (s *longResultStmt) Close() error  { return nil }
func (s *longResultStmt) NumInput() int { return 0 }
func (s *longResultStmt) Exec([]driver.Value) (driver.Result, error) {
	return nil, errors.New("not implemented")
}
func (s *longResultStmt) Query([]driver.Value) (driver.Rows, error) {
	return &longResultRows{d: s.d}, nil
}

type longResultRows struct {
	d   *longResultDriver
	row int
}

func (r *longResultRows) Columns() []string { return []string{"time", "label", "value"} }
func (r *longResultRows) Close() error      { return nil }

func (r *longResultRows) ColumnTypeScanType(index int) reflect.Type {
	switch index {
	case 0:
		return reflect.TypeOf(time.Time{})
	case 1:
		return reflect.TypeOf("")
	default:
		return reflect.TypeOf(float64(0))
	}
}

func (r *longResultRows) Next(dest []driver.Value) error {
	if r.row >= r.d.timestamps*r.d.series {
		return io.EOF
	}
	ts := r.row / r.d.series
	s := r.row % r.d.series
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	dest[0] = base.Add(time.Duration(ts) * time.Second)
	dest[1] = fmt.Sprintf("series-%d", s)
	dest[2] = float64(s)
	r.row++
	return nil
}

func queryLongResult(t *testing.T, name string, timestamps, series int) *sql.Rows {
	t.Helper()
	sql.Register(name, &longResultDriver{timestamps: timestamps, series: series})
	db, err := sql.Open(name, "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	rows, err := db.Query("select long result")
	require.NoError(t, err)
	return rows
}

// TestGetFramesLongToWideCellLimit exercises the guard through getFrames
// with real sql.Rows: the time-series format must reject an over-budget long
// result with a downstream ErrorWideFrameTooLarge before pivoting, and a
// negative limit must disable the guard.
func TestGetFramesLongToWideCellLimit(t *testing.T) {
	query := &Query{RawSQL: "select long result", Format: FormatOptionTimeSeries}

	t.Run("over-budget results are rejected", func(t *testing.T) {
		// 4 timestamps x (1 + 3 series x 1 value) = 16 cells
		rows := queryLongResult(t, "sqlds-longtowide-over", 4, 3)
		_, err := getFrames(rows, -1, 0, 15, nil, nil, query)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrorWideFrameTooLarge))
		assert.True(t, backend.IsDownstreamError(err))
	})

	t.Run("a negative limit disables the guard", func(t *testing.T) {
		rows := queryLongResult(t, "sqlds-longtowide-disabled", 4, 3)
		frames, err := getFrames(rows, -1, 0, -1, nil, nil, query)
		require.NoError(t, err)
		require.Len(t, frames, 1)
		assert.Equal(t, data.TimeSeriesTypeWide, frames[0].TimeSeriesSchema().Type)
	})

	t.Run("under-budget results pivot normally", func(t *testing.T) {
		rows := queryLongResult(t, "sqlds-longtowide-under", 4, 3)
		frames, err := getFrames(rows, -1, 0, 16, nil, nil, query)
		require.NoError(t, err)
		require.Len(t, frames, 1)
		assert.Equal(t, data.TimeSeriesTypeWide, frames[0].TimeSeriesSchema().Type)
	})
}
