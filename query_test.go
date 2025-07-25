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
		_, err := sqlQuery.Run(ctx, &Query{})

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
		_, err := sqlQuery.Run(ctx, &Query{})

		if !errors.Is(err, ErrorQuery) {
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

func TestIsProcessingDownstreamError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "ErrorInputFieldsWithoutRows returns true",
			err:      data.ErrorInputFieldsWithoutRows,
			expected: true,
		},
		{
			name:     "ErrorSeriesUnsorted returns true",
			err:      data.ErrorSeriesUnsorted,
			expected: true,
		},
		{
			name:     "ErrorNullTimeValues returns true",
			err:      data.ErrorNullTimeValues,
			expected: true,
		},
		{
			name:     "Different error returns false",
			err:      errors.New("some other error"),
			expected: false,
		},
		{
			name:     "Wrapped downstream error returns true",
			err:      fmt.Errorf("wrapped: %w", data.ErrorInputFieldsWithoutRows),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isProcessingDownstreamError(tt.err)
			if result != tt.expected {
				t.Errorf("isProcessingDownstreamError(%v) = %v; want %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestPGXErrorClassification(t *testing.T) {
	tests := []struct {
		name           string
		errorMsg       string
		expectedSource backend.ErrorSource
		expectedIsPGX  bool
	}{
		{
			name:           "nil pointer dereference",
			errorMsg:       "runtime error: invalid memory address or nil pointer dereference",
			expectedSource: backend.ErrorSourceDownstream,
			expectedIsPGX:  false, // Now handled as generic downstream error
		},
		{
			name:           "connection closed",
			errorMsg:       "connection closed",
			expectedSource: backend.ErrorSourceDownstream,
			expectedIsPGX:  true,
		},
		{
			name:           "broken pipe",
			errorMsg:       "broken pipe",
			expectedSource: backend.ErrorSourceDownstream,
			expectedIsPGX:  true,
		},
		{
			name:           "pgconn error",
			errorMsg:       "pgconn: connection failed",
			expectedSource: backend.ErrorSourceDownstream,
			expectedIsPGX:  true,
		},
		{
			name:           "regular SQL error",
			errorMsg:       "syntax error at position 1",
			expectedSource: backend.ErrorSourcePlugin,
			expectedIsPGX:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := errors.New(tt.errorMsg)

			// Test IsPGXConnectionError
			isPGX := IsPGXConnectionError(err)
			if isPGX != tt.expectedIsPGX {
				t.Errorf("IsPGXConnectionError() = %v, expected %v", isPGX, tt.expectedIsPGX)
			}

			// Test ClassifyError
			source, _ := ClassifyError(err)
			if source != tt.expectedSource {
				t.Errorf("ClassifyError() source = %v, expected %v", source, tt.expectedSource)
			}

			// Test isProcessingDownstreamError
			if tt.expectedSource == backend.ErrorSourceDownstream {
				isDownstream := isProcessingDownstreamError(err)
				if !isDownstream {
					t.Errorf("isProcessingDownstreamError() = %v, expected true for downstream error", isDownstream)
				}
			}
		})
	}
}

func TestIsGenericDownstreamError(t *testing.T) {
	tests := []struct {
		name     string
		errorMsg string
		expected bool
	}{
		{
			name:     "nil pointer dereference",
			errorMsg: "runtime error: invalid memory address or nil pointer dereference",
			expected: true,
		},
		{
			name:     "invalid memory address",
			errorMsg: "runtime error: invalid memory address",
			expected: true,
		},
		{
			name:     "nil pointer dereference uppercase",
			errorMsg: "NIL POINTER DEREFERENCE",
			expected: true,
		},
		{
			name:     "regular SQL error",
			errorMsg: "syntax error at position 1",
			expected: false,
		},
		{
			name:     "connection error",
			errorMsg: "connection closed",
			expected: false,
		},
		{
			name:     "nil error",
			errorMsg: "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			if tt.errorMsg != "" {
				err = errors.New(tt.errorMsg)
			}

			result := IsGenericDownstreamError(err)
			if result != tt.expected {
				t.Errorf("IsGenericDownstreamError(%v) = %v, expected %v", tt.errorMsg, result, tt.expected)
			}
		})
	}
}
