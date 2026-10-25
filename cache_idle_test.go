package sqlds

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

// liveConnector hands out a connection that can be opened and closed, so
// dbClosed can probe an open pool without the nil connection noopConnector
// returns.
type liveConnector struct{}

func (liveConnector) Connect(context.Context) (driver.Conn, error) { return fakeConn{}, nil }
func (liveConnector) Driver() driver.Driver                        { return nil }

type fakeConn struct{}

func (fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not implemented") }
func (fakeConn) Close() error                        { return nil }
func (fakeConn) Begin() (driver.Tx, error)           { return nil, errors.New("not implemented") }

func newLiveTestDB() *sql.DB { return sql.OpenDB(liveConnector{}) }

type fakeClock struct{ ns atomic.Int64 }

func newFakeClock() *fakeClock {
	c := &fakeClock{}
	c.ns.Store(time.Unix(1_700_000_000, 0).UnixNano())
	return c
}

func (f *fakeClock) now() time.Time          { return time.Unix(0, f.ns.Load()) }
func (f *fakeClock) advance(d time.Duration) { f.ns.Add(int64(d)) }

func newTestIdleCache(t *testing.T, ttl time.Duration) (*idleEvictingCache, *fakeClock) {
	t.Helper()
	clock := newFakeClock()
	c := newIdleEvictingCache(ttl, clock.now)
	t.Cleanup(c.Dispose)
	return c, clock
}

func TestIdleEvictingCache_Sweep(t *testing.T) {
	const ttl = time.Minute
	tests := []struct {
		name     string
		key      string
		idleFor  time.Duration
		wantKept bool
	}{
		{name: "fresh entry is kept", key: "uid-1", idleFor: 0, wantKept: true},
		{name: "entry just under the ttl is kept", key: "uid-2", idleFor: ttl - time.Nanosecond, wantKept: true},
		{name: "entry at the ttl is evicted", key: "uid-3", idleFor: ttl, wantKept: false},
		{name: "entry far past the ttl is evicted", key: "uid-4", idleFor: 10 * ttl, wantKept: false},
		{name: "default entry is never evicted", key: defaultKey("uid"), idleFor: 10 * ttl, wantKept: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, clock := newTestIdleCache(t, ttl)
			db := newLiveTestDB()
			c.Store(tc.key, CachedConnection{db: db})

			clock.advance(tc.idleFor)
			c.sweep()

			_, kept := c.Load(tc.key)
			if kept != tc.wantKept {
				t.Fatalf("after %v idle: kept = %v, want %v", tc.idleFor, kept, tc.wantKept)
			}
			if dbClosed(db) == tc.wantKept {
				t.Fatalf("after %v idle: db closed = %v, want %v", tc.idleFor, dbClosed(db), !tc.wantKept)
			}
		})
	}
}

func TestIdleEvictingCache_NonPositiveTTLPanics(t *testing.T) {
	for _, ttl := range []time.Duration{0, -time.Second} {
		t.Run(ttl.String(), func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("NewIdleEvictingCache(%v) did not panic", ttl)
				}
			}()
			NewIdleEvictingCache(ttl)
		})
	}
}

func TestIdleEvictingCache_LoadRefreshesIdleTimer(t *testing.T) {
	const ttl = time.Minute
	c, clock := newTestIdleCache(t, ttl)
	db := newLiveTestDB()
	c.Store("uid-k", CachedConnection{db: db})

	clock.advance(ttl - time.Second)
	if _, ok := c.Load("uid-k"); !ok {
		t.Fatal("expected entry before the ttl")
	}
	clock.advance(ttl - time.Second)
	c.sweep()

	if _, ok := c.Load("uid-k"); !ok {
		t.Fatal("expected Load to have refreshed the idle timer")
	}
	if dbClosed(db) {
		t.Fatal("expected refreshed entry to stay open")
	}
}

func TestIdleEvictingCache_StoreOverwriteClosesPrevious(t *testing.T) {
	c, _ := newTestIdleCache(t, time.Minute)
	first, second := newLiveTestDB(), newLiveTestDB()
	c.Store("k", CachedConnection{db: first})
	c.Store("k", CachedConnection{db: second})

	got, _ := c.Load("k")
	if got.DB() != second {
		t.Fatalf("got %p want second-stored *sql.DB %p", got.DB(), second)
	}
	if !dbClosed(first) {
		t.Fatal("expected the overwritten *sql.DB to be closed")
	}
	if dbClosed(second) {
		t.Fatal("expected the stored *sql.DB to stay open")
	}
}

func TestIdleEvictingCache_StoreSameDBTwiceKeepsItOpen(t *testing.T) {
	c, _ := newTestIdleCache(t, time.Minute)
	db := newLiveTestDB()
	c.Store("k", CachedConnection{db: db})
	c.Store("k", CachedConnection{db: db})
	if dbClosed(db) {
		t.Fatal("re-storing the same *sql.DB must not close it")
	}
}

func TestIdleEvictingCache_RangeVisitsEveryEntryAndStopsEarly(t *testing.T) {
	c, _ := newTestIdleCache(t, time.Minute)
	keys := []string{"a", "b", "c", "d"}
	for _, k := range keys {
		c.Store(k, CachedConnection{db: newLiveTestDB()})
	}

	seen := map[string]bool{}
	c.Range(func(key string, _ CachedConnection) bool {
		seen[key] = true
		return true
	})
	if len(seen) != len(keys) {
		t.Fatalf("Range visited %d entries, want %d", len(seen), len(keys))
	}

	visits := 0
	c.Range(func(string, CachedConnection) bool {
		visits++
		return false
	})
	if visits != 1 {
		t.Fatalf("Range visited %d entries after f returned false, want 1", visits)
	}
}

func TestIdleEvictingCache_DisposeClosesEverythingAndStopsSweeper(t *testing.T) {
	c, _ := newTestIdleCache(t, time.Hour)
	dbs := []*sql.DB{newLiveTestDB(), newLiveTestDB()}
	c.Store(defaultKey("uid"), CachedConnection{db: dbs[0]})
	c.Store("uid-args", CachedConnection{db: dbs[1]})

	c.Dispose()

	for i, db := range dbs {
		if !dbClosed(db) {
			t.Fatalf("db %d still open after Dispose", i)
		}
	}
	if _, ok := c.Load("uid-args"); ok {
		t.Fatal("expected entries to be cleared by Dispose")
	}
	select {
	case <-c.done:
	default:
		t.Fatal("expected the sweeper goroutine to have exited")
	}
	c.Dispose()
}

func TestIdleEvictingCache_StoreAfterDisposeClosesTheValue(t *testing.T) {
	c, _ := newTestIdleCache(t, time.Hour)
	c.Dispose()

	late := newLiveTestDB()
	c.Store("uid-late", CachedConnection{db: late})

	if _, ok := c.Load("uid-late"); ok {
		t.Fatal("a disposed cache must not keep new entries")
	}
	if !dbClosed(late) {
		t.Fatal("a value stored after Dispose must be closed")
	}
}

func TestIdleEvictingCache_ConcurrentUseClosesEveryUnreachableDB(t *testing.T) {
	const ttl = time.Second
	c, clock := newTestIdleCache(t, ttl)

	var mu sync.Mutex
	var created []*sql.DB
	newDB := func() *sql.DB {
		db := newLiveTestDB()
		mu.Lock()
		created = append(created, db)
		mu.Unlock()
		return db
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("uid-%d", i%3)
			for j := 0; j < 200; j++ {
				c.Store(key, CachedConnection{db: newDB()})
				c.Load(key)
				c.Range(func(string, CachedConnection) bool { return true })
				if j%10 == 0 {
					clock.advance(ttl)
				}
				c.sweep()
			}
		}(i)
	}
	wg.Wait()
	c.Dispose()

	for i, db := range created {
		if !dbClosed(db) {
			t.Fatalf("db %d of %d is still open after the run and Dispose", i, len(created))
		}
	}
}

type countingDriver struct {
	noopDriver
	connects int
}

func (d *countingDriver) Connect(_ context.Context, _ backend.DataSourceInstanceSettings, _ json.RawMessage) (*sql.DB, error) {
	d.connects++
	return newLiveTestDB(), nil
}

func TestConnector_IdleEvictingCacheReopensEvictedConnection(t *testing.T) {
	const ttl = time.Minute
	ctx := context.Background()
	cache, clock := newTestIdleCache(t, ttl)
	driver := &countingDriver{}
	conn, err := NewConnector(ctx, driver, backend.DataSourceInstanceSettings{UID: "uid"}, true, WithCache(cache))
	if err != nil {
		t.Fatalf("NewConnector: %v", err)
	}
	q := &Query{ConnectionArgs: json.RawMessage(`{"grafana-http-headers":{"X-Grafana-User":["alice"]}}`)}

	_, first, err := conn.GetConnectionFromQuery(ctx, q)
	if err != nil {
		t.Fatalf("GetConnectionFromQuery: %v", err)
	}
	if driver.connects != 2 {
		t.Fatalf("connects = %d after bootstrap and one keyed query, want 2", driver.connects)
	}

	clock.advance(ttl)
	cache.sweep()

	if _, ok := cache.Load(conn.defaultKey); !ok {
		t.Fatal("default entry must survive the sweep")
	}
	if !dbClosed(first.DB()) {
		t.Fatal("expected the idle keyed pool to be closed")
	}

	_, second, err := conn.GetConnectionFromQuery(ctx, q)
	if err != nil {
		t.Fatalf("GetConnectionFromQuery after sweep: %v", err)
	}
	if driver.connects != 3 {
		t.Fatalf("connects = %d after an evicted key was queried again, want 3", driver.connects)
	}
	if second.DB() == first.DB() || dbClosed(second.DB()) {
		t.Fatal("expected a fresh open pool for the re-queried key")
	}
}
