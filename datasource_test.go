package sqlds

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

type fakeDriver struct {
	db *sql.DB

	Driver
}

func (d *fakeDriver) Connect(backend.DataSourceInstanceSettings, json.RawMessage) (db *sql.DB, err error) {
	return d.db, nil
}

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
			ds := &sqldatasource{c: d, EnableMultipleConnections: true}
			settings := backend.DataSourceInstanceSettings{UID: tt.dsUID}
			key := defaultKey(tt.dsUID)
			// Add the mandatory default db
			ds.storeDBConnection(key, dbConnection{db: db, settings: settings})
			if tt.existingDB != nil {
				key = keyWithConnectionArgs(tt.dsUID, []byte(tt.args))
				ds.storeDBConnection(key, dbConnection{db: tt.existingDB, settings: settings})
			}

			key, dbConn, err := ds.getDBConnectionFromConnArgs(tt.dsUID, json.RawMessage(tt.args))
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
		ds := &sqldatasource{c: d, EnableMultipleConnections: false}
		_, _, err := ds.getDBConnectionFromConnArgs("dsUID", json.RawMessage("foo"))
		if err == nil || !errors.Is(err, MissingMultipleConnectionsConfig) {
			t.Errorf("expecting error: %v", MissingMultipleConnectionsConfig)
		}
	})

	t.Run("it should return an error if the default connection is missing", func(t *testing.T) {
		ds := &sqldatasource{c: d}
		_, _, err := ds.getDBConnectionFromConnArgs("dsUID", json.RawMessage{})
		if err == nil || !errors.Is(err, MissingDBConnection) {
			t.Errorf("expecting error: %v", MissingDBConnection)
		}
	})
}

func Test_Dispose(t *testing.T) {
	t.Run("it should not delete connections", func(t *testing.T) {
		ds := &sqldatasource{}
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
