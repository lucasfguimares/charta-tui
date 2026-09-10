package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m *Model) View() tea.View {
	content := ""
	if m.width > 0 && (m.width < minimumWidth || m.height < minimumHeight) {
		content = m.styles.modal.Render(fmt.Sprintf(
			"charta needs at least %dx%d\ncurrent terminal: %dx%d",
			minimumWidth,
			minimumHeight,
			m.width,
			m.height,
		))
	} else {
		switch m.mode {
		case modeLogs:
			content = m.renderLogScreen()
		case modeHelp:
			content = m.renderHelp()
		case modeProfile:
			content = m.renderProfileForm()
		case modePassword:
			content = m.renderPasswordPrompt()
		case modeCellDetail:
			content = m.renderCellDetail()
		case modeColumnInspector:
			content = m.renderColumnInspector()
		case modeRowInspector:
			content = m.renderRowInspector()
		case modeResultSearch, modeGotoRow:
			content = m.renderResultPrompt()
		case modeHistory, modeHistorySearch:
			content = m.renderHistoryScreen()
		case modeLibrary, modeLibrarySearch:
			content = m.renderLibraryScreen()
		case modeFavoriteForm:
			content = m.renderFavoriteForm()
		case modeSnippetForm:
			content = m.renderSnippetForm()
		case modeConfirmQuery, modeConfirmDelete, modeConfirmClose, modeConfirmQuit, modeConfirmHistoryClear:
			content = m.renderConfirmation()
		default:
			content = m.renderWorkspace()
		}
	}
	view := tea.NewView(content)
	view.AltScreen = true
	view.WindowTitle = "charta"
	return view
}

func (m *Model) renderWorkspace() string {
	tabs := m.renderTabs()
	contentHeight := max(8, m.height-5)
	mainContent := ""
	if tab := m.currentTab(); tab != nil {
		editorHeight := max(5, contentHeight/2)
		resultHeight := max(4, contentHeight-editorHeight)
		editorStyle := m.styles.panel
		resultStyle := m.styles.panel
		if m.focus == focusEditor {
			editorStyle = m.styles.focusedPanel
		}
		if m.focus == focusResults {
			resultStyle = m.styles.focusedPanel
		}
		editorBody := m.renderSQLEditor(tab, max(10, m.contentWidth()-4), max(3, editorHeight-2))
		editor := editorStyle.Width(m.contentWidth() - 2).Height(editorHeight - 2).Render(editorBody)
		resultBody := tab.table.View()
		results := resultStyle.Width(m.contentWidth() - 2).Height(resultHeight - 2).Render(resultBody)
		mainContent = lipgloss.JoinVertical(lipgloss.Left, editor, results)
	} else {
		mainContent = m.styles.panel.Width(m.contentWidth() - 2).Height(contentHeight - 2).Render(
			m.styles.dim.Render("Select a connection and press Enter, or press a to add one."),
		)
	}

	body := mainContent
	if m.isBrowserOpen {
		browserStyle := m.styles.panel
		if m.focus == focusBrowser {
			browserStyle = m.styles.focusedPanel
		}
		browser := browserStyle.Width(browserWidth - 2).Height(contentHeight - 2).Render(m.renderBrowser(contentHeight - 4))
		body = lipgloss.JoinHorizontal(lipgloss.Top, browser, mainContent)
	}
	return m.styles.app.Render(lipgloss.JoinVertical(lipgloss.Left, tabs, body, m.renderStatus()))
}

func (m *Model) renderTabs() string {
	if len(m.tabs) == 0 {
		return m.styles.header.Render("charta  SQL workbench")
	}
	parts := make([]string, 0, len(m.tabs))
	for i, tab := range m.tabs {
		label := fmt.Sprintf("%d:%s", i+1, tab.connection.Name)
		if tab.isRunning {
			label += " ◌"
		}
		if i == m.activeTab {
			parts = append(parts, m.styles.activeTab.Render(label))
		} else {
			parts = append(parts, m.styles.inactiveTab.Render(label))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Bottom, parts...)
}

func (m *Model) renderBrowser(height int) string {
	if len(m.browserItems) == 0 {
		return m.styles.dim.Render("No connections\n\n[a] add connection")
	}
	start := 0
	if m.browserCursor >= height {
		start = m.browserCursor - height + 1
	}
	end := min(len(m.browserItems), start+height)
	lines := make([]string, 0, end-start+2)
	lines = append(lines, m.styles.header.Render("Connections"))
	for index := start; index < end; index++ {
		item := m.browserItems[index]
		marker := " "
		if item.kind != browserColumn {
			marker = "▸"
			if item.isExpanded {
				marker = "▾"
			}
		}
		line := strings.Repeat("  ", item.level) + marker + " " + item.label
		line = truncate(line, browserWidth-4)
		if index == m.browserCursor {
			line = m.styles.selected.Width(browserWidth - 4).Render(line)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "", m.styles.dim.Render("a add  e edit  c copy  d delete\nt test  x disconnect  r refresh"))
	return strings.Join(lines, "\n")
}

func (m *Model) renderStatus() string {
	status := m.statusText
	if tab := m.currentTab(); tab != nil && tab.statusText != "" {
		status = tab.statusText
	}
	keys := "Ctrl+Enter Run  Ctrl+Shift+F Format  F8 Errors"
	if m.focus == focusResults {
		keys = "Arrows/WASD move  Enter detail  v row  c/C copy  ? help"
		if tab := m.currentTab(); tab != nil && tab.table.IsColumnSelected() {
			keys = "←/→ column  Enter inspect  Space freeze  u clear  ↓ rows"
		}
	}
	if tab := m.currentTab(); tab != nil && m.focus == focusEditor {
		errors := diagnosticCounts(tab.analysis.Diagnostics)[0]
		prefix := dialectFor(tab.connection.Driver).DisplayName()
		if tab.connection.DefaultSchema != "" {
			prefix += " [" + tab.connection.DefaultSchema + "]"
		}
		prefix += fmt.Sprintf(" | %s | UTF-8 | Errors: %d", cursorStatus(tab), errors)
		if len(tab.analysis.Diagnostics) > 0 {
			current := tab.analysis.Diagnostics[min(tab.diagnostic, len(tab.analysis.Diagnostics)-1)]
			prefix += " | " + current.Severity.String() + ": " + current.Message
		}
		status = prefix
	}
	available := max(0, m.width-utf8.RuneCountInString(keys)-5)
	status = truncate(status, available)
	line := status + strings.Repeat(" ", max(1, m.width-utf8.RuneCountInString(status)-utf8.RuneCountInString(keys)-4)) + keys
	if m.isStatusError {
		return m.styles.error.Width(max(1, m.width-2)).Render(line)
	}
	return m.styles.status.Width(max(1, m.width-2)).Render(line)
}

func (m *Model) renderProfileForm() string {
	title := "Add connection"
	if !m.form.isNew {
		title = "Edit connection"
	}
	lines := []string{
		m.styles.header.Render(title),
		m.styles.accent.Render("Driver: " + string(m.form.connection.Driver) + "  (Ctrl+D to change)"),
		"",
	}
	for _, field := range m.form.fields {
		lines = append(lines, field.label, field.input.View())
	}
	if m.form.errorText != "" {
		lines = append(lines, "", m.styles.error.Render(m.form.errorText))
	}
	lines = append(lines, "", m.styles.dim.Render("Ctrl+S save  Tab next field  Esc cancel"))
	return m.center(m.styles.modal.Render(strings.Join(lines, "\n")))
}

func (m *Model) renderPasswordPrompt() string {
	content := strings.Join([]string{
		m.styles.header.Render("Password required"),
		"Connect to " + m.pendingConnection.Name,
		"",
		m.passwordInput.View(),
		"",
		m.styles.dim.Render("Enter connect  Esc cancel"),
	}, "\n")
	return m.center(m.styles.modal.Render(content))
}

func (m *Model) renderResultPrompt() string {
	title := "Find in results"
	help := "Enter find next  Esc cancel"
	if m.mode == modeGotoRow {
		title = "Go to result row"
		help = "Enter go  Esc cancel"
	}
	content := strings.Join([]string{
		m.styles.header.Render(title),
		"",
		m.resultInput.View(),
		"",
		m.styles.dim.Render(help),
	}, "\n")
	return m.center(m.styles.modal.Render(content))
}

func (m *Model) renderCellDetail() string {
	tab := m.currentTab()
	if tab == nil {
		return m.center(m.styles.modal.Render("No active result cell."))
	}
	column, columnOK := tab.table.SelectedColumn()
	if !columnOK {
		return m.center(m.styles.modal.Render("No active result cell."))
	}
	width := min(100, max(30, m.width-10))
	details := []string{
		m.styles.header.Render(resultColumnTitle(column)),
		m.styles.dim.Render(fmt.Sprintf("Row %d, column %d", tab.table.cursorRow+1, tab.table.cursorColumn+1)),
		"",
		m.cellDetail.View(),
		m.styles.dim.Render("↑/↓/PgUp/PgDn scroll  Esc/Enter close"),
	}
	return m.center(m.styles.modal.Width(width).Render(strings.Join(details, "\n")))
}

func (m *Model) openCellDetail(tab *queryTab) {
	cell, ok := tab.table.SelectedCell()
	if !ok {
		return
	}
	width := min(100, max(30, m.width-10))
	m.cellDetail.SetWidth(max(20, width-6))
	m.cellDetail.SetHeight(max(3, m.height-10))
	m.cellDetailText = cell.detail
	m.cellDetail.SetContent(ansi.Hardwrap(m.cellDetailText, max(20, width-6), true))
	m.cellDetail.GotoTop()
	m.mode = modeCellDetail
}

func (m *Model) renderRowInspector() string {
	width := min(120, max(40, m.width-10))
	header := m.styles.header.Render(fmt.Sprintf("Row %d", m.rowInspector.rowNumber))
	footer := m.styles.dim.Render("↑/↓/PgUp/PgDn navigate  Enter expand  c value  C field  Esc close")
	content := strings.Join([]string{header, "", m.rowInspector.View(m.styles), footer}, "\n")
	return m.center(m.styles.modal.Width(width).Render(content))
}

func (m *Model) openRowInspector(tab *queryTab) {
	columns, row, rowIndex, ok := tab.table.SelectedRow()
	if !ok {
		return
	}
	m.rowInspector = newRowInspector(columns, row, rowIndex)
	width := min(120, max(40, m.width-10))
	m.rowInspector.SetSize(max(20, width-6), max(3, m.height-10))
	m.mode = modeRowInspector
}

func (m *Model) renderConfirmation() string {
	var title, body, footer string
	switch m.mode {
	case modeConfirmQuery:
		title = "Confirm database change"
		body = "This statement may modify data or database state:\n\n" + truncate(strings.TrimSpace(m.pendingSQL), 72)
		footer = "y run once  ! trust writes for this tab  n/Esc cancel"
	case modeConfirmDelete:
		title = "Delete connection?"
		body = "The saved profile and keyring password will be removed."
		footer = "y delete  n/Esc cancel"
	case modeConfirmClose:
		title = "Close query tab?"
		body = "The editor contents and current results are not saved."
		footer = "y close  n/Esc cancel"
	case modeConfirmQuit:
		title = "Quit charta?"
		body = "Open query editors are session-only and will be discarded."
		footer = "y quit  n/Esc cancel"
	case modeConfirmHistoryClear:
		title = "Clear query history?"
		body = "Every persisted query history entry will be removed. Favorites and snippets are not affected."
		footer = "y clear  n/Esc cancel"
	}
	content := m.styles.warning.Render(title) + "\n\n" + body + "\n\n" + m.styles.dim.Render(footer)
	return m.center(m.styles.modal.Width(76).Render(content))
}

func (m *Model) renderHelp() string {
	content := `Keyboard shortcuts

Workspace
  Tab / Shift+Tab       cycle focus
  Ctrl+B                toggle schema browser
  Ctrl+T                new query tab for selected connection
  Ctrl+W                close current query tab
  Ctrl+PgUp / PgDn      switch query tabs

Query
  Ctrl+Enter            run statement at cursor
  Ctrl+Shift+Enter      run complete script
  Ctrl+S                save current query as favorite
  Ctrl+Shift+F          format selection/current statement
  Type 2+ characters    show best completion as ghost text
  Ctrl+Space            open full SQL autocomplete list
  Ctrl+Shift+Space      start/clear a query selection
  Up/Down               navigate autocomplete list
  Enter                 accept autocomplete
  Space                 accept ghost text, otherwise insert a space
  Tab / Shift+Tab       expand snippet / navigate placeholders
  Esc                   dismiss autocomplete until SQL/cursor changes
  F8 / Shift+F8         next / previous diagnostic
  Ctrl+C / Ctrl+G       cancel active query

Results
  Arrows / WASD         move active cell
  PgUp / PgDn           move one result page
  Home / End            first / last column
  Ctrl+Home / Ctrl+End  first / last result
  Up from first row     select the column header
  Enter                 open full cell detail
  v                     open vertical row inspector
  c / Shift+C           copy cell / row
  r / f / g             rerun / find / go to row

Column header
  Left / Right          select a column
  Enter                 open column inspector
  Space                 freeze/unfreeze through column
  u                     clear frozen columns
  Down                  return to result rows

Inspectors
  Up/Down/PgUp/PgDn     navigate or scroll
  Left/Right            previous/next column (column inspector)
  s / r                 exact statistics / refresh
  c / Shift+C           copy identifier or row field
  Esc                   return to results

Connections
  Enter                 connect or expand selected item
  a / e / c / d         add, edit, copy, delete
  t / x / r             test, disconnect, refresh

Application
  Ctrl+H                query history
  Ctrl+Shift+P          favorites and snippets
  Ctrl+L                activity logs
  F1 / ?                help
  Ctrl+Q                quit

Press Esc or F1 to return.`
	return m.center(m.styles.modal.Width(66).Render(content))
}

func (m *Model) renderLogScreen() string {
	header := m.styles.header.Render("Activity logs") + "  " + m.styles.dim.Render("metadata only; SQL and secrets are never recorded")
	footer := m.styles.dim.Render("↑/↓/PgUp/PgDn scroll  Esc return")
	return m.styles.app.Render(lipgloss.JoinVertical(lipgloss.Left, header, m.logs.View(), footer))
}

func (m *Model) renderLogs(msg logsLoadedMsg) {
	if msg.err != nil {
		m.logs.SetContent(m.styles.error.Render(msg.err.Error()))
		return
	}
	lines := make([]string, 0, len(msg.entries))
	for _, entry := range msg.entries {
		line := fmt.Sprintf(
			"%s %-5s %-18s %-10s %s",
			entry.Time.Local().Format("2006-01-02 15:04:05"),
			entry.Level,
			truncate(entry.Connection, 18),
			entry.Engine,
			entry.Message,
		)
		if entry.Duration > 0 {
			line += " duration=" + formatDuration(entry.Duration)
		}
		if entry.Rows != 0 {
			line += fmt.Sprintf(" rows=%d", entry.Rows)
		}
		if entry.Error != "" {
			line += " error=" + entry.Error
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = append(lines, "No activity has been recorded yet.")
	}
	m.logs.SetContent(strings.Join(lines, "\n"))
	m.logs.GotoBottom()
}

func (m *Model) resize() {
	contentWidth := max(20, m.contentWidth()-4)
	contentHeight := max(8, m.height-5)
	for _, tab := range m.tabs {
		tab.editor.SetWidth(contentWidth)
		tab.editor.SetHeight(max(5, contentHeight/2-2))
		tab.table.SetSize(contentWidth, max(4, contentHeight-contentHeight/2-2))
	}
	m.logs.SetWidth(max(20, m.width-4))
	m.logs.SetHeight(max(5, m.height-5))
	modalWidth := min(100, max(30, m.width-10))
	m.cellDetail.SetWidth(max(20, modalWidth-6))
	m.cellDetail.SetHeight(max(3, m.height-10))
	if m.cellDetailText != "" {
		m.cellDetail.SetContent(ansi.Hardwrap(m.cellDetailText, max(20, modalWidth-6), true))
	}
	rowWidth := min(120, max(40, m.width-10))
	m.rowInspector.SetSize(max(20, rowWidth-6), max(3, m.height-10))
	m.resizeColumnInspector()
}

func (m *Model) contentWidth() int {
	width := m.width - 2
	if m.isBrowserOpen {
		width -= browserWidth
	}
	return max(20, width)
}

func (m *Model) center(content string) string {
	return lipgloss.Place(max(1, m.width), max(1, m.height), lipgloss.Center, lipgloss.Center, content)
}
