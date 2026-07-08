package sqlds_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
	"github.com/grafana/sqlds/v5"
	"github.com/grafana/sqlds/v5/test"
	"github.com/stretchr/testify/require"
)

// TestDriverConvertersWithInputTypeMatcher proves that converters supplied by
// a driver flow through QueryData to sqlutil.FrameFromRows untouched,
// including ones that rely on sqlutil.Converter.InputTypeMatcher to match
// parameterised column types such as ClickHouse
// "SimpleAggregateFunction(max, Float64)".
func TestDriverConvertersWithInputTypeMatcher(t *testing.T) {
	// unwrap recursively strips ClickHouse-style wrapper types so one
	// converter per base type matches every wrapped permutation.
	var unwrap func(dbType string) string
	unwrap = func(dbType string) string {
		const saf = "SimpleAggregateFunction("
		if strings.HasPrefix(dbType, saf) && strings.HasSuffix(dbType, ")") {
			inner := dbType[len(saf) : len(dbType)-1]
			depth := 0
			for i, ch := range inner {
				switch {
				case ch == '(':
					depth++
				case ch == ')':
					depth--
				case ch == ',' && depth == 0:
					return unwrap(strings.TrimSpace(inner[i+1:]))
				}
			}
		}
		return dbType
	}

	converters := []sqlutil.Converter{
		{
			Name:          "Float64",
			InputScanType: reflect.TypeOf(float64(0)),
			InputTypeMatcher: func(dbType string) bool {
				return unwrap(dbType) == "Float64"
			},
			FrameConverter: sqlutil.FrameConverter{
				FieldType: data.FieldTypeFloat64,
				ConverterFunc: func(in interface{}) (interface{}, error) {
					return *(in.(*float64)), nil
				},
			},
		},
	}

	dbData := test.Data{
		Cols: []test.Column{
			{Name: "saf", DataType: "SimpleAggregateFunction(max, Float64)", Kind: float64(0)},
			{Name: "plain", DataType: "Float64", Kind: float64(0)},
		},
		Rows: [][]any{
			{1.5, 2.5},
			{3.5, 4.5},
		},
	}

	id := "converter-matcher-passthrough"
	driver, _ := test.NewDriver(id, dbData, converters, test.DriverOpts{}, nil)
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
				JSON:  []byte(`{ "rawSql": "SELECT * FROM saf_table", "format": 1 }`),
			},
		},
	}

	r, err := ds.QueryData(context.Background(), &req)
	require.NoError(t, err)
	d := r.Responses["A"]
	require.NoError(t, d.Error)
	require.Len(t, d.Frames, 1)

	frame := d.Frames[0]
	require.Len(t, frame.Fields, 2)
	// without the driver's converters the default converter would produce
	// nullable float64 fields, so the exact field type proves the
	// matcher-equipped converter was applied
	require.Equal(t, data.FieldTypeFloat64, frame.Fields[0].Type())
	require.Equal(t, data.FieldTypeFloat64, frame.Fields[1].Type())
	require.Equal(t, []float64{1.5, 3.5}, extractFloat64Values(t, frame.Fields[0]))
	require.Equal(t, []float64{2.5, 4.5}, extractFloat64Values(t, frame.Fields[1]))
}

func extractFloat64Values(t *testing.T, field *data.Field) []float64 {
	t.Helper()
	values := make([]float64, 0, field.Len())
	for i := 0; i < field.Len(); i++ {
		values = append(values, field.At(i).(float64))
	}
	return values
}
