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
	"github.com/lucasfguimares/charta-tui/internal/profile"
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
}
