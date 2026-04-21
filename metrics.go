package sqlds

import (
	"fmt"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type Metrics struct {
	DSName   string
	DSType   string
	Endpoint Endpoint
}

type Status string
type Endpoint string
type Source string

const (
	StatusOK         Status   = "ok"
	StatusError      Status   = "error"
	EndpointHealth   Endpoint = "health"
	EndpointQuery    Endpoint = "query"
	SourceDownstream Source   = "downstream"
	SourcePlugin     Source   = "plugin"
)

var durationMetric = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Namespace: "plugins",
	Name:      "plugin_request_duration_seconds",
	Help:      "Duration of plugin execution",
}, []string{"datasource_name", "datasource_type", "source", "endpoint", "status"})

var responseRowsMetric = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Namespace:                       "plugins",
	Name:                            "sql_response_rows",
	Help:                            "Number of rows returned by a SQL datasource query",
	Buckets:                         []float64{1, 10, 100, 1_000, 10_000, 100_000, 1_000_000, 10_000_000, 100_000_000},
	NativeHistogramBucketFactor:     1.1,
	NativeHistogramMaxBucketNumber:  100,
	NativeHistogramMinResetDuration: time.Hour,
}, []string{"datasource_type"})

var responseCellsMetric = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Namespace:                       "plugins",
	Name:                            "sql_response_cells",
	Help:                            "Number of cells (rows × fields) returned by a SQL datasource query",
	Buckets:                         []float64{1, 100, 10_000, 1_000_000, 10_000_000, 100_000_000, 1_000_000_000, 10_000_000_000},
	NativeHistogramBucketFactor:     1.1,
	NativeHistogramMaxBucketNumber:  100,
	NativeHistogramMinResetDuration: time.Hour,
}, []string{"datasource_type"})

func NewMetrics(dsName, dsType string, endpoint Endpoint) Metrics {
	dsName, ok := sanitizeLabelName(dsName)
	if !ok {
		backend.Logger.Warn("Failed to sanitize datasource name for prometheus label", dsName)
	}
	return Metrics{DSName: dsName, DSType: dsType, Endpoint: endpoint}
}

func (m *Metrics) WithEndpoint(endpoint Endpoint) Metrics {
	return Metrics{DSName: m.DSName, DSType: m.DSType, Endpoint: endpoint}
}

func (m *Metrics) CollectDuration(source Source, status Status, duration float64) {
	durationMetric.WithLabelValues(m.DSName, m.DSType, string(source), string(m.Endpoint), string(status)).Observe(duration)
}

func (m *Metrics) CollectResponseSize(rows, cells int64) {
	responseRowsMetric.WithLabelValues(m.DSType).Observe(float64(rows))
	responseCellsMetric.WithLabelValues(m.DSType).Observe(float64(cells))
}

// sanitizeLabelName removes all invalid chars from the label name.
// If the label name is empty or contains only invalid chars, it will return false indicating it was not sanitized.
// copied from https://github.com/grafana/grafana/blob/main/pkg/infra/metrics/metricutil/utils.go#L14
func sanitizeLabelName(name string) (string, bool) {
	if len(name) == 0 {
		backend.Logger.Warn(fmt.Sprintf("label name cannot be empty: %s", name))
		return "", false
	}

	out := strings.Builder{}
	for i, b := range name {
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_' || (b >= '0' && b <= '9' && i > 0) {
			out.WriteRune(b)
		} else if b == ' ' {
			out.WriteRune('_')
		}
	}

	if out.Len() == 0 {
		backend.Logger.Warn(fmt.Sprintf("label name only contains invalid chars: %q", name))
		return "", false
	}

	return out.String(), true
}
