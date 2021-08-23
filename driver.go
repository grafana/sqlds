package sqlds

import (
	"database/sql"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
)

type DriverSettings struct {
	Timeout  time.Duration
	FillMode *data.FillMissing
}

// Driver is a simple interface that defines how to connect to a backend SQL datasource
// Plugin creators will need to implement this in order to create a managed datasource
type Driver interface {
	// Connect connects to the database. It does not need to call `db.Ping()`
	Connect(backend.DataSourceInstanceSettings) (*sql.DB, error)
	// Settings are read whenever the plugin is initialized, or after the data source settings are updated
	Settings(backend.DataSourceInstanceSettings) DriverSettings
	Macros() Macros
	Converters() []sqlutil.Converter
}
