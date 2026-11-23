package sqlds

import (
	"context"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func healthErrorCount(t *testing.T, m Metrics, source Source) uint64 {
	t.Helper()
	obs, err := durationMetric.GetMetricWithLabelValues(m.DSName, m.DSType, string(source), string(EndpointHealth), string(StatusError))
	require.NoError(t, err)
	out := &dto.Metric{}
	require.NoError(t, obs.(prometheus.Histogram).Write(out))
	return out.GetHistogram().GetSampleCount()
}

func TestHealthCheckerRecordsErrorSource(t *testing.T) {
	failing, err := NewConnector(context.Background(), &SQLMock{ShouldFailToConnect: true}, backend.DataSourceInstanceSettings{}, false)
	require.NoError(t, err)

	tests := []struct {
		name      string
		connector *Connector
		want      Source
	}{
		{name: "a driver that fails to open a connection is a downstream error", connector: failing, want: SourceDownstream},
		{name: "a missing connection is a plugin error", connector: &Connector{}, want: SourcePlugin},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			metrics := NewMetrics(t.Name(), "health-source-test", EndpointHealth)
			hc := &HealthChecker{Connector: tc.connector, Metrics: metrics}

			res, err := hc.Check(context.Background(), &backend.CheckHealthRequest{})
			require.NoError(t, err)
			require.Equal(t, backend.HealthStatusError, res.Status)

			for _, source := range []Source{SourceDownstream, SourcePlugin} {
				want := uint64(0)
				if source == tc.want {
					want = 1
				}
				assert.Equal(t, want, healthErrorCount(t, metrics, source), "health errors recorded as %s", source)
			}
		})
	}
}
