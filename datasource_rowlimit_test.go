package sqlds_test

import (
	"context"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/sqlds/v4"
	"github.com/grafana/sqlds/v4/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockDriver struct {
	sqlds.SQLMock
	rowLimit int64
}

func (d *mockDriver) Settings(ctx context.Context, settings backend.DataSourceInstanceSettings) sqlds.DriverSettings {
	ds := d.SQLMock.Settings(ctx, settings)
	ds.RowLimit = d.rowLimit
	return ds
}

func TestRowLimitFromConfig(t *testing.T) {
	// Create a mock config using the proper API
	mockConfig := backend.NewGrafanaCfg(map[string]string{
		"GF_SQL_ROW_LIMIT":                         "200",
		"GF_SQL_MAX_OPEN_CONNS_DEFAULT":            "10",
		"GF_SQL_MAX_IDLE_CONNS_DEFAULT":            "5",
		"GF_SQL_MAX_CONN_LIFETIME_SECONDS_DEFAULT": "3600",
	})

	// Create context with config
	ctx := backend.WithGrafanaConfig(context.Background(), mockConfig)

	// Create datasource with row limit enabled
	driver := &mockDriver{}
	ds := sqlds.NewDatasource(driver)
	ds.EnableRowLimit = true

	// Create settings and initialize datasource
	settings := backend.DataSourceInstanceSettings{UID: "rowlimit-config", JSONData: []byte("{}")}
	instance, err := ds.NewDatasource(ctx, settings)
	require.NoError(t, err)

	// Verify row limit was set correctly from config
	sqlDS, ok := instance.(*sqlds.SQLDatasource)
	require.True(t, ok)
	assert.Equal(t, int64(200), sqlDS.GetRowLimit())
}

func TestRowLimitFromDriverSettings(t *testing.T) {
	// Create datasource with driver that has row limit
	driver := &mockDriver{rowLimit: 300}
	ds := sqlds.NewDatasource(driver)
	ds.EnableRowLimit = true

	// Create settings and initialize datasource
	settings := backend.DataSourceInstanceSettings{UID: "rowlimit-driver", JSONData: []byte("{}")}
	instance, err := ds.NewDatasource(context.Background(), settings)
	require.NoError(t, err)

	// Verify driver settings row limit was used
	sqlDS, ok := instance.(*sqlds.SQLDatasource)
	require.True(t, ok)
	assert.Equal(t, int64(300), sqlDS.GetRowLimit())
}

func TestRowLimitPrecedence(t *testing.T) {
	// Create a mock config using the proper API
	mockConfig := backend.NewGrafanaCfg(map[string]string{
		"dataproxy.row_limit":                      "200",
		"GF_SQL_MAX_OPEN_CONNS_DEFAULT":            "10",
		"GF_SQL_MAX_IDLE_CONNS_DEFAULT":            "5",
		"GF_SQL_MAX_CONN_LIFETIME_SECONDS_DEFAULT": "3600",
	})

	// Create context with config
	ctx := backend.WithGrafanaConfig(context.Background(), mockConfig)

	// Create datasource with driver that has row limit
	driver := &mockDriver{rowLimit: 300}
	ds := sqlds.NewDatasource(driver)
	ds.EnableRowLimit = true

	// Create settings and initialize datasource
	settings := backend.DataSourceInstanceSettings{UID: "rowlimit-precedence", JSONData: []byte("{}")}
	instance, err := ds.NewDatasource(ctx, settings)
	require.NoError(t, err)

	// Verify driver settings take precedence over config
	sqlDS, ok := instance.(*sqlds.SQLDatasource)
	require.True(t, ok)
	assert.Equal(t, int64(300), sqlDS.GetRowLimit())
}

func TestRowLimitDisabled(t *testing.T) {
	// Create a mock config using the proper API
	mockConfig := backend.NewGrafanaCfg(map[string]string{
		"GF_SQL_ROW_LIMIT":                         "200",
		"GF_SQL_MAX_OPEN_CONNS_DEFAULT":            "10",
		"GF_SQL_MAX_IDLE_CONNS_DEFAULT":            "5",
		"GF_SQL_MAX_CONN_LIFETIME_SECONDS_DEFAULT": "3600",
	})

	// Create context with config
	ctx := backend.WithGrafanaConfig(context.Background(), mockConfig)

	// Create datasource with row limit disabled
	driver := &mockDriver{}
	ds := sqlds.NewDatasource(driver)
	ds.EnableRowLimit = false

	// Create settings and initialize datasource
	settings := backend.DataSourceInstanceSettings{UID: "rowlimit-disabled", JSONData: []byte("{}")}
	instance, err := ds.NewDatasource(ctx, settings)
	require.NoError(t, err)

	// Verify default row limit is used when feature is disabled
	sqlDS, ok := instance.(*sqlds.SQLDatasource)
	require.True(t, ok)
	assert.Equal(t, int64(-1), sqlDS.GetRowLimit())
}

func TestRowLimitDefault(t *testing.T) {
	// Create a mock config using the proper API
	mockConfig := backend.NewGrafanaCfg(map[string]string{})

	// Create context with config
	ctx := backend.WithGrafanaConfig(context.Background(), mockConfig)

	// Create datasource with row limit disabled
	driver := &mockDriver{}
	ds := sqlds.NewDatasource(driver)

	// Create settings and initialize datasource
	settings := backend.DataSourceInstanceSettings{UID: "rowlimit-disabled", JSONData: []byte("{}")}
	instance, err := ds.NewDatasource(ctx, settings)
	require.NoError(t, err)

	// Verify default row limit is used when feature is disabled
	sqlDS, ok := instance.(*sqlds.SQLDatasource)
	require.True(t, ok)
	assert.Equal(t, int64(-1), sqlDS.GetRowLimit())
}

func TestSetDefaultRowLimit(t *testing.T) {
	// Create datasource
	driver := &mockDriver{}
	ds := sqlds.NewDatasource(driver)

	// Initialize datasource
	settings := backend.DataSourceInstanceSettings{UID: "rowlimit-set", JSONData: []byte("{}")}
	instance, err := ds.NewDatasource(context.Background(), settings)
	require.NoError(t, err)

	// Cast to SQLDatasource
	sqlDS, ok := instance.(*sqlds.SQLDatasource)
	require.True(t, ok)

	// Set row limit
	sqlDS.SetDefaultRowLimit(500)

	// Verify row limit was set correctly
	assert.Equal(t, int64(500), sqlDS.GetRowLimit())
	assert.True(t, sqlDS.EnableRowLimit)
}

func TestRowLimitPassedToQuery(t *testing.T) {
	// Set up test data
	testData := test.Data{
		Cols: []test.Column{
			{Name: "id", DataType: "INTEGER", Kind: int64(0)},
			{Name: "name", DataType: "TEXT", Kind: ""},
		},
		Rows: [][]any{
			{int64(1), "test1"},
			{int64(2), "test2"},
			{int64(3), "test3"},
		},
	}

	// Create datasource with row limit
	driver, _ := test.NewDriver("rowlimit-query", testData, nil, test.DriverOpts{}, nil)
	ds := sqlds.NewDatasource(driver)

	// Create settings and initialize datasource
	settings := backend.DataSourceInstanceSettings{UID: "rowlimit-query", JSONData: []byte("{}")}
	instance, err := ds.NewDatasource(context.Background(), settings)
	require.NoError(t, err)

	// Cast to SQLDatasource and set row limit
	sqlDS, ok := instance.(*sqlds.SQLDatasource)
	require.True(t, ok)
	sqlDS.SetDefaultRowLimit(2)

	// Create query request
	req := &backend.QueryDataRequest{
		PluginContext: backend.PluginContext{
			DataSourceInstanceSettings: &settings,
		},
		Queries: []backend.DataQuery{
			{
				RefID: "A",
				JSON:  []byte(`{"rawSql": "SELECT * FROM test"}`),
			},
		},
	}

	// Execute query
	resp, err := sqlDS.QueryData(context.Background(), req)
	assert.NoError(t, err)

	// Verify response
	queryResp := resp.Responses["A"]
	assert.NoError(t, queryResp.Error)
	assert.NotNil(t, queryResp.Frames)
	assert.Len(t, queryResp.Frames, 1)

	// Verify row limit was applied (should only have 2 rows)
	frame := queryResp.Frames[0]
	rowCount, _ := frame.RowLen()
	assert.Equal(t, 2, rowCount)
}
