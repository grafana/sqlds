package sqlds_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
	"github.com/grafana/sqlds/v4"
	"github.com/grafana/sqlds/v4/test"
	"github.com/stretchr/testify/assert"
)

func Test_health_retries(t *testing.T) {
	opts := test.DriverOpts{
		ConnectError: errors.New("foo"),
	}
	cfg := `{ "timeout": 0, "retries": 5, "retryOn": ["foo"] }`
	req, handler, ds := healthRequest(t, "timeout", opts, cfg)

	_, err := ds.CheckHealth(context.Background(), &req)

	assert.Equal(t, nil, err)
	assert.Equal(t, 5, handler.State.ConnectAttempts)
}

func Test_query_retries(t *testing.T) {
	cfg := `{ "timeout": 0, "retries": 5, "retryOn": ["foo"] }`
	opts := test.DriverOpts{
		QueryError: errors.New("foo"),
	}

	req, handler, ds := queryRequest(t, "error", opts, cfg, nil)

	data, err := ds.QueryData(context.Background(), req)
	assert.Nil(t, err)
	assert.NotNil(t, data.Responses)
	assert.Equal(t, 6, handler.State.QueryAttempts)

	res := data.Responses["foo"]
	assert.NotNil(t, res.Error)
	assert.Equal(t, backend.ErrorSourceDownstream, res.ErrorSource)
}

func Test_query_apply_headers(t *testing.T) {
	var message []byte
	onConnect := func(msg []byte) {
		message = msg
	}

	opts := test.DriverOpts{
		QueryError:     errors.New("missing token"),
		QueryFailTimes: 1, // first check always fails since headers are not available on initial connect
		OnConnect:      onConnect,
	}
	cfg := `{ "timeout": 0, "retries": 1, "retryOn": ["missing token"], "forwardHeaders": true }`

	req, handler, ds := queryRequest(t, "headers", opts, cfg, nil)

	req.SetHTTPHeader("foo", "bar")

	data, err := ds.QueryData(context.Background(), req)
	assert.Nil(t, err)
	assert.NotNil(t, data.Responses)
	assert.Equal(t, 2, handler.State.QueryAttempts)

	assert.Contains(t, string(message), "bar")
}

func Test_check_health_with_headers(t *testing.T) {
	var message json.RawMessage
	onConnect := func(msg []byte) {
		message = msg
	}
	opts := test.DriverOpts{
		ConnectError:     errors.New("missing token"),
		ConnectFailTimes: 1, // first check always fails since headers are not available on initial connect
		OnConnect:        onConnect,
	}
	cfg := `{ "timeout": 0, "retries": 2, "retryOn": ["missing token"], "forwardHeaders": true }`
	req, handler, ds := healthRequest(t, "health-headers", opts, cfg)
	r := &req
	r.SetHTTPHeader("foo", "bar")

	res, err := ds.CheckHealth(context.Background(), r)
	assert.Nil(t, err)
	assert.Equal(t, "Data source is working", res.Message)
	assert.Equal(t, 2, handler.State.ConnectAttempts)
	assert.Contains(t, string(message), "bar")
}

func Test_no_errors(t *testing.T) {
	req, _, ds := healthRequest(t, "pass", test.DriverOpts{}, "{}")
	result, err := ds.CheckHealth(context.Background(), &req)

	assert.Nil(t, err)
	expected := "Data source is working"
	assert.Equal(t, expected, result.Message)
}

func Test_custom_marco_errors(t *testing.T) {
	cfg := `{ "timeout": 0, "retries": 0, "retryOn": ["foo"], query: "badArgumentCount" }`
	opts := test.DriverOpts{}

	badArgumentCountFunc := func(query *sqlds.Query, args []string) (string, error) {
		return "", sqlutil.ErrorBadArgumentCount
	}
	macros := sqlds.Macros{
		"foo": badArgumentCountFunc,
	}

	req, _, ds := queryRequest(t, "interpolate", opts, cfg, macros)

	req.Queries[0].JSON = []byte(`{ "rawSql": "select $__foo from bar;" }`)

	data, err := ds.QueryData(context.Background(), req)
	assert.Nil(t, err)

	res := data.Responses["foo"]
	assert.NotNil(t, res.Error)
	assert.Equal(t, backend.ErrorSourceDownstream, res.ErrorSource)
	assert.Contains(t, res.Error.Error(), sqlutil.ErrorBadArgumentCount.Error())
}

func Test_default_macro_errors(t *testing.T) {
	tests := []struct {
		name      string
		rawSQL    string
		wantError string
	}{
		{
			name:      "missing parameters",
			rawSQL:    "select * from bar where $__timeGroup(",
			wantError: "missing close bracket",
		},
		{
			name:      "incorrect argument count 0 - timeGroup",
			rawSQL:    "select * from bar where $__timeGroup()",
			wantError: sqlutil.ErrorBadArgumentCount.Error(),
		},
		{
			name:      "incorrect argument count 3 - timeGroup",
			rawSQL:    "select * from bar where $__timeGroup(1,2,3)",
			wantError: sqlutil.ErrorBadArgumentCount.Error(),
		},
		{
			name:      "incorrect argument count 0 - timeFilter",
			rawSQL:    "select * from bar where $__timeFilter",
			wantError: sqlutil.ErrorBadArgumentCount.Error(),
		},
		{
			name:      "incorrect argument count 3 - timeFilter",
			rawSQL:    "select * from bar where $__timeFilter(1,2,3)",
			wantError: sqlutil.ErrorBadArgumentCount.Error(),
		},
		{
			name:      "incorrect argument count 0 - timeFrom",
			rawSQL:    "select * from bar where $__timeFrom",
			wantError: sqlutil.ErrorBadArgumentCount.Error(),
		},
		{
			name:      "incorrect argument count 3 - timeFrom",
			rawSQL:    "select * from bar where $__timeFrom(1,2,3)",
			wantError: sqlutil.ErrorBadArgumentCount.Error(),
		},
	}

	// Common test configuration
	cfg := `{ "timeout": 0, "retries": 0, "retryOn": ["foo"], query: "badArgumentCount" }`
	opts := test.DriverOpts{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup request
			req, _, ds := queryRequest(t, "interpolate", opts, cfg, nil)
			req.Queries[0].JSON = []byte(fmt.Sprintf(`{ "rawSql": "%s" }`, tt.rawSQL))

			// Execute query
			data, err := ds.QueryData(context.Background(), req)
			assert.Nil(t, err)

			// Verify response
			res := data.Responses["foo"]
			assert.NotNil(t, res.Error)
			assert.Equal(t, backend.ErrorSourceDownstream, res.ErrorSource)
			assert.Contains(t, res.Error.Error(), tt.wantError)
		})
	}
}

func queryRequest(t *testing.T, name string, opts test.DriverOpts, cfg string, marcos sqlds.Macros) (*backend.QueryDataRequest, *test.SqlHandler, *sqlds.SQLDatasource) {
	driver, handler := test.NewDriver(name, test.Data{}, nil, opts, marcos)
	ds := sqlds.NewDatasource(driver)

	req, settings := setupQueryRequest(name, cfg)

	_, err := ds.NewDatasource(context.Background(), settings)
	assert.Equal(t, nil, err)
	return req, handler, ds
}

func setupQueryRequest(id string, cfg string) (*backend.QueryDataRequest, backend.DataSourceInstanceSettings) {
	s := backend.DataSourceInstanceSettings{UID: id, JSONData: []byte(cfg)}
	return &backend.QueryDataRequest{
		PluginContext: backend.PluginContext{
			DataSourceInstanceSettings: &s,
		},
		Queries: []backend.DataQuery{
			{
				RefID: "foo",
				JSON:  []byte(`{ "rawSql": "foo" }`),
			},
		},
	}, s
}

func healthRequest(t *testing.T, name string, opts test.DriverOpts, cfg string) (backend.CheckHealthRequest, *test.SqlHandler, *sqlds.SQLDatasource) {
	driver, handler := test.NewDriver(name, test.Data{}, nil, opts, nil)
	ds := sqlds.NewDatasource(driver)

	req, settings := setupHealthRequest(name, cfg)

	_, err := ds.NewDatasource(context.Background(), settings)
	assert.Equal(t, nil, err)
	return req, handler, ds
}

func setupHealthRequest(id string, cfg string) (backend.CheckHealthRequest, backend.DataSourceInstanceSettings) {
	settings := backend.DataSourceInstanceSettings{UID: id, JSONData: []byte(cfg)}
	req := backend.CheckHealthRequest{
		PluginContext: backend.PluginContext{
			DataSourceInstanceSettings: &settings,
		},
	}
	return req, settings
}
