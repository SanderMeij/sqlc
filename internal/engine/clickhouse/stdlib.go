package clickhouse

import (
	"github.com/SanderMeij/sqlc/internal/sql/catalog"
)

func defaultSchema(name string) *catalog.Schema {
	return &catalog.Schema{Name: name}
}
