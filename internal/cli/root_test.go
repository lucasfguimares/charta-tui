package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	t.Parallel()

	command := NewCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"version"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output.String(), "charta ") || !strings.Contains(output.String(), "commit:") {
		t.Fatalf("version output = %q", output.String())
	}
}

func TestHistoryLimitValidation(t *testing.T) {
	t.Parallel()
	command := NewCommand()
	command.SetArgs([]string{"--history-limit", "0"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "history limit") {
		t.Fatalf("Execute() error = %v, want history limit validation", err)
	}
}

func TestMigrateLegacyDataMovesOnlyMissingFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	cacheDir := filepath.Join(root, "cache")
	legacyConfigDir := filepath.Join(configDir, legacyApplicationDirectory)
	newConfigDir := filepath.Join(configDir, applicationDirectory)
	if err := os.MkdirAll(legacyConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyConfigDir, "connections.json"), []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyConfigDir, "query-history.json"), []byte("legacy-history"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newConfigDir, "query-history.json"), []byte("current-history"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := migrateLegacyData(configDir, cacheDir); err != nil {
		t.Fatal(err)
	}
	connections, err := os.ReadFile(filepath.Join(newConfigDir, "connections.json")) //nolint:gosec // path is rooted in t.TempDir
	if err != nil || string(connections) != "legacy" {
		t.Fatalf("migrated connections = %q, %v", connections, err)
	}
	history, err := os.ReadFile(filepath.Join(newConfigDir, "query-history.json")) //nolint:gosec // path is rooted in t.TempDir
	if err != nil || string(history) != "current-history" {
		t.Fatalf("current history = %q, %v", history, err)
	}
	if _, err := os.Stat(filepath.Join(legacyConfigDir, "connections.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy connections still exist: %v", err)
	}
}
