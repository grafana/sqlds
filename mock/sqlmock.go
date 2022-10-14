package mock

import (
	"context"
	"database/sql/driver"
	"time"
)

type sqlmock struct {
	drv   *mockDriver
	sleep int
}

// Begin meets http://golang.org/pkg/database/sql/driver/#Conn interface
func (c *sqlmock) Begin() (driver.Tx, error) {
	return c, nil
}

// Prepare meets http://golang.org/pkg/database/sql/driver/#Conn interface
func (c *sqlmock) Prepare(query string) (driver.Stmt, error) {
	return &statement{c, query}, nil
}

// Prepare meets http://golang.org/pkg/database/sql/driver/#Conn interface
func (c *sqlmock) Commit() error {
	return nil
}

// Prepare meets http://golang.org/pkg/database/sql/driver/#Conn interface
func (c *sqlmock) Rollback() error {
	return nil
}

// Prepare meets http://golang.org/pkg/database/sql/driver/#Conn interface
func (c *sqlmock) Close() error {
	return nil
}

func (c *sqlmock) Ping(ctx context.Context) error {
	// so we can test timeout retries
	if c.sleep > 0 {
		v := c.sleep
		next := float64(v) * .5
		c.sleep = int(next)
		time.Sleep(time.Duration(v) * time.Second)
	}
	return nil
}

// statement
type statement struct {
	conn  *sqlmock
	query string
}

func (stmt *statement) Exec(args []driver.Value) (driver.Result, error) {
	return nil, nil
}

func (stmt *statement) Query(args []driver.Value) (driver.Rows, error) {
	return nil, nil
}

func (stmt *statement) Close() error {
	return nil
}

func (stmt *statement) NumInput() int {
	return -1
}
