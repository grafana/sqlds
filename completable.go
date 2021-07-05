package sqlds

import (
	"context"
)

// Completable will be used to autocomplete Tables Schemas and Columns for SQL languages
type Completable interface {
	Tables(ctx context.Context) ([]string, error)
	Schemas(ctx context.Context) ([]string, error)
	Columns(ctx context.Context, table string) ([]string, error)
}
