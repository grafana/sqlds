package sqlds

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
)

// getErrorFrameFromQuery returns a error frames with empty data and meta fields
func getErrorFrameFromQuery(query *sqlutil.Query) data.Frames {
	frames := data.Frames{}
	frame := data.NewFrame(query.RefID)
	frame.Meta = &data.FrameMeta{
		ExecutedQueryString: query.RawSQL,
	}
	frames = append(frames, frame)
	return frames
}

// QueryDB sends the query to the connection and converts the rows to a dataframe.
func QueryDB(ctx context.Context, db Connection, converters []sqlutil.Converter, fillMode *data.FillMissing, query *sqlutil.Query, args ...interface{}) (data.Frames, error) {
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

func getFrames(rows *sql.Rows, limit int64, converters []sqlutil.Converter, fillMode *data.FillMissing, query *sqlutil.Query) (data.Frames, error) {
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

	if query.Format == sqlutil.FormatOptionTable {
		frame.Meta.PreferredVisualization = data.VisTypeTable
		return data.Frames{frame}, nil
	}

	if query.Format == sqlutil.FormatOptionLogs {
		frame.Meta.PreferredVisualization = data.VisTypeLogs
		return data.Frames{frame}, nil
	}

	if query.Format == sqlutil.FormatOptionTrace {
		frame.Meta.PreferredVisualization = data.VisTypeTrace
		return data.Frames{frame}, nil
	}

	count, err := frame.RowLen()

	if err != nil {
		return nil, err
	}

	if count == 0 {
		return nil, ErrorNoResults
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

func applyHeaders(query *sqlutil.Query, headers http.Header) error {
	var args map[string]interface{}
	if query.ConnectionArgs == nil {
		query.ConnectionArgs = []byte("{}")
	}
	err := json.Unmarshal(query.ConnectionArgs, &args)
	if err != nil {
		return fmt.Errorf("applyHeaders failed: %w", err)
	}
	args[HeaderKey] = headers
	raw, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("applyHeaders failed: %w", err)
	}
	query.ConnectionArgs = raw
	return nil
}
