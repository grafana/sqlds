package sqlds

import (
	"database/sql"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
)

// Driver is a simple interface that defines how to connect to a backend SQL datasource
// Plugin creators will need to implement this in order to create a managed datasource
type Driver interface {
	// Connect connects to the database. It does not need to call `db.Ping()`
	Connect(backend.DataSourceInstanceSettings) (*sql.DB, error)
	Timeout(backend.DataSourceInstanceSettings) time.Duration
	FillMode() *data.FillMissing
	Macros() Macros
	Converters() []sqlutil.Converter
	// Timeout is used when sending a query to the backing data source. If the timeout is exceeded, the QueryData function will return an error.
}
