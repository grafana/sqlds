package sqlds

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/sqlds/v2/mock"
	"github.com/stretchr/testify/assert"
)

type fakeDriver struct {
	db *sql.DB

	Driver
}

func (d fakeDriver) Connect(backend.DataSourceInstanceSettings, json.RawMessage) (db *sql.DB, err error) {
	return d.db, nil
}

// func (d fakeDriver) Settings(backend.DataSourceInstanceSettings) DriverSettings

func Test_getDBConnectionFromQuery(t *testing.T) {
	db := &sql.DB{}
	db2 := &sql.DB{}
	db3 := &sql.DB{}
	d := &fakeDriver{db: db3}
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

func Test_retries(t *testing.T) {
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
		db: db,
	}
	retries := 5
	max := time.Duration(testTimeout) * time.Second
	driverSettings := DriverSettings{Retries: retries, Timeout: max}
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

var testCounter = 0
var testTimeout = 1

type testSqlHandler struct {
	mock.DBHandler
}

func (s testSqlHandler) Ping(ctx context.Context) error {
	testCounter++                              // track the retries for the test assertion
	time.Sleep(time.Duration(testTimeout + 1)) // simulate a connection delay
	select {
	case <-ctx.Done():
		fmt.Println(ctx.Err())
		return ctx.Err()
	}
}
