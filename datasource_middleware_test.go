package sqlds_test

import (
	"context"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/sqlds/v5"
	"github.com/grafana/sqlds/v5/test"
	"github.com/stretchr/testify/assert"
)

func Test_resource_middleware_is_applied(t *testing.T) {
	called := false
	middleware := func(next backend.CallResourceHandler) backend.CallResourceHandler {
		return backend.CallResourceHandlerFunc(func(ctx context.Context, req *backend.CallResourceRequest, sender backend.CallResourceResponseSender) error {
			called = true
			return next.CallResource(ctx, req, sender)
		})
	}

	driver, _ := test.NewDriver("middleware-applied", test.Data{}, nil, test.DriverOpts{}, nil)
	ds := sqlds.NewDatasource(driver)
	ds.ResourceMiddleware = middleware

	settings := backend.DataSourceInstanceSettings{UID: "middleware-applied", JSONData: []byte("{}")}
	_, err := ds.NewDatasource(context.Background(), settings)
	assert.Nil(t, err)

	sender := &fakeResourceSender{}
	err = ds.CallResource(context.Background(), &backend.CallResourceRequest{Path: "tables", Method: "GET"}, sender)
	assert.Nil(t, err)
	assert.True(t, called, "expected ResourceMiddleware to be called")
}

func Test_resource_middleware_nil_is_skipped(t *testing.T) {
	driver, _ := test.NewDriver("middleware-nil", test.Data{}, nil, test.DriverOpts{}, nil)
	ds := sqlds.NewDatasource(driver)
	// ResourceMiddleware intentionally left nil

	settings := backend.DataSourceInstanceSettings{UID: "middleware-nil", JSONData: []byte("{}")}
	_, err := ds.NewDatasource(context.Background(), settings)
	assert.Nil(t, err)
	assert.NotNil(t, ds.CallResourceHandler, "expected CallResourceHandler to be set even without middleware")
}

// fakeResourceSender captures the last sent response.
type fakeResourceSender struct {
	response *backend.CallResourceResponse
}

func (f *fakeResourceSender) Send(resp *backend.CallResourceResponse) error {
	f.response = resp
	return nil
}
