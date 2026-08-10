package profile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestConnectionValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		mutate    func(*Connection)
		wantError bool
	}{
		{name: "valid postgres", mutate: func(*Connection) {}},
		{name: "missing database", mutate: func(c *Connection) { c.Database = "" }, wantError: true},
		{name: "invalid timeout", mutate: func(c *Connection) { c.QueryTimeoutSeconds = 0 }, wantError: true},
		{name: "invalid row limit", mutate: func(c *Connection) { c.RowLimit = 100001 }, wantError: true},
		{name: "invalid maximum column width", mutate: func(c *Connection) { c.MaxColumnWidth = 7 }, wantError: true},
		{name: "unsupported tls", mutate: func(c *Connection) { c.TLS.Mode = "mystery" }, wantError: true},
		{name: "invalid default schema", mutate: func(c *Connection) { c.DefaultSchema = "tenant\nother" }, wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			connection, err := NewConnection(DriverPostgres)
			if err != nil {
				t.Fatalf("NewConnection() error = %v", err)
			}
			connection.Database = "app"
			connection.Username = "developer"
			test.mutate(&connection)
			err = connection.Validate()
			if (err != nil) != test.wantError {
				t.Fatalf("Validate() error = %v, wantError %v", err, test.wantError)
			}
		})
	}
}

func TestNewConnectionDefaultSchema(t *testing.T) {
	t.Parallel()
	for driver, want := range map[Driver]string{
		DriverPostgres: "public", DriverSQLServer: "dbo", DriverSQLite: "main",
	} {
		connection, err := NewConnection(driver)
		if err != nil {
			t.Fatalf("NewConnection(%s): %v", driver, err)
		}
		if connection.DefaultSchema != want {
			t.Errorf("NewConnection(%s).DefaultSchema = %q, want %q", driver, connection.DefaultSchema, want)
		}
	}
}

func TestStoreRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "connections.json")
	store := NewStore(path)
	connection, err := NewConnection(DriverSQLite)
	if err != nil {
		t.Fatalf("NewConnection() error = %v", err)
	}
	connection.Name = "Local"
	connection.SQLitePath = filepath.Join(t.TempDir(), "app.sqlite")

	if err := store.Save(connection); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	connections, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(connections) != 1 || connections[0] != connection {
		t.Fatalf("List() = %#v, want %#v", connections, connection)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat() error = %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("profile permissions = %o, want 600", got)
		}
	}

	duplicate := connection
	duplicate.ID = "another-id"
	if err := store.Save(duplicate); err == nil {
		t.Fatal("Save() duplicate name error = nil")
	}
	deleted, err := store.Delete(connection.ID)
	if err != nil || !deleted {
		t.Fatalf("Delete() = %v, %v; want true, nil", deleted, err)
	}
}

type unavailableStore struct{}

func (unavailableStore) Get(string) (string, error) { return "", errors.New("keyring unavailable") }
func (unavailableStore) Set(string, string) error   { return errors.New("keyring unavailable") }
func (unavailableStore) Delete(string) error        { return nil }

func TestHybridStoreFallsBackToMemory(t *testing.T) {
	t.Parallel()

	store := NewHybridStore(unavailableStore{})
	if _, err := store.Get("id"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("Get() error = %v, want ErrSecretNotFound", err)
	}
	if err := store.Set("id", "secret"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	secret, err := store.Get("id")
	if err != nil || secret != "secret" {
		t.Fatalf("Get() = %q, %v; want secret, nil", secret, err)
	}
}
