package sqlds

import (
	"context"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var healthExternalDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Namespace: "plugins",
	Name:      "plugin_health_esternal_duration_seconds",
	Help:      "Duration of external plugin health check",
}, []string{"datasource_name", "datasource_type", "error_source"})

type HealthChecker struct {
	Connector *Connector
}

func (hc *HealthChecker) Check(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	start := time.Now()

	settings := req.PluginContext.DataSourceInstanceSettings
	_, err := hc.Connector.Connect(ctx, req.GetHTTPHeaders())
	if err != nil {
		healthExternalDuration.WithLabelValues(settings.Name, settings.Type, string(ErrorSource(err))).Observe(time.Since(start).Seconds())
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusError,
			Message: err.Error(),
		}, DownstreamError(err)
	}
	healthExternalDuration.WithLabelValues(settings.Name, settings.Type, "none").Observe(time.Since(start).Seconds())

	return &backend.CheckHealthResult{
		Status:  backend.HealthStatusOk,
		Message: "Data source is working",
	}, nil
}
