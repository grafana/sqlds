// Package responseobs observes SQL datasource response sizes and emits a
// structured warn log when a response crosses a configured threshold.
//
// The package is intentionally narrow: callers compute response shape
// (rows, cells, optionally bytes) and a single Observe call handles
// threshold evaluation and log emission. The return value signals whether
// a threshold was crossed, so downstream consumers (e.g. a large-response
// counter) can piggyback on the same decision point.
//
// Thresholds default to values appropriate for datasource plugins at
// Grafana's managed-tenant scale; plugins with known-large responses can
// override per-instance via sqlds.DriverSettings.
package responseobs

import (
	"context"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

const (
	// DefaultBytesThreshold is 50 MiB.
	DefaultBytesThreshold int64 = 50 * 1024 * 1024
	// DefaultRowsThreshold is one million rows.
	DefaultRowsThreshold int64 = 1_000_000
)

// Thresholds configures what counts as a "large" response. A zero on
// either field disables that dimension — useful at measurement layers
// where one of the two values is unavailable (e.g. sqlds can count rows
// but not serialized bytes).
type Thresholds struct {
	Bytes int64
	Rows  int64
}

// Defaults returns Thresholds populated with DefaultBytesThreshold and
// DefaultRowsThreshold.
func Defaults() Thresholds {
	return Thresholds{Bytes: DefaultBytesThreshold, Rows: DefaultRowsThreshold}
}

// Observation is what callers hand to Observe. Fields that the calling
// layer cannot measure should be left zero; Observe treats zero as
// "not measured" for threshold comparison purposes.
type Observation struct {
	Datasource backend.DataSourceInstanceSettings
	Bytes      int64
	Rows       int64
	Cells      int64
	Duration   time.Duration
	RefID      string
	// QueryHash is optional. Empty string means not computed.
	QueryHash string
}

// Observe compares obs against t. If a threshold is crossed, it emits a
// single structured warn log via backend.Logger and returns true.
// Otherwise it returns false and emits nothing. Intended to be called
// at most once per response.
func Observe(ctx context.Context, obs Observation, t Thresholds) bool {
	if !crosses(obs, t) {
		return false
	}
	backend.Logger.Warn("large datasource response",
		"app_url", appURLFromContext(ctx),
		"datasource_type", obs.Datasource.Type,
		"datasource_uid", obs.Datasource.UID,
		"datasource_name", obs.Datasource.Name,
		"bytes", obs.Bytes,
		"rows", obs.Rows,
		"cells", obs.Cells,
		"duration_ms", obs.Duration.Milliseconds(),
		"ref_id", obs.RefID,
		"query_hash", obs.QueryHash,
	)
	return true
}

func crosses(obs Observation, t Thresholds) bool {
	if t.Bytes > 0 && obs.Bytes >= t.Bytes {
		return true
	}
	if t.Rows > 0 && obs.Rows >= t.Rows {
		return true
	}
	return false
}

// appURLFromContext returns the Grafana app URL from plugin context when
// available, else the empty string. OSS deployments may not populate it;
// operators can derive a slug downstream by parsing the URL.
func appURLFromContext(ctx context.Context) string {
	cfg := backend.GrafanaConfigFromContext(ctx)
	if cfg == nil {
		return ""
	}
	url, err := cfg.AppURL()
	if err != nil {
		return ""
	}
	return url
}
