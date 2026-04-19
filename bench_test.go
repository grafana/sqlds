package sqlds

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
	"github.com/grafana/grafana-plugin-sdk-go/data"
)

// TestMain silences backend.Logger for the duration of the test binary.
// hclog writes to stdout and interleaves with go test's benchmark output,
// corrupting samples that happen to cross a log write. No existing tests
// assert on log content.
func TestMain(m *testing.M) {
	backend.Logger = log.NewNullLogger()
	os.Exit(m.Run())
}

// newBenchDS builds a minimal SQLDatasource whose Connector is pre-populated
// with a stored dbConnection under the default key. The DB handle is nil;
// benchmarks that don't dereference it are safe (e.g. GetConnectionFromQuery
// on the cached-hit single-connection path).
func newBenchDS(driver Driver) *SQLDatasource {
	ds := NewDatasource(driver)
	ds.connector.UID = "bench-uid"
	ds.connector.driverSettings = DriverSettings{}
	ds.connector.defaultKey = defaultKey(ds.connector.UID)
	ds.connector.storeDBConnection(ds.connector.defaultKey, dbConnection{
		db:       nil,
		settings: backend.DataSourceInstanceSettings{UID: "bench-uid", Name: "bench"},
	})
	return ds
}

// ---------------------------------------------------------------------------
// applyHeaders — measure JSON round-trip cost per query with ForwardHeaders=on.
// ---------------------------------------------------------------------------

func BenchmarkApplyHeaders(b *testing.B) {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer abc.def.ghi")
	headers.Set("X-Grafana-User", "user@grafana.com")
	headers.Set("X-Request-Id", "req-01HW0000000000000000000000")

	cases := []struct {
		name string
		args []byte
	}{
		{"Nil", nil},
		{"Empty", []byte(`{}`)},
		{"Small", []byte(`{"database":"prod"}`)},
		{"Large", []byte(`{"database":"prod","schema":"public","timeout":30,"ssl":true,"extra":{"region":"us-east-1","cluster":"a"}}`)},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				q := &Query{}
				if tc.args != nil {
					q.ConnectionArgs = append(json.RawMessage(nil), tc.args...)
				}
				applyHeaders(q, headers)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// fixFrameForLongToMulti — measure slice-growth cost on the nullable-time path.
// ---------------------------------------------------------------------------

func BenchmarkFixFrameForLongToMulti(b *testing.B) {
	for _, n := range []int{100, 1000, 10000} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			times := make([]*time.Time, n)
			for i := range times {
				t := time.UnixMilli(int64(i))
				times[i] = &t
			}
			values := make([]float64, n)

			b.ReportAllocs()
			for b.Loop() {
				frame := data.NewFrame("",
					data.NewField("time", nil, times),
					data.NewField("value", nil, values),
				)
				if err := fixFrameForLongToMulti(frame); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Connector.GetConnectionFromQuery — hot path on every query (single-conn).
// Includes the fmt.Sprintf inside defaultKey that candidate 5 targets.
// ---------------------------------------------------------------------------

func BenchmarkConnector_GetConnectionFromQuery_SingleConn(b *testing.B) {
	ds := newBenchDS(&SQLMock{})
	q := &Query{}
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		_, _, err := ds.connector.GetConnectionFromQuery(ctx, q)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// ---------------------------------------------------------------------------
// Mutator interface checks — per-query type-assertion overhead in handleQuery.
// Mirrors the pattern at datasource.go:124,160,193,232-238,314.
// Post-fix this benchmark reads cached fields on SQLDatasource; the body is
// updated as part of candidate 1 so benchstat shows the delta.
// ---------------------------------------------------------------------------

func BenchmarkHandleQueryMutatorChecks(b *testing.B) {
	ds := newBenchDS(&SQLMock{})
	b.ReportAllocs()
	for b.Loop() {
		_ = ds.queryDataMutator
		_ = ds.queryMutator
		_ = ds.responseMutator
		_ = ds.queryArgSetter
		_ = ds.queryErrorMutator
		_ = ds.checkHealthMutator
	}
}

// ---------------------------------------------------------------------------
// DriverSettings getter — struct-copy + pointer deref per call.
// handleQuery calls this 10+ times; candidate 2 hoists to a local var.
// ---------------------------------------------------------------------------

func BenchmarkDriverSettings(b *testing.B) {
	ds := newBenchDS(&SQLMock{})
	var total time.Duration
	b.ReportAllocs()
	for b.Loop() {
		s := ds.DriverSettings()
		total += s.Timeout
	}
	_ = total
}

// BenchmarkHandleQuery_SettingsReads simulates the repeated settings reads
// inside handleQuery. Pre-fix each ds.DriverSettings() is a struct copy;
// post-fix the function hoists a single local.
func BenchmarkHandleQuery_SettingsReads(b *testing.B) {
	ds := newBenchDS(&SQLMock{})
	b.ReportAllocs()
	for b.Loop() {
		// Mirrors the 10 call sites in handleQuery.
		_ = ds.DriverSettings().ForwardHeaders
		_ = ds.DriverSettings().FillMode
		_ = ds.DriverSettings().Timeout
		_ = ds.DriverSettings().Retries
		_ = ds.DriverSettings().RetryOn
		_ = ds.DriverSettings().Pause
		_ = ds.DriverSettings().Errors
		_ = ds.DriverSettings().RowLimit
	}
}

// ---------------------------------------------------------------------------
// Converters() per query + per retry — candidate 2 caches at NewDatasource.
// SQLMock.Converters returns an empty slice literal each call; this shape
// matches what several production drivers do.
// ---------------------------------------------------------------------------

func BenchmarkDriverConverters(b *testing.B) {
	ds := newBenchDS(&SQLMock{})
	b.ReportAllocs()
	for b.Loop() {
		_ = ds.driver().Converters()
	}
}

// ---------------------------------------------------------------------------
// defaultKey — fmt.Sprintf allocation per query (candidate 5).
// ---------------------------------------------------------------------------

func BenchmarkDefaultKey(b *testing.B) {
	const uid = "bench-uid-0123456789abcdef"
	b.ReportAllocs()
	for b.Loop() {
		_ = defaultKey(uid)
	}
}

// ---------------------------------------------------------------------------
// Response.Set — deferred candidate; benchmark before deciding to refactor.
// ---------------------------------------------------------------------------

func BenchmarkResponse_ConcurrentSet(b *testing.B) {
	for _, workers := range []int{1, 10, 50} {
		b.Run(strconv.Itoa(workers), func(b *testing.B) {
			resp := NewResponse(backend.NewQueryDataResponse())
			var wg sync.WaitGroup
			b.ReportAllocs()
			for b.Loop() {
				wg.Add(workers)
				for w := range workers {
					go func(id int) {
						defer wg.Done()
						refID := "r" + strconv.Itoa(id)
						resp.Set(refID, backend.DataResponse{})
					}(w)
				}
				wg.Wait()
			}
		})
	}
}
