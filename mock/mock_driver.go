package mock

import (
	"database/sql"
	"database/sql/driver"
	"sync"
)

var pool *mockDriver

func init() {
	pool = &mockDriver{
		conns: make(map[string]*sqlmock),
	}
	sql.Register("sqlmock", pool)
}

type mockDriver struct {
	sync.Mutex
	counter int
	conns   map[string]*sqlmock
}

func (d *mockDriver) Open(dsn string) (driver.Conn, error) {
	if len(d.conns) == 0 {
		mock := &sqlmock{
			drv:   d,
			sleep: 10,
		}
		d.conns = map[string]*sqlmock{
			dsn: mock,
		}
	}
	return d.conns[dsn], nil
}
