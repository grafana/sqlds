package sqlds

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"runtime/debug"

	"github.com/grafana/dataplane/sdata/timeseries"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
)

// FormatQueryOption defines how the user has chosen to represent the data
// Deprecated: use sqlutil.FormatQueryOption directly instead
type FormatQueryOption = sqlutil.FormatQueryOption

// Deprecated: use the values in sqlutil directly instead
const (
	// FormatOptionTimeSeries formats the query results as a timeseries using "LongToWide"
	FormatOptionTimeSeries = sqlutil.FormatOptionTimeSeries
	// FormatOptionTable formats the query results as a table using "LongToWide"
	FormatOptionTable = sqlutil.FormatOptionTable
	// FormatOptionLogs sets the preferred visualization to logs
	FormatOptionLogs = sqlutil.FormatOptionLogs
	// FormatOptionsTrace sets the preferred visualization to trace
	FormatOptionTrace = sqlutil.FormatOptionTrace
	// FormatOptionMulti formats the query results as a timeseries using "LongToMulti"
	FormatOptionMulti = sqlutil.FormatOptionMulti
)

// Deprecated: use sqlutil.Query directly instead
type Query = sqlutil.Query

// GetQuery wraps sqlutil's GetQuery to add headers if needed
func GetQuery(query backend.DataQuery, headers http.Header, setHeaders bool) (*Query, error) {
	model, err := sqlutil.GetQuery(query)
	if err != nil {
		return nil, backend.PluginError(err)
	}

	if setHeaders {
		applyHeaders(model, headers)
	}

	return model, nil
}

type DBQuery struct {
	DB         Connection
	fillMode   *data.FillMissing
	Settings   backend.DataSourceInstanceSettings
	metrics    Metrics
	DSName     string
	converters []sqlutil.Converter
	rowLimit   int64
}

func NewQuery(db Connection, settings backend.DataSourceInstanceSettings, converters []sqlutil.Converter, fillMode *data.FillMissing, rowLimit int64) *DBQuery {
	return &DBQuery{
		DB:         db,
		DSName:     settings.Name,
		converters: converters,
		fillMode:   fillMode,
		metrics:    NewMetrics(settings.Name, settings.Type, EndpointQuery),
		rowLimit:   rowLimit,
	}
}

// Run sends the query to the connection and converts the rows to a dataframe.
func (q *DBQuery) Run(ctx context.Context, query *Query, queryErrorMutator QueryErrorMutator, args ...interface{}) (data.Frames, error) {
	start := time.Now()
	rows, err := q.DB.QueryContext(ctx, query.RawSQL, args...)
	if err != nil {
		var errWithSource backend.ErrorWithSource
		defer func() {
			q.metrics.CollectDuration(Source(errWithSource.ErrorSource()), StatusError, time.Since(start).Seconds())
		}()

		if errors.Is(err, context.Canceled) {
			errWithSource := backend.NewErrorWithSource(err, backend.ErrorSourceDownstream)
			return sqlutil.ErrorFrameFromQuery(query), errWithSource
		}

		// Wrap with ErrorQuery to enable retry logic in datasource
		queryErr := fmt.Errorf("%w: %w", ErrorQuery, err)

		// Handle driver specific errors
		if queryErrorMutator != nil {
			errWithSource = queryErrorMutator.MutateQueryError(queryErr)
			return sqlutil.ErrorFrameFromQuery(query), errWithSource
		}

		// If we get to this point, assume the error is from the plugin
		errWithSource = backend.NewErrorWithSource(queryErr, backend.DefaultErrorSource)

		return sqlutil.ErrorFrameFromQuery(query), errWithSource
	}
	q.metrics.CollectDuration(SourceDownstream, StatusOK, time.Since(start).Seconds())

	// Check for an error response
	if err := rows.Err(); err != nil {
		queryErr := fmt.Errorf("%w: %w", ErrorQuery, err)
		errWithSource := backend.NewErrorWithSource(queryErr, backend.DefaultErrorSource)
		if errors.Is(err, sql.ErrNoRows) {
			// Should we even response with an error here?
			// The panel will simply show "no data"
			errWithSource = backend.NewErrorWithSource(fmt.Errorf("%w: %s", err, "Error response from database"), backend.ErrorSourceDownstream)
			return sqlutil.ErrorFrameFromQuery(query), errWithSource
		}
		if queryErrorMutator != nil {
			errWithSource = queryErrorMutator.MutateQueryError(queryErr)
		}

		q.metrics.CollectDuration(Source(errWithSource.ErrorSource()), StatusError, time.Since(start).Seconds())
		return sqlutil.ErrorFrameFromQuery(query), errWithSource
	}

	defer func() {
		if err := rows.Close(); err != nil {
			backend.Logger.Error(err.Error())
		}
	}()

	return q.convertRowsToFrames(rows, query, queryErrorMutator)
}

func (q *DBQuery) convertRowsToFrames(rows *sql.Rows, query *Query, queryErrorMutator QueryErrorMutator) (data.Frames, error) {
	source := SourcePlugin
	status := StatusOK
	start := time.Now()
	defer func() {
		q.metrics.CollectDuration(source, status, time.Since(start).Seconds())
	}()

	res, err := getFrames(rows, q.rowLimit, q.converters, q.fillMode, query)
	if err != nil {
		status = StatusError

		// Additional checks for processing errors
		if backend.IsDownstreamHTTPError(err) {
			source = SourceDownstream
		} else if queryErrorMutator != nil {
			errWithSource := queryErrorMutator.MutateQueryError(err)
			source = Source(errWithSource.ErrorSource())
		}

		return sqlutil.ErrorFrameFromQuery(query), backend.NewErrorWithSource(
			fmt.Errorf("%w: %s", err, "Could not process SQL results"),
			backend.ErrorSource(source),
		)
	}

	q.observeResponseSize(res)

	return res, nil
}

// observeResponseSize records rows + cells (rows × fields) across all returned frames.
// Skips emission entirely if any frame has inconsistent field lengths, since partial
// totals would mislead operators investigating large responses.
func (q *DBQuery) observeResponseSize(frames data.Frames) {
	var totalRows, totalCells int64
	for _, frame := range frames {
		if frame == nil {
			continue
		}
		rowLen, err := frame.RowLen()
		if err != nil {
			backend.Logger.Debug("skipping response size observation", "error", err.Error())
			return
		}
		totalRows += int64(rowLen)
		totalCells += int64(rowLen) * int64(len(frame.Fields))
	}
	q.metrics.CollectResponseSize(totalRows, totalCells)
}

// getFrames converts rows to dataframes
func getFrames(rows *sql.Rows, limit int64, converters []sqlutil.Converter, fillMode *data.FillMissing, query *Query) (data.Frames, error) {
	// Validate rows before processing to prevent panics
	if err := validateRows(rows); err != nil {
		backend.Logger.Error("Invalid SQL rows", "error", err.Error())
		return nil, err
	}

	frame, err := sqlutil.FrameFromRows(rows, limit, converters...)
	if err != nil {
		return nil, err
	}
	frame.Name = query.RefID
	if frame.Meta == nil {
		frame.Meta = &data.FrameMeta{}
	}

	count, err := frame.RowLen()
	if err != nil {
		return nil, err
	}

	// the handling of zero-rows differs between various "format"s.
	zeroRows := count == 0

	frame.Meta.ExecutedQueryString = query.RawSQL
	frame.Meta.PreferredVisualization = data.VisTypeGraph

	switch query.Format {
	case FormatOptionMulti:
		if zeroRows {
			return nil, ErrorNoResults
		}

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
		if zeroRows {
			return nil, ErrorNoResults
		}

		if frame.TimeSeriesSchema().Type == data.TimeSeriesTypeLong {
			frame, err = data.LongToWide(frame, fillMode)
			if err != nil {
				return nil, err
			}
		}
	}
	return data.Frames{frame}, nil
}

// accessColumns checks whether we can access rows.Columns, checking
// for error or panic. In the case of panic, logs the stack trace at debug level
// for security
func accessColumns(rows *sql.Rows) (columnErr error) {
	defer func() {
		if r := recover(); r != nil {
			columnErr = fmt.Errorf("panic accessing columns: %v", r)
			stack := string(debug.Stack())
			backend.Logger.Debug("accessColumns panic stack trace", "stack", stack)
		}
	}()
	_, columnErr = rows.Columns()
	return columnErr
}

// validateRows performs safety checks on SQL rows to prevent panics
func validateRows(rows *sql.Rows) error {
	if rows == nil {
		return fmt.Errorf("%w: rows is nil", ErrorRowValidation)
	}

	err := accessColumns(rows)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrorRowValidation, err)
	}
	return nil
}

// fixFrameForLongToMulti edits the passed in frame so that it's first time field isn't nullable and has the correct meta
func fixFrameForLongToMulti(frame *data.Frame) error {
	if frame == nil {
		return fmt.Errorf("can not convert to wide series, input is nil")
	}

	timeFields := frame.TypeIndices(data.FieldTypeTime, data.FieldTypeNullableTime)
	if len(timeFields) == 0 {
		return fmt.Errorf("can not convert to wide series, input is missing a time field")
	}

	// the timeseries package expects the first time field in the frame to be non-nullable and ignores the rest
	timeField := frame.Fields[timeFields[0]]
	if timeField.Type() == data.FieldTypeNullableTime {
		n := timeField.Len()
		newValues := make([]time.Time, n)
		for i := 0; i < n; i++ {
			val, ok := timeField.ConcreteAt(i)
			if !ok {
				return fmt.Errorf("can not convert to wide series, input has null time values")
			}
			newValues[i] = val.(time.Time)
		}
		newField := data.NewField(timeField.Name, timeField.Labels, newValues)
		newField.Config = timeField.Config
		frame.Fields[timeFields[0]] = newField

		// LongToMulti requires the meta to be set for the frame
		if frame.Meta == nil {
			frame.Meta = &data.FrameMeta{}
		}
		frame.Meta.Type = data.FrameTypeTimeSeriesLong
		frame.Meta.TypeVersion = data.FrameTypeVersion{0, 1}
	}
	return nil
}

func applyHeaders(query *Query, headers http.Header) *Query {
	headerBytes, err := json.Marshal(headers)
	if err != nil {
		backend.Logger.Warn(fmt.Sprintf("Failed to apply headers: %s", err.Error()))
		return query
	}

	if injected, ok := injectJSONKey(query.ConnectionArgs, HeaderKey, headerBytes); ok {
		query.ConnectionArgs = injected
		return query
	}

	return applyHeadersSlow(query, headerBytes)
}

// injectJSONKey appends `"key":<value>` into the trailing JSON object in `in`
// without decoding the rest of the object. It returns (newBytes, true) on
// success. It bails to (nil, false) when `in` isn't a plain JSON object or
// already contains `"key"` as a substring — callers should fall back to the
// slow path in those cases to preserve overwrite semantics.
func injectJSONKey(in []byte, key string, value []byte) ([]byte, bool) {
	trimmed := bytes.TrimSpace(in)
	if len(trimmed) == 0 {
		trimmed = []byte(`{}`)
	}
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return nil, false
	}

	keyToken := make([]byte, 0, len(key)+2)
	keyToken = append(keyToken, '"')
	keyToken = append(keyToken, key...)
	keyToken = append(keyToken, '"')
	if bytes.Contains(trimmed, keyToken) {
		return nil, false
	}

	body := bytes.TrimSpace(trimmed[1 : len(trimmed)-1])
	buf := make([]byte, 0, len(body)+len(keyToken)+len(value)+4)
	buf = append(buf, '{')
	if len(body) > 0 {
		buf = append(buf, body...)
		buf = append(buf, ',')
	}
	buf = append(buf, keyToken...)
	buf = append(buf, ':')
	buf = append(buf, value...)
	buf = append(buf, '}')
	return buf, true
}

// applyHeadersSlow preserves the original decode/encode behaviour for edge
// cases the fast path rejects (malformed input or an existing HeaderKey).
func applyHeadersSlow(query *Query, headerBytes []byte) *Query {
	var args map[string]any
	if query.ConnectionArgs == nil {
		query.ConnectionArgs = []byte("{}")
	}
	if err := json.Unmarshal(query.ConnectionArgs, &args); err != nil {
		backend.Logger.Warn(fmt.Sprintf("Failed to apply headers: %s", err.Error()))
		return query
	}
	args[HeaderKey] = json.RawMessage(headerBytes)
	raw, err := json.Marshal(args)
	if err != nil {
		backend.Logger.Warn(fmt.Sprintf("Failed to apply headers: %s", err.Error()))
		return query
	}
	query.ConnectionArgs = raw
	return query
}
