package sqlds

import (
	"database/sql"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
)

// Driver is a simple interface that defines how to connect to a backend SQL datasource
// Plugin creators will need to implement this in order to create a managed datasource
type Driver interface {
	// Connect connects to the database. It does not need to call `db.Ping()`
	Connect(backend.DataSourceInstanceSettings) (*sql.DB, error)
	FillMode() *data.FillMissing
	Macros() Macros
}
