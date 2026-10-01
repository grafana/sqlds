package sqlds

// A 10,000,000-cell wide frame costs 80-160MB before Arrow marshaling, past the response sizes Grafana can deliver.
const defaultLongToWideCellLimit = int64(10_000_000)

// resolveLongToWideCellLimit maps DriverSettings.LongToWideCellLimit onto the
// SDK convention: 0 means the default and a negative value means no limit.
func resolveLongToWideCellLimit(v int64) int64 {
	if v == 0 {
		return defaultLongToWideCellLimit
	}
	if v < 0 {
		return 0
	}
	return v
}
