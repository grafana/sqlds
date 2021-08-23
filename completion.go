package sqlds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

// ErrorNotImplemented is returned if the function is not implemented by the provided Driver (the Completable pointer is nil)
var ErrorNotImplemented = errors.New("not implemented")

// Completable will be used to autocomplete Tables Schemas and Columns for SQL languages
type Completable interface {
	Schemas(ctx context.Context) ([]string, error)
	Tables(ctx context.Context, schema string) ([]string, error)
	Columns(ctx context.Context, table string) ([]string, error)
}

func handleError(rw http.ResponseWriter, err error) {
	rw.WriteHeader(http.StatusBadRequest)
	_, err = rw.Write([]byte(err.Error()))
	if err != nil {
		backend.Logger.Error(err.Error())
	}
}

func sendResourceResponse(rw http.ResponseWriter, res []string) {
	rw.Header().Add("Content-Type", "application/json")
	if err := json.NewEncoder(rw).Encode(res); err != nil {
		handleError(rw, err)
		return
	}
}

type tableRequest struct {
	Schema string `json:"schema"`
}

type columnRequest struct {
	Table string `json:"table"`
}

func (ds *sqldatasource) getSchemas(rw http.ResponseWriter, req *http.Request) {
	if ds.Completable == nil {
		handleError(rw, ErrorNotImplemented)
		return
	}

	res, err := ds.Completable.Schemas(req.Context())
	if err != nil {
		handleError(rw, err)
		return
	}

	sendResourceResponse(rw, res)
}

func (ds *sqldatasource) getTables(rw http.ResponseWriter, req *http.Request) {
	if ds.Completable == nil {
		handleError(rw, ErrorNotImplemented)
		return
	}

	reqBody := tableRequest{}
	if err := json.NewDecoder(req.Body).Decode(&reqBody); err != nil {
		handleError(rw, err)
		return
	}
	res, err := ds.Completable.Tables(req.Context(), reqBody.Schema)
	if err != nil {
		handleError(rw, err)
		return
	}

	sendResourceResponse(rw, res)
}

func (ds *sqldatasource) getColumns(rw http.ResponseWriter, req *http.Request) {
	if ds.Completable == nil {
		handleError(rw, ErrorNotImplemented)
		return
	}

	column := columnRequest{}
	if err := json.NewDecoder(req.Body).Decode(&column); err != nil {
		handleError(rw, err)
		return
	}
	res, err := ds.Completable.Columns(req.Context(), column.Table)
	if err != nil {
		handleError(rw, err)
		return
	}

	sendResourceResponse(rw, res)
}

func (ds *sqldatasource) registerRoutes(mux *http.ServeMux) error {
	defaultRoutes := map[string]func(http.ResponseWriter, *http.Request){
		"/tables":  ds.getTables,
		"/schemas": ds.getSchemas,
		"/columns": ds.getColumns,
	}
	for route, handler := range defaultRoutes {
		mux.HandleFunc(route, handler)
	}
	for route, handler := range ds.CustomRoutes {
		if _, ok := defaultRoutes[route]; ok {
			return fmt.Errorf("unable to redefine %s, use the Completable interface instead", route)
		}
		mux.HandleFunc(route, handler)
	}
	return nil
}
