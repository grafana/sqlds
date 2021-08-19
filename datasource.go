package sqlds

import (
	"context"
	"database/sql"
	"net/http"
	"sync"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/grafana/grafana-plugin-sdk-go/backend/resource/httpadapter"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/pkg/errors"
)

type sqldatasource struct {
	dbConnections map[string]*sql.DB
	c             Driver
	settings      backend.DataSourceInstanceSettings

	backend.CallResourceHandler
	Completable
	CustomRoutes map[string]func(http.ResponseWriter, *http.Request)
}

// NewDatasource creates a new `sqldatasource`.
// It uses the provided settings argument to call the ds.Driver to connect to the SQL server
func (ds *sqldatasource) NewDatasource(settings backend.DataSourceInstanceSettings) (instancemgmt.Instance, error) {
	db, err := ds.c.Connect(settings, nil)
	if err != nil {
		return nil, err
	}
	ds.dbConnections = map[string]*sql.DB{"": db}
	ds.settings = settings
	mux := http.NewServeMux()
	err = ds.registerRoutes(mux)
	if err != nil {
		return nil, err
	}
	ds.CallResourceHandler = httpadapter.New(mux)

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
	for k, db := range ds.dbConnections {
		db.Close()
		delete(ds.dbConnections, k)
	}
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
			frames, err := ds.handleQuery(query)

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
	key := ""
	db := ds.dbConnections[key]
	var err error
	if len(q.Args) != 0 {
		key = string(q.Args[:])
		if cachedDB, ok := ds.dbConnections[key]; ok {
			db = cachedDB
		} else {
			db, err = ds.c.Connect(ds.settings, q.Args)
			if err != nil {
				return nil, "", err
			}
			ds.dbConnections[key] = db
		}
	}
	return db, key, nil
}

// handleQuery will call query, and attempt to reconnect if the query failed
func (ds *sqldatasource) handleQuery(req backend.DataQuery) (data.Frames, error) {
	// Convert the backend.DataQuery into a Query object
	q, err := GetQuery(req)
	if err != nil {
		return nil, err
	}

	// Apply supported macros to the query
	q.RawSQL, err = interpolate(ds.c, q)
	if err != nil {
		return nil, errors.WithMessage(err, "Could not apply macros")
	}

	// Apply the default FillMode, overwritting it if the query specifies it
	fillMode := ds.c.FillMode()
	if q.FillMissing != nil {
		fillMode = q.FillMissing
	}

	// Retrieve the database connection
	db, cacheKey, err := ds.getDB(q)
	if err != nil {
		return nil, err
	}

	// FIXES:
	//  * Some datasources (snowflake) expire connections or have an authentication token that expires if not used in 1 or 4 hours.
	//    Because the datasource driver does not include an option for permanent connections, we retry the connection
	//    if the query fails. NOTE: this does not include some errors like "ErrNoRows"
	res, err := query(db, ds.c.Converters(), fillMode, q)
	if err == nil {
		return res, nil
	}

	if errors.Cause(err) == ErrorQuery {
		ds.dbConnections[cacheKey], err = ds.c.Connect(ds.settings, q.Args)
		if err != nil {
			return nil, err
		}
		return query(ds.dbConnections[cacheKey], ds.c.Converters(), fillMode, q)
	}

	return nil, err
}

// CheckHealth pings the connected SQL database
func (ds *sqldatasource) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	if err := ds.dbConnections[""].Ping(); err != nil {
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
