package sqlds_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/sqlds/v3"
	"github.com/grafana/sqlds/v3/test"
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

	req, handler, ds := queryRequest(t, "error", opts, cfg)

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

	req, handler, ds := queryRequest(t, "headers", opts, cfg)

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
	req, handler, ds := healthRequest(t, "timeout", opts, cfg)
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

func queryRequest(t *testing.T, name string, opts test.DriverOpts, cfg string) (*backend.QueryDataRequest, *test.SqlHandler, *sqlds.SQLDatasource) {
	driver, handler := test.NewDriver(name, test.Data{}, nil, opts)
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
	driver, handler := test.NewDriver(name, test.Data{}, nil, opts)
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
