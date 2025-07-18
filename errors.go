package sqlds

import (
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
	// ErrorPGXLifecycle is returned for PGX v5 specific connection lifecycle issues
	ErrorPGXLifecycle = errors.New("PGX connection lifecycle error")
)

func ErrorSource(err error) backend.ErrorSource {
	if backend.IsDownstreamError(err) {
		return backend.ErrorSourceDownstream
	}
	return backend.ErrorSourcePlugin
}

// IsPGXConnectionError checks if an error is related to PGX v5 connection issues
func IsPGXConnectionError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())
	pgxConnectionErrors := []string{
		"connection closed",
		"connection reset",
		"connection refused",
		"broken pipe",
		"eof",
		"context canceled",
		"context deadline exceeded",
		"pgconn",
		"conn is closed",
		"bad connection",
	}

	for _, pgxErr := range pgxConnectionErrors {
		if strings.Contains(errStr, pgxErr) {
			return true
		}
	}

	return false
}

// IsGenericDownstreamError checks if an error is a generic downstream error
func IsGenericDownstreamError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())
	genericDownstreamErrors := []string{
		"invalid memory address",
		"nil pointer dereference",
	}

	for _, genericErr := range genericDownstreamErrors {
		if strings.Contains(errStr, genericErr) {
			return true
		}
	}

	return false
}

// ClassifyError determines the appropriate error source and type for SQL errors
func ClassifyError(err error) (backend.ErrorSource, error) {
	if err == nil {
		return backend.ErrorSourcePlugin, nil
	}

	// Check for generic downstream errors first
	if IsGenericDownstreamError(err) {
		return backend.ErrorSourceDownstream, err
	}

	// Check for PGX v5 specific connection errors
	if IsPGXConnectionError(err) {
		// These are typically downstream connection issues
		return backend.ErrorSourceDownstream, ErrorPGXLifecycle
	}

	// Check for row validation errors
	if errors.Is(err, ErrorRowValidation) {
		return backend.ErrorSourceDownstream, err
	}

	// Default to existing logic
	if backend.IsDownstreamError(err) {
		return backend.ErrorSourceDownstream, err
	}

	return backend.ErrorSourcePlugin, err
}
