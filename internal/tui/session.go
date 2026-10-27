package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/lucasfguimares/tui-db/internal/activity"
	"github.com/lucasfguimares/tui-db/internal/profile"
	"github.com/lucasfguimares/tui-db/internal/sqleditor"
	"github.com/lucasfguimares/tui-db/internal/workspace"
)

const (
	sessionSaveDelay      = 2 * time.Second
	missingConnectionName = "no connection"
)

type sessionLoadedMsg struct {
	result  workspace.LoadResult
	isOwner bool
	err     error
}

type sessionSaveDueMsg struct {
	tabID   int
	version uint64
}

type sessionSavedMsg struct {
	report workspace.SaveReport
	err    error
}

// isSessionPersisted reports whether tabs survive a restart.
func (m *Model) isSessionPersisted() bool {
	return m.workspace != nil && m.workspace.IsOwner() && m.isSessionReady
}

func (m *Model) loadSessionCmd() tea.Cmd {
	store := m.workspace
	return func() tea.Msg {
		result, err := store.Load()
		return sessionLoadedMsg{result: result, isOwner: store.IsOwner(), err: err}
	}
}

func (m *Model) handleSessionLoaded(msg sessionLoadedMsg) {
	if msg.err != nil {
		m.setError(fmt.Errorf("restoring session: %w", msg.err))
		return
	}
	m.applySession(msg.result.Snapshot)
	m.isSessionReady = true
	m.sessionStructure = m.sessionSignature()
	m.markSessionVersions()
	switch {
	case !msg.isOwner:
		m.sessionNotice = "Session not saved: open in another tui-db instance"
		m.statusText = "Session is open in another tui-db instance — tabs here are not saved"
		m.isStatusError = true
	case msg.result.RecoveredPath != "":
		m.statusText = "Session file was unreadable; preserved as " + msg.result.RecoveredPath
		m.isStatusError = true
	case len(msg.result.Snapshot.Tabs) > 0:
		m.statusText = fmt.Sprintf("Restored %d tab(s)", len(m.tabs))
		m.isStatusError = false
	}
}

// applySession recreates tabs from a snapshot without connecting. Trust and
// results always start from their zero values.
func (m *Model) applySession(snapshot workspace.Snapshot) {
	for _, saved := range snapshot.Tabs {
		connection, ok := m.profileByID(saved.ConnectionID)
		if !ok {
			connection = profile.Connection{Name: missingConnectionName}
		}
		m.addTab(connection, saved.SQL)
		tab := m.tabs[len(m.tabs)-1]
		if !ok {
			tab.isDetached = true
			tab.missingConnectionID = saved.ConnectionID
			tab.statusText = "Profile not found — select a connection and press Ctrl+R"
		}
		text := tab.editor.Value()
		setEditorCursor(&tab.editor, sqleditor.OffsetAt(text, saved.Cursor.Line, saved.Cursor.Column))
		if saved.Selection != nil {
			anchor := sqleditor.OffsetAt(text, saved.Selection.Line, saved.Selection.Column)
			tab.selection = &anchor
		}
	}
	if len(m.tabs) == 0 {
		return
	}
	m.activeTab = min(max(snapshot.ActiveTab, 0), len(m.tabs)-1)
	m.setFocus(focusEditor)
}

func (m *Model) snapshot() workspace.Snapshot {
	tabs := make([]workspace.Tab, 0, len(m.tabs))
	for _, tab := range m.tabs {
		text := tab.editor.Value()
		saved := workspace.Tab{ConnectionID: tab.connection.ID, SQL: text}
		if tab.isDetached {
			saved.ConnectionID = tab.missingConnectionID
		}
		if offset, ok := cursorOffset(tab.editor); ok {
			saved.Cursor = workspacePosition(text, offset)
		}
		if tab.selection != nil {
			anchor := workspacePosition(text, *tab.selection)
			saved.Selection = &anchor
		}
		tabs = append(tabs, saved)
	}
	return workspace.Snapshot{Tabs: tabs, ActiveTab: max(m.activeTab, 0)}
}

func workspacePosition(text string, offset int) workspace.Position {
	position := sqleditor.PositionAt(text, offset)
	return workspace.Position{Line: position.Line, Column: position.Column}
}

// saveSessionCmd captures the snapshot on the update loop and writes it off-loop.
func (m *Model) saveSessionCmd() tea.Cmd {
	if !m.isSessionPersisted() {
		return nil
	}
	m.sessionSeq++
	store, seq, snapshot := m.workspace, m.sessionSeq, m.snapshot()
	return func() tea.Msg {
		report, err := store.Save(seq, snapshot)
		return sessionSavedMsg{report: report, err: err}
	}
}

// saveSessionNow is the final, blocking save performed right before quitting.
func (m *Model) saveSessionNow() {
	if !m.isSessionPersisted() {
		return
	}
	m.sessionSeq++
	_, _ = m.workspace.Save(m.sessionSeq, m.snapshot())
}

func (m *Model) handleSessionSaved(msg sessionSavedMsg) {
	switch {
	case errors.Is(msg.err, workspace.ErrNotOwner):
	case msg.err != nil:
		m.sessionNotice = "Saving session failed: " + activity.Redact(msg.err.Error())
		m.isStatusError = true
	case msg.report.DroppedTabs > 0 || msg.report.TruncatedTabs > 0:
		m.sessionNotice = fmt.Sprintf(
			"Session limits: %d tab(s) beyond %d not saved, %d truncated",
			msg.report.DroppedTabs, workspace.MaxTabs, msg.report.TruncatedTabs,
		)
		m.isStatusError = true
	default:
		m.sessionNotice = ""
	}
}

func scheduleSessionSave(tabID int, version uint64) tea.Cmd {
	return tea.Tick(sessionSaveDelay, func(time.Time) tea.Msg { return sessionSaveDueMsg{tabID: tabID, version: version} })
}

func (m *Model) handleSessionSaveDue(msg sessionSaveDueMsg) tea.Cmd {
	tab := m.tabByID(msg.tabID)
	if tab == nil || tab.document == nil || tab.document.Version() != msg.version {
		return nil
	}
	return m.saveSessionCmd()
}

// sessionChangesCmd runs after every update: structural tab changes save
// immediately and editor text changes schedule a versioned, debounced save.
func (m *Model) sessionChangesCmd() tea.Cmd {
	if !m.isSessionPersisted() {
		return nil
	}
	var cmds []tea.Cmd
	for _, tab := range m.tabs {
		if tab.document == nil {
			continue
		}
		if version := tab.document.Version(); m.sessionVersions[tab.id] != version {
			m.sessionVersions[tab.id] = version
			cmds = append(cmds, scheduleSessionSave(tab.id, version))
		}
	}
	if signature := m.sessionSignature(); signature != m.sessionStructure {
		m.sessionStructure = signature
		cmds = append(cmds, m.saveSessionCmd())
	}
	return tea.Batch(cmds...)
}

func (m *Model) markSessionVersions() {
	m.sessionVersions = map[int]uint64{}
	for _, tab := range m.tabs {
		if tab.document != nil {
			m.sessionVersions[tab.id] = tab.document.Version()
		}
	}
}

// sessionSignature identifies tab order, connection association, and the active tab.
func (m *Model) sessionSignature() string {
	var builder strings.Builder
	builder.WriteString(strconv.Itoa(m.activeTab))
	for _, tab := range m.tabs {
		builder.WriteByte('|')
		builder.WriteString(strconv.Itoa(tab.id))
		builder.WriteByte(':')
		builder.WriteString(tab.connection.ID)
	}
	return builder.String()
}

// reassociateTab binds an orphaned tab to the selected profile without connecting.
func (m *Model) reassociateTab() tea.Cmd {
	tab := m.currentTab()
	if tab == nil || !tab.isDetached {
		m.setError(errors.New("only tabs without a connection can be reassociated"))
		return nil
	}
	connection, ok := m.selectedProfile()
	if !ok {
		m.setError(errors.New("select a connection in the browser first"))
		return nil
	}
	tab.connection = connection
	tab.isDetached, tab.missingConnectionID = false, ""
	tab.table = newResultTable(connection.EffectiveMaxColumnWidth())
	tab.statusText = "Ready"
	m.resize()
	m.statusText = fmt.Sprintf("Tab associated with %s", connection.Name)
	m.isStatusError = false
	if tab.document == nil {
		return nil
	}
	return scheduleSQLAnalysis(tab.id, tab.document.Version())
}

func (m *Model) hasRunningQueries() bool {
	for _, tab := range m.tabs {
		if tab.isRunning {
			return true
		}
	}
	return false
}

func (m *Model) requestQuit() tea.Cmd {
	if m.hasRunningQueries() || !m.isSessionPersisted() && m.hasDirtyTabs() {
		m.mode = modeConfirmQuit
		return nil
	}
	m.saveSessionNow()
	return tea.Quit
}
