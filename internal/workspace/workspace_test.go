package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

func openStore(t *testing.T, path string) *Store {
	t.Helper()
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestStoreRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "workspace.json")
	store := openStore(t, path)
	if !store.IsOwner() {
		t.Fatal("first store is not the session owner")
	}
	want := Snapshot{
		Tabs: []Tab{
			{ConnectionID: "a", SQL: "SELECT 1;", Cursor: Position{Line: 1, Column: 4}},
			{ConnectionID: "b", SQL: "SELECT\n2;", Cursor: Position{Line: 2, Column: 1}, Selection: &Position{Line: 1, Column: 1}},
		},
		ActiveTab: 1,
	}
	if _, err := store.Save(1, want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
	result, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if result.RecoveredPath != "" || !reflect.DeepEqual(result.Snapshot, want) {
		t.Fatalf("Load() = %+v, want %+v", result, want)
	}
}

func TestStoreSurvivesUnclosedProcess(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "workspace.json")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Save(1, Snapshot{Tabs: []Tab{{SQL: "SELECT 42;"}}}); err != nil {
		t.Fatal(err)
	}
	// Simulate a killed process: the lock is released by the OS, the store is never closed.
	_ = first.lock.Close()

	second := openStore(t, path)
	result, err := second.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Snapshot.Tabs) != 1 || result.Snapshot.Tabs[0].SQL != "SELECT 42;" {
		t.Fatalf("Load() = %+v", result.Snapshot)
	}
}

func TestStorePreservesUnreadableFiles(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"corrupt":         "{not json",
		"unknown version": `{"version": 99, "tabs": [{"sql": "SELECT 1;"}]}`,
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "workspace.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			store := openStore(t, path)
			result, err := store.Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if len(result.Snapshot.Tabs) != 0 {
				t.Fatalf("tabs = %+v, want empty", result.Snapshot.Tabs)
			}
			if !strings.HasPrefix(filepath.Base(result.RecoveredPath), "workspace.json.corrupt-") {
				t.Fatalf("RecoveredPath = %q", result.RecoveredPath)
			}
			preserved, err := os.ReadFile(result.RecoveredPath)
			if err != nil || string(preserved) != content {
				t.Fatalf("preserved = %q, %v", preserved, err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("original still present: %v", err)
			}
		})
	}
}

func TestStoreContendedLock(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "workspace.json")
	owner, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Save(1, Snapshot{Tabs: []Tab{{SQL: "owner"}}}); err != nil {
		t.Fatal(err)
	}
	second := openStore(t, path)
	if second.IsOwner() {
		t.Fatal("second store acquired a held lock")
	}
	if _, err := second.Save(1, Snapshot{Tabs: []Tab{{SQL: "intruder"}}}); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("Save() error = %v, want ErrNotOwner", err)
	}
	result, err := second.Load()
	if err != nil || len(result.Snapshot.Tabs) != 1 || result.Snapshot.Tabs[0].SQL != "owner" {
		t.Fatalf("Load() = %+v, %v", result.Snapshot, err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	third := openStore(t, path)
	if !third.IsOwner() {
		t.Fatal("lock was not released by Close")
	}
}

func TestStoreAppliesLimits(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "workspace.json")
	store := openStore(t, path)
	tabs := make([]Tab, MaxTabs+5)
	tabs[0].SQL = strings.Repeat("é", MaxSQLBytes/2+1)
	report, err := store.Save(1, Snapshot{Tabs: tabs, ActiveTab: MaxTabs + 2})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if report.DroppedTabs != 5 || report.TruncatedTabs != 1 {
		t.Fatalf("report = %+v", report)
	}
	result, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Snapshot.Tabs) != MaxTabs || result.Snapshot.ActiveTab != MaxTabs-1 {
		t.Fatalf("tabs = %d, active = %d", len(result.Snapshot.Tabs), result.Snapshot.ActiveTab)
	}
	sql := result.Snapshot.Tabs[0].SQL
	if len(sql) > MaxSQLBytes || !utf8.ValidString(sql) {
		t.Fatalf("truncated sql has %d bytes, valid = %v", len(sql), utf8.ValidString(sql))
	}
}

func TestStoreIgnoresStaleSequence(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "workspace.json")
	store := openStore(t, path)
	if _, err := store.Save(2, Snapshot{Tabs: []Tab{{SQL: "new"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(1, Snapshot{Tabs: []Tab{{SQL: "old"}}}); err != nil {
		t.Fatal(err)
	}
	result, err := store.Load()
	if err != nil || result.Snapshot.Tabs[0].SQL != "new" {
		t.Fatalf("Load() = %+v, %v", result.Snapshot, err)
	}
}
