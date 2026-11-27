package pool

import (
	"context"
	"database/sql/driver"
	"errors"
	"sync"
	"time"
)

// opaqueLifetime bounds a connection whose credential has no readable expiry.
const opaqueLifetime = time.Hour

var errTransactionOptions = errors.New("pool: the driver does not support transaction options")

// Credential holds the credentials that the requests of a pool forward, one for each Grafana
// sign-in. Its zero value is ready to use and safe for concurrent use.
type Credential struct {
	mu     sync.Mutex
	latest string
	stored map[string]time.Time
}

// Store records v as the credential of the request that is about to use the pool.
func (c *Credential) Store(v string) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stored == nil {
		c.stored = map[string]time.Time{}
	}
	for k, t := range c.stored {
		if now.Sub(t) > DefaultIdleTTL {
			delete(c.stored, k)
		}
	}
	c.stored[v] = now
	c.latest = v
}

// Latest returns the credential of the latest Store, or "" before the first.
func (c *Credential) Latest() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.latest
}

func (c *Credential) current(v string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.stored[v]
	return ok && time.Since(t) <= DefaultIdleTTL
}

// Connector returns a connector for drv that logs each connection in with connect and Latest. A
// connection closes before reuse once no Store has seen its credential for DefaultIdleTTL, or it expires.
func (c *Credential) Connector(drv driver.Driver, connect func(ctx context.Context, credential string) (driver.Conn, error)) driver.Connector {
	return credentialConnector{cred: c, drv: drv, connect: connect}
}

type credentialConnector struct {
	cred    *Credential
	drv     driver.Driver
	connect func(context.Context, string) (driver.Conn, error)
}

func (c credentialConnector) Connect(ctx context.Context) (driver.Conn, error) {
	credential := c.cred.Latest()
	conn, err := c.connect(ctx, credential)
	if err != nil {
		return nil, err
	}
	return &credentialConn{Conn: conn, credential: credential, expires: expiry(credential, time.Now()), cred: c.cred}, nil
}

func expiry(credential string, now time.Time) time.Time {
	// The server checked the signature at login, so exp needs no check here.
	var claims struct {
		Exp float64 `json:"exp"`
	}
	if jwtPayload(credential, &claims) && claims.Exp > 0 {
		return time.Unix(int64(claims.Exp), 0)
	}
	return now.Add(opaqueLifetime)
}

func (c credentialConnector) Driver() driver.Driver {
	return c.drv
}

// credentialConn passes each optional driver interface through to Conn. When
// Conn does not implement one, it does what database/sql does without it.
type credentialConn struct {
	driver.Conn
	credential string
	expires    time.Time
	cred       *Credential
}

func (c *credentialConn) ResetSession(ctx context.Context) error {
	if !c.cred.current(c.credential) || !time.Now().Before(c.expires) {
		return driver.ErrBadConn
	}
	if r, ok := c.Conn.(driver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

func (c *credentialConn) IsValid() bool {
	v, ok := c.Conn.(driver.Validator)
	return !ok || v.IsValid()
}

func (c *credentialConn) Ping(ctx context.Context) error {
	if p, ok := c.Conn.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c *credentialConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if b, ok := c.Conn.(driver.ConnBeginTx); ok {
		return b.BeginTx(ctx, opts)
	}
	if opts.Isolation != 0 || opts.ReadOnly {
		return nil, errTransactionOptions
	}
	return c.Conn.Begin() //nolint:staticcheck // database/sql calls Begin on a connection without ConnBeginTx.
}

func (c *credentialConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if p, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return p.PrepareContext(ctx, query)
	}
	return c.Prepare(query)
}

func (c *credentialConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if e, ok := c.Conn.(driver.ExecerContext); ok {
		return e.ExecContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

func (c *credentialConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if q, ok := c.Conn.(driver.QueryerContext); ok {
		return q.QueryContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

func (c *credentialConn) CheckNamedValue(v *driver.NamedValue) error {
	if n, ok := c.Conn.(driver.NamedValueChecker); ok {
		return n.CheckNamedValue(v)
	}
	return driver.ErrSkip
}
