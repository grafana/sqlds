package sqlds

import (
	"context"
	"database/sql"
)

// Completable will be used to autocomplete Tables Schemas and Columns for SQL languages
type Completable interface {
	Tables(ctx context.Context, db *sql.DB) ([]string, error)
	Schemas(ctx context.Context, db *sql.DB) ([]string, error)
	Columns(ctx context.Context, db *sql.DB, table string) ([]string, error)
}
