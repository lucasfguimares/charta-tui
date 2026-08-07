package tui

import (
	"testing"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/lucasfguimares/tui-db/internal/database"
	"github.com/lucasfguimares/tui-db/internal/profile"
)

func TestStatementAtCursor(t *testing.T) {
	t.Parallel()

	editor := textarea.New()
	editor.SetValue("SELECT 1;\nSELECT 2;")
	editor.MoveToEnd()
	statement, ok := statementAtCursor(editor)
	if !ok || statement != "SELECT 2;" {
		t.Fatalf("statementAtCursor() = %q, %v", statement, ok)
	}
}

func TestHandleKeyCancelsRunningQuery(t *testing.T) {
	t.Parallel()

	cancelled := false
	table := newResultTable(20)
	tab := &queryTab{
		table:     table,
		isRunning: true,
		cancel:    func() { cancelled = true },
	}
	model := &Model{
		tabs:      []*queryTab{tab},
		activeTab: 0,
		focus:     focusResults,
		mode:      modeWorkspace,
	}
	model.handleKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !cancelled || tab.statusText != "Cancelling query…" {
		t.Fatalf("cancelled = %v, status = %q", cancelled, tab.statusText)
	}
}

func TestHandleResultKeyNavigatesActiveCell(t *testing.T) {
	t.Parallel()

	table := newResultTable(20)
	table.SetSize(40, 8)
	table.SetResult(database.Result{
		Columns: []database.ResultColumn{{Name: "id"}, {Name: "name"}},
		Rows:    [][]any{{int64(1), "one"}, {int64(2), "two"}},
	})
	model := &Model{
		tabs:      []*queryTab{{table: table}},
		activeTab: 0,
		focus:     focusResults,
		mode:      modeWorkspace,
	}
	model.handleResultKey(tea.KeyPressMsg{Code: 's', Text: "s"})
	model.handleResultKey(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if table.cursorRow != 1 || table.cursorColumn != 1 {
		t.Fatalf("active cell = %d,%d, want 1,1", table.cursorRow, table.cursorColumn)
	}
}

func TestSelectedStatement(t *testing.T) {
	t.Parallel()

	editor := textarea.New()
	editor.SetValue("SELECT 1;\nSELECT 2;")
	editor.MoveToBegin()
	start, ok := cursorOffset(editor)
	if !ok {
		t.Fatal("cursorOffset() failed")
	}
	editor.MoveToEnd()
	tab := &queryTab{editor: editor, selection: &start}
	if _, ok := selectedStatement(tab); ok {
		t.Fatal("selectedStatement() accepted multiple statements")
	}

	editor.SetValue("SELECT 42;")
	editor.MoveToEnd()
	tab.editor = editor
	start = 0
	statement, ok := selectedStatement(tab)
	if !ok || statement != "SELECT 42;" {
		t.Fatalf("selectedStatement() = %q, %v", statement, ok)
	}
}

func TestDefaultSQL(t *testing.T) {
	t.Parallel()

	if got := defaultSQL(profile.DriverSQLServer); got == "" {
		t.Fatal("defaultSQL(sqlserver) is empty")
	}
	if got := truncate("abcdef", 4); got != "abc…" {
		t.Fatalf("truncate() = %q, want abc…", got)
	}
}

func TestFormatDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{name: "nanoseconds", duration: 123 * time.Nanosecond, want: "123ns"},
		{name: "microseconds", duration: 12 * time.Microsecond, want: "12µs"},
		{name: "milliseconds", duration: 12 * time.Millisecond, want: "12ms"},
		{name: "seconds", duration: 2*time.Second + 345*time.Millisecond, want: "2.345s"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := formatDuration(test.duration); got != test.want {
				t.Fatalf("formatDuration() = %q, want %q", got, test.want)
			}
		})
	}
}
