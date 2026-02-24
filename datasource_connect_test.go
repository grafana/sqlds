package sqlds

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
)

type fakeDriver struct {
	openDBfn func(msg json.RawMessage) (*sql.DB, error)

	Driver
}

func (d fakeDriver) Connect(_ context.Context, _ backend.DataSourceInstanceSettings, msg json.RawMessage) (db *sql.DB, err error) {
	return d.openDBfn(msg)
}

func (d fakeDriver) Macros() Macros {
	return Macros{}
}

func (d fakeDriver) Converters() []sqlutil.Converter {
	return []sqlutil.Converter{}
}

type fakeSQLConnector struct{}

func (f fakeSQLConnector) Connect(_ context.Context) (driver.Conn, error) {
	return nil, nil
}

func (f fakeSQLConnector) Driver() driver.Driver {
	return nil
}

func Test_getDBConnectionFromQuery(t *testing.T) {
	db := &sql.DB{}
	db2 := &sql.DB{}
	db3 := &sql.DB{}
	d := &fakeDriver{openDBfn: func(msg json.RawMessage) (*sql.DB, error) { return db3, nil }}
	tests := []struct {
		existingDB  *sql.DB
		expectedDB  *sql.DB
		desc        string
		dsUID       string
		args        string
		expectedKey string
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
			expectedKey: "uid1-2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae",
			existingDB:  db2,
			expectedDB:  db2,
		},
		{
			desc:        "it should create a new connection with the given args",
			dsUID:       "uid1",
			args:        "foo",
			expectedKey: "uid1-2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae",
			expectedDB:  db3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			conn := &Connector{UID: tt.dsUID, driver: d, enableMultipleConnections: true, driverSettings: DriverSettings{}}
			settings := backend.DataSourceInstanceSettings{UID: tt.dsUID}
			key := datasourceCacheKey(tt.dsUID, nil)
			// Add the mandatory default db
			conn.storeDBConnection(key, dbConnection{db, settings})
			if tt.existingDB != nil {
				key = datasourceCacheKey(tt.dsUID, []byte(tt.args))
				conn.storeDBConnection(key, dbConnection{tt.existingDB, settings})
			}

			key, dbConn, err := conn.GetConnectionFromQuery(context.Background(), &Query{ConnectionArgs: json.RawMessage(tt.args)})
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
}

func Test_Dispose(t *testing.T) {
	t.Run("it should close connections", func(t *testing.T) {
		db := sql.OpenDB(fakeSQLConnector{})
		d := &fakeDriver{openDBfn: func(msg json.RawMessage) (*sql.DB, error) { return db, nil }}
		conn := &Connector{driver: d}
		ds := &SQLDatasource{connector: conn}
		conn.connections.Store("uid1", dbConnection{db: db})
		conn.connections.Store("foo", dbConnection{db: db})
		ds.Dispose()
		count := 0
		conn.connections.Range(func(key, value interface{}) bool {
			count++
			return true
		})
		if count != 0 {
			t.Errorf("did not close all connections")
		}
	})
}
