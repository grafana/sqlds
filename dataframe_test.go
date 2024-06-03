package sqlds_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
	"github.com/grafana/sqlds/v3"
	"github.com/grafana/sqlds/v3/test"
	"github.com/stretchr/testify/require"
)

// we test how no-rows sql responses are converted to dataframes
func TestNoRowsFrame(t *testing.T) {

	tts := []struct {
		name               string
		data               test.Data
		format             sqlutil.FormatQueryOption
		expectedFieldCount int
	}{
		{
			name:   "empty table",
			format: sqlutil.FormatOptionTable,
			data: test.Data{
				Cols: []test.Column{
					{
						Name:     "name",
						DataType: "TEXT",
						Kind:     "",
					},
					{
						Name:     "age",
						DataType: "INTEGER",
						Kind:     int64(0),
					},
				},
				Rows: [][]any{},
			},
			expectedFieldCount: 2,
		},
		{
			name:   "empty wide",
			format: sqlutil.FormatOptionTimeSeries,
			data: test.Data{
				Cols: []test.Column{
					{
						Name:     "time",
						DataType: "TIMESTAMP",
						Kind:     time.Unix(0, 0),
					},
					{
						Name:     "v1",
						DataType: "FLOAT",
						Kind:     float64(0),
					},
					{
						Name:     "v2",
						DataType: "FLOAT",
						Kind:     float64(0),
					},
				},
				Rows: [][]any{},
			},
			expectedFieldCount: 0,
		},
		{
			name:   "empty long",
			format: sqlutil.FormatOptionTimeSeries,
			data: test.Data{
				Cols: []test.Column{
					{
						Name:     "time",
						DataType: "TIMESTAMP",
						Kind:     time.Unix(0, 0),
					},
					{
						Name:     "tag",
						DataType: "TEXT",
						Kind:     "",
					},
					{
						Name:     "value",
						DataType: "FLOAT",
						Kind:     float64(0),
					},
				},
				Rows: [][]any{},
			},
			expectedFieldCount: 0,
		},
		{
			name:   "empty multi",
			format: sqlutil.FormatOptionMulti,
			data: test.Data{
				Cols: []test.Column{
					{
						Name:     "time",
						DataType: "TIMESTAMP",
						Kind:     time.Unix(0, 0),
					},
					{
						Name:     "tag",
						DataType: "TEXT",
						Kind:     "",
					},
					{
						Name:     "value",
						DataType: "FLOAT",
						Kind:     float64(0),
					},
				},
				Rows: [][]any{},
			},
			expectedFieldCount: 0,
		},
		{
			name:   "logs",
			format: sqlutil.FormatOptionLogs,
			data: test.Data{
				Cols: []test.Column{
					{
						Name:     "time",
						DataType: "TIMESTAMP",
						Kind:     time.Unix(0, 0),
					},
					{
						Name:     "text",
						DataType: "TEXT",
						Kind:     "",
					},
				},
				Rows: [][]any{},
			},
			expectedFieldCount: 2,
		},
		{
			name:   "trace",
			format: sqlutil.FormatOptionLogs,
			data: test.Data{
				Cols: []test.Column{
					{
						Name:     "time",
						DataType: "TIMESTAMP",
						Kind:     time.Unix(0, 0),
					},
					// FIXME: i do not know what kind of data is in trace-frames
				},
				Rows: [][]any{},
			},
			expectedFieldCount: 1,
		},
	}

	for _, tt := range tts {
		t.Run(tt.name, func(t *testing.T) {
			id := "empty-frames" + tt.name
			driver, _ := test.NewDriver(id, tt.data, nil, test.DriverOpts{})
			ds := sqlds.NewDatasource(driver)

			settings := backend.DataSourceInstanceSettings{UID: id, JSONData: []byte("{}")}
			_, err := ds.NewDatasource(context.Background(), settings)

			require.NoError(t, err)

			req := backend.QueryDataRequest{
				PluginContext: backend.PluginContext{
					DataSourceInstanceSettings: &settings,
				},
				Queries: []backend.DataQuery{
					{
						RefID: "A",
						JSON:  []byte(fmt.Sprintf(`{ "rawSql": "SELECT 42", "format": %d }`, tt.format)),
					},
				},
			}

			r, err := ds.QueryData(context.Background(), &req)
			require.NoError(t, err)
			d := r.Responses["A"]
			require.NotNil(t, d)
			require.NoError(t, d.Error)
			require.Len(t, d.Frames, 1)
			require.Len(t, d.Frames[0].Fields, tt.expectedFieldCount)

		})
	}
}
