package sqlds

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
