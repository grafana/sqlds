package sqlds

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grafana/sqlds/v5/responseobs"
)

var (
	errorPingCompleted  = errors.New("ping completed")
	errorQueryCompleted = errors.New("query completed")
)

type testConnection struct {
	PingWait time.Duration

	QueryWait     time.Duration
	QueryRunCount int
}

func (t *testConnection) Close() error {
	t.QueryRunCount = 0
	return nil
}

func (t *testConnection) Ping() error {
	return errorPingCompleted
}

func (t *testConnection) PingContext(ctx context.Context) error {
	done := make(chan bool)
	go func() {
		time.Sleep(t.QueryWait)
		done <- true
	}()

	select {
	case <-ctx.Done():
		return context.Canceled
	case <-done:
		return errorPingCompleted
	}
}

func (t *testConnection) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	t.QueryRunCount++

	done := make(chan bool)
	go func() {
		time.Sleep(t.QueryWait)
		done <- true
	}()

	select {
	case <-ctx.Done():
		return nil, context.Canceled
	case <-done:
		return nil, errorQueryCompleted
	}
}

func TestQuery_Timeout(t *testing.T) {
	t.Run("it should return context.Canceled if the query timeout is exceeded", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
		defer cancel()

		conn := &testConnection{
			PingWait:  time.Second * 5,
			QueryWait: time.Second * 5,
		}

		defer conn.Close()

		settings := backend.DataSourceInstanceSettings{
			Name: "foo",
		}

		sqlQuery := NewQuery(conn, settings, []sqlutil.Converter{}, nil, defaultRowLimit)
		_, err := sqlQuery.Run(ctx, &Query{}, nil)

		if !errors.Is(err, context.Canceled) {
			t.Fatal("expected error to be context.Canceled, received", err)
		}

		if conn.QueryRunCount != 1 {
			t.Fatal("expected the querycontext function to run only once, but ran", conn.QueryRunCount, "times")
		}
	})

	t.Run("it should run to completion and not return a query timeout error", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
		defer cancel()

		conn := &testConnection{
			PingWait:  time.Second,
			QueryWait: time.Second,
		}

		defer conn.Close()

		settings := backend.DataSourceInstanceSettings{
			Name: "foo",
		}

		sqlQuery := NewQuery(conn, settings, []sqlutil.Converter{}, nil, defaultRowLimit)
		_, err := sqlQuery.Run(ctx, &Query{}, nil)

		if !errors.Is(err, errorQueryCompleted) {
			t.Fatal("expected function to complete, received error: ", err)
		}
	})
}

func TestFixFrameForLongToMulti(t *testing.T) {
	t.Run("fix time", func(t *testing.T) {
		time1 := time.UnixMilli(1)
		time2 := time.UnixMilli(2)
		frame := data.NewFrame("",
			data.NewField("time", nil, []*time.Time{&time1, &time2}),
			data.NewField("host", nil, []string{"a", "b"}),
			data.NewField("iface", nil, []string{"eth0", "eth0"}),
			data.NewField("in_bytes", nil, []float64{1, 2}),
			data.NewField("out_bytes", nil, []int64{3, 4}),
		)

		err := fixFrameForLongToMulti(frame)
		require.NoError(t, err)

		require.Equal(t, frame.Fields[0].Type(), data.FieldTypeTime)
		require.Equal(t, frame.Fields[0].Len(), 2)
		require.Equal(t, frame.Fields[0].At(0).(time.Time), time1)
		require.Equal(t, frame.Fields[0].At(1).(time.Time), time2)

		require.Equal(t, frame.Meta.Type, data.FrameTypeTimeSeriesLong)
		require.Equal(t, frame.Meta.TypeVersion, data.FrameTypeVersion{0, 1})
	})
	t.Run("errors for null time", func(t *testing.T) {
		time1 := time.UnixMilli(1)
		frame := data.NewFrame("",
			data.NewField("time", nil, []*time.Time{&time1, nil}),
			data.NewField("host", nil, []string{"a", "b"}),
			data.NewField("in_bytes", nil, []float64{1, 2}),
		)

		err := fixFrameForLongToMulti(frame)
		require.Equal(t, err, fmt.Errorf("can not convert to wide series, input has null time values"))
	})
	t.Run("error for no time", func(t *testing.T) {
		frame := data.NewFrame("",
			data.NewField("host", nil, []string{"a", "b"}),
			data.NewField("in_bytes", nil, []float64{1, 2}),
		)

		err := fixFrameForLongToMulti(frame)
		require.Equal(t, err, fmt.Errorf("can not convert to wide series, input is missing a time field"))
	})
}

func TestLabelNameSanitization(t *testing.T) {
	testcases := []struct {
		input    string
		expected string
		err      bool
	}{
		{input: "job", expected: "job"},
		{input: "job._loal['", expected: "job_loal"},
		{input: "", expected: "", err: true},
		{input: ";;;", expected: "", err: true},
		{input: "Data source", expected: "Data_source"},
	}

	for _, tc := range testcases {
		got, ok := sanitizeLabelName(tc.input)
		if tc.err {
			assert.Equal(t, false, ok)
		} else {
			assert.Equal(t, true, ok)
			assert.Equal(t, tc.expected, got)
		}
	}
}

func TestValidateRows(t *testing.T) {
	t.Run("returns error for nil rows", func(t *testing.T) {
		err := validateRows(nil)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrorRowValidation)
		require.Contains(t, err.Error(), "rows is nil")
	})
}

func TestFixFrameForLongToMulti_NilFrame(t *testing.T) {
	err := fixFrameForLongToMulti(nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "can not convert to wide series, input is nil")
}

func TestFixFrameForLongToMulti_NonNullableTime(t *testing.T) {
	time1 := time.UnixMilli(1)
	time2 := time.UnixMilli(2)
	frame := data.NewFrame("",
		data.NewField("time", nil, []time.Time{time1, time2}),
		data.NewField("host", nil, []string{"a", "b"}),
		data.NewField("value", nil, []float64{1, 2}),
	)

	err := fixFrameForLongToMulti(frame)
	require.NoError(t, err)

	// Verify the frame wasn't modified since time was already non-nullable
	require.Equal(t, data.FieldTypeTime, frame.Fields[0].Type())
	require.Equal(t, 2, frame.Fields[0].Len())
}

func TestNewQuery(t *testing.T) {
	conn := &testConnection{}
	settings := backend.DataSourceInstanceSettings{
		Name: "test-datasource",
		Type: "test-type",
	}
	converters := []sqlutil.Converter{}
	fillMode := &data.FillMissing{}
	rowLimit := int64(1000)

	dbQuery := NewQuery(conn, settings, converters, fillMode, rowLimit)

	require.NotNil(t, dbQuery)
	require.Equal(t, conn, dbQuery.DB)
	require.Equal(t, "test-datasource", dbQuery.DSName)
	require.Equal(t, converters, dbQuery.converters)
	require.Equal(t, fillMode, dbQuery.fillMode)
	require.Equal(t, rowLimit, dbQuery.rowLimit)
	require.NotNil(t, dbQuery.metrics)
}

func TestRun_WithDownStreamErrorMutator(t *testing.T) {
	ctx := context.Background()
	conn := &testConnection{
		QueryWait: 0,
	}
	settings := backend.DataSourceInstanceSettings{
		Name: "test",
	}

	query := &Query{
		RawSQL: "SELECT * FROM test",
		RefID:  "A",
	}

	// Create a mock ErrorMutator
	mockMutator := &mockErrorMutator{
		shouldMutate: true,
	}

	dbQuery := NewQuery(conn, settings, []sqlutil.Converter{}, nil, defaultRowLimit)
	_, err := dbQuery.Run(ctx, query, mockMutator)

	require.Error(t, err)
	require.True(t, mockMutator.called, "ErrorMutator should have been called")
}

func TestRun_ErrorQueryWrapping(t *testing.T) {
	t.Run("query errors are wrapped with ErrorQuery for retry logic", func(t *testing.T) {
		ctx := context.Background()
		conn := &testConnection{
			QueryWait: 0,
		}
		settings := backend.DataSourceInstanceSettings{
			Name: "test",
		}

		query := &Query{
			RawSQL: "SELECT * FROM test",
			RefID:  "A",
		}

		dbQuery := NewQuery(conn, settings, []sqlutil.Converter{}, nil, defaultRowLimit)
		_, err := dbQuery.Run(ctx, query, nil)

		require.Error(t, err)
		// Verify the error is wrapped with ErrorQuery to enable retry logic in datasource.go
		require.True(t, errors.Is(err, ErrorQuery), "Error should be wrapped with ErrorQuery for retry detection")
		// Verify the original error is preserved in the chain
		require.True(t, errors.Is(err, errorQueryCompleted), "Original error should be preserved")
	})

	t.Run("context.Canceled is NOT wrapped with ErrorQuery", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		conn := &testConnection{
			QueryWait: 0,
		}
		settings := backend.DataSourceInstanceSettings{
			Name: "test",
		}

		query := &Query{
			RawSQL: "SELECT * FROM test",
			RefID:  "A",
		}

		dbQuery := NewQuery(conn, settings, []sqlutil.Converter{}, nil, defaultRowLimit)
		_, err := dbQuery.Run(ctx, query, nil)

		require.Error(t, err)
		// Context cancellation should NOT be wrapped with ErrorQuery
		require.True(t, errors.Is(err, context.Canceled), "Error should be context.Canceled")
		// Verify it's classified as downstream error
		var errWithSource backend.ErrorWithSource
		require.True(t, errors.As(err, &errWithSource), "Error should implement ErrorWithSource")
		require.Equal(t, backend.ErrorSourceDownstream, errWithSource.ErrorSource())
	})

	t.Run("QueryErrorMutator receives ErrorQuery-wrapped errors", func(t *testing.T) {
		ctx := context.Background()
		conn := &testConnection{
			QueryWait: 0,
		}
		settings := backend.DataSourceInstanceSettings{
			Name: "test",
		}

		query := &Query{
			RawSQL: "SELECT * FROM test",
			RefID:  "A",
		}

		var receivedErr error
		mockMutator := &mockErrorMutatorWithCapture{
			capturedErr: &receivedErr,
		}

		dbQuery := NewQuery(conn, settings, []sqlutil.Converter{}, nil, defaultRowLimit)
		_, err := dbQuery.Run(ctx, query, mockMutator)

		require.Error(t, err)
		require.NotNil(t, receivedErr, "Mutator should have received an error")
		// Verify the mutator received an ErrorQuery-wrapped error
		require.True(t, errors.Is(receivedErr, ErrorQuery), "Mutator should receive ErrorQuery-wrapped error")
		require.True(t, errors.Is(receivedErr, errorQueryCompleted), "Original error should be in chain")
	})
}

func TestCollectResponseSize(t *testing.T) {
	// Use a unique datasource_type label so this test does not race with
	// observations from other tests against the same global histogram vec.
	const dsType = "TestCollectResponseSize-type"

	m := NewMetrics("test-ds", dsType, EndpointQuery)
	m.CollectResponseSize(42, 168)

	rows := histogramSnapshot(t, responseRowsMetric, dsType)
	require.Equal(t, uint64(1), rows.GetSampleCount(), "rows histogram should record one observation")
	require.Equal(t, 42.0, rows.GetSampleSum(), "rows histogram sum should equal observed value")

	cells := histogramSnapshot(t, responseCellsMetric, dsType)
	require.Equal(t, uint64(1), cells.GetSampleCount(), "cells histogram should record one observation")
	require.Equal(t, 168.0, cells.GetSampleSum(), "cells histogram sum should equal observed value")
}

func TestObserveResponseSize_MultipleFrames(t *testing.T) {
	const dsType = "TestObserveResponseSize-type"

	frames := data.Frames{
		data.NewFrame("a",
			data.NewField("t", nil, []time.Time{time.UnixMilli(1), time.UnixMilli(2)}),
			data.NewField("v", nil, []float64{1, 2}),
		),
		data.NewFrame("b",
			data.NewField("t", nil, []time.Time{time.UnixMilli(3)}),
			data.NewField("v", nil, []float64{3}),
			data.NewField("w", nil, []float64{4}),
		),
	}

	q := &DBQuery{metrics: NewMetrics("test-ds", dsType, EndpointQuery)}
	q.observeResponseSize(context.Background(), frames, "A", time.Now())

	rows := histogramSnapshot(t, responseRowsMetric, dsType)
	require.Equal(t, uint64(1), rows.GetSampleCount())
	require.Equal(t, 3.0, rows.GetSampleSum(), "should sum rows across frames (2 + 1)")

	cells := histogramSnapshot(t, responseCellsMetric, dsType)
	require.Equal(t, uint64(1), cells.GetSampleCount())
	require.Equal(t, 7.0, cells.GetSampleSum(), "should sum cells: 2*2 + 1*3 = 7")
}

func TestObserveResponseSize_ThresholdCrossed_EmitsLog(t *testing.T) {
	rec := swapBackendLogger(t)

	frame := data.NewFrame("big",
		data.NewField("v", nil, []int64{1, 2, 3, 4, 5}),
	)
	q := &DBQuery{
		Settings:   backend.DataSourceInstanceSettings{Type: "mssql", UID: "uid1", Name: "ds1"},
		metrics:    NewMetrics("ds1", "TestObserveResponseSize-cross", EndpointQuery),
		thresholds: responseobs.Thresholds{Rows: 3},
	}
	q.observeResponseSize(context.Background(), data.Frames{frame}, "A", time.Now())

	require.Len(t, rec.entries, 1, "expected one large-response log")
	assert.Equal(t, "large datasource response", rec.entries[0].msg)
}

func TestObserveResponseSize_ThresholdNotCrossed_NoLog(t *testing.T) {
	rec := swapBackendLogger(t)

	frame := data.NewFrame("small",
		data.NewField("v", nil, []int64{1, 2}),
	)
	q := &DBQuery{
		Settings:   backend.DataSourceInstanceSettings{Type: "mssql", UID: "uid1", Name: "ds1"},
		metrics:    NewMetrics("ds1", "TestObserveResponseSize-nocross", EndpointQuery),
		thresholds: responseobs.Thresholds{Rows: 100},
	}
	q.observeResponseSize(context.Background(), data.Frames{frame}, "A", time.Now())

	assert.Empty(t, rec.entries, "no log expected below threshold")
}

// recordedLogEntry + recordingBackendLogger capture warn/error/etc calls on
// backend.Logger so tests can assert side effects without relying on the
// default hclog output. Not parallel-safe because backend.Logger is global.
type recordedLogEntry struct {
	level log.Level
	msg   string
	args  []interface{}
}

type recordingBackendLogger struct {
	mu      sync.Mutex
	entries []recordedLogEntry
}

func (l *recordingBackendLogger) record(level log.Level, msg string, args []interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, recordedLogEntry{level: level, msg: msg, args: args})
}

func (l *recordingBackendLogger) Debug(msg string, args ...interface{}) {
	l.record(log.Debug, msg, args)
}
func (l *recordingBackendLogger) Info(msg string, args ...interface{}) {
	l.record(log.Info, msg, args)
}
func (l *recordingBackendLogger) Warn(msg string, args ...interface{}) {
	l.record(log.Warn, msg, args)
}
func (l *recordingBackendLogger) Error(msg string, args ...interface{}) {
	l.record(log.Error, msg, args)
}
func (l *recordingBackendLogger) With(_ ...interface{}) log.Logger          { return l }
func (l *recordingBackendLogger) Level() log.Level                          { return log.Debug }
func (l *recordingBackendLogger) FromContext(_ context.Context) log.Logger  { return l }

// swapBackendLogger replaces backend.Logger with a recording logger for the
// lifetime of the test and restores it on cleanup.
func swapBackendLogger(t *testing.T) *recordingBackendLogger {
	t.Helper()
	rec := &recordingBackendLogger{}
	orig := backend.Logger
	backend.Logger = rec
	t.Cleanup(func() { backend.Logger = orig })
	return rec
}

// histogramSnapshot extracts the dto.Histogram for a given label value, so tests
// can assert on sample count and sum directly.
func histogramSnapshot(t *testing.T, vec *prometheus.HistogramVec, dsType string) *dto.Histogram {
	t.Helper()
	obs, err := vec.GetMetricWithLabelValues(dsType)
	require.NoError(t, err)
	hist, ok := obs.(prometheus.Histogram)
	require.True(t, ok, "expected prometheus.Histogram")
	out := &dto.Metric{}
	require.NoError(t, hist.Write(out))
	require.NotNil(t, out.Histogram)
	return out.Histogram
}

// mockErrorMutator is a simple implementation for testing
type mockErrorMutator struct {
	shouldMutate bool
	called       bool
}

func (m *mockErrorMutator) MutateQueryError(err error) backend.ErrorWithSource {
	m.called = true
	if m.shouldMutate {
		return backend.NewErrorWithSource(err, backend.ErrorSourceDownstream)
	}
	return backend.NewErrorWithSource(err, backend.ErrorSourcePlugin)
}

// mockErrorMutatorWithCapture captures the error it receives for testing
type mockErrorMutatorWithCapture struct {
	capturedErr *error
}

func (m *mockErrorMutatorWithCapture) MutateQueryError(err error) backend.ErrorWithSource {
	if m.capturedErr != nil {
		*m.capturedErr = err
	}
	return backend.NewErrorWithSource(err, backend.ErrorSourceDownstream)
}
