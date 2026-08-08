package sqlds_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
	"github.com/stretchr/testify/require"

	"github.com/grafana/sqlds/v5"
	"github.com/grafana/sqlds/v5/diagnostics"
	"github.com/grafana/sqlds/v5/test"
)

// twoRowTable is a small result set used to assert that a capture reports what
// the datasource actually returned, not just what was asked of it.
func twoRowTable() test.Data {
	return test.Data{
		Cols: []test.Column{
			{Name: "name", DataType: "TEXT", Kind: ""},
			{Name: "age", DataType: "INTEGER", Kind: int64(0)},
		},
		Rows: [][]any{
			{"ada", int64(36)},
			{"alan", int64(41)},
		},
	}
}

func queryDataRequest(settings backend.DataSourceInstanceSettings, rawSQL string) *backend.QueryDataRequest {
	return &backend.QueryDataRequest{
		PluginContext: backend.PluginContext{DataSourceInstanceSettings: &settings},
		Queries: []backend.DataQuery{
			{
				RefID: "A",
				JSON:  []byte(fmt.Sprintf(`{ "rawSql": %q, "format": %d }`, rawSQL, sqlutil.FormatOptionTable)),
			},
		},
	}
}

// TestQueryData_CaptureThroughPublicAPI drives a query the way Grafana does, to
// prove the seam works end to end rather than only at the DBQuery level: a host
// installs a Recorder on the context, and the interpolated SQL plus the shape of
// the result come back out.
func TestQueryData_CaptureThroughPublicAPI(t *testing.T) {
	id := "diagnostics-capture-success"
	driver, _ := test.NewDriver(id, twoRowTable(), nil, test.DriverOpts{}, nil)
	ds := sqlds.NewDatasource(driver)

	settings := backend.DataSourceInstanceSettings{
		UID:      id,
		Name:     "my-warehouse",
		Type:     "grafana-snowflake-datasource",
		JSONData: []byte("{}"),
	}
	_, err := ds.NewDatasource(context.Background(), settings)
	require.NoError(t, err)

	rec := diagnostics.NewSliceRecorder(0)
	ctx := diagnostics.WithRecorder(context.Background(), rec)

	resp, err := ds.QueryData(ctx, queryDataRequest(settings, "SELECT name, age FROM people"))
	require.NoError(t, err)
	require.NoError(t, resp.Responses["A"].Error)
	require.Len(t, resp.Responses["A"].Frames, 1)

	got := rec.Interactions()
	require.Len(t, got, 1)
	require.Equal(t, diagnostics.KindSQLQuery, got[0].Kind)
	require.Equal(t, "SELECT name, age FROM people", got[0].Statement)
	require.Equal(t, "A", got[0].RefID)
	require.Equal(t, id, got[0].DatasourceUID)
	require.Equal(t, "my-warehouse", got[0].DatasourceName)
	require.Equal(t, "grafana-snowflake-datasource", got[0].DatasourceType)
	require.Empty(t, got[0].Err)
	require.Equal(t, 1, got[0].FrameCount)
	require.Equal(t, 2, got[0].RowCount, "the capture reports the rows the datasource returned")
	require.False(t, got[0].StatementTruncated)
}

// TestQueryData_CaptureRecordsInterpolatedSQL is the point of the whole exercise:
// the bundle needs the SQL the database saw, not the SQL the panel stored. A
// macro in the query text is resolved before it reaches the driver, and the
// capture must show the resolved form.
func TestQueryData_CaptureRecordsInterpolatedSQL(t *testing.T) {
	id := "diagnostics-capture-macros"
	macros := sqlds.Macros{
		"customTable": func(_ *sqlutil.Query, _ []string) (string, error) { return "people_v2", nil },
	}
	driver, _ := test.NewDriver(id, twoRowTable(), nil, test.DriverOpts{}, macros)
	ds := sqlds.NewDatasource(driver)

	settings := backend.DataSourceInstanceSettings{UID: id, Name: "n", Type: "t", JSONData: []byte("{}")}
	_, err := ds.NewDatasource(context.Background(), settings)
	require.NoError(t, err)

	rec := diagnostics.NewSliceRecorder(0)
	ctx := diagnostics.WithRecorder(context.Background(), rec)

	_, err = ds.QueryData(ctx, queryDataRequest(settings, "SELECT name FROM $__customTable()"))
	require.NoError(t, err)

	got := rec.Interactions()
	require.Len(t, got, 1)
	require.Equal(t, "SELECT name FROM people_v2", got[0].Statement,
		"the capture must show the SQL the database saw, with macros resolved")
}

// TestQueryData_CaptureOff_ChangesNothing pins the backward-compatibility
// contract: without a Recorder the response must be identical to one produced by
// a build that has no capture code at all.
func TestQueryData_CaptureOff_ChangesNothing(t *testing.T) {
	id := "diagnostics-capture-off"
	driver, _ := test.NewDriver(id, twoRowTable(), nil, test.DriverOpts{}, nil)
	ds := sqlds.NewDatasource(driver)

	settings := backend.DataSourceInstanceSettings{UID: id, Name: "n", Type: "t", JSONData: []byte("{}")}
	_, err := ds.NewDatasource(context.Background(), settings)
	require.NoError(t, err)

	resp, err := ds.QueryData(context.Background(), queryDataRequest(settings, "SELECT name, age FROM people"))
	require.NoError(t, err)

	dr := resp.Responses["A"]
	require.NoError(t, dr.Error)
	require.Len(t, dr.Frames, 1)
	rowLen, err := dr.Frames[0].RowLen()
	require.NoError(t, err)
	require.Equal(t, 2, rowLen)
	require.Len(t, resp.Responses, 1, "capture must not add responses when it is off")
}

// TestQueryData_CaptureRecordsDownstreamFailure covers the case the diagnostics
// bundle exists for: the query failed at the database and the engineer needs to
// see both the statement and the failure.
func TestQueryData_CaptureRecordsDownstreamFailure(t *testing.T) {
	id := "diagnostics-capture-failure"
	driver, _ := test.NewDriver(id, twoRowTable(), nil, test.DriverOpts{
		QueryError: fmt.Errorf("relation \"people\" does not exist"),
	}, nil)
	ds := sqlds.NewDatasource(driver)

	settings := backend.DataSourceInstanceSettings{UID: id, Name: "n", Type: "t", JSONData: []byte("{}")}
	_, err := ds.NewDatasource(context.Background(), settings)
	require.NoError(t, err)

	rec := diagnostics.NewSliceRecorder(0)
	ctx := diagnostics.WithRecorder(context.Background(), rec)

	resp, err := ds.QueryData(ctx, queryDataRequest(settings, "SELECT name FROM people"))
	require.NoError(t, err)
	require.Error(t, resp.Responses["A"].Error)

	got := rec.Interactions()
	require.GreaterOrEqual(t, len(got), 1)
	require.Equal(t, "SELECT name FROM people", got[0].Statement)
	require.Contains(t, got[0].Err, "does not exist")
}
