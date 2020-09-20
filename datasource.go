package sqlds

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/datasource"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"

	"github.com/pkg/errors"
)

var (
	// ErrorBadDatasource ...
	ErrorBadDatasource = errors.New("type assertion to datasource failed")
	// ErrorJSON is returned when json.Unmarshal fails
	ErrorJSON = errors.New("error unmarshaling query JSON the Query Model")
)

// Driver is a simple interface that defines how to connect to a backend SQL datasource
// Plugin creators will need to implement this in order to create a managed datasource
type Driver interface {
	// Connect connects to the database. It does not need to call `db.Ping()`
	Connect(backend.DataSourceInstanceSettings) (*sql.DB, error)
	FillMode() *data.FillMissing
	Macros() Macros
}

// Datasource ...
type Datasource struct {
	im instancemgmt.InstanceManager
	c  Driver
}

type sqldatasource struct {
	db *sql.DB
	c  Driver
}

// GetQuery returns a Query object given a backend.DataQuery using json.Unmarshal
func GetQuery(query backend.DataQuery) (*Query, error) {
	model := &Query{}

	if err := json.Unmarshal(query.JSON, &model); err != nil {
		return nil, ErrorJSON
	}

	// Copy directly from the well typed query
	return &Query{
		RawSQL:        model.RawSQL,
		Interval:      query.Interval,
		TimeRange:     query.TimeRange,
		MaxDataPoints: query.MaxDataPoints,
	}, nil
}

// NewDatasource ...
func (ds *Datasource) NewDatasource(settings backend.DataSourceInstanceSettings) (instancemgmt.Instance, error) {
	db, err := ds.c.Connect(settings)
	if err != nil {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		return nil, err
	}

	return &sqldatasource{
		db: db,
		c:  ds.c,
	}, nil
}

// NewDatasource ...
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

// Query ...
type Query struct {
	RawSQL string `json:"rawSql"`

	Interval      time.Duration     `json:"-"`
	TimeRange     backend.TimeRange `json:"-"`
	MaxDataPoints int64             `json:"-"`
}

// WithSQL copies the Query, but with a different RawSQL value.
// This is mostly useful in the Interpolate function, where the RawSQL value is modified in a loop
func (q *Query) WithSQL(query string) *Query {
	return &Query{
		RawSQL:        query,
		Interval:      q.Interval,
		TimeRange:     q.TimeRange,
		MaxDataPoints: q.MaxDataPoints,
	}
}

func query(db *sql.DB, driver Driver, req backend.DataQuery) (data.Frames, error) {
	// Convert the backend.DataQuery into a Query object
	query, err := GetQuery(req)
	if err != nil {
		return nil, err
	}

	// Apply supported macros to the query
	// TODO: driver.interpolate
	rawSQL, err := interpolate(driver, query)
	if err != nil {
		return nil, errors.WithMessage(err, "Could not apply macros")
	}

	// Query the rows from the database
	rows, err := db.Query(rawSQL)
	if err != nil {
		return nil, errors.WithMessage(err, "Could not query series")
	}

	// Check for an error response
	if err := rows.Err(); err != nil {
		if err == sql.ErrNoRows {
			// Should we even response with an error here?
			// The panel will simply show "no data"
			return nil, errors.WithMessage(err, "No results from query")
		}
		return nil, errors.WithMessage(err, "Error response from database")
	}

	defer func() {
		if err := rows.Close(); err != nil {
			backend.Logger.Error(err.Error())
		}
	}()

	// Convert the response to frames
	res, err := getFrames(rows, query.MaxDataPoints, driver.FillMode())
	if err != nil {
		return nil, errors.WithMessage(err, "Could not process SQL results")
	}

	return res, nil
}

func (ds *sqldatasource) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
	// create response struct
	response := backend.NewQueryDataResponse()

	// Execute each query and store the results by query RefID
	for _, q := range req.Queries {
		frames, err := query(ds.db, ds.c, q)
		response.Responses[q.RefID] = backend.DataResponse{
			Frames: frames,
			Error:  err,
		}
	}

	return response, nil

}

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

// QueryData ...
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

// CheckHealth ...
func (ds *Datasource) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	h, err := ds.im.Get(req.PluginContext)
	if err != nil {
		return nil, err
	}

	if val, ok := h.(*sqldatasource); ok {
		return val.CheckHealth(ctx, req)
	}

	return nil, ErrorBadDatasource
}

func getFrames(rows *sql.Rows, limit int64, fillMode *data.FillMissing) (data.Frames, error) {
	frame, _, err := sqlutil.FrameFromRows(rows, limit, sqlutil.StringConverter{})
	if err != nil {
		return nil, err
	}

	frame, err = data.LongToWide(frame, fillMode)

	if err != nil {
		return nil, err
	}

	return data.Frames{frame}, nil
}
