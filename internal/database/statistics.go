package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lucasfguimares/charta-tui/internal/profile"
)

// ColumnStatistics contains exact counts read from a proven source relation.
type ColumnStatistics struct {
	TotalRows        int64
	DistinctCount    int64
	NullCount        int64
	HasDistinctCount bool
	Duration         time.Duration
}

// Statistics reads exact source-relation counts for a result column.
func (i *Inspector) Statistics(
	ctx context.Context,
	connection profile.Connection,
	column ResultColumn,
) (ColumnStatistics, error) {
	query, err := columnStatisticsQuery(connection, column)
	if err != nil {
		return ColumnStatistics{}, err
	}
	db, err := i.manager.Connection(ctx, connection)
	if err != nil {
		return ColumnStatistics{}, err
	}
	started := time.Now()
	var totalRows, nullCount int64
	var distinctCount sql.NullInt64
	if err := db.QueryRowContext(ctx, query).Scan(&totalRows, &distinctCount, &nullCount); err != nil {
		return ColumnStatistics{}, fmt.Errorf("reading column statistics: %w", withContextError(ctx, err))
	}
	return ColumnStatistics{
		TotalRows:        totalRows,
		DistinctCount:    distinctCount.Int64,
		NullCount:        nullCount,
		HasDistinctCount: distinctCount.Valid,
		Duration:         time.Since(started),
	}, nil
}

func columnStatisticsQuery(connection profile.Connection, column ResultColumn) (string, error) {
	if column.SourceTable == "" || column.SourceColumn == "" {
		return "", errors.New("statistics: result column has no proven source")
	}
	if connection.Driver == profile.DriverSQLServer && column.SourceDatabase != "" &&
		!strings.EqualFold(connection.Database, column.SourceDatabase) {
		return "", errors.New("statistics: cross-database source is not supported")
	}
	quote, err := statisticsIdentifierQuoter(connection.Driver)
	if err != nil {
		return "", err
	}
	relation := quote(column.SourceTable)
	if column.SourceSchema != "" {
		relation = quote(column.SourceSchema) + "." + relation
	}
	identifier := quote(column.SourceColumn)
	distinct := "COUNT(DISTINCT " + identifier + ")"
	if !supportsDistinctCount(connection.Driver, column.DatabaseType) {
		distinct = "NULL"
	}
	count := "COUNT(*)"
	if connection.Driver == profile.DriverSQLServer {
		count = "COUNT_BIG(*)"
		if distinct != "NULL" {
			distinct = "COUNT_BIG(DISTINCT " + identifier + ")"
		}
		return fmt.Sprintf(
			"SELECT %s, %s, %s - COUNT_BIG(%s) FROM %s",
			count, distinct, count, identifier, relation,
		), nil
	}
	return fmt.Sprintf(
		"SELECT %s, %s, %s - COUNT(%s) FROM %s",
		count, distinct, count, identifier, relation,
	), nil
}

func statisticsIdentifierQuoter(driver profile.Driver) (func(string) string, error) {
	switch driver {
	case profile.DriverPostgres, profile.DriverSQLite:
		return func(value string) string {
			return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
		}, nil
	case profile.DriverSQLServer:
		return func(value string) string {
			return `[` + strings.ReplaceAll(value, `]`, `]]`) + `]`
		}, nil
	default:
		return nil, fmt.Errorf("statistics: unsupported driver %q", driver)
	}
}

func supportsDistinctCount(driver profile.Driver, databaseType string) bool {
	typeName := strings.ToUpper(strings.TrimSpace(databaseType))
	if driver == profile.DriverSQLServer {
		for _, unsupported := range []string{"TEXT", "NTEXT", "IMAGE", "XML"} {
			if typeName == unsupported {
				return false
			}
		}
	}
	return driver != profile.DriverPostgres || typeName != "JSON"
}
