package sqlds

import "github.com/pkg/errors"

var (
	// ErrorBadDatasource ...
	ErrorBadDatasource = errors.New("type assertion to datasource failed")
	// ErrorJSON is returned when json.Unmarshal fails
	ErrorJSON = errors.New("error unmarshaling query JSON the Query Model")
	// ErrorQuery is returned when the query could not complete / execute
	ErrorQuery = errors.New("error querying the database")
)
