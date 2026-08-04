package sqlds

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
)

type deferDriver struct {
	connectErr error
	db         *sql.DB
	calls      int
}

func (d *deferDriver) Connect(_ context.Context, _ backend.DataSourceInstanceSettings, _ json.RawMessage) (*sql.DB, error) {
	d.calls++
	if d.connectErr != nil {
		return nil, d.connectErr
	}
	return d.db, nil
}
func (d *deferDriver) Settings(context.Context, backend.DataSourceInstanceSettings) DriverSettings {
	return DriverSettings{}
}
func (d *deferDriver) Macros() Macros                  { return Macros{} }
func (d *deferDriver) Converters() []sqlutil.Converter { return nil }

func TestNewConnector_SoftFailsBootstrapConnect(t *testing.T) {
	d := &deferDriver{connectErr: errors.New("trying to use non-allowed auth method default")}
	conn, err := NewConnector(context.Background(), d, backend.DataSourceInstanceSettings{UID: "uid"}, false)
	if err != nil {
		t.Fatalf("expected NewConnector to succeed with deferred connect, got %v", err)
	}
	cached, ok := conn.getDBConnection(defaultKey("uid"))
	if !ok {
		t.Fatal("expected settings stub under default key")
	}
	if cached.db != nil {
		t.Fatal("expected nil db when bootstrap Connect failed")
	}
	if cached.settings.UID != "uid" {
		t.Fatalf("expected settings preserved, got %+v", cached.settings)
	}
}

func TestGetConnectionFromQuery_ConnectsWhenBootstrapDeferred(t *testing.T) {
	live := &sql.DB{}
	d := &deferDriver{connectErr: errors.New("not ready yet")}
	conn, err := NewConnector(context.Background(), d, backend.DataSourceInstanceSettings{UID: "uid"}, false)
	if err != nil {
		t.Fatal(err)
	}
	// First Connect failed; clear error so on-demand succeeds
	d.connectErr = nil
	d.db = live

	_, cached, err := conn.GetConnectionFromQuery(context.Background(), &Query{})
	if err != nil {
		t.Fatalf("expected on-demand connect, got %v", err)
	}
	if cached.db != live {
		t.Fatalf("expected live db, got %v", cached.db)
	}
	if d.calls < 2 {
		t.Fatalf("expected bootstrap + on-demand Connect calls, got %d", d.calls)
	}
}
