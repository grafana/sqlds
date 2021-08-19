package sqlds

import (
	"database/sql"
	"encoding/json"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
)

// Driver is a simple interface that defines how to connect to a backend SQL datasource
// Plugin creators will need to implement this in order to create a managed datasource
type Driver interface {
	// Connect connects to the database. It does not need to call `db.Ping()`
	// Optionally, it receives a JSON object with query arguments to configure the connection
	Connect(backend.DataSourceInstanceSettings, json.RawMessage) (*sql.DB, error)
	FillMode() *data.FillMissing
	Macros() Macros
	Converters() []sqlutil.Converter
}
