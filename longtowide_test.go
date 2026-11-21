package sqlds

import (
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/stretchr/testify/assert"
)

func TestWithLongToWideCellLimit(t *testing.T) {
	q := NewQuery(nil, backend.DataSourceInstanceSettings{}, nil, nil, defaultRowLimit)
	assert.Equal(t, int64(0), q.longToWideCellLimit)
	q.WithLongToWideCellLimit(-1)
	assert.Equal(t, int64(-1), q.longToWideCellLimit)
}

func TestResolveLongToWideCellLimit(t *testing.T) {
	tests := []struct {
		name    string
		setting int64
		env     string
		want    int64
	}{
		{name: "unset means no limit", setting: 0, want: 0},
		{name: "negative means no limit", setting: -1, want: 0},
		{name: "positive setting applies", setting: 5, want: 5},
		{name: "env takes precedence", setting: 5, env: "100", want: 100},
		{name: "env applies when the setting is unset", setting: 0, env: "100", want: 100},
		{name: "env 0 removes a configured limit", setting: 5, env: "0", want: 0},
		{name: "negative env removes a configured limit", setting: 5, env: "-1", want: 0},
		{name: "invalid env is ignored", setting: 5, env: "ten million", want: 5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envLongToWideCellLimit, tc.env)
			assert.Equal(t, tc.want, resolveLongToWideCellLimit(tc.setting))
		})
	}
}
