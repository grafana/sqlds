package sqlds

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
	"github.com/pkg/errors"
)

// FormatQueryOption defines how the user has chosen to represent the data
type FormatQueryOption uint32

const (
	// FormatOptionTimeSeries formats the query results as a timeseries using "WideToLong"
	FormatOptionTimeSeries FormatQueryOption = iota
	// FormatOptionTable formats the query results as a table using "LongToWide"
	FormatOptionTable
)

// Query is the model that represents the query that users submit from the panel / queryeditor.
// For the sake of backwards compatibility, when making changes to this type, ensure that changes are
// only additive.
type Query struct {
	RawSQL string            `json:"rawSql"`
	Format FormatQueryOption `json:"format"`

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

// GetQuery returns a Query object given a backend.DataQuery using json.Unmarshal
func GetQuery(query backend.DataQuery) (*Query, error) {
	model := &Query{}

	if err := json.Unmarshal(query.JSON, &model); err != nil {
		return nil, ErrorJSON
	}

	// Copy directly from the well typed query
	return &Query{
		RawSQL:        model.RawSQL,
		Format:        model.Format,
		Interval:      query.Interval,
		TimeRange:     query.TimeRange,
		MaxDataPoints: query.MaxDataPoints,
	}, nil
}

// query sends the query to the sql.DB and converts the rows to a dataframe.
func query(db *sql.DB, converters []sqlutil.StringConverter, fillMode *data.FillMissing, query *Query) (data.Frames, error) {
	// Query the rows from the database
	rows, err := db.Query(query.RawSQL)
	if err != nil {
		return nil, errors.Wrap(ErrorQuery, err.Error())
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
	res, err := getFrames(rows, -1, converters, fillMode, query)
	if err != nil {
		return nil, errors.WithMessage(err, "Could not process SQL results")
	}

	return res, nil
}

func getFrames(rows *sql.Rows, limit int64, converters []sqlutil.StringConverter, fillMode *data.FillMissing, query *Query) (data.Frames, error) {
	frame, _, err := sqlutil.FrameFromRows(rows, limit, converters...)
	if err != nil {
		return nil, err
	}
	if frame.Meta == nil {
		frame.Meta = &data.FrameMeta{}
	}

	frame.Meta.ExecutedQueryString = query.RawSQL

	if query.Format == FormatOptionTable {
		return data.Frames{frame}, nil
	}

	if frame.TimeSeriesSchema().Type == data.TimeSeriesTypeLong {
		frame, err := data.LongToWide(frame, fillMode)
		if err != nil {
			return nil, err
		}
		return data.Frames{frame}, nil
	}

	return data.Frames{frame}, nil
}
