package cli

import (
	"bytes"
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
