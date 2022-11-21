package mock

import (
	"context"
	"database/sql/driver"
)

type sqlmock struct {
	drv *mockDriver
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
	return c.drv.handler.Ping(ctx)
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
	if stmt.conn.drv.handler != nil {
		return stmt.conn.drv.handler.Query(args)
	}
	return nil, nil
}

func (stmt *statement) Close() error {
	return nil
}

func (stmt *statement) NumInput() int {
	return -1
}

type rows struct {
	conn *sqlmock
}

func (r rows) Columns() []string {
	if r.conn.drv.handler != nil {
		return r.conn.drv.handler.Columns()
	}
	return []string{}
}

func (r rows) Close() error {
	return nil
}

func (r rows) Next(dest []driver.Value) error {
	if r.conn.drv.handler != nil {
		return r.conn.drv.handler.Next(dest)
	}
	return nil
}
