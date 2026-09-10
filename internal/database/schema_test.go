package database

import (
	"net/url"
	"testing"

	"github.com/lucasfguimares/charta-tui/internal/profile"
)

type schemaTestSecrets struct{}

func (schemaTestSecrets) Get(string) (string, error) { return "secret", nil }

func TestPostgresDataSourceIncludesDefaultSchema(t *testing.T) {
	connection, err := profile.NewConnection(profile.DriverPostgres)
	if err != nil {
		t.Fatal(err)
	}
	connection.Database = "app"
	connection.Username = "developer"
	connection.DefaultSchema = `Tenant "A"`
	_, dataSource, err := NewManager(schemaTestSecrets{}).dataSource(connection)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(dataSource)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := parsed.Query().Get("search_path"), `"Tenant ""A""", public`; got != want {
		t.Fatalf("search_path = %q, want %q", got, want)
	}
}

func TestStatementForConnectionUsesDefaultSchema(t *testing.T) {
	sqlServer := profile.Connection{Driver: profile.DriverSQLServer, DefaultSchema: "tenant"}
	if got := statementForConnection(sqlServer, "SELECT * FROM users;"); got != "SELECT * FROM [tenant].users;" {
		t.Fatalf("SQL Server statement = %q", got)
	}
	postgres := profile.Connection{Driver: profile.DriverPostgres, DefaultSchema: "tenant"}
	if got := statementForConnection(postgres, "SELECT * FROM users;"); got != "SELECT * FROM users;" {
		t.Fatalf("PostgreSQL statement should use search_path, got %q", got)
	}
}
