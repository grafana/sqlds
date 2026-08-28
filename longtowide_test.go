package sqlds

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// longTestFrame builds a time-sorted long frame with the given number of
// distinct timestamps and series (one string factor), one float64 value
// field, and one row per timestamp-series pair.
func longTestFrame(timestamps, series int) *data.Frame {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var (
		times  []time.Time
		labels []string
		values []float64
	)
	for ts := 0; ts < timestamps; ts++ {
		for s := 0; s < series; s++ {
			times = append(times, base.Add(time.Duration(ts)*time.Second))
			labels = append(labels, fmt.Sprintf("series-%d", s))
			values = append(values, float64(s))
		}
	}
	return data.NewFrame("long",
		data.NewField("time", nil, times),
		data.NewField("label", nil, labels),
		data.NewField("value", nil, values),
	)
}

func TestCheckLongToWideCellBudget(t *testing.T) {
	t.Run("under the limit passes", func(t *testing.T) {
		// 10 timestamps x (1 + 3 series x 1 value) = 40 cells
		frame := longTestFrame(10, 3)
		require.NoError(t, checkLongToWideCellBudget(frame, frame.TimeSeriesSchema(), 40))
	})

	t.Run("over the limit fails with a downstream error", func(t *testing.T) {
		frame := longTestFrame(10, 3)
		err := checkLongToWideCellBudget(frame, frame.TimeSeriesSchema(), 39)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrorWideFrameTooLarge))
		assert.True(t, backend.IsDownstreamError(err))
	})

	t.Run("limit of zero disables the guard", func(t *testing.T) {
		frame := longTestFrame(100, 100)
		require.NoError(t, checkLongToWideCellBudget(frame, frame.TimeSeriesSchema(), 0))
	})

	t.Run("equal timestamps count once", func(t *testing.T) {
		// 5 timestamps x (1 + 4 series x 1 value) = 25 cells
		frame := longTestFrame(5, 4)
		require.NoError(t, checkLongToWideCellBudget(frame, frame.TimeSeriesSchema(), 25))
		require.Error(t, checkLongToWideCellBudget(frame, frame.TimeSeriesSchema(), 24))
	})

	t.Run("multiple value fields multiply the projection", func(t *testing.T) {
		base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		frame := data.NewFrame("long",
			data.NewField("time", nil, []time.Time{base, base.Add(time.Second)}),
			data.NewField("label", nil, []string{"a", "a"}),
			data.NewField("v1", nil, []float64{1, 2}),
			data.NewField("v2", nil, []float64{3, 4}),
		)
		// 2 timestamps x (1 + 1 series x 2 values) = 6 cells
		require.NoError(t, checkLongToWideCellBudget(frame, frame.TimeSeriesSchema(), 6))
		require.Error(t, checkLongToWideCellBudget(frame, frame.TimeSeriesSchema(), 5))
	})

	t.Run("bool factors count as series dimensions", func(t *testing.T) {
		base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		frame := data.NewFrame("long",
			data.NewField("time", nil, []time.Time{base, base}),
			data.NewField("flag", nil, []bool{true, false}),
			data.NewField("value", nil, []float64{1, 2}),
		)
		// 1 timestamp x (1 + 2 series x 1 value) = 3 cells
		require.NoError(t, checkLongToWideCellBudget(frame, frame.TimeSeriesSchema(), 3))
		require.Error(t, checkLongToWideCellBudget(frame, frame.TimeSeriesSchema(), 2))
	})

	t.Run("unsorted input passes through to LongToWide's own error", func(t *testing.T) {
		base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		frame := data.NewFrame("long",
			data.NewField("time", nil, []time.Time{base.Add(time.Second), base}),
			data.NewField("label", nil, []string{"a", "b"}),
			data.NewField("value", nil, []float64{1, 2}),
		)
		// The guard stops counting at the first backward time step, so a
		// budget the ascending prefix cannot trip passes through and
		// LongToWide reports the unsorted input itself.
		require.NoError(t, checkLongToWideCellBudget(frame, frame.TimeSeriesSchema(), 1000))
		_, err := data.LongToWide(frame, nil)
		require.Error(t, err)
	})

	t.Run("non-long frames pass", func(t *testing.T) {
		frame := data.NewFrame("wide",
			data.NewField("time", nil, []time.Time{time.Now()}),
			data.NewField("value", nil, []float64{1}),
		)
		require.NoError(t, checkLongToWideCellBudget(frame, frame.TimeSeriesSchema(), 1))
	})

	t.Run("empty frames pass", func(t *testing.T) {
		frame := data.NewFrame("long",
			data.NewField("time", nil, []time.Time{}),
			data.NewField("label", nil, []string{}),
			data.NewField("value", nil, []float64{}),
		)
		require.NoError(t, checkLongToWideCellBudget(frame, frame.TimeSeriesSchema(), 1))
	})

	t.Run("factor values containing NUL bytes stay distinct", func(t *testing.T) {
		// Without length-prefixed hashing, ("\x00"*j, "\x00"*(n-1-j)) for
		// every j serializes to the same byte stream, the guard counts one
		// series, and LongToWide then builds n series anyway.
		base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		const n = 6
		var (
			times   []time.Time
			factorA []string
			factorB []string
			vals    []float64
		)
		for j := 0; j < n; j++ {
			times = append(times, base)
			factorA = append(factorA, strings.Repeat("\x00", j))
			factorB = append(factorB, strings.Repeat("\x00", n-1-j))
			vals = append(vals, float64(j))
		}
		frame := data.NewFrame("long",
			data.NewField("time", nil, times),
			data.NewField("a", nil, factorA),
			data.NewField("b", nil, factorB),
			data.NewField("value", nil, vals),
		)
		wide, err := data.LongToWide(frame, nil)
		require.NoError(t, err)
		require.Len(t, wide.Fields, 1+n)
		// 1 timestamp x (1 + 6 series x 1 value) = 7 cells
		require.NoError(t, checkLongToWideCellBudget(frame, frame.TimeSeriesSchema(), 7))
		require.Error(t, checkLongToWideCellBudget(frame, frame.TimeSeriesSchema(), 6))
	})

	t.Run("projection matches what LongToWide builds", func(t *testing.T) {
		frame := longTestFrame(7, 5)
		wide, err := data.LongToWide(frame, nil)
		require.NoError(t, err)
		rows, err := wide.RowLen()
		require.NoError(t, err)
		cells := int64(rows) * int64(len(wide.Fields))
		// 7 timestamps x (1 + 5 series x 1 value) = 42 cells
		assert.Equal(t, int64(42), cells)
		require.NoError(t, checkLongToWideCellBudget(frame, frame.TimeSeriesSchema(), cells))
		require.Error(t, checkLongToWideCellBudget(frame, frame.TimeSeriesSchema(), cells-1))
	})
}

func TestCheckLongToWideCellBudgetNullTime(t *testing.T) {
	// A null time value passes through the guard so LongToWide reports its
	// own, more precise error.
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	frame := data.NewFrame("long",
		data.NewField("time", nil, []*time.Time{&base, nil}),
		data.NewField("label", nil, []string{"a", "b"}),
		data.NewField("value", nil, []float64{1, 2}),
	)
	// The budget must be generous enough that the guard reaches the null
	// value instead of tripping on the ascending prefix first.
	require.NoError(t, checkLongToWideCellBudget(frame, frame.TimeSeriesSchema(), 1000))
	_, err := data.LongToWide(frame, nil)
	require.ErrorIs(t, err, data.ErrorNullTimeValues)
}

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

func TestExceedsCellBudget(t *testing.T) {
	tests := []struct {
		name                              string
		distinctTs, tuples, values, limit int64
		want                              bool
	}{
		{"exactly at the limit", 10, 3, 1, 40, false},
		{"one over the limit", 10, 3, 1, 39, true},
		{"zero counts never exceed", 0, 0, 1, 1, false},
		{"tuples alone past the limit", 1, 11, 1, 10, true},
		{"large values are overflow-safe", math.MaxInt64 / 2, math.MaxInt64 / 2, math.MaxInt64 / 2, math.MaxInt64, true},
		{"max limit never trips small counts", 1000, 1000, 10, math.MaxInt64, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, exceedsCellBudget(tc.distinctTs, tc.tuples, tc.values, tc.limit))
		})
	}
}
