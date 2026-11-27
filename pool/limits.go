package pool

import (
	"context"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/config"
)

// SQLLimits bounds the connections of a *sql.DB. A zero field takes the Grafana [sql_datasources] default.
type SQLLimits struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

type limitSetter interface {
	SetMaxOpenConns(n int)
	SetMaxIdleConns(n int)
	SetConnMaxLifetime(d time.Duration)
}

// ApplySQLLimits bounds db, a *sql.DB, with limits. A zero field takes the Grafana default from ctx,
// and keeps the database/sql default when ctx has none.
func ApplySQLLimits(ctx context.Context, db limitSetter, limits SQLLimits) {
	defaults, err := config.GrafanaConfigFromContext(ctx).SQL()
	haveDefaults := err == nil
	if n, ok := pick(limits.MaxOpenConns, defaults.DefaultMaxOpenConns, haveDefaults); ok {
		db.SetMaxOpenConns(n)
	}
	if n, ok := pick(limits.MaxIdleConns, defaults.DefaultMaxIdleConns, haveDefaults); ok {
		db.SetMaxIdleConns(n)
	}
	lifetime := time.Duration(defaults.DefaultMaxConnLifetimeSeconds) * time.Second
	if d, ok := pick(limits.ConnMaxLifetime, lifetime, haveDefaults); ok {
		db.SetConnMaxLifetime(d)
	}
}

func pick[T ~int | ~int64](value, grafana T, haveGrafana bool) (T, bool) {
	if value != 0 {
		return value, true
	}
	return grafana, haveGrafana
}
