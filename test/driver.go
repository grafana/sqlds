package test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"reflect"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
	"github.com/grafana/sqlds/v2"
	"github.com/grafana/sqlds/v2/mock"
)

var registered = map[string]*SqlHandler{}

// NewDriver creates and registers a new test datasource driver
func NewDriver(name string, dbdata Data, converters []sqlutil.Converter, opts DriverOpts) (TestDS, *SqlHandler) {
	if registered[name] == nil {
		handler := NewDriverHandler(dbdata, opts)
		registered[name] = &handler
		mock.RegisterDriver(name, &handler)
	}

	return NewTestDS(
		func() (*sql.DB, error) {
			return sql.Open(name, "")
		},
		converters,
	), registered[name]
}

// NewTestDS creates a new test datasource driver
func NewTestDS(openDBfn func() (*sql.DB, error), converters []sqlutil.Converter) TestDS {
	return TestDS{
		openDBfn:   openDBfn,
		converters: converters,
	}
}

// NewDriverHandler creates a new driver handler
func NewDriverHandler(data Data, opts DriverOpts) SqlHandler {
	return SqlHandler{
		Data: data,
		Opts: opts,
	}
}

// SqlHandler handles driver functions
type SqlHandler struct {
	mock.DBHandler
	Data  Data
	Opts  DriverOpts
	State State
	row   int
}

// Ping represents a database ping
func (s *SqlHandler) Ping(ctx context.Context) error {
	s.State.ConnectAttempts += 1
	if s.Opts.ConnectDelay > 0 {
		time.Sleep(time.Duration(s.Opts.ConnectDelay * int(time.Second))) // simulate a connection delay
	}
	if s.Opts.ConnectError != nil {
		return s.Opts.ConnectError
	}
	return nil
}

// Query represents a database query
func (s *SqlHandler) Query(args []driver.Value) (driver.Rows, error) {
	s.State.QueryAttempts += 1
	if s.Opts.QueryDelay > 0 {
		time.Sleep(time.Duration(s.Opts.QueryDelay * int(time.Second))) // simulate a query delay
	}
	s.row = 0
	// only show the error if we have not exceeded the fail times and the error is not nil
	if s.Opts.QueryError != nil && (s.Opts.QueryFailTimes == 0 || s.State.QueryAttempts <= s.Opts.QueryFailTimes) {
		return s, s.Opts.QueryError
	}

	return s, nil
}

// Columns represents columns from a query
func (s *SqlHandler) Columns() []string {
	var cols []string
	for _, c := range s.Data.Cols {
		cols = append(cols, c.Name)
	}
	return cols
}

// Next iterates over rows
func (s *SqlHandler) Next(dest []driver.Value) error {
	if s.row+1 > len(s.Data.Rows) {
		return io.EOF
	}

	s.row++
	for _, row := range s.Data.Rows {
		for i, col := range row {
			dest[i] = col
		}
	}
	return nil
}

// Close implements the database Close interface
func (s SqlHandler) Close() error {
	return nil
}

// ColumnTypeScanType returns the scan type for the column
func (s SqlHandler) ColumnTypeScanType(index int) reflect.Type {
	kind := s.Data.Cols[index].Kind
	return reflect.TypeOf(kind)
}

// ColumnTypeDatabaseTypeName returns the database type for the column
func (s SqlHandler) ColumnTypeDatabaseTypeName(index int) string {
	return s.Data.Cols[index].DataType
}

// Data - the columns/rows
type Data struct {
	Cols []Column
	Rows [][]any
}

// Column - the column meta
type Column struct {
	Name     string
	Kind     any
	DataType string
}

// TestDS ...
type TestDS struct {
	openDBfn   func() (*sql.DB, error)
	converters []sqlutil.Converter
	sqlds.Driver
}

// Open - opens the test database
func (s TestDS) Open() (*sql.DB, error) {
	return s.openDBfn()
}

// DriverOpts the optional settings
type DriverOpts struct {
	ConnectDelay   int
	QueryDelay     int
	ConnectError   error
	QueryError     error
	QueryFailTimes int
}

// State is the state of the connections/queries
type State struct {
	QueryAttempts   int
	ConnectAttempts int
}
