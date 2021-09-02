package sqlds

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/grafana/grafana-plugin-sdk-go/backend/resource/httpadapter"
	"github.com/grafana/grafana-plugin-sdk-go/data"
)

const defaultKey = "_default"

type sqldatasource struct {
	Completable

	dbConnections  sync.Map
	c              Driver
	driverSettings DriverSettings
	settings       backend.DataSourceInstanceSettings

	backend.CallResourceHandler
	CustomRoutes map[string]func(http.ResponseWriter, *http.Request)
	// Enabling multiple connections may cause that concurrent connection limits
	// are hit. The datasource enabling this should make sure connections are cached
	// if necessary.
	EnableMultipleConnections bool
}

// NewDatasource creates a new `sqldatasource`.
// It uses the provided settings argument to call the ds.Driver to connect to the SQL server
func (ds *sqldatasource) NewDatasource(settings backend.DataSourceInstanceSettings) (instancemgmt.Instance, error) {
	db, err := ds.c.Connect(settings, nil)
	if err != nil {
		return nil, err
	}
	ds.dbConnections.Store(defaultKey, db)
	ds.settings = settings
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

// Dispose cleans up datasource instance resources.
func (ds *sqldatasource) Dispose() {
	ds.dbConnections.Range(func(key, db interface{}) bool {
		err := db.(*sql.DB).Close()
		if err != nil {
			backend.Logger.Error(err.Error())
		}
		ds.dbConnections.Delete(key)
		return true
	})
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
			frames, err := ds.handleQuery(ctx, query)

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

func (ds *sqldatasource) getDB(q *Query) (*sql.DB, string, error) {
	// The database connection may vary depending on query arguments
	// The raw arguments are used as key to store the db connection in memory so they can be reused
	key := defaultKey
	db, ok := ds.dbConnections.Load(key)
	if !ok {
		return nil, "", fmt.Errorf("unable to get default db connection")
	}
	if !ds.EnableMultipleConnections || len(q.ConnectionArgs) == 0 {
		return db.(*sql.DB), key, nil
	}

	key = string(q.ConnectionArgs)
	if cachedDB, ok := ds.dbConnections.Load(key); ok {
		return cachedDB.(*sql.DB), key, nil
	}

	var err error
	db, err = ds.c.Connect(ds.settings, q.ConnectionArgs)
	if err != nil {
		return nil, "", err
	}
	// Assign this connection in the cache
	ds.dbConnections.Store(key, db)

	return db.(*sql.DB), key, nil
}

// handleQuery will call query, and attempt to reconnect if the query failed
func (ds *sqldatasource) handleQuery(ctx context.Context, req backend.DataQuery) (data.Frames, error) {
	// Convert the backend.DataQuery into a Query object
	q, err := GetQuery(req)
	if err != nil {
		return getErrorFrameFromQuery(q), err
	}

	// Apply supported macros to the query
	q.RawSQL, err = interpolate(ds.c, q)
	if err != nil {
		return getErrorFrameFromQuery(q), fmt.Errorf("%s: %w", "Could not apply macros", err)
	}

	// Apply the default FillMode, overwritting it if the query specifies it
	fillMode := ds.driverSettings.FillMode
	if q.FillMissing != nil {
		fillMode = q.FillMissing
	}

	// Retrieve the database connection
	db, cacheKey, err := ds.getDB(q)
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
	res, err := query(ctx, db, ds.c.Converters(), fillMode, q)
	if err == nil {
		return res, nil
	}

	if errors.Is(err, ErrorNoResults) {
		return res, nil
	}

	if errors.Is(err, ErrorQuery) {
		db, err = ds.c.Connect(ds.settings, q.ConnectionArgs)
		if err != nil {
			return nil, err
		}
		ds.dbConnections.Store(cacheKey, db)

		return query(ctx, db, ds.c.Converters(), fillMode, q)
	}

	return nil, err
}

// CheckHealth pings the connected SQL database
func (ds *sqldatasource) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	db, ok := ds.dbConnections.Load(defaultKey)
	if !ok {
		return nil, fmt.Errorf("unable to get default db connection")
	}
	if err := db.(*sql.DB).Ping(); err != nil {
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
