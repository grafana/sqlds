package pool

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type fakeConn struct {
	key string
	n   int
}

type fakeOpener struct {
	mu      sync.Mutex
	opens   map[string]int
	closed  map[*fakeConn]int
	openErr error
	block   chan struct{}
}

func newFakeOpener() *fakeOpener {
	return &fakeOpener{opens: map[string]int{}, closed: map[*fakeConn]int{}}
}

func (f *fakeOpener) open(ctx context.Context, key string) (*fakeConn, error) {
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.openErr != nil {
		return nil, f.openErr
	}
	f.opens[key]++
	return &fakeConn{key: key, n: f.opens[key]}, nil
}

func (f *fakeOpener) close(c *fakeConn) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed[c]++
	return nil
}

func (f *fakeOpener) openCount(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opens[key]
}

func (f *fakeOpener) closeCount(c *fakeConn) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed[c]
}

func newTestManager(t *testing.T, f *fakeOpener, opts Options) *Manager[string, *fakeConn] {
	t.Helper()
	m := New(f.open, f.close, opts)
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func lease(t *testing.T, m *Manager[string, *fakeConn], key string) *fakeConn {
	t.Helper()
	var got *fakeConn
	if err := m.With(context.Background(), key, func(c *fakeConn) error {
		got = c
		return nil
	}); err != nil {
		t.Fatalf("With(%q) = %v, want nil", key, err)
	}
	return got
}

func TestWithOpensOnePoolPerKey(t *testing.T) {
	f := newFakeOpener()
	m := newTestManager(t, f, Options{})

	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() { lease(t, m, "alice") })
	}
	wg.Wait()
	lease(t, m, "bob")

	tests := []struct {
		key  string
		want int
	}{
		{key: "alice", want: 1},
		{key: "bob", want: 1},
	}
	for _, tc := range tests {
		if got := f.openCount(tc.key); got != tc.want {
			t.Errorf("opens for %q = %d, want %d", tc.key, got, tc.want)
		}
	}
}

func TestWithDoesNotKeepFailedOpen(t *testing.T) {
	f := newFakeOpener()
	f.openErr = errors.New("bad dsn")
	m := newTestManager(t, f, Options{})

	err := m.With(context.Background(), "alice", func(*fakeConn) error { return nil })
	if !errors.Is(err, f.openErr) {
		t.Fatalf("With() = %v, want an error that wraps %v", err, f.openErr)
	}

	f.openErr = nil
	if got := lease(t, m, "alice"); got.n != 1 {
		t.Errorf("pool after a failed open is open #%d, want #1", got.n)
	}
}

func TestWithReturnsWhenContextEndsDuringOpen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFakeOpener()
		f.block = make(chan struct{})
		m := newTestManager(t, f, Options{})

		opened := make(chan error, 1)
		go func() {
			opened <- m.With(context.Background(), "alice", func(*fakeConn) error { return nil })
		}()
		synctest.Wait()

		cause := errors.New("panel closed")
		ctx, cancel := context.WithCancelCause(context.Background())
		waited := make(chan error, 1)
		go func() {
			waited <- m.With(ctx, "alice", func(*fakeConn) error { return nil })
		}()
		synctest.Wait()
		cancel(cause)

		if err := <-waited; !errors.Is(err, cause) {
			t.Errorf("waiting With() = %v, want %v", err, cause)
		}
		close(f.block)
		if err := <-opened; err != nil {
			t.Errorf("opening With() = %v, want nil", err)
		}
	})
}

func TestIdleEviction(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		idle       time.Duration
		wantClosed int
	}{
		{name: "identity pool closes after IdleTTL", key: "alice", idle: DefaultIdleTTL + time.Minute, wantClosed: 1},
		{name: "identity pool stays open before IdleTTL", key: "alice", idle: DefaultIdleTTL - time.Minute, wantClosed: 0},
		{name: "shared pool never closes for idleness", key: "", idle: 10 * DefaultIdleTTL, wantClosed: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newFakeOpener()
				m := newTestManager(t, f, Options{})

				c := lease(t, m, tc.key)
				time.Sleep(tc.idle)
				synctest.Wait()

				if got := f.closeCount(c); got != tc.wantClosed {
					t.Errorf("closes = %d, want %d", got, tc.wantClosed)
				}
			})
		})
	}
}

func TestIdleEvictionThenReopen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFakeOpener()
		m := newTestManager(t, f, Options{IdleTTL: time.Minute})

		first := lease(t, m, "alice")
		time.Sleep(2 * time.Minute)
		synctest.Wait()

		second := lease(t, m, "alice")
		if second == first || second.n != 2 {
			t.Errorf("pool after eviction is open #%d, want a new pool #2", second.n)
		}
	})
}

func TestLeaseHoldsPoolOpen(t *testing.T) {
	tests := []struct {
		name    string
		release func(t *testing.T, m *Manager[string, *fakeConn])
	}{
		{name: "idle sweep", release: func(*testing.T, *Manager[string, *fakeConn]) {
			time.Sleep(2 * DefaultIdleTTL)
		}},
		{name: "manager close", release: func(t *testing.T, m *Manager[string, *fakeConn]) {
			if err := m.Close(); err != nil {
				t.Errorf("Close() = %v, want nil", err)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newFakeOpener()
				m := newTestManager(t, f, Options{})

				inFn := make(chan *fakeConn)
				done := make(chan struct{})
				go func() {
					_ = m.With(context.Background(), "alice", func(c *fakeConn) error {
						inFn <- c
						<-done
						return nil
					})
				}()
				c := <-inFn

				tc.release(t, m)
				synctest.Wait()
				if got := f.closeCount(c); got != 0 {
					t.Fatalf("closes while fn runs = %d, want 0", got)
				}

				close(done)
				synctest.Wait()
				if tc.name == "manager close" {
					if got := f.closeCount(c); got != 1 {
						t.Errorf("closes after fn returns = %d, want 1", got)
					}
				}
			})
		})
	}
}

func TestReconnect(t *testing.T) {
	errStale := errors.New("stale connection")
	errSyntax := errors.New("syntax error")
	canceled, cancel := context.WithCancelCause(context.Background())
	cancel(errors.New("query done"))

	tests := []struct {
		name      string
		ctx       context.Context
		reconnect func(error) bool
		firstErr  error
		wantErr   error
		wantOpens int
		wantCalls int
	}{
		{
			name:      "reconnect error opens a new pool and calls fn again",
			ctx:       context.Background(),
			reconnect: func(err error) bool { return errors.Is(err, errStale) },
			firstErr:  errStale,
			wantOpens: 2,
			wantCalls: 2,
		},
		{
			name:      "other error returns as is",
			ctx:       context.Background(),
			reconnect: func(err error) bool { return errors.Is(err, errStale) },
			firstErr:  errSyntax,
			wantErr:   errSyntax,
			wantOpens: 1,
			wantCalls: 1,
		},
		{
			name:      "nil Reconnect never reconnects",
			ctx:       context.Background(),
			firstErr:  errStale,
			wantErr:   errStale,
			wantOpens: 1,
			wantCalls: 1,
		},
		{
			name:      "done context does not reconnect",
			ctx:       canceled,
			reconnect: func(error) bool { return true },
			firstErr:  errStale,
			wantErr:   errStale,
			wantOpens: 1,
			wantCalls: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeOpener()
			m := newTestManager(t, f, Options{Reconnect: tc.reconnect})
			first := lease(t, m, "alice")

			calls := 0
			err := m.With(tc.ctx, "alice", func(c *fakeConn) error {
				calls++
				if c == first {
					return tc.firstErr
				}
				return nil
			})

			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
				t.Errorf("With() = %v, want %v", err, tc.wantErr)
			}
			if got := f.openCount("alice"); got != tc.wantOpens {
				t.Errorf("opens = %d, want %d", got, tc.wantOpens)
			}
			if calls != tc.wantCalls {
				t.Errorf("fn calls = %d, want %d", calls, tc.wantCalls)
			}
			wantClosed := tc.wantOpens - 1
			if got := f.closeCount(first); got != wantClosed {
				t.Errorf("closes of the first pool = %d, want %d", got, wantClosed)
			}
		})
	}
}

func TestClose(t *testing.T) {
	f := newFakeOpener()
	m := New(f.open, f.close, Options{})
	shared := lease(t, m, "")
	alice := lease(t, m, "alice")

	if err := m.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	if err := m.Close(); err != nil {
		t.Errorf("second Close() = %v, want nil", err)
	}
	for _, c := range []*fakeConn{shared, alice} {
		if got := f.closeCount(c); got != 1 {
			t.Errorf("closes of pool %q = %d, want 1", c.key, got)
		}
	}

	err := m.With(context.Background(), "alice", func(*fakeConn) error { return nil })
	if !errors.Is(err, ErrClosed) {
		t.Errorf("With() after Close = %v, want %v", err, ErrClosed)
	}
}
