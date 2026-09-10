package tui

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/lucasfguimares/charta-tui/internal/activity"
	"github.com/lucasfguimares/charta-tui/internal/database"
	"github.com/lucasfguimares/charta-tui/internal/profile"
	"github.com/lucasfguimares/charta-tui/internal/queryhistory"
	"github.com/lucasfguimares/charta-tui/internal/querylibrary"
	"github.com/lucasfguimares/charta-tui/internal/sqleditor"
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

func TestAutocompleteFiltersAndAcceptsQualifiedColumn(t *testing.T) {
	t.Parallel()

	editor := textarea.New()
	editor.SetValue("SELECT u.\nFROM usr AS u")
	setEditorCursor(&editor, strings.Index(editor.Value(), "\n"))
	editor.Focus()
	document := sqleditor.NewDocument(editor.Value())
	tab := &queryTab{
		id: 1, connection: profile.Connection{ID: "connection-1", Driver: profile.DriverSQLServer, DefaultSchema: "dbo"},
		editor: editor, table: newResultTable(20), document: document,
	}
	model := &Model{
		tabs: []*queryTab{tab}, activeTab: 0, focus: focusEditor, mode: modeWorkspace,
		catalog: map[string]*catalogState{}, autocompleteCache: map[string]*autocompleteCatalogState{
			"connection-1": {isLoaded: true, catalog: autocompleteTestCatalog()},
		},
		styles: defaultStyles(),
	}

	cmd := model.handleKey(tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
	model.Update(cmd())
	if tab.completion.mode != autocompleteList || len(tab.completion.result.Items) != 4 {
		t.Fatalf("completion mode=%v items=%#v", tab.completion.mode, tab.completion.result.Items)
	}
	model.handleKey(tea.KeyPressMsg{Code: 'n', Text: "n"})
	cmd = model.refreshAutocompleteCmd(tab)
	model.Update(cmd())
	if got := completionItemLabels(tab.completion.result.Items); len(got) != 1 || got[0] != "usr_nome" {
		t.Fatalf("filtered items = %#v", got)
	}
	model.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if tab.completion.mode != autocompleteClosed || tab.editor.Value() != "SELECT u.usr_nome\nFROM usr AS u" {
		t.Fatalf("accepted completion mode=%v SQL=%q", tab.completion.mode, tab.editor.Value())
	}
}

func TestAutocompleteKeyboardNavigationAndRendering(t *testing.T) {
	t.Parallel()

	tab := &queryTab{editor: textarea.New(), table: newResultTable(20), connection: profile.Connection{ID: "connection-1"}, completion: autocompleteState{
		mode: autocompleteList,
		result: sqleditor.CompletionResult{Items: []sqleditor.CompletionItem{
			{Label: "usr_cod", Kind: sqleditor.CompletionColumn, SQLType: "INTEGER", IsPrimaryKey: true},
			{Label: "usr_nome", Kind: sqleditor.CompletionColumn, SQLType: "VARCHAR", IsNullable: true, HasNullable: true},
		}},
	}}
	model := &Model{
		tabs: []*queryTab{tab}, activeTab: 0, focus: focusEditor, mode: modeWorkspace,
		autocompleteCache: map[string]*autocompleteCatalogState{"connection-1": {isLoaded: true}},
		styles:            defaultStyles(),
	}
	model.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if tab.completion.selected != 1 {
		t.Fatalf("selected = %d, want 1", tab.completion.selected)
	}
	view := model.renderAutocomplete(tab, 58)
	for _, text := range []string{"usr_nome", "COLUMN", "VARCHAR", "NULL", "Enter accept", "Tab change focus"} {
		if !strings.Contains(view, text) {
			t.Errorf("autocomplete render missing %q:\n%s", text, view)
		}
	}
	model.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if tab.completion.mode != autocompleteClosed {
		t.Fatal("Esc did not close autocomplete")
	}
}

func TestInlineAutocompleteSuggestsAndAcceptsBestItem(t *testing.T) {
	t.Parallel()

	model, tab := inlineAutocompleteTestModel("INS", len("INS"))
	runInlineAutocomplete(model, tab)
	if tab.completion.mode != autocompleteInline || inlineCompletionSuffix(tab) != "ERT" {
		t.Fatalf("inline completion mode=%v suffix=%q result=%#v", tab.completion.mode, inlineCompletionSuffix(tab), tab.completion.result)
	}
	if tab.editor.Value() != "INS" {
		t.Fatalf("ghost text mutated SQL to %q", tab.editor.Value())
	}
	if view := ansi.Strip(model.renderSQLEditor(tab, 80, 8)); !strings.Contains(view, "INSERT") {
		t.Fatalf("rendered editor does not contain ghost completion:\n%s", view)
	}

	model.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if tab.editor.Value() != "INSERT" || tab.completion.mode != autocompleteClosed {
		t.Fatalf("accepted SQL=%q mode=%v", tab.editor.Value(), tab.completion.mode)
	}
}

func TestInlineAutocompleteSpaceAcceptsSuggestionOrInsertsSpace(t *testing.T) {
	t.Parallel()

	t.Run("accept suggestion", func(t *testing.T) {
		t.Parallel()

		model, tab := inlineAutocompleteTestModel("INS", len("INS"))
		runInlineAutocomplete(model, tab)
		model.handleKey(tea.KeyPressMsg{Code: ' ', Text: " "})
		if tab.editor.Value() != "INSERT" || tab.completion.mode != autocompleteClosed {
			t.Fatalf("Space with suggestion produced SQL=%q mode=%v", tab.editor.Value(), tab.completion.mode)
		}
	})

	t.Run("insert space without suggestion", func(t *testing.T) {
		t.Parallel()

		model, tab := inlineAutocompleteTestModel("XYZ", len("XYZ"))
		runInlineAutocomplete(model, tab)
		if suffix := inlineCompletionSuffix(tab); suffix != "" {
			t.Fatalf("unexpected inline suggestion %q", suffix)
		}
		model.handleKey(tea.KeyPressMsg{Code: ' ', Text: " "})
		if tab.editor.Value() != "XYZ " {
			t.Fatalf("Space without suggestion produced %q", tab.editor.Value())
		}
	})
}

func TestInlineAutocompleteRequiresEligiblePrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		sql    string
		cursor int
	}{
		{name: "one character", sql: "I", cursor: 1},
		{name: "string", sql: "'IN'", cursor: 3},
		{name: "comment", sql: "-- IN", cursor: 5},
		{name: "middle of token", sql: "INSERT", cursor: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			model, tab := inlineAutocompleteTestModel(test.sql, test.cursor)
			runInlineAutocomplete(model, tab)
			if suffix := inlineCompletionSuffix(tab); suffix != "" || tab.completion.mode != autocompleteClosed {
				t.Fatalf("mode=%v suffix=%q result=%#v", tab.completion.mode, suffix, tab.completion.result)
			}
		})
	}
}

func TestAutocompleteEnterAndTabKeepEditorNavigationContracts(t *testing.T) {
	t.Parallel()

	model, tab := inlineAutocompleteTestModel("INS", len("INS"))
	model.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if tab.editor.Value() != "INS\n" {
		t.Fatalf("Enter without completion produced %q, want newline", tab.editor.Value())
	}

	tab.completion.mode = autocompleteList
	model.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if tab.completion.mode != autocompleteClosed || model.focus != focusResults {
		t.Fatalf("Tab left completion mode=%v focus=%v, want closed/results", tab.completion.mode, model.focus)
	}
}

func TestInlineAutocompleteDismissalAndStaleDebounce(t *testing.T) {
	t.Parallel()

	model, tab := inlineAutocompleteTestModel("INS", len("INS"))
	first := model.scheduleInlineAutocomplete(tab, tab.document.Version())
	if first == nil {
		t.Fatal("first autocomplete was not scheduled")
	}
	firstRequest := tab.completion.requestID
	second := model.scheduleInlineAutocomplete(tab, tab.document.Version())
	if second == nil || tab.completion.requestID == firstRequest {
		t.Fatal("second autocomplete did not supersede the first")
	}
	if cmd := model.handleAutocompleteDue(autocompleteDueMsg{tabID: tab.id, requestID: firstRequest, version: tab.document.Version()}); cmd != nil {
		t.Fatal("stale debounce produced a completion command")
	}
	runInlineAutocomplete(model, tab)
	model.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	requestID := tab.completion.requestID
	if cmd := model.handleAutocompleteDue(autocompleteDueMsg{tabID: tab.id, requestID: requestID, version: tab.document.Version()}); cmd != nil {
		t.Fatal("dismissed SQL was suggested again without an edit")
	}
}

func TestAutocompleteCatalogLoadsAutomaticallyOnceAndExplicitlyRetries(t *testing.T) {
	t.Parallel()

	connection := profile.Connection{ID: "connection-1"}
	model := &Model{inspector: database.NewInspector(nil), autocompleteCache: map[string]*autocompleteCatalogState{}}
	if cmd := model.startAutocompleteCatalogCmd(connection, false); cmd == nil {
		t.Fatal("first automatic catalog load returned nil")
	}
	cache := model.ensureAutocompleteCache(connection.ID)
	if !cache.autoAttempted || !cache.isLoading {
		t.Fatalf("automatic cache state = %#v", cache)
	}
	cache.isLoading = false
	if cmd := model.startAutocompleteCatalogCmd(connection, false); cmd != nil {
		t.Fatal("automatic catalog load was attempted more than once")
	}
	if cmd := model.startAutocompleteCatalogCmd(connection, true); cmd == nil {
		t.Fatal("explicit catalog load did not retry")
	}
}

func TestCompletionColumnMapsCatalogDetails(t *testing.T) {
	t.Parallel()

	column := completionColumn(database.Column{Name: "group_id", Type: "BIGINT", Nullable: true, Key: "PK/FK"})
	if column.Name != "group_id" || column.SQLType != "BIGINT" || !column.IsNullable || !column.IsPrimaryKey || !column.IsForeignKey {
		t.Fatalf("completionColumn() = %#v", column)
	}
}

func TestAutocompleteDiscardsStaleResults(t *testing.T) {
	t.Parallel()

	tab := &queryTab{id: 7, completion: autocompleteState{mode: autocompleteList, requestID: 2, isCalculating: true}}
	model := &Model{tabs: []*queryTab{tab}, activeTab: 0}
	model.handleAutocompleteResult(autocompleteResultMsg{
		tabID: 7, requestID: 1,
		result: sqleditor.CompletionResult{Items: []sqleditor.CompletionItem{{Label: "stale"}}},
	})
	if len(tab.completion.result.Items) != 0 || !tab.completion.isCalculating {
		t.Fatalf("stale completion was applied: %#v", tab.completion)
	}
	model.handleAutocompleteResult(autocompleteResultMsg{
		tabID: 7, requestID: 2,
		result: sqleditor.CompletionResult{Items: []sqleditor.CompletionItem{{Label: "current"}}},
	})
	if len(tab.completion.result.Items) != 1 || tab.completion.result.Items[0].Label != "current" || tab.completion.isCalculating {
		t.Fatalf("current completion was not applied: %#v", tab.completion)
	}
}

func inlineAutocompleteTestModel(sql string, cursor int) (*Model, *queryTab) {
	editor := textarea.New()
	editor.SetValue(sql)
	setEditorCursor(&editor, cursor)
	editor.Focus()
	document := sqleditor.NewDocument(sql)
	analysis := sqleditor.Analyze(sql, sqleditor.PostgreSQL(), document.Version())
	_ = document.Apply(analysis)
	tab := &queryTab{
		id: 1, connection: profile.Connection{ID: "connection-1", Driver: profile.DriverPostgres},
		editor: editor, table: newResultTable(20), document: document, analysis: analysis,
	}
	model := &Model{
		tabs: []*queryTab{tab}, activeTab: 0, focus: focusEditor, mode: modeWorkspace,
		autocompleteCache: map[string]*autocompleteCatalogState{"connection-1": {isLoaded: true}},
		styles:            defaultStyles(),
	}
	return model, tab
}

func runInlineAutocomplete(model *Model, tab *queryTab) {
	model.scheduleInlineAutocomplete(tab, tab.document.Version())
	cmd := model.handleAutocompleteDue(autocompleteDueMsg{
		tabID: tab.id, requestID: tab.completion.requestID, version: tab.document.Version(),
	})
	if cmd == nil {
		return
	}
	model.Update(cmd())
}

func TestSnippetExpansionAndPlaceholderNavigation(t *testing.T) {
	t.Parallel()
	editor := textarea.New()
	editor.SetValue("sel")
	editor.MoveToEnd()
	editor.Focus()
	tab := &queryTab{id: 1, connection: profile.Connection{Driver: profile.DriverPostgres}, editor: editor, document: sqleditor.NewDocument("sel")}
	model := &Model{tabs: []*queryTab{tab}, activeTab: 0, focus: focusEditor, mode: modeWorkspace, librarySnippets: querylibrary.BuiltInSnippets()}
	model.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if tab.editor.Value() != "SELECT\n    ${columns}\nFROM ${table};" || len(tab.placeholders) != 2 {
		t.Fatalf("expanded SQL = %q, placeholders = %#v", tab.editor.Value(), tab.placeholders)
	}
	model.handleKey(tea.KeyPressMsg{Code: 'i', Text: "id"})
	if !strings.Contains(tab.editor.Value(), "    id\n") {
		t.Fatalf("placeholder replacement = %q", tab.editor.Value())
	}
	model.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	model.handleKey(tea.KeyPressMsg{Code: 'u', Text: "users"})
	if tab.editor.Value() != "SELECT\n    id\nFROM users;" || len(tab.placeholders) != 0 {
		t.Fatalf("final SQL = %q, placeholders = %#v", tab.editor.Value(), tab.placeholders)
	}
}

func TestFavoritePlaceholderNavigation(t *testing.T) {
	t.Parallel()

	editor := textarea.New()
	tab := &queryTab{
		id:         1,
		connection: profile.Connection{Driver: profile.DriverPostgres},
		editor:     editor,
		document:   sqleditor.NewDocument(""),
		table:      newResultTable(20),
	}
	model := &Model{tabs: []*queryTab{tab}, activeTab: 0, focus: focusResults, mode: modeLibrary}
	favorite := querylibrary.Favorite{
		Name: "User by ID",
		SQL:  "SELECT * FROM ${table} WHERE id = ${id};",
	}

	if cmd := model.loadFavorite(favorite, false); cmd != nil {
		t.Fatal("loadFavorite() returned an unexpected command")
	}
	if model.mode != modeWorkspace || model.focus != focusEditor {
		t.Fatalf("mode and focus = %v, %v; want workspace and editor", model.mode, model.focus)
	}
	if len(tab.placeholders) != 2 {
		t.Fatalf("loaded placeholders = %#v", tab.placeholders)
	}

	model.handleKey(tea.KeyPressMsg{Code: 'u', Text: "users"})
	model.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	model.handleKey(tea.KeyPressMsg{Code: '4', Text: "42"})
	if tab.editor.Value() != "SELECT * FROM users WHERE id = 42;" || len(tab.placeholders) != 0 {
		t.Fatalf("completed favorite = %q, placeholders = %#v", tab.editor.Value(), tab.placeholders)
	}
}

func TestFavoriteRunWaitsForPlaceholderInput(t *testing.T) {
	t.Parallel()

	editor := textarea.New()
	tab := &queryTab{
		id:         1,
		connection: profile.Connection{Driver: profile.DriverPostgres},
		editor:     editor,
		document:   sqleditor.NewDocument(""),
		table:      newResultTable(20),
	}
	model := &Model{tabs: []*queryTab{tab}, activeTab: 0, focus: focusEditor, mode: modeLibrary}
	favorite := querylibrary.Favorite{Name: "User by ID", SQL: "SELECT * FROM users WHERE id = ${id};"}

	if cmd := model.loadFavorite(favorite, true); cmd != nil {
		t.Fatal("loadFavorite() started a query with unresolved placeholders")
	}
	if tab.isRunning || len(tab.placeholders) != 1 {
		t.Fatalf("isRunning = %v, placeholders = %#v", tab.isRunning, tab.placeholders)
	}
	if tab.statusText != "Fill placeholders before running — Tab navigates" {
		t.Fatalf("status = %q", tab.statusText)
	}
}

func TestFavoriteWithoutPlaceholdersStillRunsImmediately(t *testing.T) {
	t.Parallel()

	tab := &queryTab{
		id:         1,
		connection: profile.Connection{Driver: profile.DriverPostgres},
		editor:     textarea.New(),
		document:   sqleditor.NewDocument(""),
		table:      newResultTable(20),
	}
	model := &Model{tabs: []*queryTab{tab}, activeTab: 0, focus: focusEditor, mode: modeLibrary}
	favorite := querylibrary.Favorite{Name: "All users", SQL: "SELECT * FROM users;"}

	if cmd := model.loadFavorite(favorite, true); cmd == nil {
		t.Fatal("loadFavorite() did not start a favorite without placeholders")
	}
	if !tab.isRunning {
		t.Fatal("favorite without placeholders is not running")
	}
	if tab.cancel != nil {
		tab.cancel()
	}
}

func TestHandleQueryFinishedPersistsHistory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	historyStore := queryhistory.NewStore(filepath.Join(dir, "history.json"), 10)
	activityLog, err := activity.Open(filepath.Join(dir, "activity.jsonl"), slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = activityLog.Close() })
	tab := &queryTab{
		id: 1, requestID: 2, isRunning: true, startedAt: time.Now().Add(-time.Millisecond), lastSQL: "SELECT 1",
		connection: profile.Connection{ID: "connection", Name: "Local", Driver: profile.DriverSQLite, SQLitePath: "/tmp/local.db"},
		table:      newResultTable(20), editor: textarea.New(),
	}
	model := &Model{tabs: []*queryTab{tab}, activeTab: 0, history: historyStore, activity: activityLog}
	cmd := model.handleQueryFinished(queryFinishedMsg{tabID: 1, requestID: 2, result: database.Result{Rows: [][]any{{int64(1)}}, Columns: []database.ResultColumn{{Name: "value"}}}, columnWidths: []int{8}})
	if cmd == nil {
		t.Fatal("handleQueryFinished() did not return history command")
	}
	msg, ok := cmd().(historyRecordedMsg)
	if !ok || msg.err != nil {
		t.Fatalf("history command = %#v", msg)
	}
	entries, err := historyStore.List(queryhistory.Filter{})
	if err != nil || len(entries) != 1 {
		t.Fatalf("history entries = %#v, %v", entries, err)
	}
	entry := entries[0]
	if entry.SQL != "SELECT 1" || entry.ConnectionName != "Local" || entry.Database != "local.db" || entry.Rows != 1 || entry.Status != queryhistory.StatusSuccess || entry.Duration <= 0 {
		t.Fatalf("history entry = %#v", entry)
	}
	tab.requestID = 3
	tab.isRunning = true
	tab.startedAt = time.Now().Add(-time.Millisecond)
	tab.lastSQL = "SELECT slow()"
	cancelCmd := model.handleQueryFinished(queryFinishedMsg{tabID: 1, requestID: 3, result: database.Result{Duration: time.Millisecond}, err: context.Canceled})
	cancelMsg := cancelCmd().(historyRecordedMsg)
	if cancelMsg.err != nil {
		t.Fatalf("cancel history error = %v", cancelMsg.err)
	}
	entries, err = historyStore.List(queryhistory.Filter{Status: queryhistory.StatusCancelled})
	if err != nil || len(entries) != 1 || entries[0].Error != "query cancelled" {
		t.Fatalf("cancelled entries = %#v, %v", entries, err)
	}
}

func autocompleteTestCatalog() sqleditor.Catalog {
	return sqleditor.Catalog{DefaultSchema: "dbo", Schemas: []sqleditor.SchemaMetadata{{
		Name: "dbo", Relations: []sqleditor.RelationMetadata{{
			Schema: "dbo", Name: "usr", Kind: "BASE TABLE", Columns: []sqleditor.ColumnMetadata{
				{Name: "usr_cod", SQLType: "INTEGER", IsPrimaryKey: true},
				{Name: "usr_nome", SQLType: "VARCHAR"},
				{Name: "usr_status", SQLType: "CHAR"},
				{Name: "usr_grp", SQLType: "INTEGER", IsForeignKey: true},
			},
		}},
	}}}
}

func completionItemLabels(items []sqleditor.CompletionItem) []string {
	labels := make([]string, len(items))
	for index, item := range items {
		labels[index] = item.Label
	}
	return labels
}

func TestInvalidatesSchemaMetadata(t *testing.T) {
	t.Parallel()

	for _, sql := range []string{
		"CREATE TABLE users (id INT)",
		"ALTER TABLE users ADD name TEXT",
		"DROP TABLE users",
		"TRUNCATE TABLE users",
		"ATTACH DATABASE 'other.db' AS other",
		"DETACH DATABASE other",
		"PRAGMA foreign_keys = ON",
		"SELECT 1; CREATE INDEX users_id ON users(id)",
	} {
		if !invalidatesSchemaMetadata(sql) {
			t.Errorf("invalidatesSchemaMetadata(%q) = false", sql)
		}
	}
	for _, sql := range []string{"SELECT * FROM users", "INSERT INTO users VALUES (1)", "UPDATE users SET id = 2"} {
		if invalidatesSchemaMetadata(sql) {
			t.Errorf("invalidatesSchemaMetadata(%q) = true", sql)
		}
	}
}
