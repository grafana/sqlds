package sqlds

import (
	"fmt"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type MockDB struct {
	Driver
}

func (h *MockDB) Macros() (macros Macros) {
	return map[string]MacroFunc{
		"foo": func(query *Query, args []string) (out string, err error) {
			return "bar", nil
		},
		"params": func(query *Query, args []string) (out string, err error) {
			if args[0] != "" {
				return "bar_" + args[0], nil
			}
			return "bar", nil
		},
	}
}

func (h *MockDB) Timeout(backend.DataSourceInstanceSettings) time.Duration {
	return time.Minute
}

func TestInterpolate(t *testing.T) {
	type test struct {
		name   string
		input  string
		output string
	}
	tests := []test{
		{input: "select * from foo", output: "select * from foo", name: "macro with incorrect syntax"},
		{input: "select * from $__foo()", output: "select * from bar", name: "correct macro"},
		{input: "select '$__foo()' from $__foo()", output: "select 'bar' from bar", name: "multiple instances of same macro"},
		{input: "select * from $__foo()$__foo()", output: "select * from barbar", name: "multiple instances of same macro without space"},
		{input: "select * from $__foo", output: "select * from bar", name: "macro without paranthesis"},
		{input: "select * from $__params()", output: "select * from bar", name: "macro without params"},
		{input: "select * from $__params(hello)", output: "select * from bar_hello", name: "with param"},
		{input: "select * from $__params(hello) AND $__params(hello)", output: "select * from bar_hello AND bar_hello", name: "same macro multiple times with same param"},
		{input: "select * from $__params(hello) AND $__params(world)", output: "select * from bar_hello AND bar_world", name: "same macro multiple times with different param"},
		{input: "select * from $__params(world) AND $__foo() AND $__params(hello)", output: "select * from bar_world AND bar AND bar_hello", name: "different macros with different params"},
	}
	for i, tc := range tests {
		driver := MockDB{}
		t.Run(fmt.Sprintf("[%d/%d] %s", i+1, len(tests), tc.name), func(t *testing.T) {
			query := &Query{
				RawSQL: tc.input,
			}
			interpolatedQuery, err := interpolate(&driver, query)
			require.Nil(t, err)
			assert.Equal(t, tc.output, interpolatedQuery)
		})
	}
}
