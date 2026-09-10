package tui

import (
	"testing"

	"github.com/lucasfguimares/charta-tui/internal/profile"
)

func TestProfileFormEditsDefaultSchema(t *testing.T) {
	connection, err := profile.NewConnection(profile.DriverPostgres)
	if err != nil {
		t.Fatal(err)
	}
	form := newProfileForm(connection, true)
	if got := form.value("schema"); got != "public" {
		t.Fatalf("schema field = %q", got)
	}
	for index := range form.fields {
		if form.fields[index].key == "schema" {
			form.fields[index].input.SetValue("tenant_app")
		}
	}
	form.applyValues()
	if form.connection.DefaultSchema != "tenant_app" {
		t.Fatalf("DefaultSchema = %q", form.connection.DefaultSchema)
	}
}

func TestFindSchemaUsesSQLServerCaseInsensitiveMatching(t *testing.T) {
	if got, ok := findSchema([]string{"dbo", "Sales"}, profile.DriverSQLServer, "sales"); !ok || got != "Sales" {
		t.Fatalf("findSchema() = %q, %v", got, ok)
	}
	if _, ok := findSchema([]string{"public"}, profile.DriverPostgres, "PUBLIC"); ok {
		t.Fatal("PostgreSQL schema matching must preserve quoted identifier case")
	}
}
