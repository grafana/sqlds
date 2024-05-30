package sqlds

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

func Test_getDBConnectionFromQuery(t *testing.T) {
	db := &sql.DB{}
	db2 := &sql.DB{}
	db3 := &sql.DB{}
	d := &fakeDriver{openDBfn: func(msg json.RawMessage) (*sql.DB, error) { return db3, nil }}
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
			conn := &Connector{UID: tt.dsUID, driver: d, enableMultipleConnections: true, driverSettings: DriverSettings{}}
			settings := backend.DataSourceInstanceSettings{UID: tt.dsUID}
			key := defaultKey(tt.dsUID)
			// Add the mandatory default db
			conn.storeDBConnection(key, dbConnection{db, settings})
			if tt.existingDB != nil {
				key = keyWithConnectionArgs(tt.dsUID, []byte(tt.args))
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

	t.Run("it should return an error if connection args are used without enabling multiple connections", func(t *testing.T) {
		conn := &Connector{driver: d, enableMultipleConnections: false}
		_, _, err := conn.GetConnectionFromQuery(context.Background(), &Query{ConnectionArgs: json.RawMessage("foo")})
		if err == nil || !errors.Is(err, MissingMultipleConnectionsConfig) {
			t.Errorf("expecting error: %v", MissingMultipleConnectionsConfig)
		}
	})

	t.Run("it should return an error if the default connection is missing", func(t *testing.T) {
		conn := &Connector{driver: d}
		_, _, err := conn.GetConnectionFromQuery(context.Background(), &Query{})
		if err == nil || !errors.Is(err, MissingDBConnection) {
			t.Errorf("expecting error: %v", MissingDBConnection)
		}
	})
}

func Test_Dispose(t *testing.T) {
	t.Run("it should not delete connections", func(t *testing.T) {
		conn := &Connector{}

		ds := &SQLDatasource{connector: conn}
		conn.connections.Store(defaultKey("uid1"), dbConnection{})
		conn.connections.Store("foo", dbConnection{})
		ds.Dispose()
		count := 0
		conn.connections.Range(func(key, value interface{}) bool {
			count++
			return true
		})
		if count != 2 {
			t.Errorf("missing connections")
		}
	})
}
