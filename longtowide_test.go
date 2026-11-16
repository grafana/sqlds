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
	assert.Equal(t, defaultLongToWideCellLimit, resolveLongToWideCellLimit(0))
	assert.Equal(t, int64(0), resolveLongToWideCellLimit(-1))
	assert.Equal(t, int64(5), resolveLongToWideCellLimit(5))
}
