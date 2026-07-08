package responseobs

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCrosses(t *testing.T) {
	cases := []struct {
		name   string
		obs    Observation
		thresh Thresholds
		want   bool
	}{
		{
			name:   "neither crossed",
			obs:    Observation{Bytes: 100, Rows: 100},
			thresh: Thresholds{Bytes: 1000, Rows: 1000},
			want:   false,
		},
		{
			name:   "bytes crossed only",
			obs:    Observation{Bytes: 2000, Rows: 100},
			thresh: Thresholds{Bytes: 1000, Rows: 1000},
			want:   true,
		},
		{
			name:   "rows crossed only",
			obs:    Observation{Bytes: 100, Rows: 2000},
			thresh: Thresholds{Bytes: 1000, Rows: 1000},
			want:   true,
		},
		{
			name:   "both crossed",
			obs:    Observation{Bytes: 2000, Rows: 2000},
			thresh: Thresholds{Bytes: 1000, Rows: 1000},
			want:   true,
		},
		{
			name:   "bytes threshold disabled, rows crossed",
			obs:    Observation{Bytes: 1 << 30, Rows: 2000},
			thresh: Thresholds{Bytes: 0, Rows: 1000},
			want:   true,
		},
		{
			name:   "both thresholds disabled",
			obs:    Observation{Bytes: 1 << 30, Rows: 1 << 20},
			thresh: Thresholds{},
			want:   false,
		},
		{
			name:   "exactly at threshold crosses (>=)",
			obs:    Observation{Rows: 1000},
			thresh: Thresholds{Rows: 1000},
			want:   true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, crosses(tc.obs, tc.thresh))
		})
	}
}

func TestDefaults(t *testing.T) {
	d := Defaults()
	assert.Equal(t, int64(50*1024*1024), d.Bytes)
	assert.Equal(t, int64(1_000_000), d.Rows)
}

func TestObserve_NoCross_SilentReturnsFalse(t *testing.T) {
	rec := swapLogger(t)

	ok := Observe(context.Background(),
		Observation{Rows: 10},
		Thresholds{Rows: 100})

	assert.False(t, ok)
	assert.Empty(t, rec.entries, "no log expected below threshold")
}

func TestObserve_Cross_LogsAndReturnsTrue(t *testing.T) {
	rec := swapLogger(t)

	obs := Observation{
		Datasource: backend.DataSourceInstanceSettings{
			Type: "mssql",
			UID:  "abc123",
			Name: "my-sql",
		},
		Rows:     2_000_000,
		Cells:    10_000_000,
		Duration: 2500 * time.Millisecond,
		RefID:    "A",
	}
	ok := Observe(context.Background(), obs, Thresholds{Rows: DefaultRowsThreshold})

	require.True(t, ok)
	require.Len(t, rec.entries, 1)
	e := rec.entries[0]
	assert.Equal(t, log.Warn, e.level)
	assert.Equal(t, "large datasource response", e.msg)
	fields := e.kv()
	assert.Equal(t, "mssql", fields["datasource_type"])
	assert.Equal(t, "abc123", fields["datasource_uid"])
	assert.Equal(t, "my-sql", fields["datasource_name"])
	assert.Equal(t, int64(2_000_000), fields["rows"])
	assert.Equal(t, int64(10_000_000), fields["cells"])
	assert.Equal(t, int64(2500), fields["duration_ms"])
	assert.Equal(t, "A", fields["ref_id"])
}

func TestObserve_AppURLFromContext(t *testing.T) {
	rec := swapLogger(t)

	cfg := backend.NewGrafanaCfg(map[string]string{
		backend.AppURL: "https://example.grafana.net/",
	})
	ctx := backend.WithGrafanaConfig(context.Background(), cfg)

	ok := Observe(ctx,
		Observation{Rows: DefaultRowsThreshold},
		Thresholds{Rows: DefaultRowsThreshold})

	require.True(t, ok)
	require.Len(t, rec.entries, 1)
	assert.Equal(t, "https://example.grafana.net/", rec.entries[0].kv()["app_url"])
}

func TestObserve_AppURLAbsent_EmptyField(t *testing.T) {
	rec := swapLogger(t)

	ok := Observe(context.Background(),
		Observation{Rows: DefaultRowsThreshold},
		Thresholds{Rows: DefaultRowsThreshold})

	require.True(t, ok)
	require.Len(t, rec.entries, 1)
	assert.Equal(t, "", rec.entries[0].kv()["app_url"])
}

func TestObserve_Cross_IncrementsCounter(t *testing.T) {
	swapLogger(t)

	ds := backend.DataSourceInstanceSettings{Type: "counter-cross", UID: "ds-uid-1"}
	appURL := "https://counter-cross.grafana.net/"
	cfg := backend.NewGrafanaCfg(map[string]string{backend.AppURL: appURL})
	ctx := backend.WithGrafanaConfig(context.Background(), cfg)

	before := counterValue(t, ds.Type, appURL, ds.UID)
	ok := Observe(ctx,
		Observation{Datasource: ds, Rows: DefaultRowsThreshold},
		Thresholds{Rows: DefaultRowsThreshold})

	require.True(t, ok)
	assert.Equal(t, before+1, counterValue(t, ds.Type, appURL, ds.UID))
}

func TestObserve_NoCross_DoesNotIncrementCounter(t *testing.T) {
	swapLogger(t)

	ds := backend.DataSourceInstanceSettings{Type: "counter-nocross", UID: "ds-uid-2"}

	before := counterValue(t, ds.Type, "", ds.UID)
	ok := Observe(context.Background(),
		Observation{Datasource: ds, Rows: 10},
		Thresholds{Rows: 1000})

	require.False(t, ok)
	assert.Equal(t, before, counterValue(t, ds.Type, "", ds.UID))
}

// counterValue reads the large-responses counter for a given label set.
// Returns 0 if the series has not been created yet.
func counterValue(t *testing.T, dsType, appURL, dsUID string) float64 {
	t.Helper()
	c, err := largeResponsesCounter.GetMetricWithLabelValues(dsType, appURL, dsUID)
	require.NoError(t, err)
	m := &dto.Metric{}
	require.NoError(t, c.Write(m))
	if m.Counter == nil {
		return 0
	}
	return m.Counter.GetValue()
}

// swapLogger replaces backend.Logger with a recording logger for the
// duration of the test and returns it. Tests using this cannot run in
// parallel with each other because backend.Logger is a global.
func swapLogger(t *testing.T) *recordingLogger {
	t.Helper()
	rec := &recordingLogger{}
	orig := backend.Logger
	backend.Logger = rec
	t.Cleanup(func() { backend.Logger = orig })
	return rec
}

type logEntry struct {
	level log.Level
	msg   string
	args  []interface{}
}

// kv returns args as a map[string]interface{}, treating consecutive
// pairs as key/value. Odd-length args are ignored.
func (e logEntry) kv() map[string]interface{} {
	out := map[string]interface{}{}
	for i := 0; i+1 < len(e.args); i += 2 {
		k, ok := e.args[i].(string)
		if !ok {
			continue
		}
		out[k] = e.args[i+1]
	}
	return out
}

type recordingLogger struct {
	mu      sync.Mutex
	entries []logEntry
}

func (l *recordingLogger) record(level log.Level, msg string, args []interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, logEntry{level: level, msg: msg, args: args})
}

func (l *recordingLogger) Debug(msg string, args ...interface{}) { l.record(log.Debug, msg, args) }
func (l *recordingLogger) Info(msg string, args ...interface{})  { l.record(log.Info, msg, args) }
func (l *recordingLogger) Warn(msg string, args ...interface{})  { l.record(log.Warn, msg, args) }
func (l *recordingLogger) Error(msg string, args ...interface{}) { l.record(log.Error, msg, args) }
func (l *recordingLogger) With(_ ...interface{}) log.Logger      { return l }
func (l *recordingLogger) Level() log.Level                      { return log.Debug }
func (l *recordingLogger) FromContext(_ context.Context) log.Logger {
	return l
}
