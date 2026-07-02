package sqlds_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
	"github.com/grafana/sqlds/v5"
	"github.com/grafana/sqlds/v5/mock"
	"github.com/grafana/sqlds/v5/test"
)

// toggleHandler is a mock DB handler whose queries succeed until failQueries is
// flipped, at which point every query returns a retryable error. This lets a
// test exercise the happy path before inducing the reconnect failure.
type toggleHandler struct {
	*test.SqlHandler
	mu          sync.Mutex
	failQueries bool
}

func (h *toggleHandler) setFailing(v bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.failQueries = v
}

func (h *toggleHandler) Query(args []driver.Value) (driver.Rows, error) {
	h.mu.Lock()
	fail := h.failQueries
	h.mu.Unlock()
	if fail {
		return nil, errors.New("retry me")
	}
	return h.SqlHandler.Query(args)
}

// reconnectingDriver is a sqlds.Driver whose Connect succeeds for the initial
// bootstrap connection but fails on every subsequent call. This mirrors the
// production failure mode where a datasource's live connection is fine, but a
// re-connect attempt (triggered by a retryable query error) cannot reach the
// server — e.g. an auth token that can no longer be refreshed.
type reconnectingDriver struct {
	driverName string
	mu         sync.Mutex
	connects   int
}

func (d *reconnectingDriver) Connect(_ context.Context, _ backend.DataSourceInstanceSettings, _ json.RawMessage) (*sql.DB, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.connects++
	if d.connects == 1 {
		// Bootstrap connection (created by NewConnector) succeeds.
		return sql.Open(d.driverName, "")
	}
	// Every reconnect fails to reach the server.
	return nil, errors.New("cannot reach server")
}

func (d *reconnectingDriver) Settings(ctx context.Context, config backend.DataSourceInstanceSettings) sqlds.DriverSettings {
	settings, _ := test.LoadSettings(ctx, config)
	return settings
}

func (d *reconnectingDriver) Macros() sqlds.Macros            { return nil }
func (d *reconnectingDriver) Converters() []sqlutil.Converter { return nil }

// Test_reconnect_failure_does_not_close_shared_connection makes sure that
// when a query triggers a reconnect and that reconnect fails, the underlying
// database is not closed unless a new database connection can be made. Before
// the associated changes in connector.go/Reconnect, in this case the datasource
// would be left unusable as all subsequent queries would encounter an
// "sql: database is closed" error.
func Test_reconnect_failure_does_not_close_shared_connection(t *testing.T) {
	driverName := "reconnect-poison"

	// The handler serves one row on the happy path; once flipped to failing it
	// returns a retryable error, so handleQuery enters its reconnect-and-retry
	// path.
	base := test.NewDriverHandler(test.Data{
		Cols: []test.Column{{Name: "n", Kind: int64(0)}},
		Rows: [][]any{{int64(1)}},
	}, test.DriverOpts{})
	handler := &toggleHandler{SqlHandler: &base}
	mock.RegisterDriver(driverName, handler)

	drv := &reconnectingDriver{driverName: driverName}
	ds := sqlds.NewDatasource(drv)

	cfg := `{ "timeout": 0, "retries": 1, "retryOn": ["retry me"] }`
	settings := backend.DataSourceInstanceSettings{UID: driverName, JSONData: []byte(cfg)}
	if _, err := ds.NewDatasource(context.Background(), settings); err != nil {
		t.Fatalf("failed to create datasource: %v", err)
	}

	newReq := func() *backend.QueryDataRequest {
		return &backend.QueryDataRequest{
			PluginContext: backend.PluginContext{DataSourceInstanceSettings: &settings},
			Queries: []backend.DataQuery{
				{RefID: "foo", JSON: []byte(`{ "rawSql": "select 1" }`)},
			},
		}
	}

	// Happy path: the datasource works before anything goes wrong.
	happy, err := ds.QueryData(context.Background(), newReq())
	if err != nil {
		t.Fatalf("unexpected top-level error on happy-path query: %v", err)
	}
	if happyErr := happy.Responses["foo"].Error; happyErr != nil {
		t.Fatalf("expected happy-path query to succeed, got: %v", happyErr)
	}

	// Now the server becomes unreachable for queries, triggering reconnects.
	handler.setFailing(true)

	// First failing query triggers a reconnect, which fails to reach the server.
	first, err := ds.QueryData(context.Background(), newReq())
	if err != nil {
		t.Fatalf("unexpected top-level error on first query: %v", err)
	}
	if first.Responses["foo"].Error == nil {
		t.Fatal("expected first query to error (reconnect failed)")
	}

	// Second query: the shared connection should still be usable. The bug closes
	// it during the first query's failed reconnect, so this comes back as
	// "sql: database is closed" and stays that way forever.
	second, err := ds.QueryData(context.Background(), newReq())
	if err != nil {
		t.Fatalf("unexpected top-level error on second query: %v", err)
	}
	secondErr := second.Responses["foo"].Error
	if secondErr == nil {
		t.Fatal("expected second query to error")
	}
	if strings.Contains(secondErr.Error(), "database is closed") {
		t.Fatalf("connection was poisoned by the failed reconnect: %v", secondErr)
	}
}
