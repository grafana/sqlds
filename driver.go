package sqlds

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
)

type DriverSettings struct {
	Timeout  time.Duration
	FillMode *data.FillMissing
	Retries  int
	Pause    int
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

// Connection represents a SQL connection and is satisfied by the *sql.DB type
// For now, we only add the functions that we need / actively use. Some other candidates for future use could include the ExecContext and BeginTxContext functions
type Connection interface {
	Close() error
	Ping() error
	PingContext(ctx context.Context) error
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
}

// QueryMutator is an additional interface that could be implemented by driver.
// This adds ability to the driver it can mutate query before run.
type QueryMutator interface {
	MutateQuery(ctx context.Context, req backend.DataQuery) (context.Context, backend.DataQuery)
}
