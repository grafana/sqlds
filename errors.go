package sqlds

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

var (
	// ErrorBadDatasource is returned if the data source could not be asserted to the correct type (this should basically never happen?)
	ErrorBadDatasource = errors.New("type assertion to datasource failed")
	// ErrorJSON is returned when json.Unmarshal fails
	ErrorJSON = errors.New("error unmarshaling query JSON the Query Model")
	// ErrorQuery is returned when the query could not complete / execute
	ErrorQuery = errors.New("error querying the database")
	// ErrorTimeout is returned if the query has timed out
	ErrorTimeout = errors.New("query timeout exceeded")
	// ErrorNoResults is returned if there were no results returned
	ErrorNoResults = errors.New("no results returned from query")
	// ErrorRowValidation is returned when SQL rows validation fails (e.g., connection issues, corrupt results)
	ErrorRowValidation = errors.New("SQL rows validation failed")
	// ErrorConnectionClosed is returned when the database connection is unexpectedly closed
	ErrorConnectionClosed = errors.New("database connection closed")
)

func ErrorSource(err error) backend.ErrorSource {
	if backend.IsDownstreamError(err) {
		return backend.ErrorSourceDownstream
	}
	return backend.ErrorSourcePlugin
}

// isConnectionClosedError reports whether err indicates the underlying
// *sql.DB connection pool has been closed. This happens as a side effect of
// sqlds tearing down or reconnecting the shared connection (see
// Connector.Dispose / Connector.Reconnect), typically while a concurrent
// query is still in flight, so it should be treated as a downstream error
// rather than counted against the plugin.
//
// database/sql returns an unexported sentinel ("sql: database is closed")
// when the pool is closed, so we match its message in addition to the
// exported sql.ErrConnDone ("sql: connection is already closed").
func isConnectionClosedError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, sql.ErrConnDone) {
		return true
	}
	return strings.Contains(err.Error(), "sql: database is closed")
}
