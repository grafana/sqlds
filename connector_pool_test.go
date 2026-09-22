package sqlds

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/config"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
)

// poolConn is an inert but functional driver.Conn, so tests can open and
// release real pooled connections and read the result back from sql.DBStats.
type poolConn struct{}

func (poolConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not implemented") }
func (poolConn) Close() error                        { return nil }
func (poolConn) Begin() (driver.Tx, error)           { return nil, errors.New("not implemented") }

type poolConnector struct{}

func (poolConnector) Connect(context.Context) (driver.Conn, error) { return poolConn{}, nil }
func (poolConnector) Driver() driver.Driver                        { return nil }

// poolDriver is a sqlds Driver. boundInConnect > 0 makes Connect cap the
// pool itself, standing in for drivers that call db.SetMaxOpenConns.
type poolDriver struct {
	settings       DriverSettings
	boundInConnect int
	connectErr     error
}

func (d *poolDriver) Connect(context.Context, backend.DataSourceInstanceSettings, json.RawMessage) (*sql.DB, error) {
	if d.connectErr != nil {
		return nil, d.connectErr
	}
	db := sql.OpenDB(poolConnector{})
	if d.boundInConnect > 0 {
		db.SetMaxOpenConns(d.boundInConnect)
	}
	return db, nil
}
func (d *poolDriver) Settings(context.Context, backend.DataSourceInstanceSettings) DriverSettings {
	return d.settings
}
func (d *poolDriver) Macros() Macros                  { return Macros{} }
func (d *poolDriver) Converters() []sqlutil.Converter { return nil }

func grafanaPoolCtx(open, idle, lifetimeSeconds string) context.Context {
	return config.WithGrafanaConfig(context.Background(), config.NewGrafanaCfg(map[string]string{
		config.SQLRowLimit:                      "1000000",
		config.SQLMaxOpenConnsDefault:           open,
		config.SQLMaxIdleConnsDefault:           idle,
		config.SQLMaxConnLifetimeSecondsDefault: lifetimeSeconds,
	}))
}

func bootstrapDB(t *testing.T, conn *Connector) *sql.DB {
	t.Helper()
	cached, ok := conn.getDBConnection(conn.defaultKey)
	if !ok || cached.db == nil {
		t.Fatal("expected a bootstrap db under the default key")
	}
	return cached.db
}

// idleAfterRelease opens n connections at once, releases them all, and
// returns how many the pool kept idle.
func idleAfterRelease(t *testing.T, db *sql.DB, n int) int {
	t.Helper()
	ctx := context.Background()
	conns := make([]*sql.Conn, 0, n)
	for i := 0; i < n; i++ {
		c, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	for _, c := range conns {
		_ = c.Close()
	}
	return db.Stats().Idle
}

func TestConnector_AppliesGrafanaPoolDefaults(t *testing.T) {
	conn, err := NewConnector(grafanaPoolCtx("7", "3", "60"), &poolDriver{}, backend.DataSourceInstanceSettings{UID: "uid"}, false)
	if err != nil {
		t.Fatal(err)
	}
	db := bootstrapDB(t, conn)
	if got := db.Stats().MaxOpenConnections; got != 7 {
		t.Fatalf("MaxOpenConnections = %d, want 7 from Grafana defaults", got)
	}
	if got := idleAfterRelease(t, db, 5); got != 3 {
		t.Fatalf("idle after release = %d, want 3 from Grafana defaults", got)
	}
}

func TestConnector_DriverSettingsOverrideGrafanaPoolDefaults(t *testing.T) {
	d := &poolDriver{settings: DriverSettings{MaxOpenConns: 2, MaxIdleConns: 1}}
	conn, err := NewConnector(grafanaPoolCtx("7", "3", "60"), d, backend.DataSourceInstanceSettings{UID: "uid"}, false)
	if err != nil {
		t.Fatal(err)
	}
	db := bootstrapDB(t, conn)
	if got := db.Stats().MaxOpenConnections; got != 2 {
		t.Fatalf("MaxOpenConnections = %d, want 2 from DriverSettings", got)
	}
	if got := idleAfterRelease(t, db, 2); got != 1 {
		t.Fatalf("idle after release = %d, want 1 from DriverSettings", got)
	}
}

func TestConnector_AppliesConnMaxLifetimeFromDriverSettings(t *testing.T) {
	d := &poolDriver{settings: DriverSettings{ConnMaxLifetime: time.Nanosecond}}
	conn, err := NewConnector(context.Background(), d, backend.DataSourceInstanceSettings{UID: "uid"}, false)
	if err != nil {
		t.Fatal(err)
	}
	db := bootstrapDB(t, conn)
	c, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	_ = c.Close()
	if got := db.Stats().MaxLifetimeClosed; got != 1 {
		t.Fatalf("MaxLifetimeClosed = %d, want 1 (lifetime from DriverSettings)", got)
	}
	if got := db.Stats().MaxOpenConnections; got != 0 {
		t.Fatalf("MaxOpenConnections = %d, want 0 (unset by driver and no Grafana config)", got)
	}
}

func TestConnector_RespectsPoolBoundInsideConnect(t *testing.T) {
	d := &poolDriver{boundInConnect: 5, settings: DriverSettings{MaxOpenConns: 2}}
	conn, err := NewConnector(grafanaPoolCtx("7", "3", "60"), d, backend.DataSourceInstanceSettings{UID: "uid"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := bootstrapDB(t, conn).Stats().MaxOpenConnections; got != 5 {
		t.Fatalf("MaxOpenConnections = %d, want the driver's own 5 left untouched", got)
	}
}

func TestConnector_LeavesPoolUntouchedWithoutAnyConfig(t *testing.T) {
	conn, err := NewConnector(context.Background(), &poolDriver{}, backend.DataSourceInstanceSettings{UID: "uid"}, false)
	if err != nil {
		t.Fatal(err)
	}
	db := bootstrapDB(t, conn)
	if got := db.Stats().MaxOpenConnections; got != 0 {
		t.Fatalf("MaxOpenConnections = %d, want 0 (database/sql default)", got)
	}
	if got := idleAfterRelease(t, db, 5); got != 2 {
		t.Fatalf("idle after release = %d, want database/sql default of 2", got)
	}
}

func TestConnector_AppliesPoolDefaultsOnEveryConnectPath(t *testing.T) {
	ctx := grafanaPoolCtx("7", "3", "60")
	d := &poolDriver{connectErr: errors.New("not ready yet")}
	conn, err := NewConnector(ctx, d, backend.DataSourceInstanceSettings{UID: "uid"}, true)
	if err != nil {
		t.Fatal(err)
	}
	d.connectErr = nil

	// Deferred default connection, opened on demand after a failed bootstrap.
	_, deferred, err := conn.GetConnectionFromQuery(ctx, &Query{})
	if err != nil {
		t.Fatal(err)
	}
	if got := deferred.db.Stats().MaxOpenConnections; got != 7 {
		t.Fatalf("deferred default db: MaxOpenConnections = %d, want 7", got)
	}

	// Per connection-args connection (multiple connections enabled).
	_, perArgs, err := conn.GetConnectionFromQuery(ctx, &Query{ConnectionArgs: json.RawMessage(`{"database":"other"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if perArgs.db == deferred.db {
		t.Fatal("expected a distinct db for distinct connection args")
	}
	if got := perArgs.db.Stats().MaxOpenConnections; got != 7 {
		t.Fatalf("per-args db: MaxOpenConnections = %d, want 7", got)
	}

	// Reconnect replaces the cached db with a fresh one.
	reconnected, err := conn.Reconnect(ctx, deferred, &Query{}, conn.defaultKey)
	if err != nil {
		t.Fatal(err)
	}
	if got := reconnected.Stats().MaxOpenConnections; got != 7 {
		t.Fatalf("reconnected db: MaxOpenConnections = %d, want 7", got)
	}
}

func TestConnector_AppliesGrafanaConnMaxLifetime(t *testing.T) {
	conn, err := NewConnector(grafanaPoolCtx("7", "3", "1"), &poolDriver{}, backend.DataSourceInstanceSettings{UID: "uid"}, false)
	if err != nil {
		t.Fatal(err)
	}
	db := bootstrapDB(t, conn)
	c, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// The Grafana value is in seconds; a units slip here would pass every
	// other test, so wait past one second and check the connection aged out.
	time.Sleep(1100 * time.Millisecond)
	_ = c.Close()
	if got := db.Stats().MaxLifetimeClosed; got != 1 {
		t.Fatalf("MaxLifetimeClosed = %d, want 1 (1 s lifetime from Grafana defaults)", got)
	}
}

func TestConnector_NegativeMaxIdleConnsKeepsNoIdleConnections(t *testing.T) {
	d := &poolDriver{settings: DriverSettings{MaxIdleConns: -1}}
	conn, err := NewConnector(grafanaPoolCtx("7", "3", "60"), d, backend.DataSourceInstanceSettings{UID: "uid"}, false)
	if err != nil {
		t.Fatal(err)
	}
	db := bootstrapDB(t, conn)
	if got := db.Stats().MaxOpenConnections; got != 7 {
		t.Fatalf("MaxOpenConnections = %d, want 7 from Grafana defaults", got)
	}
	if got := idleAfterRelease(t, db, 5); got != 0 {
		t.Fatalf("idle after release = %d, want 0 (negative MaxIdleConns keeps none)", got)
	}
}
