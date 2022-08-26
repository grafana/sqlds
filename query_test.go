package sqlds

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
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

type testAsyncDB struct {
	queryID string
	AsyncDB
}

func (db *testAsyncDB) StartQuery(ctx context.Context, query string, args ...interface{}) (string, error) {
	return "new", nil
}
func (db *testAsyncDB) GetQueryID(ctx context.Context, query string, args ...interface{}) (bool, string, error) {
	return db.queryID != "", db.queryID, nil
}

func Test_AsyncDB(t *testing.T) {
	t.Run("startQuery", func(t *testing.T) {
		testCases := map[string]struct {
			queryID    string
			expectedID string
		}{
			"queryID found": {
				queryID:    "foo",
				expectedID: "foo",
			},
			"queryID not found": {
				queryID:    "",
				expectedID: "new",
			},
		}
		for name, testcase := range testCases {
			t.Run(name, func(t *testing.T) {
				db := &testAsyncDB{queryID: testcase.queryID}
				id, err := startQuery(context.Background(), db, &Query{})
				if err != nil {
					t.Fatalf("unexpected error %v", err)
				}
				if id != testcase.expectedID {
					t.Fatalf("unexpected id %s", id)
				}
			})
		}
	})
}
