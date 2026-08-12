package database

import (
	"strings"
	"testing"

	"github.com/lucasfguimares/tui-db/internal/profile"
)

func TestColumnStatisticsQueryQuotesProvenIdentifiers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		driver   profile.Driver
		column   ResultColumn
		expected []string
	}{
		{
			name: "postgres", driver: profile.DriverPostgres,
			column:   ResultColumn{SourceSchema: `tenant"one`, SourceTable: "users", SourceColumn: "group_id"},
			expected: []string{`"tenant""one"."users"`, `COUNT(DISTINCT "group_id")`},
		},
		{
			name: "sql server", driver: profile.DriverSQLServer,
			column:   ResultColumn{SourceSchema: "dbo", SourceTable: "user]data", SourceColumn: "group_id"},
			expected: []string{`[dbo].[user]]data]`, `COUNT_BIG(DISTINCT [group_id])`},
		},
		{
			name: "sqlite", driver: profile.DriverSQLite,
			column:   ResultColumn{SourceSchema: "main", SourceTable: "users", SourceColumn: "group_id"},
			expected: []string{`"main"."users"`, `COUNT(DISTINCT "group_id")`},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			query, err := columnStatisticsQuery(profile.Connection{Driver: test.driver}, test.column)
			if err != nil {
				t.Fatal(err)
			}
			for _, expected := range test.expected {
				if !strings.Contains(query, expected) {
					t.Fatalf("query %q does not contain %q", query, expected)
				}
			}
		})
	}
}

func TestColumnStatisticsQueryRejectsUnknownAndCrossDatabaseSources(t *testing.T) {
	t.Parallel()

	if _, err := columnStatisticsQuery(profile.Connection{Driver: profile.DriverPostgres}, ResultColumn{}); err == nil {
		t.Fatal("columnStatisticsQuery() accepted a column without source")
	}
	connection := profile.Connection{Driver: profile.DriverSQLServer, Database: "app"}
	column := ResultColumn{SourceDatabase: "other", SourceTable: "users", SourceColumn: "id"}
	if _, err := columnStatisticsQuery(connection, column); err == nil {
		t.Fatal("columnStatisticsQuery() accepted a cross-database source")
	}
}

func TestColumnStatisticsQuerySkipsUnsupportedDistinctType(t *testing.T) {
	t.Parallel()

	query, err := columnStatisticsQuery(
		profile.Connection{Driver: profile.DriverSQLServer},
		ResultColumn{SourceTable: "users", SourceColumn: "payload", DatabaseType: "NTEXT"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "SELECT COUNT_BIG(*), NULL,") {
		t.Fatalf("unsupported distinct query = %q", query)
	}
}
