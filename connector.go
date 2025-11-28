package sqlds

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

var emptyConnArgs json.RawMessage = nil

type Connector struct {
	UID              string
	connections      sync.Map
	driver           Driver
	driverSettings   DriverSettings
	instanceSettings backend.DataSourceInstanceSettings
	// Enabling multiple connections may cause that concurrent connection limits
	// are hit. The datasource enabling this should make sure connections are cached
	// if necessary.
	enableMultipleConnections bool
}

func NewConnector(ctx context.Context, driver Driver, settings backend.DataSourceInstanceSettings, enableMultipleConnections bool) (*Connector, error) {
	db, err := driver.Connect(ctx, settings, emptyConnArgs)
	if err != nil {
		return nil, backend.DownstreamError(err)
	}

	conn := &Connector{
		UID:                       settings.UID,
		driver:                    driver,
		driverSettings:            driver.Settings(ctx, settings),
		enableMultipleConnections: enableMultipleConnections,
		instanceSettings:          settings,
	}
	conn.storeDBConnection(datasourceCacheKey(settings.UID, emptyConnArgs), dbConnection{db, settings})
	return conn, nil
}

func (c *Connector) Connect(ctx context.Context, headers http.Header) (*dbConnection, error) {
	dbConn, ok := c.getDBConnection(datasourceCacheKey(c.UID, emptyConnArgs))
	if !ok {
		return nil, ErrorMissingDBConnection
	}

	if c.driverSettings.Retries == 0 {
		err := c.connect(ctx, dbConn)
		return nil, err
	}

	err := c.connectWithRetries(ctx, dbConn, c.UID, headers)
	return &dbConn, err
}

func (c *Connector) connectWithRetries(ctx context.Context, conn dbConnection, key string, headers http.Header) error {
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
		conn := dbConnection{
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

func (c *Connector) connect(ctx context.Context, conn dbConnection) error {
	if err := c.ping(ctx, conn); err != nil {
		return backend.DownstreamError(err)
	}

	return nil
}

func (c *Connector) ping(ctx context.Context, conn dbConnection) error {
	if c.driverSettings.Timeout == 0 {
		return conn.db.PingContext(ctx)
	}

	ctx, cancel := context.WithTimeout(ctx, c.driverSettings.Timeout)
	defer cancel()

	return conn.db.PingContext(ctx)
}

func (c *Connector) Reconnect(ctx context.Context, dbConn dbConnection, q *Query, cacheKey string) (*sql.DB, error) {
	if err := dbConn.db.Close(); err != nil {
		backend.Logger.Warn(fmt.Sprintf("closing existing connection failed: %s", err.Error()))
	}

	db, err := c.driver.Connect(ctx, dbConn.settings, q.ConnectionArgs)
	if err != nil {
		return nil, backend.DownstreamError(err)
	}
	c.storeDBConnection(cacheKey, dbConnection{db, dbConn.settings})
	return db, nil
}

func (ds *Connector) getDBConnection(key string) (dbConnection, bool) {
	conn, ok := ds.connections.Load(key)
	if !ok {
		return dbConnection{}, false
	}
	return conn.(dbConnection), true
}

func (ds *Connector) storeDBConnection(key string, dbConn dbConnection) {
	ds.connections.Store(key, dbConn)
}

// Dispose is called when an existing SQLDatasource needs to be replaced
func (c *Connector) Dispose() {
	c.connections.Range(func(_, conn interface{}) bool {
		_ = conn.(dbConnection).db.Close()
		return true
	})
	c.connections.Clear()
}

func (c *Connector) GetConnectionFromQuery(ctx context.Context, q *Query) (string, dbConnection, error) {
	key := datasourceCacheKey(c.UID, q.ConnectionArgs)
	if cachedConn, ok := c.getDBConnection(key); ok {
		backend.Logger.Debug("cached connection")
		return key, cachedConn, nil
	}

	db, err := c.driver.Connect(ctx, c.instanceSettings, q.ConnectionArgs)
	if err != nil {
		backend.Logger.Debug("connect error " + err.Error())
		return "", dbConnection{}, backend.DownstreamError(err)
	}

	backend.Logger.Debug("new connection(multiple) created")
	dbConn := dbConnection{db, c.instanceSettings}
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
