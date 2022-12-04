package sqlds

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/grafana/grafana-plugin-sdk-go/backend/resource/httpadapter"
	"github.com/grafana/grafana-plugin-sdk-go/data"
)

const defaultKeySuffix = "default"

var (
	ErrorMissingMultipleConnectionsConfig = errors.New("received connection arguments but the feature is not enabled")
	ErrorMissingDBConnection              = errors.New("unable to get default db connection")

	// Deprecated: ErrorMissingMultipleConnectionsConfig should be used instead
	MissingMultipleConnectionsConfig = ErrorMissingMultipleConnectionsConfig
	// Deprecated: ErrorMissingDBConnection should be used instead
	MissingDBConnection = ErrorMissingDBConnection
)

func defaultKey(datasourceUID string) string {
	return fmt.Sprintf("%s-%s", datasourceUID, defaultKeySuffix)
}

func keyWithConnectionArgs(datasourceUID string, connArgs json.RawMessage) string {
	return fmt.Sprintf("%s-%s", datasourceUID, string(connArgs))
}

type dbConnection struct {
	db       *sql.DB
	settings backend.DataSourceInstanceSettings
}

type SQLDatasource struct {
	Completable

	dbConnections  sync.Map
	c              Driver
	driverSettings DriverSettings

	backend.CallResourceHandler
	CustomRoutes map[string]func(http.ResponseWriter, *http.Request)
	// Enabling multiple connections may cause that concurrent connection limits
	// are hit. The datasource enabling this should make sure connections are cached
	// if necessary.
	EnableMultipleConnections bool
}

func (ds *SQLDatasource) getDBConnection(key string) (dbConnection, bool) {
	conn, ok := ds.dbConnections.Load(key)
	if !ok {
		return dbConnection{}, false
	}
	return conn.(dbConnection), true
}

func (ds *SQLDatasource) storeDBConnection(key string, dbConn dbConnection) {
	ds.dbConnections.Store(key, dbConn)
}

func getDatasourceUID(settings backend.DataSourceInstanceSettings) string {
	datasourceUID := settings.UID
	// Grafana < 8.0 won't include the UID yet
	if datasourceUID == "" {
		datasourceUID = fmt.Sprintf("%d", settings.ID)
	}
	return datasourceUID
}

// NewDatasource creates a new `SQLDatasource`.
// It uses the provided settings argument to call the ds.Driver to connect to the SQL server
func (ds *SQLDatasource) NewDatasource(settings backend.DataSourceInstanceSettings) (instancemgmt.Instance, error) {
	db, err := ds.c.Connect(settings, nil)
	if err != nil {
		return nil, err
	}
	key := defaultKey(getDatasourceUID(settings))
	ds.storeDBConnection(key, dbConnection{db, settings})

	mux := http.NewServeMux()
	err = ds.registerRoutes(mux)
	if err != nil {
		return nil, err
	}

	ds.CallResourceHandler = httpadapter.New(mux)
	ds.driverSettings = ds.c.Settings(settings)

	return ds, nil
}

// NewDatasource initializes the Datasource wrapper and instance manager
func NewDatasource(c Driver) *SQLDatasource {
	return &SQLDatasource{
		c: c,
	}
}

// Dispose cleans up datasource instance resources.
// Note: Called when testing and saving a datasource
func (ds *SQLDatasource) Dispose() {
}

// QueryData creates the Responses list and executes each query
func (ds *SQLDatasource) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
	var (
		response = NewResponse(backend.NewQueryDataResponse())
		wg       = sync.WaitGroup{}
	)

	wg.Add(len(req.Queries))

	// Execute each query and store the results by query RefID
	for _, q := range req.Queries {
		go func(query backend.DataQuery) {
			frames, err := ds.handleQuery(ctx, query, getDatasourceUID(*req.PluginContext.DataSourceInstanceSettings))

			response.Set(query.RefID, backend.DataResponse{
				Frames: frames,
				Error:  err,
			})

			wg.Done()
		}(q)
	}

	wg.Wait()
	return response.Response(), nil

}

func (ds *SQLDatasource) GetDBFromQuery(q *Query, datasourceUID string) (*sql.DB, error) {
	_, dbConn, err := ds.getDBConnectionFromQuery(q, datasourceUID)
	return dbConn.db, err
}

func (ds *SQLDatasource) getDBConnectionFromQuery(q *Query, datasourceUID string) (string, dbConnection, error) {
	if !ds.EnableMultipleConnections && len(q.ConnectionArgs) > 0 {
		return "", dbConnection{}, MissingMultipleConnectionsConfig
	}
	// The database connection may vary depending on query arguments
	// The raw arguments are used as key to store the db connection in memory so they can be reused
	key := defaultKey(datasourceUID)
	dbConn, ok := ds.getDBConnection(key)
	if !ok {
		return "", dbConnection{}, MissingDBConnection
	}
	if !ds.EnableMultipleConnections || len(q.ConnectionArgs) == 0 {
		return key, dbConn, nil
	}

	key = keyWithConnectionArgs(datasourceUID, q.ConnectionArgs)
	if cachedConn, ok := ds.getDBConnection(key); ok {
		return key, cachedConn, nil
	}

	var err error
	db, err := ds.c.Connect(dbConn.settings, q.ConnectionArgs)
	if err != nil {
		return "", dbConnection{}, err
	}
	// Assign this connection in the cache
	dbConn = dbConnection{db, dbConn.settings}
	ds.storeDBConnection(key, dbConn)

	return key, dbConn, nil
}

// handleQuery will call query, and attempt to reconnect if the query failed
func (ds *SQLDatasource) handleQuery(ctx context.Context, req backend.DataQuery, datasourceUID string) (data.Frames, error) {
	// Convert the backend.DataQuery into a Query object
	q, err := GetQuery(req)
	if err != nil {
		return nil, err
	}

	// Apply supported macros to the query
	q.RawSQL, err = Interpolate(ds.c, q)
	if err != nil {
		return getErrorFrameFromQuery(q), fmt.Errorf("%s: %w", "Could not apply macros", err)
	}

	// Apply the default FillMode, overwritting it if the query specifies it
	fillMode := ds.driverSettings.FillMode
	if q.FillMissing != nil {
		fillMode = q.FillMissing
	}

	// Retrieve the database connection
	cacheKey, dbConn, err := ds.getDBConnectionFromQuery(q, datasourceUID)
	if err != nil {
		return getErrorFrameFromQuery(q), err
	}

	if ds.driverSettings.Timeout != 0 {
		tctx, cancel := context.WithTimeout(ctx, ds.driverSettings.Timeout)
		defer cancel()

		ctx = tctx
	}

	// FIXES:
	//  * Some datasources (snowflake) expire connections or have an authentication token that expires if not used in 1 or 4 hours.
	//    Because the datasource driver does not include an option for permanent connections, we retry the connection
	//    if the query fails. NOTE: this does not include some errors like "ErrNoRows"
	res, err := QueryDB(ctx, dbConn.db, ds.c.Converters(), fillMode, q)
	if err == nil {
		return res, nil
	}

	if errors.Is(err, ErrorNoResults) {
		return res, nil
	}

	// If there's a query error that didn't exceed the
	// context deadline retry the query
	if errors.Is(err, ErrorQuery) && !errors.Is(err, context.DeadlineExceeded) {
		for i := 0; i < ds.driverSettings.Retries; i++ {
			backend.Logger.Warn(fmt.Sprintf("query failed. retrying %d times", i))
			db, err := ds.dbReconnect(dbConn, q, cacheKey)
			if err != nil {
				return nil, err
			}

			if ds.driverSettings.Pause > 0 {
				time.Sleep(time.Duration(ds.driverSettings.Pause * int(time.Second)))
			}
			res, err = QueryDB(ctx, db, ds.c.Converters(), fillMode, q)
			if err == nil {
				return res, err
			}
		}
	}

	// allow retries on timeouts
	if errors.Is(err, context.DeadlineExceeded) {
		for i := 0; i < ds.driverSettings.Retries; i++ {
			backend.Logger.Warn(fmt.Sprintf("connection timed out. retrying %d times", i))
			db, err := ds.dbReconnect(dbConn, q, cacheKey)
			if err != nil {
				continue
			}

			res, err = QueryDB(ctx, db, ds.c.Converters(), fillMode, q)
			if err == nil {
				return res, err
			}
		}
	}

	return nil, err
}

func (ds *SQLDatasource) dbReconnect(dbConn dbConnection, q *Query, cacheKey string) (*sql.DB, error) {
	if err := dbConn.db.Close(); err != nil {
		backend.Logger.Warn(fmt.Sprintf("closing existing connection failed: %s", err.Error()))
	}

	db, err := ds.c.Connect(dbConn.settings, q.ConnectionArgs)
	if err != nil {
		return nil, err
	}
	ds.storeDBConnection(cacheKey, dbConnection{db, dbConn.settings})
	return db, nil
}

// CheckHealth pings the connected SQL database
func (ds *SQLDatasource) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	key := defaultKey(getDatasourceUID(*req.PluginContext.DataSourceInstanceSettings))
	dbConn, ok := ds.getDBConnection(key)
	if !ok {
		return nil, MissingDBConnection
	}

	if ds.driverSettings.Retries == 0 {
		return ds.check(dbConn)
	}

	return ds.checkWithRetries(dbConn)
}

func (ds *SQLDatasource) DriverSettings() DriverSettings {
	return ds.driverSettings
}

func (ds *SQLDatasource) checkWithRetries(conn dbConnection) (*backend.CheckHealthResult, error) {
	var result *backend.CheckHealthResult
	var err error

	for i := 0; i < ds.driverSettings.Retries; i++ {
		result, err = ds.check(conn)
		if err == nil {
			return result, err
		}
	}

	// TODO: failed health checks don't return an error
	return result, nil
}

func (ds *SQLDatasource) check(conn dbConnection) (*backend.CheckHealthResult, error) {
	if err := ds.ping(conn); err != nil {
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusError,
			Message: err.Error(),
		}, err
	}

	return &backend.CheckHealthResult{
		Status:  backend.HealthStatusOk,
		Message: "Data source is working",
	}, nil
}

func (ds *SQLDatasource) ping(conn dbConnection) error {
	if ds.driverSettings.Timeout == 0 {
		return conn.db.Ping()
	}

	ctx, cancel := context.WithTimeout(context.Background(), ds.driverSettings.Timeout)
	defer cancel()

	return conn.db.PingContext(ctx)
}
