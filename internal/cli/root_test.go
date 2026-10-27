package cli

import (
	"bytes"
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
	if !strings.Contains(output.String(), "tui-db ") || !strings.Contains(output.String(), "commit:") {
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

func TestResolvePathsKeepsSessionNextToConfig(t *testing.T) {
	t.Parallel()
	configPath := filepath.Join(t.TempDir(), "custom", "connections.json")
	_, _, _, _, workspacePath, err := resolvePaths(configPath)
	if err != nil {
		t.Fatalf("resolvePaths() error = %v", err)
	}
	if want := filepath.Join(filepath.Dir(configPath), "workspace.json"); workspacePath != want {
		t.Fatalf("workspace path = %q, want %q", workspacePath, want)
	}
}
