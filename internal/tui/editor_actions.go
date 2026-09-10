package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/lucasfguimares/charta-tui/internal/database"
	"github.com/lucasfguimares/charta-tui/internal/profile"
	"github.com/lucasfguimares/charta-tui/internal/sqleditor"
	"github.com/lucasfguimares/charta-tui/internal/sqlscan"
)

func (m *Model) requestRun() tea.Cmd {
	tab := m.currentTab()
	if tab == nil {
		m.setError(errors.New("open a connection tab before running sql"))
		return nil
	}
	if tab.isRunning {
		m.setError(errors.New("a query is already running in this tab"))
		return nil
	}
	statement, base, ok := selectedStatementWithOffset(tab)
	if !ok {
		m.setError(errors.New("select one statement or place the cursor inside one"))
		return nil
	}
	tab.lastSQLBase = base
	tab.selection = nil
	if activatePlaceholders(tab, statement, base) {
		return nil
	}
	if sqlscan.Analyze(statement).IsRisky && !tab.isTrusted {
		m.pendingSQL = statement
		m.pendingRunScript = false
		m.mode = modeConfirmQuery
		return nil
	}
	return m.runStatement(statement)
}

func (m *Model) requestRunScript() tea.Cmd {
	tab := m.currentTab()
	if tab == nil || strings.TrimSpace(tab.editor.Value()) == "" {
		m.setError(errors.New("open a connection tab and enter sql before running the script"))
		return nil
	}
	if tab.isRunning {
		m.setError(errors.New("a query is already running in this tab"))
		return nil
	}
	script := tab.editor.Value()
	tab.lastSQLBase = 0
	if activatePlaceholders(tab, script, 0) {
		return nil
	}
	if sqlscan.Analyze(script).IsRisky && !tab.isTrusted {
		m.pendingSQL = script
		m.pendingRunScript = true
		m.mode = modeConfirmQuery
		return nil
	}
	return m.runSQL(script, true)
}

func (m *Model) toggleSelection() {
	tab := m.currentTab()
	if tab == nil {
		return
	}
	if tab.selection != nil {
		tab.selection = nil
		tab.statusText = "Selection cleared"
		return
	}
	offset, ok := cursorOffset(tab.editor)
	if !ok {
		return
	}
	tab.selection = &offset
	tab.statusText = "Selection started — move the cursor, then press Ctrl+Enter"
}

func (m *Model) runStatement(statement string) tea.Cmd {
	return m.runSQL(statement, false)
}

func (m *Model) runSQL(statement string, script bool) tea.Cmd {
	tab := m.currentTab()
	if tab == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background()) //nolint:gosec // retained by the tab and called on completion, cancellation or shutdown
	tab.cancel = cancel
	tab.isRunning = true
	tab.statusText = "Running query…"
	tab.startedAt = time.Now()
	tab.lastSQL = statement
	tab.table.SetRunning(tab.startedAt)
	m.isStatusError = false
	tab.requestID++
	tabID, requestID, connection := tab.id, tab.requestID, tab.connection
	runQuery := func() tea.Msg {
		var result database.Result
		var err error
		if script {
			result, err = m.runner.RunScript(ctx, connection, statement)
		} else {
			result, err = m.runner.Run(ctx, connection, statement)
		}
		layoutStarted := time.Now()
		columnWidths := calculateResultColumnWidths(
			result,
			defaultResultTableConfig(connection.EffectiveMaxColumnWidth()),
		)
		return queryFinishedMsg{
			tabID:          tabID,
			requestID:      requestID,
			result:         result,
			columnWidths:   columnWidths,
			layoutDuration: time.Since(layoutStarted),
			err:            err,
		}
	}
	return tea.Batch(runQuery, queryTickCmd(tabID, requestID))
}

func (m *Model) formatCurrent() tea.Cmd {
	tab := m.currentTab()
	if tab == nil || m.focus != focusEditor {
		return nil
	}
	value := tab.editor.Value()
	start, end := 0, len(value)
	cursor, ok := cursorOffset(tab.editor)
	if !ok {
		return nil
	}
	selectionStart := -1
	if tab.selection != nil && *tab.selection != cursor {
		start, end = *tab.selection, cursor
		if start > end {
			start, end = end, start
		}
		selectionStart = start
	} else {
		statements := tab.analysis.Statements
		if len(statements) == 0 {
			statements = sqleditor.Analyze(value, dialectFor(tab.connection.Driver), 0).Statements
		}
		if statement, found := sqleditor.StatementAt(statements, cursor); found {
			start, end = statement.Range.Start.Offset, statement.Range.End.Offset
		}
	}
	formatter := sqleditor.Formatter{Dialect: dialectFor(tab.connection.Driver), Options: tab.format}
	formatted, err := formatter.Format(value[start:end])
	if err != nil {
		tab.statusText = "Format failed: " + err.Error()
		return nil
	}
	newCursor := start + mapLogicalOffset(value[start:end], formatted, cursor-start, dialectFor(tab.connection.Driver))
	updated := value[:start] + formatted + value[end:]
	tab.editor.SetValue(updated)
	setEditorCursor(&tab.editor, newCursor)
	if selectionStart >= 0 {
		anchor := start
		tab.selection = &anchor
	} else {
		tab.selection = nil
	}
	version := tab.document.SetText(updated)
	tab.analysis = sqleditor.Analyze(updated, dialectFor(tab.connection.Driver), version)
	_ = tab.document.Apply(tab.analysis)
	tab.statusText = "SQL formatted"
	return nil
}

func mapLogicalOffset(before, after string, offset int, dialect sqleditor.Dialect) int {
	if offset < 0 {
		return 0
	}
	if offset > len(before) {
		offset = len(before)
	}
	beforeTokens, _ := (sqleditor.Lexer{Dialect: dialect}).Lex(before)
	afterTokens, _ := (sqleditor.Lexer{Dialect: dialect}).Lex(after)
	ordinal, within := 0, 0
	for _, token := range beforeTokens {
		if token.Kind == sqleditor.TokenWhitespace {
			continue
		}
		if offset <= token.Range.End.Offset {
			within = max(0, offset-token.Range.Start.Offset)
			break
		}
		ordinal++
	}
	seen := 0
	for _, token := range afterTokens {
		if token.Kind == sqleditor.TokenWhitespace {
			continue
		}
		if seen == ordinal {
			return min(token.Range.Start.Offset+within, token.Range.End.Offset)
		}
		seen++
	}
	return len(after)
}

func (m *Model) navigateDiagnostic(delta int) {
	tab := m.currentTab()
	if tab == nil || len(tab.analysis.Diagnostics) == 0 {
		return
	}
	tab.diagnostic = (tab.diagnostic + delta + len(tab.analysis.Diagnostics)) % len(tab.analysis.Diagnostics)
	diagnostic := tab.analysis.Diagnostics[tab.diagnostic]
	setEditorCursor(&tab.editor, diagnostic.Range.Start.Offset)
	m.setFocus(focusEditor)
	tab.statusText = diagnostic.String()
}

func setEditorCursor(editor *textarea.Model, offset int) {
	position := sqleditor.PositionAt(editor.Value(), offset)
	editor.MoveToBegin()
	for range max(0, position.Line-1) {
		editor.CursorDown()
	}
	editor.SetCursorColumn(max(0, position.Column-1))
}

func scheduleSQLAnalysis(tabID int, version uint64) tea.Cmd {
	return tea.Tick(220*time.Millisecond, func(time.Time) tea.Msg { return sqlAnalysisDueMsg{tabID: tabID, version: version} })
}

func dialectFor(driver profile.Driver) sqleditor.Dialect { return sqleditor.ByID(string(driver)) }
