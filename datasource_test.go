package sqlds

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
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
		dsID        int64
		args        string
		existingDB  *sql.DB
		expectedKey string
		expectedDB  *sql.DB
	}{
		{
			desc:        "it should return the default db with no args",
			dsID:        1,
			args:        "",
			expectedKey: "1-default",
			expectedDB:  db,
		},
		{
			desc:        "it should return the cached connection for the given args",
			dsID:        1,
			args:        "foo",
			expectedKey: "1-foo",
			existingDB:  db2,
			expectedDB:  db2,
		},
		{
			desc:        "it should create a new connection with the given args",
			dsID:        1,
			args:        "foo",
			expectedKey: "1-foo",
			expectedDB:  db3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			ds := &sqldatasource{c: d, EnableMultipleConnections: true}
			settings := backend.DataSourceInstanceSettings{ID: tt.dsID}
			key := defaultKey(tt.dsID)
			// Add the mandatory default db
			ds.storeDBConnection(key, dbConnection{db, settings})
			if tt.existingDB != nil {
				key = keyWithConnectionArgs(tt.dsID, []byte(tt.args))
				ds.storeDBConnection(key, dbConnection{tt.existingDB, settings})
			}

			key, dbConn, err := ds.getDBConnectionFromQuery(&Query{DatasourceID: tt.dsID, ConnectionArgs: json.RawMessage(tt.args)})
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
		_, _, err := ds.getDBConnectionFromQuery(&Query{ConnectionArgs: json.RawMessage("foo")})
		if err == nil || !errors.Is(err, MissingMultipleConnectionsConfig) {
			t.Errorf("expecting error: %v", MissingMultipleConnectionsConfig)
		}
	})

	t.Run("it should return an error if the default connection is missing", func(t *testing.T) {
		ds := &sqldatasource{c: d}
		_, _, err := ds.getDBConnectionFromQuery(&Query{})
		if err == nil || !errors.Is(err, MissingDBConnection) {
			t.Errorf("expecting error: %v", MissingDBConnection)
		}
	})
}

func Test_Dispose(t *testing.T) {
	t.Run("it should close all db connections", func(t *testing.T) {
		db1, mock1, err := sqlmock.New()
		if err != nil {
			t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
		}
		db2, mock2, err := sqlmock.New()
		if err != nil {
			t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
		}

		ds := &sqldatasource{}
		ds.dbConnections.Store(defaultKey(1), dbConnection{db: db1})
		ds.dbConnections.Store("foo", dbConnection{db: db2})

		mock1.ExpectClose()
		mock2.ExpectClose()
		ds.Dispose()

		err = mock1.ExpectationsWereMet()
		if err != nil {
			t.Error(err)
		}
		err = mock2.ExpectationsWereMet()
		if err != nil {
			t.Error(err)
		}

		ds.dbConnections.Range(func(key, value interface{}) bool {
			t.Errorf("db connections were not deleted")
			return false
		})
	})
}
