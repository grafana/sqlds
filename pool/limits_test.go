package pool

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/config"
)

type recordedLimits struct {
	open, idle *int
	lifetime   *time.Duration
}

func (r *recordedLimits) SetMaxOpenConns(n int)              { r.open = &n }
func (r *recordedLimits) SetMaxIdleConns(n int)              { r.idle = &n }
func (r *recordedLimits) SetConnMaxLifetime(d time.Duration) { r.lifetime = &d }

func TestApplySQLLimits(t *testing.T) {
	withDefaults := config.WithGrafanaConfig(context.Background(), config.NewGrafanaCfg(map[string]string{
		"GF_SQL_ROW_LIMIT":                         "1000000",
		"GF_SQL_MAX_OPEN_CONNS_DEFAULT":            "10",
		"GF_SQL_MAX_IDLE_CONNS_DEFAULT":            "5",
		"GF_SQL_MAX_CONN_LIFETIME_SECONDS_DEFAULT": "60",
	}))
	ptr := func(n int) *int { return &n }
	dur := func(d time.Duration) *time.Duration { return &d }
	tests := []struct {
		name   string
		ctx    context.Context
		limits SQLLimits
		want   recordedLimits
	}{
		{"zero fields take the Grafana defaults", withDefaults, SQLLimits{}, recordedLimits{ptr(10), ptr(5), dur(time.Minute)}},
		{"plugin values win", withDefaults, SQLLimits{3, 2, 30 * time.Second}, recordedLimits{ptr(3), ptr(2), dur(30 * time.Second)}},
		{"negative plugin values pass through", withDefaults, SQLLimits{MaxIdleConns: -1}, recordedLimits{ptr(10), ptr(-1), dur(time.Minute)}},
		{"no Grafana defaults keep the database/sql defaults", context.Background(), SQLLimits{}, recordedLimits{}},
		{"no Grafana defaults still apply plugin values", context.Background(), SQLLimits{MaxOpenConns: 4}, recordedLimits{open: ptr(4)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got recordedLimits
			ApplySQLLimits(tt.ctx, &got, tt.limits)
			if !equalPtr(got.open, tt.want.open) || !equalPtr(got.idle, tt.want.idle) || !equalPtr(got.lifetime, tt.want.lifetime) {
				t.Errorf("limits = %s, want %s", got, tt.want)
			}
		})
	}
}

func equalPtr[T comparable](a, b *T) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func (r recordedLimits) String() string {
	return fmt.Sprintf("{open %s, idle %s, lifetime %s}", show(r.open), show(r.idle), show(r.lifetime))
}

func show[T any](p *T) string {
	if p == nil {
		return "unset"
	}
	return fmt.Sprint(*p)
}
