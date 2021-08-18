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
	db       *sql.DB
	c        Driver
	settings backend.DataSourceInstanceSettings

	backend.CallResourceHandler
	Completable
	DB func(q *Query) (*sql.DB, error)
}

// NewDatasource creates a new `sqldatasource`.
// It uses the provided settings argument to call the ds.Driver to connect to the SQL server
func (ds *sqldatasource) NewDatasource(settings backend.DataSourceInstanceSettings) (instancemgmt.Instance, error) {
	db, err := ds.c.Connect(settings)
	if err != nil {
		return nil, err
	}
	ds.db = db
	ds.settings = settings
	mux := http.NewServeMux()
	ds.registerRoutes(mux)
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
	ds.db.Close()
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

	// The database connection may vary depending on query arguments
	db := ds.db
	if ds.DB != nil {
		db, err = ds.DB(q)
		if err != nil {
			return nil, err
		}
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
		ds.db, err = ds.c.Connect(ds.settings)
		if err != nil {
			return nil, err
		}
		return query(db, ds.c.Converters(), fillMode, q)
	}

	return nil, err
}

// CheckHealth pings the connected SQL database
func (ds *sqldatasource) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	if err := ds.db.Ping(); err != nil {
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
