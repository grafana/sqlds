package sqlds

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

// gatedDriver blocks a keyed Connect until release is closed, so a test can
// dispose the Connector while GetConnectionFromQuery is opening a pool.
type gatedDriver struct {
	noopDriver
	entered chan struct{}
	release chan struct{}
}

func (d gatedDriver) Connect(_ context.Context, _ backend.DataSourceInstanceSettings, args json.RawMessage) (*sql.DB, error) {
	if len(args) > 0 {
		close(d.entered)
		<-d.release
	}
	return sql.OpenDB(poolConnector{}), nil
}

func waitClosed(t *testing.T, db *sql.DB) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !dbClosed(db) {
		if time.Now().After(deadline) {
			t.Fatal("connection opened after Dispose is still open after its context ended")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestReconnectAfterDisposeClosesTheConnectionWhenTheContextEnds(t *testing.T) {
	conn, err := NewConnector(context.Background(), &poolDriver{}, backend.DataSourceInstanceSettings{UID: "uid"}, false)
	if err != nil {
		t.Fatal(err)
	}
	cached, _ := conn.getDBConnection(conn.defaultKey)
	conn.Dispose()

	ctx, cancel := context.WithCancel(context.Background())
	db, err := conn.Reconnect(ctx, cached, &Query{}, conn.defaultKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := conn.getDBConnection(conn.defaultKey); ok {
		t.Error("Reconnect after Dispose cached a connection that nothing will close")
	}
	if dbClosed(db) {
		t.Fatal("the connection must stay open for the request that opened it")
	}

	cancel()
	waitClosed(t, db)
}

func TestGetConnectionFromQueryDoesNotCacheAPoolOpenedAcrossDispose(t *testing.T) {
	d := gatedDriver{entered: make(chan struct{}), release: make(chan struct{})}
	conn, err := NewConnector(context.Background(), d, backend.DataSourceInstanceSettings{UID: "uid"}, true)
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		key string
		db  CachedConnection
		err error
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan result, 1)
	go func() {
		key, db, err := conn.GetConnectionFromQuery(ctx, &Query{ConnectionArgs: json.RawMessage(`{"user":"a"}`)})
		done <- result{key, db, err}
	}()

	<-d.entered
	conn.Dispose()
	close(d.release)
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	if _, ok := conn.getDBConnection(r.key); ok {
		t.Error("a pool opened across Dispose was cached, and nothing will close it")
	}
	if dbClosed(r.db.db) {
		t.Fatal("the pool must stay open for the request that opened it")
	}

	cancel()
	waitClosed(t, r.db.db)
}
