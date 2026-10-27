//go:build integration

package database

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lucasfguimares/tui-db/internal/profile"
	_ "github.com/microsoft/go-mssqldb"
)

func TestPostgresIntegration(t *testing.T) {
	testNetworkDatabase(t, profile.DriverPostgres, "TUIDB_TEST_POSTGRES", "SELECT 1 AS one")
}

func TestSQLServerIntegration(t *testing.T) {
	testNetworkDatabase(t, profile.DriverSQLServer, "TUIDB_TEST_SQLSERVER", "SELECT 1 AS one")
}

func testNetworkDatabase(t *testing.T, driver profile.Driver, prefix, query string) {
	t.Helper()
	host := os.Getenv(prefix + "_HOST")
	if host == "" {
		t.Skip(prefix + "_HOST is not configured")
	}

	connection, err := profile.NewConnection(driver)
	if err != nil {
		t.Fatal(err)
	}
	connection.Name = "integration"
	connection.Host = host
	connection.Database = os.Getenv(prefix + "_DATABASE")
	connection.Username = os.Getenv(prefix + "_USERNAME")
	connection.TLS.Mode = profile.TLSModeDisable
	if rawPort := os.Getenv(prefix + "_PORT"); rawPort != "" {
		port, parseErr := strconv.ParseUint(rawPort, 10, 16)
		if parseErr != nil {
			t.Fatalf("parse port: %v", parseErr)
		}
		connection.Port = uint16(port)
	}

	secrets := profile.NewMemoryStore()
	if err := secrets.Set(connection.ID, os.Getenv(prefix+"_PASSWORD")); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(secrets)
	t.Cleanup(func() { _ = manager.Close() })
	runner := NewRunner(manager)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := runner.Run(ctx, connection, query)
	if err != nil {
		t.Fatalf("run query: %v", err)
	}
	if len(result.Rows) != 1 || len(result.Rows[0]) != 1 || fmt.Sprint(result.Rows[0][0]) != "1" {
		t.Fatalf("unexpected result: %#v", result.Rows)
	}
	testNetworkRelationDetails(ctx, t, connection, manager, runner)
}

func testNetworkRelationDetails(ctx context.Context, t *testing.T, connection profile.Connection, manager *Manager, runner *Runner) {
	t.Helper()
	schema := "public"
	if connection.Driver == profile.DriverSQLServer {
		schema = "dbo"
	}
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	table, view, index := "tuidb_details_"+suffix, "tuidb_details_view_"+suffix, "tuidb_details_idx_"+suffix
	statements := []string{
		fmt.Sprintf("CREATE TABLE %s.%s (id INT PRIMARY KEY, code VARCHAR(20), owner VARCHAR(20))", schema, table),
		fmt.Sprintf("CREATE UNIQUE INDEX %s ON %s.%s (code, owner)", index, schema, table),
		fmt.Sprintf("CREATE VIEW %s.%s AS SELECT code FROM %s.%s", schema, view, schema, table),
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = runner.Run(cleanup, connection, fmt.Sprintf("DROP VIEW %s.%s", schema, view))
		_, _ = runner.Run(cleanup, connection, fmt.Sprintf("DROP TABLE %s.%s", schema, table))
	})
	for _, statement := range statements {
		if _, err := runner.Run(ctx, connection, statement); err != nil {
			t.Fatalf("run %q: %v", statement, err)
		}
	}
	inspector := NewInspector(manager)
	details, err := inspector.RelationDetails(ctx, connection, schema, table)
	if err != nil {
		t.Fatalf("RelationDetails(table): %v", err)
	}
	var unique, primary bool
	for _, candidate := range details.Indexes {
		if candidate.Name == index && candidate.IsUnique && fmt.Sprint(candidate.Columns) == "[code owner]" {
			unique = true
		}
		primary = primary || candidate.IsPrimary
	}
	if !unique || !primary {
		t.Fatalf("indexes = %+v", details.Indexes)
	}
	viewDetails, err := inspector.RelationDetails(ctx, connection, schema, view)
	if err != nil {
		t.Fatalf("RelationDetails(view): %v", err)
	}
	if viewDetails.Definition == "" {
		t.Fatal("view definition is empty")
	}
}
