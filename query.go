package sqlds

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/grafana/dataplane/sdata/timeseries"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
)

// FormatQueryOption defines how the user has chosen to represent the data
type FormatQueryOption uint32

const (
	// FormatOptionTimeSeries formats the query results as a timeseries using "LongToWide"
	FormatOptionTimeSeries FormatQueryOption = iota
	// FormatOptionTable sets the preferred visualization to table
	FormatOptionTable
	// FormatOptionLogs sets the preferred visualization to logs
	FormatOptionLogs
	// FormatOptionsTrace sets the preferred visualization to trace
	FormatOptionTrace
	// FormatOptionMulti formats the query results as a timeseries using "LongToMulti"
	FormatOptionMulti
)

// Query is the model that represents the query that users submit from the panel / queryeditor.
// For the sake of backwards compatibility, when making changes to this type, ensure that changes are
// only additive.
type Query struct {
	RawSQL         string            `json:"rawSql"`
	Format         FormatQueryOption `json:"format"`
	ConnectionArgs json.RawMessage   `json:"connectionArgs"`

	RefID         string            `json:"-"`
	Interval      time.Duration     `json:"-"`
	TimeRange     backend.TimeRange `json:"-"`
	MaxDataPoints int64             `json:"-"`
	FillMissing   *data.FillMissing `json:"fillMode,omitempty"`

	// Macros
	Schema string `json:"schema,omitempty"`
	Table  string `json:"table,omitempty"`
	Column string `json:"column,omitempty"`
}

// WithSQL copies the Query, but with a different RawSQL value.
// This is mostly useful in the Interpolate function, where the RawSQL value is modified in a loop
func (q *Query) WithSQL(query string) *Query {
	return &Query{
		RawSQL:         query,
		ConnectionArgs: q.ConnectionArgs,
		RefID:          q.RefID,
		Interval:       q.Interval,
		TimeRange:      q.TimeRange,
		MaxDataPoints:  q.MaxDataPoints,
		FillMissing:    q.FillMissing,
		Schema:         q.Schema,
		Table:          q.Table,
		Column:         q.Column,
	}
}

// GetQuery returns a Query object given a backend.DataQuery using json.Unmarshal
func GetQuery(query backend.DataQuery, headers http.Header, setHeaders bool) (*Query, error) {
	model := &Query{}

	if err := json.Unmarshal(query.JSON, &model); err != nil {
		return nil, PluginError(fmt.Errorf("%w: %v", ErrorJSON, err))
	}

	if setHeaders {
		applyHeaders(model, headers)
	}

	// Copy directly from the well typed query
	return &Query{
		RawSQL:         model.RawSQL,
		Format:         model.Format,
		ConnectionArgs: model.ConnectionArgs,
		RefID:          query.RefID,
		Interval:       query.Interval,
		TimeRange:      query.TimeRange,
		MaxDataPoints:  query.MaxDataPoints,
		FillMissing:    model.FillMissing,
		Schema:         model.Schema,
		Table:          model.Table,
		Column:         model.Column,
	}, nil
}

// getErrorFrameFromQuery returns a error frames with empty data and meta fields
func getErrorFrameFromQuery(query *Query) data.Frames {
	frames := data.Frames{}
	frame := data.NewFrame(query.RefID)
	frame.Meta = &data.FrameMeta{
		ExecutedQueryString: query.RawSQL,
	}
	frames = append(frames, frame)
	return frames
}

// QueryDB sends the query to the connection and converts the rows to a dataframe.
func QueryDB(ctx context.Context, db Connection, converters []sqlutil.Converter, fillMode *data.FillMissing, query *Query, args ...interface{}) (data.Frames, error) {
	// Query the rows from the database
	rows, err := db.QueryContext(ctx, query.RawSQL, args...)
	if err != nil {
		errType := ErrorQuery
		if errors.Is(err, context.Canceled) {
			errType = context.Canceled
		}
		err := DownstreamError(fmt.Errorf("%w: %s", errType, err.Error()))
		return getErrorFrameFromQuery(query), err
	}

	// Check for an error response
	if err := rows.Err(); err != nil {
		if err == sql.ErrNoRows {
			// Should we even response with an error here?
			// The panel will simply show "no data"
			err := DownstreamError(fmt.Errorf("%s: %w", "No results from query", err))
			return getErrorFrameFromQuery(query), err
		}
		err := DownstreamError(fmt.Errorf("%s: %w", "Error response from database", err))
		return getErrorFrameFromQuery(query), err
	}

	defer func() {
		if err := rows.Close(); err != nil {
			backend.Logger.Error(err.Error())
		}
	}()

	// Convert the response to frames
	res, err := getFrames(rows, -1, converters, fillMode, query)
	if err != nil {
		err := PluginError(fmt.Errorf("%w: %s", err, "Could not process SQL results"))
		return getErrorFrameFromQuery(query), err
	}

	return res, nil
}

func getFrames(rows *sql.Rows, limit int64, converters []sqlutil.Converter, fillMode *data.FillMissing, query *Query) (data.Frames, error) {
	frame, err := sqlutil.FrameFromRows(rows, limit, converters...)
	if err != nil {
		return nil, err
	}
	frame.Name = query.RefID
	if frame.Meta == nil {
		frame.Meta = &data.FrameMeta{}
	}

	frame.Meta.ExecutedQueryString = query.RawSQL
	frame.Meta.PreferredVisualization = data.VisTypeGraph

	count, err := frame.RowLen()
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, ErrorNoResults
	}

	switch query.Format {
	case FormatOptionMulti:
		if frame.TimeSeriesSchema().Type == data.TimeSeriesTypeLong {

			err = fixFrameForLongToMulti(frame)
			if err != nil {
				return nil, err
			}

			frames, err := timeseries.LongToMulti(&timeseries.LongFrame{frame})
			if err != nil {
				return nil, err
			}
			return frames.Frames(), nil
		}
	case FormatOptionTable:
		frame.Meta.PreferredVisualization = data.VisTypeTable
	case FormatOptionLogs:
		frame.Meta.PreferredVisualization = data.VisTypeLogs
	case FormatOptionTrace:
		frame.Meta.PreferredVisualization = data.VisTypeTrace
	// Format as timeSeries
	default:
		if frame.TimeSeriesSchema().Type == data.TimeSeriesTypeLong {
			frame, err = data.LongToWide(frame, fillMode)
			if err != nil {
				return nil, err
			}
		}
	}
	return data.Frames{frame}, nil
}

// fixFrameForLongToMulti edits the passed in frame so that it's first time field isn't nullable and has the correct meta
func fixFrameForLongToMulti(frame *data.Frame) error {
	timeFields := frame.TypeIndices(data.FieldTypeTime, data.FieldTypeNullableTime)
	if len(timeFields) == 0 {
		return fmt.Errorf("can not convert to wide series, input is missing a time field")
	}

	// the timeseries package expects the first time field in the frame to be non-nullable and ignores the rest
	timeField := frame.Fields[timeFields[0]]
	if timeField.Type() == data.FieldTypeNullableTime {
		newValues := []time.Time{}
		for i := 0; i < timeField.Len(); i++ {
			val, ok := timeField.ConcreteAt(i)
			if !ok {
				return fmt.Errorf("can not convert to wide series, input has null time values")
			}
			newValues = append(newValues, val.(time.Time))
		}
		newField := data.NewField(timeField.Name, timeField.Labels, newValues)
		newField.Config = timeField.Config
		frame.Fields[timeFields[0]] = newField

		// LongToMulti requires the meta to be set for the frame
		frame.Meta.Type = data.FrameTypeTimeSeriesLong
		frame.Meta.TypeVersion = data.FrameTypeVersion{0, 1}
	}
	return nil
}

func applyHeaders(query *Query, headers http.Header) *Query {
	var args map[string]interface{}
	if query.ConnectionArgs == nil {
		query.ConnectionArgs = []byte("{}")
	}
	err := json.Unmarshal(query.ConnectionArgs, &args)
	if err != nil {
		backend.Logger.Warn(fmt.Sprintf("Failed to apply headers: %s", err.Error()))
		return query
	}
	args[HeaderKey] = headers
	raw, err := json.Marshal(args)
	if err != nil {
		backend.Logger.Warn(fmt.Sprintf("Failed to apply headers: %s", err.Error()))
		return query
	}

	query.ConnectionArgs = raw

	return query
}
