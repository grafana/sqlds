package sqlds

import (
	"context"
	"database/sql"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/datasource"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/pkg/errors"
)

// Datasource contains the entrypoints / handlers for the plugin, and manages the plugin instance
type Datasource struct {
	im instancemgmt.InstanceManager
	c  Driver
}

type sqldatasource struct {
	db       *sql.DB
	c        Driver
	settings backend.DataSourceInstanceSettings
}

// NewDatasource creates a new `sqldatasource`.
// It uses the provided settings argument to call the ds.Driver to connect to the SQL server
func (ds *Datasource) NewDatasource(settings backend.DataSourceInstanceSettings) (instancemgmt.Instance, error) {
	db, err := ds.c.Connect(settings)
	if err != nil {
		return nil, err
	}

	return &sqldatasource{
		db:       db,
		c:        ds.c,
		settings: settings,
	}, nil
}

// NewDatasource initializes the Datasource wrapper and instance manager
func NewDatasource(c Driver) datasource.ServeOpts {
	ds := &Datasource{
		c: c,
	}

	ds.im = datasource.NewInstanceManager(ds.NewDatasource)

	return datasource.ServeOpts{
		QueryDataHandler:   ds,
		CheckHealthHandler: ds,
	}
}

// QueryData creates the Responses list and executes each query
func (ds *sqldatasource) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
	// create response struct
	response := backend.NewQueryDataResponse()

	// Execute each query and store the results by query RefID
	for _, q := range req.Queries {
		frames, err := ds.handleQuery(q)
		response.Responses[q.RefID] = backend.DataResponse{
			Frames: frames,
			Error:  err,
		}
	}

	return response, nil

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

	// FIXES:
	//  * Some datasources (snowflake) expire connections or have an authentication token that expires if not used in 1 or 4 hours.
	//    Because the datasource driver does not include an option for permanent connections, we retry the connection
	//    if the query fails. NOTE: this does not include some errors like "ErrNoRows"
	res, err := query(ds.db, ds.c.Converters(), ds.c.FillMode(), q)
	if err == nil {
		return res, nil
	}

	if errors.Cause(err) == ErrorQuery {
		ds.db, err = ds.c.Connect(ds.settings)
		if err != nil {
			return nil, err
		}
		return query(ds.db, ds.c.Converters(), ds.c.FillMode(), q)
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

// QueryData calls the wrapped instance's QueryData function
func (ds *Datasource) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
	h, err := ds.im.Get(req.PluginContext)
	if err != nil {
		return nil, err
	}

	if val, ok := h.(*sqldatasource); ok {
		return val.QueryData(ctx, req)
	}

	return nil, ErrorBadDatasource
}

// CheckHealth calls the wrapped instance's CheckHealth function
func (ds *Datasource) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	h, err := ds.im.Get(req.PluginContext)
	if err != nil {
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusError,
			Message: err.Error(),
		}, nil
	}

	if val, ok := h.(*sqldatasource); ok {
		return val.CheckHealth(ctx, req)
	}

	return nil, ErrorBadDatasource
}
