package queryhistory

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestStoreRetentionFilteringAndDeletion(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nested", "history.json")
	store := NewStore(path, 2)
	base := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	entries := []Entry{
		{ID: "one", SQL: "SELECT 1", ConnectionID: "a", ConnectionName: "A", ExecutedAt: base, Status: StatusSuccess},
		{ID: "two", SQL: "SELECT users", ConnectionID: "b", ConnectionName: "B", ExecutedAt: base.Add(time.Minute), Status: StatusError, Error: "bad"},
		{ID: "three", SQL: "UPDATE users", ConnectionID: "a", ConnectionName: "A", ExecutedAt: base.Add(2 * time.Minute), Status: StatusCancelled},
	}
	for _, entry := range entries {
		if err := store.Add(entry); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}
	all, err := store.List(Filter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(all) != 2 || all[0].ID != "three" || all[1].ID != "two" {
		t.Fatalf("List() = %#v", all)
	}
	filtered, err := store.List(Filter{Search: "users", Status: StatusError, ConnectionID: "b", From: base.Add(30 * time.Second)})
	if err != nil || len(filtered) != 1 || filtered[0].ID != "two" {
		t.Fatalf("filtered = %#v, %v", filtered, err)
	}
	deleted, err := store.Delete("two")
	if err != nil || !deleted {
		t.Fatalf("Delete() = %v, %v", deleted, err)
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
	all, err = store.List(Filter{})
	if err != nil || len(all) != 0 {
		t.Fatalf("List() after clear = %#v, %v", all, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("permissions = %o", info.Mode().Perm())
		}
	}
}

func TestRedactSQL(t *testing.T) {
	t.Parallel()
	input := "CREATE USER app PASSWORD 'super-secret'; SET api_key = 'token'; SELECT 'visible'; -- postgres://u:p@host/db"
	got := RedactSQL(input)
	for _, secret := range []string{"super-secret", "token", ":p@"} {
		if strings.Contains(got, secret) {
			t.Fatalf("RedactSQL() leaked %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "SELECT 'visible'") {
		t.Fatalf("RedactSQL() removed ordinary SQL: %s", got)
	}
	if strings.Count(got, "[REDACTED]") != 3 || !strings.Contains(got, "SET api_key = '[REDACTED]'") {
		t.Fatalf("RedactSQL() produced malformed redaction: %s", got)
	}
}
