package sqlds

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/grafana/dataplane/sdata/timeseries"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
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

var duration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Namespace: "plugins",
	Name:      "plugin_external_requests_duration_seconds",
	Help:      "Duration of requests to external services",
}, []string{"datasource_name", "datasource_type", "error_source"})

// GetQuery wraps sqlutil's GetQuery to add headers if needed
func GetQuery(query backend.DataQuery, headers http.Header, setHeaders bool) (*Query, error) {
	model, err := sqlutil.GetQuery(query)

	if err != nil {
		return nil, PluginError(err)
	}

	if setHeaders {
		applyHeaders(model, headers)
	}

	return model, nil
}

type DBQuery struct {
	DSName     string
	DB         Connection
	Settings   backend.DataSourceInstanceSettings
	converters []sqlutil.Converter
	fillMode   *data.FillMissing
}

func NewQuery(db Connection, settings backend.DataSourceInstanceSettings, converters []sqlutil.Converter, fillMode *data.FillMissing) *DBQuery {
	dsName, ok := sanitizeLabelName(settings.Name)
	if !ok {
		backend.Logger.Warn("Failed to sanitize datasource name for prometheus label", dsName)
	}
	return &DBQuery{
		DB:         db,
		DSName:     dsName,
		converters: converters,
		fillMode:   fillMode,
	}
}

// Run sends the query to the connection and converts the rows to a dataframe.
func (q *DBQuery) Run(ctx context.Context, query *Query, args ...interface{}) (data.Frames, error) {
	start := time.Now()
	var errWithSource error

	defer func() {
		if q.DSName != "" {
			errorSource := "none"
			if errWithSource != nil {
				errorSource = string(ErrorSource(errWithSource))
			}

			duration.WithLabelValues(q.DSName, q.Settings.Type, errorSource).Observe(time.Since(start).Seconds())
		}
	}()

	rows, err := q.DB.QueryContext(ctx, query.RawSQL, args...)
	if err != nil {
		errType := ErrorQuery
		if errors.Is(err, context.Canceled) {
			errType = context.Canceled
		}
		errWithSource := DownstreamError(fmt.Errorf("%w: %s", errType, err.Error()))
		return sqlutil.ErrorFrameFromQuery(query), errWithSource
	}

	// Check for an error response
	if err := rows.Err(); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Should we even response with an error here?
			// The panel will simply show "no data"
			errWithSource := DownstreamError(fmt.Errorf("%s: %w", "No results from query", err))
			return sqlutil.ErrorFrameFromQuery(query), errWithSource
		}
		errWithSource := DownstreamError(fmt.Errorf("%s: %w", "Error response from database", err))
		return sqlutil.ErrorFrameFromQuery(query), errWithSource
	}

	defer func() {
		if err := rows.Close(); err != nil {
			backend.Logger.Error(err.Error())
		}
	}()

	// Convert the response to frames
	res, err := getFrames(rows, -1, q.converters, q.fillMode, query)
	if err != nil {
		errWithSource := PluginError(fmt.Errorf("%w: %s", err, "Could not process SQL results"))
		return sqlutil.ErrorFrameFromQuery(query), errWithSource
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
		if frame.Meta == nil {
			frame.Meta = &data.FrameMeta{}
		}
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

// sanitizeLabelName removes all invalid chars from the label name.
// If the label name is empty or contains only invalid chars, it will return false indicating it was not sanitized.
// copied from https://github.com/grafana/grafana/blob/main/pkg/infra/metrics/metricutil/utils.go#L14
func sanitizeLabelName(name string) (string, bool) {
	if len(name) == 0 {
		backend.Logger.Warn(fmt.Sprintf("label name cannot be empty: %s", name))
		return "", false
	}

	out := strings.Builder{}
	for i, b := range name {
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_' || (b >= '0' && b <= '9' && i > 0) {
			out.WriteRune(b)
		} else if b == ' ' {
			out.WriteRune('_')
		}
	}

	if out.Len() == 0 {
		backend.Logger.Warn(fmt.Sprintf("label name only contains invalid chars: %q", name))
		return "", false
	}

	return out.String(), true
}
