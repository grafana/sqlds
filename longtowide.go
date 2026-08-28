package sqlds

import (
	"encoding/binary"
	"fmt"
	"hash/maphash"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
)

// defaultLongToWideCellLimit is the projected-cell budget applied when
// DriverSettings.LongToWideCellLimit is 0. The wide pivot allocates every
// cell up front (null-filled), so at 8-16 bytes a cell a 10,000,000-cell
// frame already costs 80-160MB before Arrow marshaling doubles it, which is
// past the response sizes Grafana can deliver.
const defaultLongToWideCellLimit = int64(10_000_000)

// resolveLongToWideCellLimit maps the DriverSettings convention onto the
// internal one: 0 means "use the default", negative means "disabled", which
// checkLongToWideCellBudget expresses as 0.
func resolveLongToWideCellLimit(v int64) int64 {
	if v == 0 {
		return defaultLongToWideCellLimit
	}
	if v < 0 {
		return 0
	}
	return v
}

// checkLongToWideCellBudget projects the size of the wide frame that
// data.LongToWide would build from longFrame and returns a downstream error
// when the projection exceeds limit (in cells, rows x fields). tsSchema must
// be longFrame's own schema, passed in so the caller's computation is
// reused. A limit of 0 or lower disables the check.
//
// The projection mirrors LongToWide's own accounting: one wide row per
// distinct timestamp (the input must be time-sorted), and one wide field per
// value field for each distinct factor-value combination, plus the time
// field. Nil factor values count as empty strings, exactly as LongToWide
// treats them. Inputs LongToWide rejects itself (unsorted times, null time
// values) pass through so its own, more precise error is the one reported;
// the non-time and non-string-or-bool arms are defensive and unreachable,
// since TimeSeriesSchema only classifies those field types as time or
// factor. The walk allocates only the set of tuple hashes, at most one entry
// per long row, always a small fraction of the long frame already held in
// memory, and it fails fast as soon as the partial counts already exceed the
// limit. Tuples are compared by 64-bit hash over length-prefixed values, so
// the encoding is injective even for values containing NUL bytes and only a
// genuine hash collision can undercount; a collision makes the guard too
// permissive, never wrongly reject.
func checkLongToWideCellBudget(longFrame *data.Frame, tsSchema data.TimeSeriesSchema, limit int64) error {
	if limit <= 0 {
		return nil
	}
	if tsSchema.Type != data.TimeSeriesTypeLong {
		return nil
	}
	rows, err := longFrame.RowLen()
	if err != nil || rows == 0 {
		return nil
	}
	values := int64(len(tsSchema.ValueIndices))

	var (
		h          maphash.Hash
		lenBuf     [8]byte
		lastTime   time.Time
		distinctTs int64
		tuples     int64
		seen       = make(map[uint64]struct{})
	)
	for i := 0; i < rows; i++ {
		raw, ok := longFrame.ConcreteAt(tsSchema.TimeIndex, i)
		if !ok {
			// Null time value: LongToWide returns ErrorNullTimeValues.
			return nil
		}
		t, ok := raw.(time.Time)
		if !ok {
			return nil
		}
		if i == 0 || t.After(lastTime) {
			distinctTs++
			lastTime = t
		} else if t.Before(lastTime) {
			// Unsorted input: LongToWide returns ErrorSeriesUnsorted.
			return nil
		}

		h.Reset()
		for _, factorIdx := range tsSchema.FactorIndices {
			raw, _ := longFrame.ConcreteAt(factorIdx, i)
			var strVal string
			switch v := raw.(type) {
			case string:
				strVal = v
			case bool:
				if v {
					strVal = "true"
				} else {
					strVal = "false"
				}
			default:
				return nil
			}
			// The length prefix delimits each value exactly, so values
			// containing NUL or separator-like bytes cannot alias across
			// field boundaries.
			binary.LittleEndian.PutUint64(lenBuf[:], uint64(len(strVal)))
			_, _ = h.Write(lenBuf[:])
			_, _ = h.WriteString(strVal)
		}
		key := h.Sum64()
		if _, dup := seen[key]; !dup {
			seen[key] = struct{}{}
			tuples++
		}

		if exceedsCellBudget(distinctTs, tuples, values, limit) {
			return backend.DownstreamError(fmt.Errorf(
				"%w: at least %d timestamps and %d series with %d value fields project past the %d-cell limit; add filters or aggregation to the query, or use the table format",
				ErrorWideFrameTooLarge, distinctTs, tuples, values, limit,
			))
		}
	}
	return nil
}

// exceedsCellBudget reports whether distinctTs * (1 + tuples*values) > limit
// without overflowing int64. The comparisons are exact: a > limit/b under
// integer division holds if and only if a*b > limit for positive a and b.
func exceedsCellBudget(distinctTs, tuples, values, limit int64) bool {
	if distinctTs <= 0 || tuples <= 0 || values <= 0 {
		return false
	}
	if tuples > limit/values {
		return true
	}
	perTimestamp := 1 + tuples*values
	return distinctTs > limit/perTimestamp
}
