package sqlds

import (
	"database/sql"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

// stubConn is a minimal CachedConnection used by cache tests that do not
// need a real *sql.DB. It tracks Close invocations so tests can assert
// Dispose semantics without exercising database/sql.
type stubConn struct {
	id     string
	closed atomic.Int32
}

func (s *stubConn) DB() *sql.DB                                  { return nil }
func (s *stubConn) Settings() backend.DataSourceInstanceSettings { return backend.DataSourceInstanceSettings{} }
func (s *stubConn) Close() error {
	s.closed.Add(1)
	return nil
}

func TestSyncMapCache_LoadStoreRoundTrip(t *testing.T) {
	c := NewSyncMapCache()
	v := &stubConn{id: "x"}
	c.Store("k", v)

	got, ok := c.Load("k")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got != v {
		t.Fatalf("got %p want %p (same reference)", got, v)
	}
}

func TestSyncMapCache_LoadMissingKey(t *testing.T) {
	c := NewSyncMapCache()
	got, ok := c.Load("nope")
	if ok || got != nil {
		t.Fatalf("got (%v, %v) want (nil, false)", got, ok)
	}
}

func TestSyncMapCache_StoreOverwrite(t *testing.T) {
	c := NewSyncMapCache()
	v1 := &stubConn{id: "first"}
	v2 := &stubConn{id: "second"}
	c.Store("k", v1)
	c.Store("k", v2)
	got, _ := c.Load("k")
	if got != v2 {
		t.Fatalf("got %v want second-stored value", got)
	}
}

func TestSyncMapCache_RangeIteratesEveryEntry(t *testing.T) {
	c := NewSyncMapCache()
	keys := []string{"a", "b", "c", "d"}
	for _, k := range keys {
		c.Store(k, &stubConn{id: k})
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
		c.Store(k, &stubConn{id: k})
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
	entries := []*stubConn{{id: "a"}, {id: "b"}, {id: "c"}}
	for _, e := range entries {
		c.Store(e.id, e)
	}

	c.Dispose()

	for _, e := range entries {
		if e.closed.Load() != 1 {
			t.Fatalf("entry %q Close called %d times, want 1", e.id, e.closed.Load())
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
			c.Store(strconv.Itoa(i), &stubConn{id: strconv.Itoa(i)})
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

// Compile-time assertion that dbConnection satisfies CachedConnection via
// its adapter methods. The check lives in the test file so it catches a
// future signature change without leaking into the production binary.
var _ CachedConnection = dbConnection{}
