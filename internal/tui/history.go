package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/lucasfguimares/tui-db/internal/activity"
	"github.com/lucasfguimares/tui-db/internal/database"
	"github.com/lucasfguimares/tui-db/internal/profile"
	"github.com/lucasfguimares/tui-db/internal/queryhistory"
)

func (m *Model) openHistory() tea.Cmd {
	m.mode = modeHistory
	m.historyCursor = 0
	return m.loadHistoryCmd()
}

func (m *Model) loadHistoryCmd() tea.Cmd {
	if m.history == nil {
		return func() tea.Msg { return historyLoadedMsg{} }
	}
	filter := m.historyFilter
	return func() tea.Msg {
		entries, err := m.history.List(filter)
		return historyLoadedMsg{entries: entries, err: err}
	}
}

func (m *Model) applyHistory(msg historyLoadedMsg) {
	if msg.err != nil {
		m.historyEntries = nil
		m.historyError = activity.Redact(msg.err.Error())
		m.setError(msg.err)
		return
	}
	m.historyError = ""
	m.historyEntries = msg.entries
	if m.historyCursor >= len(msg.entries) {
		m.historyCursor = max(0, len(msg.entries)-1)
	}
}

func (m *Model) handleHistoryKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if m.mode == modeHistorySearch {
		if key == "esc" {
			m.historyInput.Blur()
			m.mode = modeHistory
			return nil
		}
		if key == "enter" {
			m.historyFilter.Search = strings.TrimSpace(m.historyInput.Value())
			m.historyInput.Blur()
			m.mode = modeHistory
			m.historyCursor = 0
			return m.loadHistoryCmd()
		}
		updated, cmd := m.historyInput.Update(msg)
		m.historyInput = updated
		return cmd
	}

	switch key {
	case "esc", "ctrl+h":
		m.mode = modeWorkspace
	case "up", "k":
		if m.historyCursor > 0 {
			m.historyCursor--
		}
	case "down", "j":
		if m.historyCursor < len(m.historyEntries)-1 {
			m.historyCursor++
		}
	case "home":
		m.historyCursor = 0
	case "end":
		m.historyCursor = max(0, len(m.historyEntries)-1)
	case "/":
		m.historyInput.SetValue(m.historyFilter.Search)
		m.historyInput.Focus()
		m.mode = modeHistorySearch
		return textinput.Blink
	case "s":
		m.cycleHistoryStatus()
		return m.loadHistoryCmd()
	case "c":
		m.cycleHistoryConnection()
		return m.loadHistoryCmd()
	case "p":
		m.cycleHistoryPeriod()
		return m.loadHistoryCmd()
	case "delete", "backspace":
		return m.deleteSelectedHistoryCmd()
	case "ctrl+delete":
		m.mode = modeConfirmHistoryClear
	case "enter":
		return m.openSelectedHistory(false)
	case "ctrl+enter":
		return m.openSelectedHistory(true)
	}
	return nil
}

func (m *Model) deleteSelectedHistoryCmd() tea.Cmd {
	if m.history == nil || m.historyCursor < 0 || m.historyCursor >= len(m.historyEntries) {
		return nil
	}
	id := m.historyEntries[m.historyCursor].ID
	return func() tea.Msg {
		_, err := m.history.Delete(id)
		return historyChangedMsg{err: err}
	}
}

func (m *Model) clearHistoryCmd() tea.Cmd {
	if m.history == nil {
		return nil
	}
	return func() tea.Msg { return historyChangedMsg{err: m.history.Clear()} }
}

func (m *Model) openSelectedHistory(run bool) tea.Cmd {
	if m.historyCursor < 0 || m.historyCursor >= len(m.historyEntries) {
		return nil
	}
	entry := m.historyEntries[m.historyCursor]
	connection, ok := m.profileByID(entry.ConnectionID)
	if !ok {
		m.setError(fmt.Errorf("connection %q is no longer available", entry.ConnectionName))
		return nil
	}
	m.ensureTab(connection)
	tab := m.currentTab()
	setTabSQL(tab, entry.SQL)
	tab.statusText = "Loaded from query history"
	m.mode = modeWorkspace
	if run {
		return m.requestRunScript()
	}
	return nil
}

func (m *Model) cycleHistoryStatus() {
	values := []queryhistory.Status{queryhistory.StatusUnknown, queryhistory.StatusSuccess, queryhistory.StatusError, queryhistory.StatusCancelled}
	index := 0
	for i, value := range values {
		if value == m.historyFilter.Status {
			index = i
			break
		}
	}
	m.historyFilter.Status = values[(index+1)%len(values)]
	m.historyCursor = 0
}

func (m *Model) cycleHistoryConnection() {
	ids := []string{""}
	for _, connection := range m.profiles {
		ids = append(ids, connection.ID)
	}
	index := 0
	for i, id := range ids {
		if id == m.historyFilter.ConnectionID {
			index = i
			break
		}
	}
	m.historyFilter.ConnectionID = ids[(index+1)%len(ids)]
	m.historyCursor = 0
}

func (m *Model) cycleHistoryPeriod() {
	m.historyPeriod = (m.historyPeriod + 1) % 4
	now := time.Now()
	switch m.historyPeriod {
	case 1:
		m.historyFilter.From = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	case 2:
		m.historyFilter.From = now.AddDate(0, 0, -7)
	case 3:
		m.historyFilter.From = now.AddDate(0, 0, -30)
	default:
		m.historyFilter.From = time.Time{}
	}
	m.historyFilter.To = time.Time{}
	m.historyCursor = 0
}

func (m *Model) renderHistoryScreen() string {
	status := "all"
	if m.historyFilter.Status != queryhistory.StatusUnknown {
		status = string(m.historyFilter.Status)
	}
	connection := "all"
	if value, ok := m.profileByID(m.historyFilter.ConnectionID); ok {
		connection = value.Name
	}
	periods := []string{"all", "today", "7 days", "30 days"}
	header := m.styles.header.Render("Query history") + "  " + m.styles.dim.Render(fmt.Sprintf(
		"search=%q  connection=%s  status=%s  period=%s", m.historyFilter.Search, connection, status, periods[m.historyPeriod],
	))
	if m.historyError != "" {
		header += "\n" + m.styles.error.Render(m.historyError)
	}
	available := max(1, m.height-5)
	linesPerEntry := 3
	visible := max(1, available/linesPerEntry)
	start := 0
	if m.historyCursor >= visible {
		start = m.historyCursor - visible + 1
	}
	end := min(len(m.historyEntries), start+visible)
	lines := make([]string, 0, visible*linesPerEntry)
	for i := start; i < end; i++ {
		entry := m.historyEntries[i]
		marker := "✓"
		rows := fmt.Sprintf("%d rows", entry.Rows)
		if entry.Status == queryhistory.StatusError {
			marker, rows = "✕", "ERROR"
		}
		if entry.Status == queryhistory.StatusCancelled {
			marker, rows = "■", "CANCELLED"
		}
		database := entry.ConnectionName
		if entry.Database != "" {
			database += "/" + filepath.Base(entry.Database)
		}
		meta := fmt.Sprintf("%s  %-22s %8s %12s  %s", entry.ExecutedAt.Local().Format("2006-01-02 15:04:05"), truncate(database, 22), formatDuration(entry.Duration), rows, marker)
		sql := truncate(strings.ReplaceAll(strings.TrimSpace(entry.SQL), "\n", " "), max(20, m.width-8))
		if i == m.historyCursor {
			meta = m.styles.selected.Width(max(20, m.width-4)).Render(meta)
			sql = m.styles.accent.Render(sql)
		}
		lines = append(lines, meta, "  "+sql)
		if entry.Error != "" {
			lines = append(lines, "  "+m.styles.error.Render(truncate(entry.Error, max(20, m.width-8))))
		} else {
			lines = append(lines, "")
		}
	}
	if len(lines) == 0 {
		lines = append(lines, m.styles.dim.Render("No queries match the current filters."))
	}
	footer := m.styles.dim.Render("Enter open  Ctrl+Enter rerun  / search  c connection  s status  p period  Delete remove  Ctrl+Delete clear  Esc return")
	if m.mode == modeHistorySearch {
		footer = m.historyInput.View() + "  " + m.styles.dim.Render("Enter apply  Esc cancel")
	}
	return m.styles.app.Render(lipgloss.JoinVertical(lipgloss.Left, header, strings.Join(lines, "\n"), footer))
}

func historyDatabase(connection profile.Connection) string {
	if connection.Database != "" {
		return connection.Database
	}
	if connection.SQLitePath != "" {
		return filepath.Base(connection.SQLitePath)
	}
	return ""
}

func statusForError(isCancelled bool) queryhistory.Status {
	if isCancelled {
		return queryhistory.StatusCancelled
	}
	return queryhistory.StatusError
}

func (m *Model) recordHistoryCmd(tab *queryTab, result database.Result, status queryhistory.Status, errorMessage string) tea.Cmd {
	if m.history == nil {
		return nil
	}
	rows := int64(len(result.Rows))
	if len(result.Columns) == 0 {
		rows = result.AffectedRows
	}
	entry := queryhistory.Entry{
		SQL: tab.lastSQL, ConnectionID: tab.connection.ID, ConnectionName: tab.connection.Name,
		Database: historyDatabase(tab.connection), ExecutedAt: tab.startedAt, Duration: result.Duration,
		Rows: rows, Status: status, Error: errorMessage,
	}
	tabID := tab.id
	return func() tea.Msg { return historyRecordedMsg{tabID: tabID, err: m.history.Add(entry)} }
}
