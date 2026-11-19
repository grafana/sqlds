package sqlds_test

import (
	"context"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	sqlds "github.com/grafana/sqlds/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getFakeConnector(t *testing.T, shouldFail bool) *sqlds.Connector {
	t.Helper()
	c, _ := sqlds.NewConnector(context.TODO(), &sqlds.SQLMock{ShouldFailToConnect: shouldFail}, backend.DataSourceInstanceSettings{}, false)
	return c
}

func TestHealthChecker_Check(t *testing.T) {
	tests := []struct {
		name            string
		Connector       *sqlds.Connector
		Metrics         sqlds.Metrics
		PreCheckHealth  func(ctx context.Context, req *backend.CheckHealthRequest) *backend.CheckHealthResult
		PostCheckHealth func(ctx context.Context, req *backend.CheckHealthRequest) *backend.CheckHealthResult
		ctx             context.Context
		req             *backend.CheckHealthRequest
		want            *backend.CheckHealthResult
		wantErr         error
	}{
		{
			name:      "default health check should return valid result",
			Connector: getFakeConnector(t, false),
		},
		{
			name:      "should not error when pre check succeed",
			Connector: getFakeConnector(t, false),
			PreCheckHealth: func(ctx context.Context, req *backend.CheckHealthRequest) *backend.CheckHealthResult {
				return &backend.CheckHealthResult{Status: backend.HealthStatusOk}
			},
		},
		{
			name:      "should error when pre check failed",
			Connector: getFakeConnector(t, false),
			PreCheckHealth: func(ctx context.Context, req *backend.CheckHealthRequest) *backend.CheckHealthResult {
				return &backend.CheckHealthResult{Status: backend.HealthStatusError, Message: "unknown error"}
			},
			want: &backend.CheckHealthResult{Status: backend.HealthStatusError, Message: "unknown error"},
		},
		{
			name:      "should return actual error when pre and post health check succeed but actual connect failed",
			Connector: getFakeConnector(t, true),
			PreCheckHealth: func(ctx context.Context, req *backend.CheckHealthRequest) *backend.CheckHealthResult {
				return &backend.CheckHealthResult{Status: backend.HealthStatusOk}
			},
			PostCheckHealth: func(ctx context.Context, req *backend.CheckHealthRequest) *backend.CheckHealthResult {
				return &backend.CheckHealthResult{Status: backend.HealthStatusOk}
			},
			want: &backend.CheckHealthResult{Status: backend.HealthStatusError, Message: "failed to create mock"},
		},
		{
			name:      "should not error when post check succeed",
			Connector: getFakeConnector(t, false),
			PostCheckHealth: func(ctx context.Context, req *backend.CheckHealthRequest) *backend.CheckHealthResult {
				return &backend.CheckHealthResult{Status: backend.HealthStatusOk}
			},
		},
		{
			name:      "should error when post check failed",
			Connector: getFakeConnector(t, false),
			PostCheckHealth: func(ctx context.Context, req *backend.CheckHealthRequest) *backend.CheckHealthResult {
				return &backend.CheckHealthResult{Status: backend.HealthStatusError, Message: "unknown error"}
			},
			want: &backend.CheckHealthResult{Status: backend.HealthStatusError, Message: "unknown error"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connector := tt.Connector
			if connector == nil {
				connector = &sqlds.Connector{}
			}
			req := tt.req
			if req == nil {
				req = &backend.CheckHealthRequest{}
			}
			want := tt.want
			if want == nil {
				want = &backend.CheckHealthResult{Status: backend.HealthStatusOk, Message: "Data source is working"}
			}
			ctx := tt.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			hc := &sqlds.HealthChecker{
				Connector:       connector,
				Metrics:         tt.Metrics,
				PreCheckHealth:  tt.PreCheckHealth,
				PostCheckHealth: tt.PostCheckHealth,
			}
			got, err := hc.Check(ctx, req)
			if tt.wantErr != nil {
				require.NotNil(t, err)
				assert.Equal(t, tt.wantErr.Error(), err.Error())
				return
			}
			require.Nil(t, err)
			require.NotNil(t, got)
			assert.Equal(t, want.Message, got.Message)
			assert.Equal(t, want.Status, got.Status)
		})
	}
}

func TestHealthChecker_Check_ErrorSource(t *testing.T) {
	t.Run("a failed connection is reported as a downstream error", func(t *testing.T) {
		hc := &sqlds.HealthChecker{Connector: getFakeConnector(t, true)}

		got, err := hc.Check(context.Background(), &backend.CheckHealthRequest{})
		require.NoError(t, err)
		require.Equal(t, backend.HealthStatusError, got.Status)
		assert.Equal(t, backend.ErrorSourceDownstream, got.ErrorSource)
	})

	t.Run("a missing connection is reported as a plugin error", func(t *testing.T) {
		hc := &sqlds.HealthChecker{Connector: &sqlds.Connector{}}

		got, err := hc.Check(context.Background(), &backend.CheckHealthRequest{})
		require.NoError(t, err)
		require.Equal(t, backend.HealthStatusError, got.Status)
		assert.Equal(t, backend.ErrorSourcePlugin, got.ErrorSource)
	})

	t.Run("a healthy check has no error source", func(t *testing.T) {
		hc := &sqlds.HealthChecker{Connector: getFakeConnector(t, false)}

		got, err := hc.Check(context.Background(), &backend.CheckHealthRequest{})
		require.NoError(t, err)
		require.Equal(t, backend.HealthStatusOk, got.Status)
		assert.Equal(t, backend.ErrorSource(""), got.ErrorSource)
	})

	t.Run("a result returned by a check hook is not changed", func(t *testing.T) {
		hc := &sqlds.HealthChecker{
			Connector: getFakeConnector(t, false),
			PreCheckHealth: func(context.Context, *backend.CheckHealthRequest) *backend.CheckHealthResult {
				return &backend.CheckHealthResult{Status: backend.HealthStatusError, Message: "bad config", ErrorSource: backend.ErrorSourceDownstream}
			},
		}

		got, err := hc.Check(context.Background(), &backend.CheckHealthRequest{})
		require.NoError(t, err)
		assert.Equal(t, backend.ErrorSourceDownstream, got.ErrorSource)
	})
}
