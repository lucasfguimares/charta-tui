//go:build integration

package tui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/lucasfguimares/tui-db/internal/database"
	"github.com/lucasfguimares/tui-db/internal/profile"
)

func TestReadAutocompleteCatalogSQLite(t *testing.T) {
	t.Parallel()

	connection, err := profile.NewConnection(profile.DriverSQLite)
	if err != nil {
		t.Fatal(err)
	}
	connection.Name = "autocomplete-test"
	connection.SQLitePath = filepath.Join(t.TempDir(), "autocomplete.sqlite")
	manager := database.NewManager(profile.NewMemoryStore())
	t.Cleanup(func() {
		if closeErr := manager.Close(); closeErr != nil {
			t.Errorf("closing manager: %v", closeErr)
		}
	})
	runner := database.NewRunner(manager)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, statement := range []string{
		`CREATE TABLE groups (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, group_id INTEGER REFERENCES groups(id), name TEXT)`,
		`CREATE VIEW active_users AS SELECT id, name FROM users`,
	} {
		if _, runErr := runner.Run(ctx, connection, statement); runErr != nil {
			t.Fatalf("Run(%q): %v", statement, runErr)
		}
	}
	model := &Model{inspector: database.NewInspector(manager)}
	catalog, err := model.readAutocompleteCatalog(ctx, connection)
	if err != nil {
		t.Fatalf("readAutocompleteCatalog(): %v", err)
	}
	if len(catalog.Schemas) != 1 || catalog.Schemas[0].Name != "main" || len(catalog.Schemas[0].Relations) != 3 {
		t.Fatalf("catalog = %#v", catalog)
	}
	foundUsers := false
	for _, relation := range catalog.Schemas[0].Relations {
		if relation.Name != "users" {
			continue
		}
		foundUsers = true
		if len(relation.Columns) != 3 || !relation.Columns[0].IsPrimaryKey || !relation.Columns[1].IsForeignKey || !relation.Columns[2].HasNullable {
			t.Fatalf("users metadata = %#v", relation.Columns)
		}
	}
	if !foundUsers {
		t.Fatal("users relation missing from autocomplete catalog")
	}
}
