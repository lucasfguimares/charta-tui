package database

import (
	"context"
	"path/filepath"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/lucasfguimares/tui-db/internal/profile"
)

func TestSQLiteQueryAndCatalog(t *testing.T) {
	t.Parallel()

	connection, err := profile.NewConnection(profile.DriverSQLite)
	if err != nil {
		t.Fatalf("NewConnection() error = %v", err)
	}
	connection.Name = "test"
	connection.SQLitePath = filepath.Join(t.TempDir(), "test.sqlite")
	connection.RowLimit = 2

	manager := NewManager(profile.NewMemoryStore())
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	runner := NewRunner(manager)
	ctx := context.Background()
	statements := []string{
		`CREATE TABLE groups (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE items (
            id INTEGER PRIMARY KEY,
            group_id INTEGER NOT NULL REFERENCES groups(id),
            name TEXT
        )`,
		`CREATE TABLE group_details (
			group_id INTEGER PRIMARY KEY REFERENCES groups,
            notes TEXT
        )`,
		`INSERT INTO groups (id, name) VALUES (1, 'one')`,
		`INSERT INTO items (group_id, name) VALUES (1, 'one'), (1, 'two'), (1, 'three')`,
	}
	for _, statement := range statements {
		if _, err := runner.Run(ctx, connection, statement); err != nil {
			t.Fatalf("Run(%q) error = %v", statement, err)
		}
	}
	result, err := runner.Run(ctx, connection, `SELECT id, group_id, name FROM items ORDER BY id`)
	if err != nil {
		t.Fatalf("Run(select) error = %v", err)
	}
	if len(result.Rows) != 2 || !result.IsTruncated {
		t.Fatalf("result = %#v, want 2 truncated rows", result)
	}
	if result.Columns[0].Key != "PK" || result.Columns[1].Key != "FK" {
		t.Fatalf("result column keys = %q, %q; want PK, FK", result.Columns[0].Key, result.Columns[1].Key)
	}
	if result.Columns[1].ReferenceTable != "groups" || result.Columns[1].ReferenceColumn != "id" {
		t.Fatalf("foreign key target = %#v", result.Columns[1])
	}

	typed, err := runner.Run(ctx, connection, `SELECT NULL AS null_value, '' AS empty_value, 0 AS zero_value`)
	if err != nil {
		t.Fatalf("Run(typed values) error = %v", err)
	}
	if typed.Rows[0][0] != nil || typed.Rows[0][1] != "" || typed.Rows[0][2] != int64(0) {
		t.Fatalf("typed values = %#v", typed.Rows[0])
	}
	failedResult, queryErr := runner.Run(ctx, connection, `SELECT * FROM missing_table`)
	if queryErr == nil {
		t.Fatal("Run(invalid query) error = nil")
	}
	details := DescribeError(queryErr, failedResult.Duration)
	if details.Code == "" || details.Message == "" || details.Duration <= 0 {
		t.Fatalf("DescribeError() = %#v", details)
	}
	cancelledContext, cancel := context.WithCancel(ctx)
	cancel()
	cancelledResult, queryErr := runner.Run(cancelledContext, connection, `SELECT 1`)
	if queryErr == nil {
		t.Fatal("Run(cancelled query) error = nil")
	}
	details = DescribeError(queryErr, cancelledResult.Duration)
	if !details.Cancelled {
		t.Fatalf("cancelled DescribeError() = %#v", details)
	}

	inspector := NewInspector(manager)
	schemas, err := inspector.Schemas(ctx, connection)
	if err != nil || len(schemas) == 0 || schemas[0] != "main" {
		t.Fatalf("Schemas() = %#v, %v", schemas, err)
	}
	relations, err := inspector.Relations(ctx, connection, "main")
	if err != nil || len(relations) != 3 {
		t.Fatalf("Relations() = %#v, %v", relations, err)
	}
	columns, err := inspector.Columns(ctx, connection, "main", "items")
	if err != nil || len(columns) != 3 || columns[0].Key != "PK" || columns[1].Key != "FK" {
		t.Fatalf("Columns() = %#v, %v", columns, err)
	}
	columns, err = inspector.Columns(ctx, connection, "main", "group_details")
	if err != nil || len(columns) != 2 || columns[0].Key != "PK/FK" {
		t.Fatalf("group_details Columns() = %#v, %v", columns, err)
	}
	if columns[0].ReferenceTable != "groups" || columns[0].ReferenceColumn != "id" {
		t.Fatalf("group_details reference = %#v", columns[0])
	}
}
