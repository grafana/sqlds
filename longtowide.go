package sqlds

import (
	"os"
	"strconv"
	"sync"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

const envLongToWideCellLimit = "GF_PLUGIN_LONG_TO_WIDE_CELL_LIMIT"

var warnInvalidLongToWideCellLimit sync.Once

// resolveLongToWideCellLimit maps a cell limit onto the SDK convention, where 0 means no limit.
// A valid GF_PLUGIN_LONG_TO_WIDE_CELL_LIMIT takes precedence over v.
func resolveLongToWideCellLimit(v int64) int64 {
	if s := os.Getenv(envLongToWideCellLimit); s != "" {
		limit, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			warnInvalidLongToWideCellLimit.Do(func() {
				backend.Logger.Warn("ignoring invalid long-to-wide cell limit", "env", envLongToWideCellLimit, "value", s, "error", err)
			})
		} else {
			v = limit
		}
	}
	return max(v, 0)
}
