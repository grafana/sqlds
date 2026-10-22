package sqlds

import (
	"strings"
	"sync"
	"time"
)

// NewIdleEvictingCache returns a ConnectionCache that closes and drops every
// entry not loaded or stored for idleTTL, checking at least once a second.
// The Connector's default entry is exempt, and Dispose stops the sweeper. A
// non-positive idleTTL is a programming error and panics.
func NewIdleEvictingCache(idleTTL time.Duration) ConnectionCache {
	return newIdleEvictingCache(idleTTL, time.Now)
}

func newIdleEvictingCache(idleTTL time.Duration, now func() time.Time) *idleEvictingCache {
	if idleTTL <= 0 {
		panic("sqlds: NewIdleEvictingCache needs a positive idleTTL")
	}
	c := &idleEvictingCache{
		idleTTL: idleTTL,
		now:     now,
		entries: map[string]*idleEntry{},
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go c.run()
	return c
}

// idleEvictingCache closes an entry it overwrites and an entry stored after
// Dispose, so every *sql.DB it has seen is closed once it is unreachable.
type idleEvictingCache struct {
	idleTTL time.Duration
	now     func() time.Time

	mu       sync.Mutex
	entries  map[string]*idleEntry
	disposed bool

	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

type idleEntry struct {
	conn     CachedConnection
	lastUsed time.Time
}

func (c *idleEvictingCache) Load(key string) (CachedConnection, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return CachedConnection{}, false
	}
	e.lastUsed = c.now()
	return e.conn, true
}

func (c *idleEvictingCache) Store(key string, v CachedConnection) {
	c.mu.Lock()
	if c.disposed {
		c.mu.Unlock()
		_ = v.Close()
		return
	}
	prev, replaced := c.entries[key]
	c.entries[key] = &idleEntry{conn: v, lastUsed: c.now()}
	c.mu.Unlock()

	if replaced && prev.conn.db != v.db {
		_ = prev.conn.Close()
	}
}

func (c *idleEvictingCache) Range(f func(key string, v CachedConnection) bool) {
	for key, conn := range c.snapshot() {
		if !f(key, conn) {
			return
		}
	}
}

func (c *idleEvictingCache) Dispose() {
	c.mu.Lock()
	c.disposed = true
	entries := c.entries
	c.entries = map[string]*idleEntry{}
	c.mu.Unlock()

	c.stopOnce.Do(func() { close(c.stop) })
	<-c.done

	for _, e := range entries {
		_ = e.conn.Close()
	}
}

func (c *idleEvictingCache) run() {
	defer close(c.done)
	ticker := time.NewTicker(sweepInterval(c.idleTTL))
	defer ticker.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-ticker.C:
			c.sweep()
		}
	}
}

// Closing happens outside the lock because sql.DB.Close waits for in-flight queries.
func (c *idleEvictingCache) sweep() {
	now := c.now()

	c.mu.Lock()
	var expired []CachedConnection
	for key, e := range c.entries {
		if isDefaultKey(key) || now.Sub(e.lastUsed) < c.idleTTL {
			continue
		}
		expired = append(expired, e.conn)
		delete(c.entries, key)
	}
	c.mu.Unlock()

	for _, conn := range expired {
		_ = conn.Close()
	}
}

func (c *idleEvictingCache) snapshot() map[string]CachedConnection {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]CachedConnection, len(c.entries))
	for k, e := range c.entries {
		out[k] = e.conn
	}
	return out
}

func sweepInterval(idleTTL time.Duration) time.Duration {
	if idleTTL/2 < time.Second {
		return time.Second
	}
	return idleTTL / 2
}

func isDefaultKey(key string) bool {
	return strings.HasSuffix(key, "-"+defaultKeySuffix)
}
