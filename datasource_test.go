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
	d := &fakeDriver{db: db}
	tests := []struct {
		desc string
		args string
		ds   *sqldatasource
		db   *sql.DB
	}{
		{
			"it should return the default db wiht no args",
			"",
			&sqldatasource{
				dbConnections: map[string]*sql.DB{
					"": db,
				},
			},
			db,
		},
		{
			"it should return the cached connection for the given args",
			"foo",
			&sqldatasource{
				dbConnections: map[string]*sql.DB{
					"foo": db,
				},
			},
			db,
		},
		{
			"it should create a new connection with the given args",
			"foo",
			&sqldatasource{
				c:             d,
				dbConnections: map[string]*sql.DB{},
			},
			db,
		},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			res, key, err := tt.ds.getDB(&Query{Args: json.RawMessage(tt.args)})
			if err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			if key != tt.args {
				t.Fatalf("unexpected cache key %s", key)
			}
			if res != tt.db {
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

		ds := &sqldatasource{
			dbConnections: map[string]*sql.DB{
				"":    db1,
				"foo": db2,
			},
		}
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

		if len(ds.dbConnections) != 0 {
			t.Errorf("db connections were not deleted: %v", ds.dbConnections)
		}
	})
}
