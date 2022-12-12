package sqlds

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
	"github.com/grafana/sqlds/v2/mock"
	"github.com/stretchr/testify/assert"
)

type fakeDriver struct {
	openDBfn func() (*sql.DB, error)

	Driver
}

func (d fakeDriver) Connect(backend.DataSourceInstanceSettings, json.RawMessage) (db *sql.DB, err error) {
	return d.openDBfn()
}

func (d fakeDriver) Macros() Macros {
	return Macros{}
}

func (d fakeDriver) Converters() []sqlutil.Converter {
	return []sqlutil.Converter{}
}

// func (d fakeDriver) Settings(backend.DataSourceInstanceSettings) DriverSettings

func Test_getDBConnectionFromQuery(t *testing.T) {
	db := &sql.DB{}
	db2 := &sql.DB{}
	db3 := &sql.DB{}
	d := &fakeDriver{openDBfn: func() (*sql.DB, error) { return db3, nil }}
	tests := []struct {
		desc        string
		dsUID       string
		args        string
		existingDB  *sql.DB
		expectedKey string
		expectedDB  *sql.DB
	}{
		{
			desc:        "it should return the default db with no args",
			dsUID:       "uid1",
			args:        "",
			expectedKey: "uid1-default",
			expectedDB:  db,
		},
		{
			desc:        "it should return the cached connection for the given args",
			dsUID:       "uid1",
			args:        "foo",
			expectedKey: "uid1-foo",
			existingDB:  db2,
			expectedDB:  db2,
		},
		{
			desc:        "it should create a new connection with the given args",
			dsUID:       "uid1",
			args:        "foo",
			expectedKey: "uid1-foo",
			expectedDB:  db3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			ds := &SQLDatasource{c: d, EnableMultipleConnections: true}
			settings := backend.DataSourceInstanceSettings{UID: tt.dsUID}
			key := defaultKey(tt.dsUID)
			// Add the mandatory default db
			ds.storeDBConnection(key, dbConnection{db, settings})
			if tt.existingDB != nil {
				key = keyWithConnectionArgs(tt.dsUID, []byte(tt.args))
				ds.storeDBConnection(key, dbConnection{tt.existingDB, settings})
			}

			key, dbConn, err := ds.getDBConnectionFromQuery(&Query{ConnectionArgs: json.RawMessage(tt.args)}, tt.dsUID)
			if err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			if key != tt.expectedKey {
				t.Fatalf("unexpected cache key %s", key)
			}
			if dbConn.db != tt.expectedDB {
				t.Fatalf("unexpected result %v", dbConn.db)
			}
		})
	}

	t.Run("it should return an error if connection args are used without enabling multiple connections", func(t *testing.T) {
		ds := &SQLDatasource{c: d, EnableMultipleConnections: false}
		_, _, err := ds.getDBConnectionFromQuery(&Query{ConnectionArgs: json.RawMessage("foo")}, "dsUID")
		if err == nil || !errors.Is(err, MissingMultipleConnectionsConfig) {
			t.Errorf("expecting error: %v", MissingMultipleConnectionsConfig)
		}
	})

	t.Run("it should return an error if the default connection is missing", func(t *testing.T) {
		ds := &SQLDatasource{c: d}
		_, _, err := ds.getDBConnectionFromQuery(&Query{}, "dsUID")
		if err == nil || !errors.Is(err, MissingDBConnection) {
			t.Errorf("expecting error: %v", MissingDBConnection)
		}
	})
}

func Test_Dispose(t *testing.T) {
	t.Run("it should not delete connections", func(t *testing.T) {
		ds := &SQLDatasource{}
		ds.dbConnections.Store(defaultKey("uid1"), dbConnection{})
		ds.dbConnections.Store("foo", dbConnection{})
		ds.Dispose()
		count := 0
		ds.dbConnections.Range(func(key, value interface{}) bool {
			count++
			return true
		})
		if count != 2 {
			t.Errorf("missing connections")
		}
	})
}

func Test_timeout_retries(t *testing.T) {
	dsUID := "timeout"
	settings := backend.DataSourceInstanceSettings{UID: dsUID}

	handler := testSqlHandler{}
	mockDriver := "sqlmock"
	mock.RegisterDriver(mockDriver, handler)
	db, err := sql.Open(mockDriver, "")
	if err != nil {
		t.Errorf("failed to connect to mock driver: %v", err)
	}
	timeoutDriver := fakeDriver{
		openDBfn: func() (*sql.DB, error) { return db, nil },
	}
	retries := 5
	max := time.Duration(testTimeout) * time.Second
	driverSettings := DriverSettings{Retries: retries, Timeout: max, RetryOn: []string{"deadline"}}
	ds := &SQLDatasource{c: timeoutDriver, driverSettings: driverSettings}

	key := defaultKey(dsUID)
	// Add the mandatory default db
	ds.storeDBConnection(key, dbConnection{db, settings})
	ctx := context.Background()
	req := &backend.CheckHealthRequest{
		PluginContext: backend.PluginContext{
			DataSourceInstanceSettings: &settings,
		},
	}
	result, err := ds.CheckHealth(ctx, req)

	assert.Nil(t, err)
	assert.Equal(t, retries, testCounter)
	expected := context.DeadlineExceeded.Error()
	assert.Equal(t, expected, result.Message)
}

func Test_error_retries(t *testing.T) {
	testCounter = 0
	dsUID := "error"
	settings := backend.DataSourceInstanceSettings{UID: dsUID}

	handler := testSqlHandler{
		error: errors.New("foo"),
	}
	mockDriver := "sqlmock-error"
	mock.RegisterDriver(mockDriver, handler)

	timeoutDriver := fakeDriver{
		openDBfn: func() (*sql.DB, error) {
			db, err := sql.Open(mockDriver, "")
			if err != nil {
				t.Errorf("failed to connect to mock driver: %v", err)
			}
			return db, nil
		},
	}
	retries := 5
	max := time.Duration(10) * time.Second
	driverSettings := DriverSettings{Retries: retries, Timeout: max, Pause: 1, RetryOn: []string{"foo"}}
	ds := &SQLDatasource{c: timeoutDriver, driverSettings: driverSettings}

	key := defaultKey(dsUID)
	// Add the mandatory default db
	db, _ := timeoutDriver.Connect(settings, nil)
	ds.storeDBConnection(key, dbConnection{db, settings})
	ctx := context.Background()

	qry := `{ "rawSql": "foo" }`

	req := &backend.QueryDataRequest{
		PluginContext: backend.PluginContext{
			DataSourceInstanceSettings: &settings,
		},
		Queries: []backend.DataQuery{
			{
				RefID: "foo",
				JSON:  []byte(qry),
			},
		},
	}

	data, err := ds.QueryData(ctx, req)
	assert.Nil(t, err)
	assert.Equal(t, retries+1, testCounter)
	assert.NotNil(t, data.Responses)

}

var testCounter = 0
var testTimeout = 1
var testRows = 0

type testSqlHandler struct {
	mock.DBHandler
	error
}

func (s testSqlHandler) Ping(ctx context.Context) error {
	if s.error != nil {
		return s.error
	}
	testCounter++                              // track the retries for the test assertion
	time.Sleep(time.Duration(testTimeout + 1)) // simulate a connection delay
	select {
	case <-ctx.Done():
		fmt.Println(ctx.Err())
		return ctx.Err()
	}
}

func (s testSqlHandler) Query(args []driver.Value) (driver.Rows, error) {
	fmt.Println("query")
	if s.error != nil {
		testCounter++
		return s, s.error
	}
	return s, nil
}

func (s testSqlHandler) Columns() []string {
	return []string{"foo", "bar"}
}

func (s testSqlHandler) Next(dest []driver.Value) error {
	testRows++
	if testRows > 5 {
		return io.EOF
	}
	dest[0] = "foo"
	dest[1] = "bar"
	return nil
}

func (s testSqlHandler) Close() error {
	return nil
}
