package sqlds

import (
	"context"
	"database/sql"
	"encoding/json"
	"io/ioutil"
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

func handleError(rw http.ResponseWriter, err error) {
	rw.WriteHeader(http.StatusBadRequest)
	_, err = rw.Write([]byte(err.Error()))
	if err != nil {
		backend.Logger.Error(err.Error())
	}
}

type columnRequest struct {
	Table string `json:"table"`
}

func (ds *sqldatasource) handle(resource string) func(rw http.ResponseWriter, req *http.Request) {
	return func(rw http.ResponseWriter, req *http.Request) {
		if ds.Completable == nil {
			handleError(rw, errors.New("not implemented"))
			return
		}

		var res []string
		var err error
		switch resource {
		case "tables":
			res, err = ds.Completable.Tables(req.Context(), ds.db)
		case "schemas":
			res, err = ds.Completable.Schemas(req.Context(), ds.db)
		case "columns":
			if req.Body == nil {
				handleError(rw, errors.New("missing table name in request body"))
				return
			}
			reqBody, err := ioutil.ReadAll(req.Body)
			if err != nil {
				handleError(rw, err)
				return
			}
			column := columnRequest{}
			err = json.Unmarshal(reqBody, &column)
			if err != nil {
				handleError(rw, err)
				return
			}
			res, err = ds.Completable.Columns(req.Context(), ds.db, column.Table)
			if err != nil {
				handleError(rw, err)
				return
			}
		}
		if err != nil {
			handleError(rw, err)
			return
		}

		resJSON, err := json.Marshal(res)
		if err != nil {
			handleError(rw, err)
			return
		}
		rw.Header().Add("Content-Type", "application/json")
		_, err = rw.Write(resJSON)
		if err != nil {
			backend.Logger.Error(err.Error())
		}
	}
}

func (ds *sqldatasource) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/tables", ds.handle("tables"))
	mux.HandleFunc("/schemas", ds.handle("schemas"))
	mux.HandleFunc("/columns", ds.handle("columns"))
}
