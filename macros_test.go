package sqlds

import (
	"database/sql"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil/v2"
)

type MockDB struct{}

func (h *MockDB) Connect(backend.DataSourceInstanceSettings) (db *sql.DB, err error) {
	return
}
func (h *MockDB) FillMode() (mode *data.FillMissing) {
	return
}
func (h *MockDB) Converters() (sc []sqlutil.Converter) {
	return
}

// fooMacro will replace foo with bar in the query
func fooMacro(query *Query, args []string) (out string, err error) {
	return "bar", nil
}
func (h *MockDB) Macros() (macros Macros) {
	return map[string]MacroFunc{
		"foo": fooMacro,
	}
}

func TestInterpolate(t *testing.T) {
	type test struct {
		input  string
		output string
	}
	tests := []test{
		{input: "select * from foo", output: "select * from foo"},                   // keyword without macro syntax
		{input: "select * from $__foo()", output: "select * from bar"},              // macro
		{input: "select '$__foo()' from $__foo()", output: "select 'bar' from bar"}, // multiple instances of macro
		{input: "select * from $__foo()$__foo()", output: "select * from barbar"},   // macro
		{input: "select * from $__foo", output: "select * from $__foo"},             // incorrect macro
	}
	for _, tc := range tests {
		driver := MockDB{}
		query := &Query{
			RawSQL: tc.input,
		}
		interpolatedQuery, err := interpolate(&driver, query)
		if err != nil {
			t.Errorf("Error while interpolation")
		}
		if interpolatedQuery != tc.output {
			t.Errorf("Expected %s not found", tc.output)
		}
	}
}
