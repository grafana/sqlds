package sqlds

import (
	"database/sql"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
)

type MockTimeoutDriver struct{}

// Connect connects to the database. It does not need to call `db.Ping()`
func (m *MockTimeoutDriver) Connect(_ backend.DataSourceInstanceSettings) (*sql.DB, error) {
	return nil, nil
}

func (m *MockTimeoutDriver) FillMode() *data.FillMissing {
	return nil
}

func (m *MockTimeoutDriver) Macros() Macros {
	return nil
}

func (m *MockTimeoutDriver) Converters() []sqlutil.Converter {
	return nil
}

func (m *MockTimeoutDriver) Timeout(backend.DataSourceInstanceSettings) time.Duration {
	return time.Second
}
