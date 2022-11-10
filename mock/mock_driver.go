package mock

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"sync"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

var pool *mockDriver

func RegisterDriver(name string, handler DBHandler) *mockDriver {
	pool = &mockDriver{
		conns:   make(map[string]*sqlmock),
		handler: handler,
	}
	sql.Register(name, pool)
	return pool
}

type DBHandler interface {
	Ping(ctx context.Context) error
	Query(args []driver.Value) (driver.Rows, error)
	Columns() []string
	Next(dest []driver.Value) error
}

type mockDriver struct {
	sync.Mutex
	conns   map[string]*sqlmock
	handler DBHandler
}

func (d *mockDriver) Open(dsn string) (driver.Conn, error) {
	if len(d.conns) == 0 {
		mock := &sqlmock{
			drv: d,
		}
		d.conns = map[string]*sqlmock{
			dsn: mock,
		}
	}
	return d.conns[dsn], nil
}

func (d *mockDriver) Connect(backend.DataSourceInstanceSettings, json.RawMessage) (db *sql.DB, err error) {
	return nil, errors.New("context deadline exceeded")
}
