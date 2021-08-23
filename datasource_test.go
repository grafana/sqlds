package sqlds

import (
	"database/sql"
	"encoding/json"
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

func Test_getDB(t *testing.T) {
	db := &sql.DB{}
	db2 := &sql.DB{}
	d := &fakeDriver{db: db2}
	tests := []struct {
		desc       string
		args       string
		existingDB *sql.DB
		expectedDB *sql.DB
	}{
		{
			"it should return the default db with no args",
			defaultKey,
			db,
			db,
		},
		{
			"it should return the cached connection for the given args",
			"foo",
			db,
			db,
		},
		{
			"it should create a new connection with the given args",
			"foo",
			nil,
			db2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			ds := &sqldatasource{c: d, EnableMultipleConnections: true}
			if tt.existingDB != nil {
				ds.dbConnections.Store(tt.args, tt.existingDB)
			}
			if tt.args != defaultKey {
				// Add the mandatory default db
				ds.dbConnections.Store(defaultKey, db)
			}
			res, key, err := ds.getDB(&Query{ConnectionArgs: json.RawMessage(tt.args)})
			if err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			if key != tt.args {
				t.Fatalf("unexpected cache key %s", key)
			}
			if res != tt.expectedDB {
				t.Fatalf("unexpected result %v", res)
			}
		})
	}
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
		ds.dbConnections.Store(defaultKey, db1)
		ds.dbConnections.Store("foo", db2)

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
