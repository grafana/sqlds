// Package pool keeps one connection pool for each key of a data source
// instance, and closes each pool when it is idle, replaced or disposed.
package pool

import (
	"context"
	"errors"
	"sync"
	"time"
)

// DefaultIdleTTL is the IdleTTL that a zero Options.IdleTTL selects.
const DefaultIdleTTL = 5 * time.Minute

// ErrClosed is the error that With returns after Close.
var ErrClosed = errors.New("pool: manager is closed")

// Options configures a Manager. The zero value is ready to use.
type Options struct {
	// IdleTTL is how long a pool can go unused before it closes. Zero or less selects
	// DefaultIdleTTL. The pool for the zero key never closes for idleness.
	IdleTTL time.Duration

	// Reconnect reports whether a new pool can make a failed fn succeed. A nil
	// Reconnect never reconnects.
	Reconnect func(error) bool
}

// Manager holds the pools of one data source instance, one pool for each
// key. It is safe for concurrent use.
type Manager[K comparable, C any] struct {
	open      func(context.Context, K) (C, error)
	close     func(C) error
	idleTTL   time.Duration
	reconnect func(error) bool

	mu      sync.Mutex
	entries map[K]*entry[C]
	closed  bool

	stop chan struct{}
	done chan struct{}
}

type entry[C any] struct {
	ready chan struct{}
	conn  C
	err   error

	refs     int
	lastUsed time.Time
	retired  bool
}

// New returns a Manager that calls open to open the pool for a key and
// closeFn to close it. open must be safe to call again for a key, because
// the manager opens a new pool after an idle close or a reconnect.
func New[K comparable, C any](open func(ctx context.Context, key K) (C, error), closeFn func(C) error, opts Options) *Manager[K, C] {
	idleTTL := opts.IdleTTL
	if idleTTL <= 0 {
		idleTTL = DefaultIdleTTL
	}
	m := &Manager[K, C]{
		open:      open,
		close:     closeFn,
		idleTTL:   idleTTL,
		reconnect: opts.Reconnect,
		entries:   map[K]*entry[C]{},
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	go m.sweepUntilClosed()
	return m
}

// With calls fn with the pool for key, which stays open until fn returns. If fn fails, ctx is not done
// and Options.Reconnect accepts the error, With replaces the pool and calls fn once more.
func (m *Manager[K, C]) With(ctx context.Context, key K, fn func(C) error) error {
	e, err := m.acquire(ctx, key)
	if err != nil {
		return err
	}
	err = m.call(e, fn)
	if err == nil || m.reconnect == nil || ctx.Err() != nil || !m.reconnect(err) {
		return err
	}

	m.retire(key, e)
	e, openErr := m.acquire(ctx, key)
	if openErr != nil {
		return errors.Join(err, openErr)
	}
	return m.call(e, fn)
}

// Close closes every pool that no With uses, and makes each pool in use close
// when its last fn returns. With returns ErrClosed after Close.
func (m *Manager[K, C]) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	var unused []C
	for key, e := range m.entries {
		delete(m.entries, key)
		e.retired = true
		if e.refs == 0 {
			unused = append(unused, e.conn)
		}
	}
	m.mu.Unlock()

	close(m.stop)
	<-m.done

	errs := make([]error, 0, len(unused))
	for _, c := range unused {
		if err := m.close(c); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *Manager[K, C]) acquire(ctx context.Context, key K) (*entry[C], error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrClosed
	}
	e, found := m.entries[key]
	if !found {
		e = &entry[C]{ready: make(chan struct{})}
		m.entries[key] = e
	}
	e.refs++
	m.mu.Unlock()

	if !found {
		m.openEntry(ctx, key, e)
	}
	if err := waitReady(ctx, e); err != nil {
		m.release(e)
		return nil, err
	}
	return e, nil
}

func (m *Manager[K, C]) openEntry(ctx context.Context, key K, e *entry[C]) {
	conn, err := m.open(ctx, key)
	if err != nil {
		m.mu.Lock()
		if m.entries[key] == e {
			delete(m.entries, key)
		}
		m.mu.Unlock()
	}
	e.conn, e.err = conn, err
	close(e.ready)
}

// waitReady checks ready first, so a done ctx does not hide a pool that is
// already open.
func waitReady[C any](ctx context.Context, e *entry[C]) error {
	select {
	case <-e.ready:
		return e.err
	default:
	}
	select {
	case <-e.ready:
		return e.err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (m *Manager[K, C]) call(e *entry[C], fn func(C) error) error {
	defer m.release(e)
	return fn(e.conn)
}

func (m *Manager[K, C]) release(e *entry[C]) {
	m.mu.Lock()
	e.refs--
	e.lastUsed = time.Now()
	closeNow := e.refs == 0 && e.retired && e.err == nil
	m.mu.Unlock()

	if closeNow {
		_ = m.close(e.conn)
	}
}

func (m *Manager[K, C]) retire(key K, e *entry[C]) {
	m.mu.Lock()
	if m.entries[key] != e {
		m.mu.Unlock()
		return
	}
	delete(m.entries, key)
	e.retired = true
	closeNow := e.refs == 0
	m.mu.Unlock()

	if closeNow {
		_ = m.close(e.conn)
	}
}

func (m *Manager[K, C]) sweepUntilClosed() {
	defer close(m.done)
	ticker := time.NewTicker(max(m.idleTTL/2, time.Second))
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.sweep()
		}
	}
}

// sweep closes pools outside the lock, because a close can wait for queries.
func (m *Manager[K, C]) sweep() {
	var zero K
	now := time.Now()

	m.mu.Lock()
	var idle []C
	for key, e := range m.entries {
		if key == zero || e.refs > 0 || now.Sub(e.lastUsed) < m.idleTTL {
			continue
		}
		delete(m.entries, key)
		idle = append(idle, e.conn)
	}
	m.mu.Unlock()

	for _, c := range idle {
		_ = m.close(c)
	}
}
