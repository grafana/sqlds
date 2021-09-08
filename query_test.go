package sqlds

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
	"github.com/stretchr/testify/assert"
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

		_, err := query(ctx, conn, []sqlutil.Converter{}, nil, &Query{})

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

		_, err := query(ctx, conn, []sqlutil.Converter{}, nil, &Query{})

		if !errors.Is(err, ErrorQuery) {
			t.Fatal("expected function to complete, received error: ", err)
		}
	})
}

func Test_isLogFrame(t *testing.T) {
	tests := []struct {
		name     string
		frame    data.Frame
		expected bool
	}{
		{frame: *data.NewFrameOfFieldTypes("foo", 4)},
		{frame: *data.NewFrameOfFieldTypes("foo", 4, data.FieldTypeBool)},
		{frame: *data.NewFrameOfFieldTypes("foo", 4, data.FieldTypeTime)},
		{frame: *data.NewFrameOfFieldTypes("foo", 4, data.FieldTypeString)},
		{frame: *data.NewFrameOfFieldTypes("foo", 4, data.FieldTypeString, data.FieldTypeNullableString)},
		{frame: *data.NewFrameOfFieldTypes("foo", 4, data.FieldTypeString, data.FieldTypeTime), expected: true},
		{frame: *data.NewFrameOfFieldTypes("foo", 4, data.FieldTypeNullableString, data.FieldTypeNullableString, data.FieldTypeNullableFloat64, data.FieldTypeTime), expected: true},
		{frame: *data.NewFrameOfFieldTypes("foo", 4, data.FieldTypeNullableString, data.FieldTypeNullableString, data.FieldTypeNullableFloat64)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := isLogFrame(tt.frame)
			assert.Equal(t, tt.expected, actual)
		})
	}
}
