package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/lucasfguimares/tui-db/internal/database"
	"github.com/lucasfguimares/tui-db/internal/profile"
	"github.com/lucasfguimares/tui-db/internal/sqleditor"
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

func TestFormatCurrentPreservesLogicalCursor(t *testing.T) {
	editor := textarea.New()
	editor.SetValue("select usr_cod,usr_nome from usr where usr_status='A';")
	cursor := strings.Index(editor.Value(), "usr_nome") + 4
	setEditorCursor(&editor, cursor)
	document := sqleditor.NewDocument(editor.Value())
	analysis := sqleditor.Analyze(editor.Value(), sqleditor.TSQL(), document.Version())
	_ = document.Apply(analysis)
	tab := &queryTab{connection: profile.Connection{Driver: profile.DriverSQLServer}, editor: editor, document: document, analysis: analysis, format: sqleditor.DefaultFormatOptions()}
	model := &Model{tabs: []*queryTab{tab}, activeTab: 0, focus: focusEditor, styles: defaultStyles()}
	model.formatCurrent()
	offset, ok := cursorOffset(tab.editor)
	if !ok {
		t.Fatal("cursorOffset failed after formatting")
	}
	tokens, _ := (sqleditor.Lexer{Dialect: sqleditor.TSQL()}).Lex(tab.editor.Value())
	found := false
	for _, token := range tokens {
		if token.Text == "usr_nome" && offset >= token.Range.Start.Offset && offset <= token.Range.End.Offset {
			found = true
		}
	}
	if !found {
		t.Fatalf("cursor moved outside logical token: offset=%d SQL=\n%s", offset, tab.editor.Value())
	}
}

func TestEditorRenderShowsLineMarkerAndDiagnosticInSmallViewport(t *testing.T) {
	editor := textarea.New()
	editor.SetValue("SELECT *\nFROM usr\nWHERE id =")
	editor.SetWidth(24)
	editor.SetHeight(3)
	analysis := sqleditor.Analyze(editor.Value(), sqleditor.TSQL(), 1)
	tab := &queryTab{editor: editor, analysis: analysis}
	model := &Model{focus: focusEditor, styles: defaultStyles()}
	view := model.renderSQLEditor(tab, 24, 3)
	if !strings.Contains(view, "!") || !strings.Contains(view, "ERROR [Ln 3, Col 10]") {
		t.Fatalf("rendered editor lacks diagnostic marker/panel:\n%s", view)
	}
}

func TestResizePreservesEditorState(t *testing.T) {
	editor := textarea.New()
	editor.SetValue(strings.Repeat("SELECT 1;\n", 40))
	editor.MoveToEnd()
	selection := 3
	tab := &queryTab{editor: editor, table: newResultTable(20), selection: &selection}
	model := &Model{tabs: []*queryTab{tab}, activeTab: 0, width: 120, height: 40, isBrowserOpen: true}
	before, line, column := tab.editor.Value(), tab.editor.Line(), tab.editor.Column()
	model.resize()
	if tab.editor.Value() != before || tab.editor.Line() != line || tab.editor.Column() != column || tab.selection == nil || *tab.selection != selection {
		t.Fatalf("resize changed editor state: line=%d/%d col=%d/%d selection=%v", tab.editor.Line(), line, tab.editor.Column(), column, tab.selection)
	}
}
