package sqlds

import (
	"context"
	"database/sql"
	"strconv"
	"sync"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

// newCacheTestDB returns a real *sql.DB backed by noopConnector (declared in
// connector_cache_test.go). It performs no network activity, so cache tests
// can store and close it cheaply while still exercising real database/sql
// lifecycle semantics via CachedConnection.Close.
func newCacheTestDB() *sql.DB { return sql.OpenDB(noopConnector{}) }

// dbClosed reports whether db has been closed. PingContext short-circuits to
// "sql: database is closed" before touching the driver, so this never opens a
// connection.
func dbClosed(db *sql.DB) bool { return db.PingContext(context.Background()) != nil }

func TestSyncMapCache_LoadStoreRoundTrip(t *testing.T) {
	c := NewSyncMapCache()
	db := newCacheTestDB()
	c.Store("k", CachedConnection{db: db, settings: backend.DataSourceInstanceSettings{UID: "x"}})

	got, ok := c.Load("k")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got.DB() != db {
		t.Fatalf("got %p want %p (same *sql.DB)", got.DB(), db)
	}
	if got.Settings().UID != "x" {
		t.Fatalf("got settings UID %q want %q", got.Settings().UID, "x")
	}
}

func TestSyncMapCache_LoadMissingKey(t *testing.T) {
	c := NewSyncMapCache()
	got, ok := c.Load("nope")
	if ok || got.DB() != nil {
		t.Fatalf("got (%v, %v) want (zero, false)", got, ok)
	}
}

func TestSyncMapCache_StoreOverwrite(t *testing.T) {
	c := NewSyncMapCache()
	first, second := newCacheTestDB(), newCacheTestDB()
	c.Store("k", CachedConnection{db: first})
	c.Store("k", CachedConnection{db: second})
	got, _ := c.Load("k")
	if got.DB() != second {
		t.Fatalf("got %p want second-stored *sql.DB %p", got.DB(), second)
	}
}

func TestSyncMapCache_RangeIteratesEveryEntry(t *testing.T) {
	c := NewSyncMapCache()
	keys := []string{"a", "b", "c", "d"}
	for _, k := range keys {
		c.Store(k, CachedConnection{db: newCacheTestDB()})
	}

	seen := make(map[string]bool)
	c.Range(func(key string, v CachedConnection) bool {
		seen[key] = true
		return true
	})
	if len(seen) != len(keys) {
		t.Fatalf("Range visited %d entries, want %d", len(seen), len(keys))
	}
	for _, k := range keys {
		if !seen[k] {
			t.Fatalf("Range did not visit key %q", k)
		}
	}
}

func TestSyncMapCache_RangeStopsEarly(t *testing.T) {
	c := NewSyncMapCache()
	for _, k := range []string{"a", "b", "c", "d"} {
		c.Store(k, CachedConnection{db: newCacheTestDB()})
	}
	calls := 0
	c.Range(func(string, CachedConnection) bool {
		calls++
		return false
	})
	if calls != 1 {
		t.Fatalf("Range invoked callback %d times, want 1 (early stop)", calls)
	}
}

func TestSyncMapCache_DisposeClosesEveryEntry(t *testing.T) {
	c := NewSyncMapCache()
	dbs := map[string]*sql.DB{"a": newCacheTestDB(), "b": newCacheTestDB(), "c": newCacheTestDB()}
	for k, db := range dbs {
		c.Store(k, CachedConnection{db: db})
	}

	c.Dispose()

	for k, db := range dbs {
		if !dbClosed(db) {
			t.Fatalf("entry %q not closed after Dispose", k)
		}
	}
	if _, ok := c.Load("a"); ok {
		t.Fatal("expected Load to miss after Dispose")
	}
}

func TestSyncMapCache_ConcurrentAccess(t *testing.T) {
	c := NewSyncMapCache()
	const N = 200
	var wg sync.WaitGroup
	wg.Add(N * 2)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			c.Store(strconv.Itoa(i), CachedConnection{settings: backend.DataSourceInstanceSettings{UID: strconv.Itoa(i)}})
		}()
		go func() {
			defer wg.Done()
			_, _ = c.Load(strconv.Itoa(i))
		}()
	}
	wg.Wait()
	for _, i := range []int{0, N / 2, N - 1} {
		if _, ok := c.Load(strconv.Itoa(i)); !ok {
			t.Fatalf("missing key %d after concurrent Store", i)
		}
	}
}
