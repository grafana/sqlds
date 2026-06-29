package sqlds

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
)

// noopDriver returns a real *sql.DB (via sql.OpenDB) without any backing
// network connection. Cache-wiring tests can store its output without
// triggering driver activity.
type noopDriver struct{}

func (noopDriver) Settings(_ context.Context, _ backend.DataSourceInstanceSettings) DriverSettings {
	return DriverSettings{}
}
func (noopDriver) Connect(_ context.Context, _ backend.DataSourceInstanceSettings, _ json.RawMessage) (*sql.DB, error) {
	return sql.OpenDB(noopConnector{}), nil
}
func (noopDriver) Converters() []sqlutil.Converter { return nil }
func (noopDriver) Macros() Macros                  { return Macros{} }

type noopConnector struct{}

func (noopConnector) Connect(_ context.Context) (driver.Conn, error) { return nil, nil }
func (noopConnector) Driver() driver.Driver                          { return nil }

// recordingCache is a ConnectionCache impl used to verify that NewConnector
// routes the bootstrap entry into the cache supplied via WithCache.
type recordingCache struct {
	syncMapCache
	stores []string
}

func (c *recordingCache) Store(key string, v CachedConnection) {
	c.stores = append(c.stores, key)
	c.syncMapCache.Store(key, v)
}

func TestNewConnector_DefaultCacheWhenNoOption(t *testing.T) {
	conn, err := NewConnector(context.Background(), noopDriver{}, backend.DataSourceInstanceSettings{UID: "uid"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if conn.cache == nil {
		t.Fatal("expected default cache to be set")
	}
	if _, ok := conn.cache.(*syncMapCache); !ok {
		t.Fatalf("expected *syncMapCache, got %T", conn.cache)
	}
	// Bootstrap entry should be reachable through the default cache.
	if _, ok := conn.cache.Load(defaultKey("uid")); !ok {
		t.Fatal("expected bootstrap entry under default key")
	}
}

func TestNewConnector_WithCacheRoutesBootstrap(t *testing.T) {
	rc := &recordingCache{}
	conn, err := NewConnector(context.Background(), noopDriver{}, backend.DataSourceInstanceSettings{UID: "uid"}, false, WithCache(rc))
	if err != nil {
		t.Fatal(err)
	}
	if conn.cache != rc {
		t.Fatal("Connector did not hold the supplied cache")
	}
	if len(rc.stores) != 1 || rc.stores[0] != defaultKey("uid") {
		t.Fatalf("expected single Store of bootstrap key, got %v", rc.stores)
	}
}

func TestSQLDatasource_ConnectionCacheFactory_Invoked(t *testing.T) {
	rc := &recordingCache{}
	var calls int
	factory := func() ConnectionCache {
		calls++
		return rc
	}
	ds := NewDatasource(noopDriver{})
	ds.ConnectionCacheFactory = factory

	if _, err := ds.NewDatasource(context.Background(), backend.DataSourceInstanceSettings{UID: "uid"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("factory invoked %d times, want 1", calls)
	}
	if ds.connector.cache != rc {
		t.Fatal("connector did not hold the factory's cache")
	}
}

func TestSQLDatasource_NilFactory_UsesDefault(t *testing.T) {
	ds := NewDatasource(noopDriver{})
	if _, err := ds.NewDatasource(context.Background(), backend.DataSourceInstanceSettings{UID: "uid"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := ds.connector.cache.(*syncMapCache); !ok {
		t.Fatalf("expected default *syncMapCache, got %T", ds.connector.cache)
	}
}

func TestExistingNewConnector_CallSitesUnaffected(t *testing.T) {
	// Existing call sites without options must still compile and run.
	conn, err := NewConnector(context.Background(), noopDriver{}, backend.DataSourceInstanceSettings{UID: "uid"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if conn.cache == nil {
		t.Fatal("default cache expected")
	}
}
