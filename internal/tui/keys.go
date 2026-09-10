package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/lucasfguimares/charta-tui/internal/profile"
	"github.com/lucasfguimares/charta-tui/internal/querylibrary"
	"github.com/lucasfguimares/charta-tui/internal/sqleditor"
)

func (m *Model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if m.mode != modeWorkspace {
		return m.handleModalKey(msg)
	}
	if m.focus == focusEditor {
		if cmd, handled := m.handleAutocompleteKey(msg); handled {
			return cmd
		}
		if cmd, handled := m.handlePlaceholderKey(msg); handled {
			return cmd
		}
	}

	switch key {
	case "ctrl+q":
		if m.hasDirtyTabs() {
			m.mode = modeConfirmQuit
			return nil
		}
		return tea.Quit
	case "f1", "?":
		m.mode = modeHelp
		return nil
	case "ctrl+l":
		m.mode = modeLogs
		return m.loadLogsCmd()
	case "ctrl+h":
		return m.openHistory()
	case "ctrl+shift+p":
		return m.openLibrary()
	case "ctrl+s":
		if m.focus == focusEditor {
			m.openFavoriteForm(querylibrary.Favorite{})
		}
		return nil
	case "ctrl+b":
		m.isBrowserOpen = !m.isBrowserOpen
		if !m.isBrowserOpen && m.focus == focusBrowser {
			m.setFocus(focusEditor)
		}
		m.resize()
		return nil
	case "ctrl+t":
		return m.newTabForSelection()
	case "ctrl+w":
		return m.requestCloseTab()
	case "ctrl+pgup":
		m.switchTab(-1)
		return nil
	case "ctrl+pgdown":
		m.switchTab(1)
		return nil
	case "ctrl+g", "ctrl+c":
		if tab := m.currentTab(); tab != nil && tab.isRunning && tab.cancel != nil {
			tab.cancel()
			tab.statusText = "Cancelling query…"
		}
		return nil
	case "ctrl+enter":
		return m.requestRun()
	case "ctrl+shift+enter":
		return m.requestRunScript()
	case "ctrl+shift+f":
		return m.formatCurrent()
	case "f8":
		m.navigateDiagnostic(1)
		return nil
	case "shift+f8":
		m.navigateDiagnostic(-1)
		return nil
	case "ctrl+space", "ctrl+@":
		if m.focus == focusEditor {
			return m.openAutocomplete()
		}
		return nil
	case "ctrl+shift+space":
		if m.focus == focusEditor {
			m.toggleSelection()
		}
		return nil
	case "tab":
		m.cycleFocus(1)
		return nil
	case "shift+tab":
		m.cycleFocus(-1)
		return nil
	}

	if m.focus == focusBrowser {
		return m.handleBrowserKey(msg)
	}
	if m.focus == focusResults {
		return m.handleResultKey(msg)
	}
	return m.updateFocused(msg)
}

func (m *Model) handleResultKey(msg tea.KeyPressMsg) tea.Cmd {
	tab := m.currentTab()
	if tab == nil {
		return nil
	}
	key := msg.String()
	if tab.table.HandleNavigation(key) {
		return nil
	}
	switch key {
	case "enter":
		if tab.table.IsColumnSelected() {
			return m.openColumnInspector(tab)
		}
		if _, ok := tab.table.SelectedCell(); ok {
			m.openCellDetail(tab)
		}
	case "space":
		if tab.table.IsColumnSelected() {
			if tab.table.ToggleFrozenColumns() {
				tab.statusText = fmt.Sprintf("%d column(s) frozen", tab.table.frozenColumnCount)
			} else {
				tab.statusText = "Frozen columns do not fit in the current result width"
			}
		}
	case "u":
		if tab.table.ClearFrozenColumns() {
			tab.statusText = "Frozen columns cleared"
		}
	case "v":
		if !tab.table.IsColumnSelected() {
			m.openRowInspector(tab)
		}
	case "c":
		if value, ok := tab.table.CellClipboard(); ok {
			tab.statusText = "Cell copied to clipboard"
			return tea.SetClipboard(value)
		}
	case "C", "shift+c":
		if value, ok := tab.table.RowClipboard(); ok {
			tab.statusText = "Row copied to clipboard"
			return tea.SetClipboard(value)
		}
	case "r", "R", "shift+r":
		if !tab.isRunning && tab.lastSQL != "" {
			return m.runStatement(tab.lastSQL)
		}
	case "f", "F", "shift+f":
		m.resultInput.Prompt = "Find: "
		m.resultInput.SetValue("")
		m.mode = modeResultSearch
		return m.resultInput.Focus()
	case "g", "G", "shift+g":
		m.resultInput.Prompt = "Go to row: "
		m.resultInput.SetValue("")
		m.mode = modeGotoRow
		return m.resultInput.Focus()
	}
	return nil
}

func (m *Model) handleModalKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	switch m.mode {
	case modeHistory, modeHistorySearch:
		return m.handleHistoryKey(msg)
	case modeLibrary, modeLibrarySearch:
		return m.handleLibraryKey(msg)
	case modeFavoriteForm:
		return m.handleFavoriteFormKey(msg)
	case modeSnippetForm:
		return m.handleSnippetFormKey(msg)
	case modeLogs:
		if key == "esc" || key == "ctrl+l" {
			m.mode = modeWorkspace
			return nil
		}
		updated, cmd := m.logs.Update(msg)
		m.logs = updated
		return cmd
	case modeHelp:
		if key == "esc" || key == "f1" || key == "?" {
			m.mode = modeWorkspace
		}
		return nil
	case modeCellDetail:
		if key == "esc" || key == "enter" {
			m.mode = modeWorkspace
			return nil
		}
		updated, cmd := m.cellDetail.Update(msg)
		m.cellDetail = updated
		return cmd
	case modeColumnInspector:
		return m.handleColumnInspectorKey(msg)
	case modeRowInspector:
		if key == "esc" {
			m.mode = modeWorkspace
			return nil
		}
		if m.rowInspector.HandleNavigation(key) {
			return nil
		}
		switch key {
		case "enter":
			m.rowInspector.ToggleExpanded()
		case "c":
			if value, ok := m.rowInspector.ValueClipboard(); ok {
				if tab := m.currentTab(); tab != nil {
					tab.statusText = "Row inspector value copied"
				}
				return tea.SetClipboard(value)
			}
		case "C", "shift+c":
			if value, ok := m.rowInspector.FieldClipboard(); ok {
				if tab := m.currentTab(); tab != nil {
					tab.statusText = "Row inspector field copied"
				}
				return tea.SetClipboard(value)
			}
		}
		return nil
	case modeResultSearch, modeGotoRow:
		if key == "esc" {
			m.resultInput.Blur()
			m.mode = modeWorkspace
			return nil
		}
		if key == "enter" {
			value := m.resultInput.Value()
			currentMode := m.mode
			m.resultInput.Blur()
			m.mode = modeWorkspace
			tab := m.currentTab()
			if tab == nil {
				return nil
			}
			if currentMode == modeResultSearch {
				if tab.table.Search(value) {
					tab.statusText = fmt.Sprintf("Found %q", value)
				} else {
					tab.statusText = fmt.Sprintf("No result matching %q", value)
				}
				return nil
			}
			row, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || !tab.table.GoToRow(row) {
				tab.statusText = "Row number is outside the result set"
			} else {
				tab.statusText = fmt.Sprintf("Moved to row %d", row)
			}
			return nil
		}
		updated, cmd := m.resultInput.Update(msg)
		m.resultInput = updated
		return cmd
	case modeProfile:
		if key == "esc" {
			m.form.clearPassword()
			m.mode = modeWorkspace
			return nil
		}
		if key == "ctrl+s" {
			if err := m.form.validate(); err != nil {
				m.form.errorText = err.Error()
				return nil
			}
			connection := m.form.connection
			password := m.form.value("password")
			m.form.clearPassword()
			m.statusText = "Saving connection…"
			return m.saveProfileCmd(connection, password)
		}
		return m.form.update(msg)
	case modePassword:
		if key == "esc" {
			m.passwordInput.SetValue("")
			m.mode = modeWorkspace
			return nil
		}
		if key == "enter" {
			password := m.passwordInput.Value()
			m.passwordInput.SetValue("")
			if password == "" {
				m.setError(errors.New("password cannot be empty"))
				return nil
			}
			if err := m.secrets.Set(m.pendingConnection.ID, password); err != nil {
				m.setError(err)
				return nil
			}
			connection := m.pendingConnection
			action := m.pendingPasswordAct
			m.mode = modeWorkspace
			if action == passwordTest {
				return m.testConnectionCmd(connection)
			}
			return m.loadSchemasCmd(connection)
		}
		updated, cmd := m.passwordInput.Update(msg)
		m.passwordInput = updated
		return cmd
	case modeConfirmQuery:
		if key == "esc" || key == "n" {
			m.mode = modeWorkspace
			m.pendingSQL = ""
			m.pendingRunScript = false
			return nil
		}
		if key == "y" || key == "!" {
			if tab := m.currentTab(); tab != nil && key == "!" {
				tab.isTrusted = true
			}
			statement := m.pendingSQL
			script := m.pendingRunScript
			m.pendingSQL = ""
			m.pendingRunScript = false
			m.mode = modeWorkspace
			return m.runSQL(statement, script)
		}
	case modeConfirmDelete:
		if key == "y" {
			id := m.pendingDeleteID
			m.pendingDeleteID = ""
			m.mode = modeWorkspace
			return m.deleteProfileCmd(id)
		}
		if key == "n" || key == "esc" {
			m.mode = modeWorkspace
		}
	case modeConfirmClose:
		if key == "y" {
			m.mode = modeWorkspace
			m.closeActiveTab()
		}
		if key == "n" || key == "esc" {
			m.mode = modeWorkspace
		}
	case modeConfirmQuit:
		if key == "y" {
			m.cancelAll()
			return tea.Quit
		}
		if key == "n" || key == "esc" {
			m.mode = modeWorkspace
		}
	case modeConfirmHistoryClear:
		if key == "y" {
			m.mode = modeHistory
			return m.clearHistoryCmd()
		}
		if key == "n" || key == "esc" {
			m.mode = modeHistory
		}
	}
	return nil
}

func (m *Model) handleBrowserKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	switch key {
	case "up", "k":
		if m.browserCursor > 0 {
			m.browserCursor--
		}
	case "down", "j":
		if m.browserCursor < len(m.browserItems)-1 {
			m.browserCursor++
		}
	case "enter", "right", "l":
		return m.openBrowserItem()
	case "left", "h":
		m.collapseBrowserItem()
	case "a":
		connection, err := profile.NewConnection(profile.DriverPostgres)
		if err != nil {
			m.setError(err)
			return nil
		}
		m.form = newProfileForm(connection, true)
		m.mode = modeProfile
	case "e":
		if connection, ok := m.selectedProfile(); ok {
			m.form = newProfileForm(connection, false)
			m.mode = modeProfile
		}
	case "c":
		if connection, ok := m.selectedProfile(); ok {
			duplicate, err := profile.NewConnection(connection.Driver)
			if err != nil {
				m.setError(err)
				return nil
			}
			connection.ID = duplicate.ID
			connection.Name += " Copy"
			m.form = newProfileForm(connection, true)
			m.mode = modeProfile
		}
	case "d":
		if connection, ok := m.selectedProfile(); ok {
			m.pendingDeleteID = connection.ID
			m.mode = modeConfirmDelete
		}
	case "r":
		if connection, ok := m.selectedProfile(); ok {
			delete(m.catalog, connection.ID)
			delete(m.autocompleteCache, connection.ID)
			m.invalidateColumnCaches(connection.ID)
			return m.loadSchemasCmd(connection)
		}
	case "t":
		if connection, ok := m.selectedProfile(); ok {
			m.statusText = fmt.Sprintf("Testing %s…", connection.Name)
			return m.prepareTestCmd(connection)
		}
	case "x":
		if connection, ok := m.selectedProfile(); ok {
			return m.disconnectCmd(connection)
		}
	}
	return nil
}

func (m *Model) updateFocused(message tea.Msg) tea.Cmd {
	tab := m.currentTab()
	if tab == nil {
		return nil
	}
	switch m.focus {
	case focusEditor:
		before := tab.editor.Value()
		beforeCursor, _ := cursorOffset(tab.editor)
		updated, cmd := tab.editor.Update(message)
		tab.editor = updated
		after := tab.editor.Value()
		if before != after {
			adjustPlaceholders(tab, beforeCursor, len(after)-len(before))
			if tab.document == nil {
				tab.document = sqleditor.NewDocument(before)
			}
			version := tab.document.SetText(after)
			if tab.completion.mode == autocompleteList {
				return tea.Batch(cmd, scheduleSQLAnalysis(tab.id, version), m.refreshAutocompleteCmd(tab))
			}
			return tea.Batch(cmd, scheduleSQLAnalysis(tab.id, version), m.scheduleInlineAutocomplete(tab, version))
		}
		afterCursor, _ := cursorOffset(tab.editor)
		if beforeCursor != afterCursor {
			if tab.completion.mode == autocompleteList {
				return tea.Batch(cmd, m.refreshAutocompleteCmd(tab))
			}
			version := uint64(0)
			if tab.document != nil {
				version = tab.document.Version()
			}
			return tea.Batch(cmd, m.scheduleInlineAutocomplete(tab, version))
		}
		return cmd
	case focusResults:
		return nil
	default:
		return nil
	}
}
