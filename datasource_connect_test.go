package sqlds

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

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
		desc       string
		dsUID      string
		tsUpdated  time.Time
		args       string
		existingDB *sql.DB
		expectedDB *sql.DB
	}{
		{
			desc:       "it should return the default db with no args",
			dsUID:      "uid1",
			args:       "",
			expectedDB: db,
		},
		{
			desc:       "it should return the cached connection for the given args",
			dsUID:      "uid1",
			args:       "foo",
			existingDB: db2,
			expectedDB: db2,
		},
		{
			desc:       "it should create a new connection with the given args",
			dsUID:      "uid1",
			args:       "foo",
			expectedDB: db3,
		},
		{
			desc:       "it should create a new connection if the updated time changes",
			dsUID:      "uid1",
			args:       "foo",
			tsUpdated:  time.Now(),
			existingDB: db2,
			expectedDB: db3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			ds := &SQLDatasource{c: d, EnableMultipleConnections: true}
			settings := backend.DataSourceInstanceSettings{UID: tt.dsUID}
			// Add the mandatory default db
			ds.storeDBConnection(dbConnection{db, settings}, nil)
			if tt.existingDB != nil {
				ds.storeDBConnection(dbConnection{tt.existingDB, settings}, []byte(tt.args))
			}
			if !tt.tsUpdated.IsZero() {
				settings.Updated = tt.tsUpdated
			}

			dbConn, err := ds.getDBConnectionFromArgs(context.Background(), json.RawMessage(tt.args), settings)
			if err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			if dbConn.db != tt.expectedDB {
				t.Fatalf("unexpected result %v", dbConn.db)
			}
		})
	}

	t.Run("it should return an error if connection args are used without enabling multiple connections", func(t *testing.T) {
		ds := &SQLDatasource{c: d, EnableMultipleConnections: false}
		settings := backend.DataSourceInstanceSettings{UID: "dsUID"}
		_, err := ds.getDBConnectionFromArgs(context.Background(), json.RawMessage("foo"), settings)
		if err == nil || !errors.Is(err, ErrorMissingMultipleConnectionsConfig) {
			t.Errorf("expecting error: %v", ErrorMissingMultipleConnectionsConfig)
		}
	})

	t.Run("it should return an error if the default connection is missing", func(t *testing.T) {
		ds := &SQLDatasource{c: d}
		settings := backend.DataSourceInstanceSettings{UID: "dsUID"}
		_, err := ds.getDBConnectionFromArgs(context.Background(), nil, settings)
		if err == nil || !errors.Is(err, ErrorMissingDBConnection) {
			t.Errorf("expecting error: %v", ErrorMissingDBConnection)
		}
	})
}

func Test_Dispose(t *testing.T) {
	t.Run("it should not delete connections", func(t *testing.T) {
		ds := &SQLDatasource{}
		settings := backend.DataSourceInstanceSettings{UID: "uid1"}
		ds.storeDBConnection(dbConnection{settings: settings}, nil)
		settings = backend.DataSourceInstanceSettings{UID: "foo"}
		ds.storeDBConnection(dbConnection{settings: settings}, nil)
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
