package sqlds

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/grafana/grafana-plugin-sdk-go/backend/resource/httpadapter"
	"github.com/grafana/grafana-plugin-sdk-go/data"
)

const defaultKeySuffix = "default"

var (
	MissingMultipleConnectionsConfig = errors.New("received connection arguments but the feature is not enabled")
	MissingDBConnection              = errors.New("unable to get default db connection")
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
	asyncDB  AsyncDB
}

type sqldatasource struct {
	Completable

	dbConnections  sync.Map
	c              Driver
	asyncDBGetter  AsyncDBGetter
	driverSettings DriverSettings

	backend.CallResourceHandler
	CustomRoutes map[string]func(http.ResponseWriter, *http.Request)
	// Enabling multiple connections may cause that concurrent connection limits
	// are hit. The datasource enabling this should make sure connections are cached
	// if necessary.
	EnableMultipleConnections bool
}

func (ds *sqldatasource) getDBConnection(key string) (dbConnection, bool) {
	conn, ok := ds.dbConnections.Load(key)
	if !ok {
		return dbConnection{}, false
	}
	return conn.(dbConnection), true
}

func (ds *sqldatasource) storeDBConnection(key string, dbConn dbConnection) {
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

// NewDatasource creates a new `sqldatasource`.
// It uses the provided settings argument to call the ds.Driver to connect to the SQL server
func (ds *sqldatasource) NewDatasource(settings backend.DataSourceInstanceSettings) (instancemgmt.Instance, error) {
	db, err := ds.c.Connect(settings, nil)
	if err != nil {
		return nil, err
	}

	var asyncDB AsyncDB
	if ds.asyncDBGetter != nil {
		asyncDB, err = ds.asyncDBGetter.GetAsyncDB(settings, nil)
		if err != nil {
			return nil, err
		}
	}

	key := defaultKey(getDatasourceUID(settings))
	ds.storeDBConnection(key, dbConnection{db: db, asyncDB: asyncDB, settings: settings})

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
func NewDatasource(c Driver) *sqldatasource {
	return &sqldatasource{
		c: c,
	}
}

// NewAsyncDatasource initializes the Datasource wrapper and instance manager
func NewAsyncDatasource(c Driver, a AsyncDBGetter) *sqldatasource {
	return &sqldatasource{
		c:             c,
		asyncDBGetter: a,
	}
}

// Dispose cleans up datasource instance resources.
// Note: Called when testing and saving a datasource
func (ds *sqldatasource) Dispose() {
}

type queryMeta struct {
	QueryID string `json:"queryID"`
	Status  string `json:"status"`
}

// QueryData creates the Responses list and executes each query
func (ds *sqldatasource) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
	var (
		response = NewResponse(backend.NewQueryDataResponse())
		wg       = sync.WaitGroup{}
	)

	wg.Add(len(req.Queries))

	// Execute each query and store the results by query RefID
	for _, q := range req.Queries {
		go func(query backend.DataQuery) {
			var frames data.Frames
			var err error
			_, isFromAlert := req.Headers["FromAlert"]
			if ds.asyncDBGetter != nil && !isFromAlert {
				frames, err = ds.handleAsyncQuery(ctx, query, getDatasourceUID(*req.PluginContext.DataSourceInstanceSettings))
			} else {
				frames, err = ds.handleQuery(ctx, query, getDatasourceUID(*req.PluginContext.DataSourceInstanceSettings))
			}

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

func (ds *sqldatasource) getDBConnectionFromConnArgs(datasourceUID string, connArgs json.RawMessage) (string, dbConnection, error) {
	if !ds.EnableMultipleConnections && len(connArgs) > 0 {
		return "", dbConnection{}, MissingMultipleConnectionsConfig
	}
	// The database connection may vary depending on query arguments
	// The raw arguments are used as key to store the db connection in memory so they can be reused
	key := defaultKey(datasourceUID)
	dbConn, ok := ds.getDBConnection(key)
	if !ok {
		return "", dbConnection{}, MissingDBConnection
	}
	if !ds.EnableMultipleConnections || len(connArgs) == 0 {
		return key, dbConn, nil
	}

	key = keyWithConnectionArgs(datasourceUID, connArgs)
	if cachedConn, ok := ds.getDBConnection(key); ok {
		return key, cachedConn, nil
	}

	db, err := ds.c.Connect(dbConn.settings, connArgs)
	if err != nil {
		return "", dbConnection{}, err
	}

	var asyncDB AsyncDB
	if ds.asyncDBGetter != nil {
		asyncDB, err = ds.asyncDBGetter.GetAsyncDB(dbConn.settings, connArgs)
		if err != nil {
			return "", dbConnection{}, err
		}
	}

	// Assign this connection in the cache
	dbConn = dbConnection{db: db, asyncDB: asyncDB, settings: dbConn.settings}
	ds.storeDBConnection(key, dbConn)

	return key, dbConn, nil
}

// handleQuery will call query, and attempt to reconnect if the query failed
func (ds *sqldatasource) handleAsyncQuery(ctx context.Context, req backend.DataQuery, datasourceUID string) (data.Frames, error) {
	// Convert the backend.DataQuery into a Query object
	q, err := GetQuery(req)
	if err != nil {
		return getErrorFrameFromQuery(q), err
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
	cacheKey, dbConn, err := ds.getDBConnectionFromConnArgs(datasourceUID, q.ConnectionArgs)
	if err != nil {
		return getErrorFrameFromQuery(q), err
	}
	if dbConn.asyncDB == nil {
		return nil, fmt.Errorf("unable to get AsyncDB")
	}

	if ds.driverSettings.Timeout != 0 {
		tctx, cancel := context.WithTimeout(ctx, ds.driverSettings.Timeout)
		defer cancel()
		ctx = tctx
	}

	if q.QueryID == "" {
		queryID, err := startQuery(ctx, dbConn.asyncDB, q)
		if err != nil {
			return getErrorFrameFromQuery(q), err
		}
		return data.Frames{
			{Meta: &data.FrameMeta{
				ExecutedQueryString: q.RawSQL,
				Custom:              queryMeta{QueryID: queryID, Status: "started"}},
			},
		}, nil
	}

	status, err := queryStatus(ctx, dbConn.asyncDB, q)
	if err != nil {
		return getErrorFrameFromQuery(q), err
	}
	if !status.Finished() {
		return data.Frames{
			{Meta: &data.FrameMeta{
				ExecutedQueryString: q.RawSQL,
				Custom:              queryMeta{QueryID: q.QueryID, Status: status.String()}},
			},
		}, nil
	}

	res, err := queryAsync(ctx, dbConn.db, ds.c.Converters(), fillMode, q)
	if err == nil || errors.Is(err, ErrorNoResults) {
		return res, nil
	}

	if !errors.Is(err, ErrorQuery) {
		return nil, err
	}

	// If there's a query error that didn't exceed the context deadline retry the query
	db, err := ds.c.Connect(dbConn.settings, q.ConnectionArgs)
	if err != nil {
		return nil, err
	}

	var asyncDB AsyncDB
	if ds.asyncDBGetter != nil {
		asyncDB, err = ds.asyncDBGetter.GetAsyncDB(dbConn.settings, q.ConnectionArgs)
		if err != nil {
			return nil, err
		}
	}

	// Assign this connection in the cache
	dbConn = dbConnection{db: db, asyncDB: asyncDB, settings: dbConn.settings}
	ds.storeDBConnection(cacheKey, dbConn)
	return queryAsync(ctx, dbConn.db, ds.c.Converters(), fillMode, q)
}

// handleQuery will call query, and attempt to reconnect if the query failed
func (ds *sqldatasource) handleQuery(ctx context.Context, req backend.DataQuery, datasourceUID string) (data.Frames, error) {
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
	cacheKey, dbConn, err := ds.getDBConnectionFromConnArgs(datasourceUID, q.ConnectionArgs)
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
	res, err := query(ctx, dbConn.db, ds.c.Converters(), fillMode, q)
	if err == nil {
		return res, nil
	}

	if errors.Is(err, ErrorNoResults) {
		return res, nil
	}

	// If there's a query error that didn't exceed the
	// context deadline retry the query
	if errors.Is(err, ErrorQuery) && !errors.Is(err, context.DeadlineExceeded) {
		db, err := ds.c.Connect(dbConn.settings, q.ConnectionArgs)
		if err != nil {
			return nil, err
		}
		ds.storeDBConnection(cacheKey, dbConnection{db: db, settings: dbConn.settings})

		return query(ctx, db, ds.c.Converters(), fillMode, q)
	}

	return nil, err
}

// CheckHealth pings the connected SQL database
func (ds *sqldatasource) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	key := defaultKey(getDatasourceUID(*req.PluginContext.DataSourceInstanceSettings))
	dbConn, ok := ds.getDBConnection(key)
	if !ok {
		return nil, MissingDBConnection
	}
	if err := dbConn.db.Ping(); err != nil {
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusError,
			Message: err.Error(),
		}, nil
	}

	return &backend.CheckHealthResult{
		Status:  backend.HealthStatusOk,
		Message: "Data source is working",
	}, nil
}
