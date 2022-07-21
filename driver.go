package sqlds

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
)

type DriverSettings struct {
	Timeout  time.Duration
	FillMode *data.FillMissing
}

type QueryStatus uint32

const (
	QueryUnknown QueryStatus = iota
	QuerySubmitted
	QueryRunning
	QueryFinished
	QueryCanceled
	QueryFailed
)

func (qs QueryStatus) Finished() bool {
	return qs == QueryCanceled || qs == QueryFailed || qs == QueryFinished
}

func (qs QueryStatus) String() string {
	switch qs {
	case QuerySubmitted:
		return "submitted"
	case QueryRunning:
		return "running"
	case QueryFinished:
		return "finished"
	case QueryCanceled:
		return "canceled"
	case QueryFailed:
		return "failed"
	default:
		return "unknown"
	}
}

type ExecuteQueryStatus struct {
	ID       string
	Finished bool
	State    string
}

// Driver is a simple interface that defines how to connect to a backend SQL datasource
// Plugin creators will need to implement this in order to create a managed datasource
type Driver interface {
	// Connect connects to the database. It does not need to call `db.Ping()`
	Connect(backend.DataSourceInstanceSettings, json.RawMessage) (*sql.DB, error)
	// Settings are read whenever the plugin is initialized, or after the data source settings are updated
	Settings(backend.DataSourceInstanceSettings) DriverSettings
	Macros() Macros
	Converters() []sqlutil.Converter
}

type AsyncDB interface {
	// DB generic methods
	driver.Conn
	Ping(ctx context.Context) error

	// Async flow
	StartQuery(ctx context.Context, query string, args ...interface{}) (string, error)
	GetQueryID(ctx context.Context, query string, args ...interface{}) (bool, string, error)
	QueryStatus(ctx context.Context, queryID string) (QueryStatus, error)
	CancelQuery(ctx context.Context, queryID string) error
	GetRows(ctx context.Context, queryID string) (driver.Rows, error)
}

type AsyncDBGetter interface {
	GetAsyncDB(backend.DataSourceInstanceSettings, json.RawMessage) (AsyncDB, error)
}

// Connection represents a SQL connection and is satisfied by the *sql.DB type
// For now, we only add the functions that we need / actively use. Some other candidates for future use could include the ExecContext and BeginTxContext functions
type Connection interface {
	Close() error
	Ping() error
	PingContext(ctx context.Context) error
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
}
