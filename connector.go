package sqlds

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

type Connector struct {
	UID            string
	cache          ConnectionCache
	driver         Driver
	driverSettings DriverSettings
	// defaultKey is the cache key for the single-connection path. It is
	// fmt.Sprintf("%s-default", UID) and never changes for the life of the
	// connector, so we compute it once in NewConnector.
	defaultKey string
	// Enabling multiple connections may cause that concurrent connection limits
	// are hit. The datasource enabling this should make sure connections are cached
	// if necessary.
	enableMultipleConnections bool
}

// ConnectorOption configures a Connector at construction time.
type ConnectorOption func(*Connector)

// WithCache installs a custom ConnectionCache on the Connector. When omitted,
// NewConnector defaults to NewSyncMapCache(). The option is applied before
// the bootstrap connection is stored, so the bootstrap entry lands in the
// custom cache.
func WithCache(cache ConnectionCache) ConnectorOption {
	return func(c *Connector) { c.cache = cache }
}

func NewConnector(ctx context.Context, driver Driver, settings backend.DataSourceInstanceSettings, enableMultipleConnections bool, opts ...ConnectorOption) (*Connector, error) {
	ds := driver.Settings(ctx, settings)
	db, err := driver.Connect(ctx, settings, nil)
	if err != nil {
		return nil, backend.DownstreamError(err)
	}

	conn := &Connector{
		UID:                       settings.UID,
		driver:                    driver,
		driverSettings:            ds,
		defaultKey:                defaultKey(settings.UID),
		enableMultipleConnections: enableMultipleConnections,
	}
	for _, opt := range opts {
		opt(conn)
	}
	if conn.cache == nil {
		conn.cache = NewSyncMapCache()
	}
	conn.storeDBConnection(conn.defaultKey, CachedConnection{db, settings})
	return conn, nil
}

func (c *Connector) Connect(ctx context.Context, headers http.Header) (*CachedConnection, error) {
	key := c.defaultKey
	dbConn, ok := c.getDBConnection(key)
	if !ok {
		return nil, ErrorMissingDBConnection
	}

	if c.driverSettings.Retries == 0 {
		err := c.connect(ctx, dbConn)
		return nil, err
	}

	err := c.connectWithRetries(ctx, dbConn, key, headers)
	return &dbConn, err
}

func (c *Connector) connectWithRetries(ctx context.Context, conn CachedConnection, key string, headers http.Header) error {
	q := &Query{}
	if c.driverSettings.ForwardHeaders {
		applyHeaders(q, headers)
	}

	var db *sql.DB
	var err error
	for i := 0; i < c.driverSettings.Retries; i++ {
		db, err = c.Reconnect(ctx, conn, q, key)
		if err != nil {
			return err
		}
		conn := CachedConnection{
			db:       db,
			settings: conn.settings,
		}
		err = c.connect(ctx, conn)
		if err == nil {
			break
		}

		if !shouldRetry(c.driverSettings.RetryOn, err.Error()) {
			break
		}

		if i+1 == c.driverSettings.Retries {
			break
		}

		if c.driverSettings.Pause > 0 {
			time.Sleep(time.Duration(c.driverSettings.Pause * int(time.Second)))
		}
		backend.Logger.Warn(fmt.Sprintf("connect failed: %s. Retrying %d times", err.Error(), i+1))
	}

	return err
}

func (c *Connector) connect(ctx context.Context, conn CachedConnection) error {
	if err := c.ping(ctx, conn); err != nil {
		return backend.DownstreamError(err)
	}

	return nil
}

func (c *Connector) ping(ctx context.Context, conn CachedConnection) error {
	if c.driverSettings.Timeout == 0 {
		return conn.db.PingContext(ctx)
	}

	ctx, cancel := context.WithTimeout(ctx, c.driverSettings.Timeout)
	defer cancel()

	return conn.db.PingContext(ctx)
}

func (c *Connector) Reconnect(ctx context.Context, dbConn CachedConnection, q *Query, cacheKey string) (*sql.DB, error) {
	db, err := c.driver.Connect(ctx, dbConn.settings, q.ConnectionArgs)
	if err != nil {
		return nil, backend.DownstreamError(err)
	}

	if err = dbConn.db.Close(); err != nil {
		backend.Logger.Warn(fmt.Sprintf("closing existing connection failed: %s", err.Error()))
	}

	c.storeDBConnection(cacheKey, CachedConnection{db, dbConn.settings})
	return db, nil
}

// connCache returns the Connector's ConnectionCache, lazily installing the
// default sync.Map-backed cache if none is set. NewConnector always installs a
// cache, so the lazy path only covers Connector literals built outside this
// package (e.g. test fixtures). Routing every cache access through this single
// helper keeps the nil policy uniform across get/store/Dispose.
func (c *Connector) connCache() ConnectionCache {
	if c.cache == nil {
		c.cache = NewSyncMapCache()
	}
	return c.cache
}

func (c *Connector) getDBConnection(key string) (CachedConnection, bool) {
	return c.connCache().Load(key)
}

func (c *Connector) storeDBConnection(key string, dbConn CachedConnection) {
	c.connCache().Store(key, dbConn)
}

// Dispose is called when an existing SQLDatasource needs to be replaced
func (c *Connector) Dispose() {
	c.connCache().Dispose()
}

func (c *Connector) GetConnectionFromQuery(ctx context.Context, q *Query) (string, CachedConnection, error) {
	if !c.enableMultipleConnections && !c.driverSettings.ForwardHeaders && len(q.ConnectionArgs) > 0 && string(q.ConnectionArgs) != "{}" {
		return "", CachedConnection{}, MissingMultipleConnectionsConfig
	}
	// The database connection may vary depending on query arguments
	// The raw arguments are used as key to store the db connection in memory so they can be reused
	key := c.defaultKey
	dbConn, ok := c.getDBConnection(key)
	if !ok {
		return "", CachedConnection{}, MissingDBConnection
	}
	if !c.enableMultipleConnections || len(q.ConnectionArgs) == 0 {
		backend.Logger.Debug("using single user connection")
		return key, dbConn, nil
	}

	key = keyWithConnectionArgs(c.UID, q.ConnectionArgs)
	if cachedConn, ok := c.getDBConnection(key); ok {
		backend.Logger.Debug("cached connection")
		return key, cachedConn, nil
	}

	db, err := c.driver.Connect(ctx, dbConn.settings, q.ConnectionArgs)
	if err != nil {
		backend.Logger.Debug("connect error " + err.Error())
		return "", CachedConnection{}, backend.DownstreamError(err)
	}
	backend.Logger.Debug("new connection(multiple) created")
	// Assign this connection in the cache
	dbConn = CachedConnection{db, dbConn.settings}
	c.storeDBConnection(key, dbConn)

	return key, dbConn, nil
}

func shouldRetry(retryOn []string, err string) bool {
	for _, r := range retryOn {
		if strings.Contains(err, r) {
			return true
		}
	}
	return false
}
