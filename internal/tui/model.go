// Package tui implements the interactive database workbench.
package tui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/lucasfguimares/tui-db/internal/activity"
	"github.com/lucasfguimares/tui-db/internal/database"
	"github.com/lucasfguimares/tui-db/internal/profile"
	"github.com/lucasfguimares/tui-db/internal/sqleditor"
	"github.com/lucasfguimares/tui-db/internal/sqlscan"
)

const (
	minimumWidth  = 80
	minimumHeight = 24
	browserWidth  = 30
)

type focus int

const (
	focusBrowser focus = iota
	focusEditor
	focusResults
)

type mode int

const (
	modeWorkspace mode = iota
	modeLogs
	modeHelp
	modeProfile
	modePassword
	modeConfirmQuery
	modeConfirmDelete
	modeConfirmClose
	modeConfirmQuit
	modeCellDetail
	modeResultSearch
	modeGotoRow
)

type profileRepository interface {
	List() ([]profile.Connection, error)
	Save(profile.Connection) error
	Delete(string) (bool, error)
}

type queryTab struct {
	id          int
	connection  profile.Connection
	editor      textarea.Model
	table       *resultTableModel
	result      database.Result
	isRunning   bool
	isTrusted   bool
	requestID   int
	cancel      context.CancelFunc
	statusText  string
	selection   *int
	startedAt   time.Time
	lastSQL     string
	lastSQLBase int
	document    *sqleditor.Document
	analysis    sqleditor.Analysis
	diagnostic  int
	format      sqleditor.FormatOptions
	completion  autocompleteState
}

type passwordAction int

const (
	passwordConnect passwordAction = iota
	passwordTest
)

type catalogState struct {
	isExpanded bool
	schemas    []string
	expanded   map[string]bool
	relations  map[string][]database.Relation
	columns    map[string][]database.Column
}

type browserKind int

const (
	browserProfile browserKind = iota
	browserSchema
	browserRelation
	browserColumn
)

type browserItem struct {
	kind       browserKind
	level      int
	label      string
	profileID  string
	schema     string
	relation   string
	isExpanded bool
}

type profilesLoadedMsg struct {
	profiles []profile.Connection
	err      error
}

type profileSavedMsg struct {
	connection profile.Connection
	err        error
}

type profileDeletedMsg struct {
	profileID string
	err       error
}

type passwordNeededMsg struct {
	connection profile.Connection
	action     passwordAction
}

type connectionTestedMsg struct {
	connection profile.Connection
	err        error
}

type connectionDisconnectedMsg struct {
	connection profile.Connection
	err        error
}

type schemasLoadedMsg struct {
	profileID string
	schemas   []string
	err       error
}

type relationsLoadedMsg struct {
	profileID string
	schema    string
	relations []database.Relation
	err       error
}

type columnsLoadedMsg struct {
	profileID string
	schema    string
	relation  string
	columns   []database.Column
	err       error
}

type queryFinishedMsg struct {
	tabID          int
	requestID      int
	result         database.Result
	columnWidths   []int
	layoutDuration time.Duration
	err            error
}

type queryTickMsg struct {
	tabID     int
	requestID int
}

type sqlAnalysisDueMsg struct {
	tabID   int
	version uint64
}

type sqlAnalysisMsg struct {
	tabID  int
	result sqleditor.Analysis
}

type autocompleteCatalogMsg struct {
	profileID string
	requestID uint64
	catalog   sqleditor.Catalog
	err       error
}

type autocompleteResultMsg struct {
	tabID     int
	requestID uint64
	result    sqleditor.CompletionResult
}

type logsLoadedMsg struct {
	entries []activity.Entry
	err     error
}

// Model owns the entire interactive application state.
type Model struct {
	store     profileRepository
	secrets   profile.SecretStore
	manager   *database.Manager
	inspector *database.Inspector
	runner    *database.Runner
	activity  *activity.Log

	profiles      []profile.Connection
	catalog       map[string]*catalogState
	browserItems  []browserItem
	browserCursor int
	tabs          []*queryTab
	activeTab     int
	nextTabID     int
	focus         focus
	mode          mode
	isBrowserOpen bool
	width         int
	height        int
	statusText    string
	isStatusError bool
	styles        styles

	form                  profileForm
	passwordInput         textinput.Model
	resultInput           textinput.Model
	pendingConnection     profile.Connection
	pendingPasswordAct    passwordAction
	pendingSQL            string
	pendingRunScript      bool
	pendingDeleteID       string
	logs                  viewport.Model
	cellDetail            viewport.Model
	cellDetailText        string
	autocompleteCache     map[string]*autocompleteCatalogState
	autocompleteRequestID uint64
}

// New creates the root Bubble Tea model with explicitly wired services.
func New(
	store profileRepository,
	secrets profile.SecretStore,
	manager *database.Manager,
	inspector *database.Inspector,
	runner *database.Runner,
	activityLog *activity.Log,
) *Model {
	passwordInput := textinput.New()
	passwordInput.Prompt = "Password: "
	passwordInput.EchoMode = textinput.EchoPassword
	passwordInput.SetWidth(42)
	resultInput := textinput.New()
	resultInput.SetWidth(42)

	model := &Model{
		store:             store,
		secrets:           secrets,
		manager:           manager,
		inspector:         inspector,
		runner:            runner,
		activity:          activityLog,
		profiles:          []profile.Connection{},
		catalog:           map[string]*catalogState{},
		browserItems:      []browserItem{},
		tabs:              []*queryTab{},
		activeTab:         -1,
		nextTabID:         1,
		focus:             focusBrowser,
		mode:              modeWorkspace,
		isBrowserOpen:     true,
		statusText:        "Loading connections…",
		styles:            defaultStyles(),
		passwordInput:     passwordInput,
		resultInput:       resultInput,
		logs:              viewport.New(),
		cellDetail:        viewport.New(),
		autocompleteCache: map[string]*autocompleteCatalogState{},
	}
	return model
}

// Init loads saved profiles without blocking the initial render.
func (m *Model) Init() tea.Cmd {
	return m.loadProfilesCmd()
}

// Update applies input and asynchronous service results.
func (m *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil
	case profilesLoadedMsg:
		if msg.err != nil {
			m.setError(msg.err)
			return m, nil
		}
		m.profiles = msg.profiles
		m.syncTabConnections()
		m.rebuildBrowser()
		if len(m.profiles) == 0 {
			m.statusText = "No connections yet — press a to add one"
		} else {
			m.statusText = fmt.Sprintf("%d connection(s) loaded", len(m.profiles))
		}
		return m, nil
	case profileSavedMsg:
		if msg.err != nil {
			m.mode = modeProfile
			m.form.errorText = msg.err.Error()
			return m, nil
		}
		m.mode = modeWorkspace
		delete(m.catalog, msg.connection.ID)
		delete(m.autocompleteCache, msg.connection.ID)
		m.statusText = fmt.Sprintf("Saved %s", msg.connection.Name)
		m.isStatusError = false
		return m, m.loadProfilesCmd()
	case profileDeletedMsg:
		delete(m.catalog, msg.profileID)
		delete(m.autocompleteCache, msg.profileID)
		if msg.err != nil {
			m.setError(msg.err)
			return m, m.loadProfilesCmd()
		}
		m.statusText = "Connection deleted"
		return m, m.loadProfilesCmd()
	case passwordNeededMsg:
		m.pendingConnection = msg.connection
		m.pendingPasswordAct = msg.action
		m.passwordInput.SetValue("")
		m.passwordInput.Focus()
		m.mode = modePassword
		return m, nil
	case schemasLoadedMsg:
		return m, m.handleSchemas(msg)
	case connectionTestedMsg:
		if msg.err != nil {
			m.setError(msg.err)
			return m, nil
		}
		m.statusText = fmt.Sprintf("Connection to %s succeeded", msg.connection.Name)
		m.isStatusError = false
		return m, nil
	case connectionDisconnectedMsg:
		if msg.err != nil {
			m.setError(msg.err)
			return m, nil
		}
		delete(m.catalog, msg.connection.ID)
		delete(m.autocompleteCache, msg.connection.ID)
		m.rebuildBrowser()
		m.statusText = fmt.Sprintf("Disconnected from %s", msg.connection.Name)
		m.isStatusError = false
		return m, nil
	case relationsLoadedMsg:
		return m, m.handleRelations(msg)
	case columnsLoadedMsg:
		return m, m.handleColumns(msg)
	case queryFinishedMsg:
		m.handleQueryFinished(msg)
		return m, nil
	case queryTickMsg:
		tab := m.tabByID(msg.tabID)
		if tab == nil || !tab.isRunning || tab.requestID != msg.requestID {
			return m, nil
		}
		return m, queryTickCmd(msg.tabID, msg.requestID)
	case sqlAnalysisDueMsg:
		tab := m.tabByID(msg.tabID)
		if tab == nil || tab.document == nil || tab.document.Version() != msg.version {
			return m, nil
		}
		text, dialect := tab.document.Text(), dialectFor(tab.connection.Driver)
		return m, func() tea.Msg {
			return sqlAnalysisMsg{tabID: msg.tabID, result: sqleditor.Analyze(text, dialect, msg.version)}
		}
	case sqlAnalysisMsg:
		tab := m.tabByID(msg.tabID)
		if tab == nil || tab.document == nil || !tab.document.Apply(msg.result) {
			return m, nil
		}
		tab.analysis = msg.result
		if len(tab.analysis.Diagnostics) == 0 {
			tab.diagnostic = 0
		} else if tab.diagnostic >= len(tab.analysis.Diagnostics) {
			tab.diagnostic = len(tab.analysis.Diagnostics) - 1
		}
		return m, nil
	case autocompleteCatalogMsg:
		return m, m.handleAutocompleteCatalog(msg)
	case autocompleteResultMsg:
		m.handleAutocompleteResult(msg)
		return m, nil
	case logsLoadedMsg:
		m.renderLogs(msg)
		return m, nil
	case tea.KeyPressMsg:
		return m, m.handleKey(msg)
	}

	return m, m.updateFocused(message)
}

// View renders the responsive workspace or current modal screen.
func (m *Model) View() tea.View {
	content := ""
	if m.width > 0 && (m.width < minimumWidth || m.height < minimumHeight) {
		content = m.styles.modal.Render(fmt.Sprintf(
			"tui-db needs at least %dx%d\ncurrent terminal: %dx%d",
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
		case modeResultSearch, modeGotoRow:
			content = m.renderResultPrompt()
		case modeConfirmQuery, modeConfirmDelete, modeConfirmClose, modeConfirmQuit:
			content = m.renderConfirmation()
		default:
			content = m.renderWorkspace()
		}
	}
	view := tea.NewView(content)
	view.AltScreen = true
	view.WindowTitle = "tui-db"
	return view
}

func (m *Model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if m.mode != modeWorkspace {
		return m.handleModalKey(msg)
	}
	if m.focus == focusEditor {
		if cmd, handled := m.handleAutocompleteKey(msg); handled {
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
		if _, ok := tab.table.SelectedCell(); ok {
			m.openCellDetail(tab)
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
			if tab.document == nil {
				tab.document = sqleditor.NewDocument(before)
			}
			version := tab.document.SetText(after)
			if tab.completion.isOpen {
				return tea.Batch(cmd, scheduleSQLAnalysis(tab.id, version), m.refreshAutocompleteCmd(tab))
			}
			return tea.Batch(cmd, scheduleSQLAnalysis(tab.id, version))
		}
		afterCursor, _ := cursorOffset(tab.editor)
		if tab.completion.isOpen && beforeCursor != afterCursor {
			return tea.Batch(cmd, m.refreshAutocompleteCmd(tab))
		}
		return cmd
	case focusResults:
		return nil
	default:
		return nil
	}
}

func (m *Model) openBrowserItem() tea.Cmd {
	if m.browserCursor < 0 || m.browserCursor >= len(m.browserItems) {
		return nil
	}
	item := m.browserItems[m.browserCursor]
	connection, ok := m.profileByID(item.profileID)
	if !ok {
		return nil
	}
	state := m.ensureCatalog(item.profileID)
	switch item.kind {
	case browserProfile:
		m.ensureTab(connection)
		if state.isExpanded {
			state.isExpanded = false
			m.rebuildBrowser()
			return nil
		}
		if len(state.schemas) == 0 {
			return m.prepareConnectionCmd(connection)
		}
		state.isExpanded = true
		m.rebuildBrowser()
	case browserSchema:
		if state.expanded[item.schema] {
			state.expanded[item.schema] = false
			m.rebuildBrowser()
			return nil
		}
		if _, loaded := state.relations[item.schema]; !loaded {
			return m.loadRelationsCmd(connection, item.schema)
		}
		state.expanded[item.schema] = true
		m.rebuildBrowser()
	case browserRelation:
		key := catalogKey(item.schema, item.relation)
		if state.expanded[key] {
			state.expanded[key] = false
			m.rebuildBrowser()
			return nil
		}
		if _, loaded := state.columns[key]; !loaded {
			return m.loadColumnsCmd(connection, item.schema, item.relation)
		}
		state.expanded[key] = true
		m.rebuildBrowser()
	}
	return nil
}

func (m *Model) collapseBrowserItem() {
	if m.browserCursor < 0 || m.browserCursor >= len(m.browserItems) {
		return
	}
	item := m.browserItems[m.browserCursor]
	state := m.ensureCatalog(item.profileID)
	switch item.kind {
	case browserProfile:
		state.isExpanded = false
	case browserSchema:
		state.expanded[item.schema] = false
	case browserRelation:
		state.expanded[catalogKey(item.schema, item.relation)] = false
	}
	m.rebuildBrowser()
}

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
	ctx, cancel := context.WithCancel(context.Background())
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

func (m *Model) handleQueryFinished(msg queryFinishedMsg) {
	tab := m.tabByID(msg.tabID)
	if tab == nil || tab.requestID != msg.requestID {
		return
	}
	tab.isRunning = false
	if tab.cancel != nil {
		tab.cancel()
		tab.cancel = nil
	}
	tab.result = msg.result
	if msg.err != nil {
		details := database.DescribeError(msg.err, msg.result.Duration)
		details.Message = activity.Redact(details.Message)
		if !details.Cancelled && !details.TimedOut {
			diagnostic := sqleditor.DatabaseDiagnostic(tab.lastSQL, sqleditor.DatabaseError{
				Message: details.Message, Code: details.Code, Line: details.Line,
				Column: details.Column, Position: details.Position,
			})
			base := tab.lastSQLBase
			if base < 0 || base+len(tab.lastSQL) > len(tab.editor.Value()) || tab.editor.Value()[base:base+len(tab.lastSQL)] != tab.lastSQL {
				base = strings.Index(tab.editor.Value(), tab.lastSQL)
			}
			if base >= 0 {
				diagnostic.Range.Start = sqleditor.PositionAt(tab.editor.Value(), base+diagnostic.Range.Start.Offset)
				diagnostic.Range.End = sqleditor.PositionAt(tab.editor.Value(), base+diagnostic.Range.End.Offset)
			}
			tab.analysis.Diagnostics = append(tab.analysis.Diagnostics, diagnostic)
			tab.diagnostic = len(tab.analysis.Diagnostics) - 1
			setEditorCursor(&tab.editor, diagnostic.Range.Start.Offset)
		}
		tab.table.SetErrorWithLayout(
			msg.result,
			details,
			msg.columnWidths,
			msg.layoutDuration,
		)
		if details.Cancelled {
			tab.statusText = fmt.Sprintf("Query cancelled after %s", formatDuration(msg.result.Duration))
			m.isStatusError = false
		} else {
			tab.statusText = "Query failed"
			m.isStatusError = true
		}
		m.activity.Record(context.Background(), activity.Event{
			Level: slog.LevelError, Message: "query failed", Connection: tab.connection.Name,
			Engine: string(tab.connection.Driver), Duration: msg.result.Duration, Error: msg.err,
		})
		return
	}
	rows := int64(len(msg.result.Rows))
	if len(msg.result.Columns) == 0 {
		rows = msg.result.AffectedRows
	}
	tab.statusText = fmt.Sprintf("Completed in %s — %d row(s)", formatDuration(msg.result.Duration), rows)
	if msg.result.IsTruncated {
		tab.statusText += " (truncated)"
	}
	tab.table.SetResultWithLayout(msg.result, msg.columnWidths, msg.layoutDuration)
	m.isStatusError = false
	if m.focus == focusResults {
		tab.table.Focus()
	}
	m.activity.Record(context.Background(), activity.Event{
		Level: slog.LevelInfo, Message: "query completed", Connection: tab.connection.Name,
		Engine: string(tab.connection.Driver), Duration: msg.result.Duration, Rows: rows,
	})
}

func queryTickCmd(tabID, requestID int) tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg {
		return queryTickMsg{tabID: tabID, requestID: requestID}
	})
}

func (m *Model) handleSchemas(msg schemasLoadedMsg) tea.Cmd {
	if msg.err != nil {
		if errors.Is(msg.err, profile.ErrSecretNotFound) {
			if connection, ok := m.profileByID(msg.profileID); ok {
				m.mode = modePassword
				m.pendingConnection = connection
				m.pendingPasswordAct = passwordConnect
				m.passwordInput.SetValue("")
				return m.passwordInput.Focus()
			}
		}
		m.setError(msg.err)
		return nil
	}
	state := m.ensureCatalog(msg.profileID)
	state.schemas = msg.schemas
	state.isExpanded = true
	m.rebuildBrowser()
	if connection, ok := m.profileByID(msg.profileID); ok {
		defaultSchema, schemaFound := findSchema(msg.schemas, connection.Driver, connection.DefaultSchema)
		if connection.DefaultSchema != "" && !schemaFound {
			m.setError(fmt.Errorf("default schema %q was not found in %s", connection.DefaultSchema, connection.Name))
			return nil
		}
		m.ensureTab(connection)
		m.statusText = fmt.Sprintf("Connected to %s", connection.Name)
		if connection.DefaultSchema != "" {
			m.statusText += fmt.Sprintf(" (schema %s)", connection.DefaultSchema)
		}
		m.activity.Record(context.Background(), activity.Event{
			Level: slog.LevelInfo, Message: "connection opened", Connection: connection.Name,
			Engine: string(connection.Driver),
		})
		if connection.DefaultSchema != "" {
			if _, loaded := state.relations[defaultSchema]; !loaded {
				return m.loadRelationsCmd(connection, defaultSchema)
			}
			state.expanded[defaultSchema] = true
			m.rebuildBrowser()
		}
	}
	return nil
}

func (m *Model) handleRelations(msg relationsLoadedMsg) tea.Cmd {
	if msg.err != nil {
		m.setError(msg.err)
		return nil
	}
	state := m.ensureCatalog(msg.profileID)
	state.relations[msg.schema] = msg.relations
	state.expanded[msg.schema] = true
	m.rebuildBrowser()
	return nil
}

func (m *Model) handleColumns(msg columnsLoadedMsg) tea.Cmd {
	if msg.err != nil {
		m.setError(msg.err)
		return nil
	}
	state := m.ensureCatalog(msg.profileID)
	key := catalogKey(msg.schema, msg.relation)
	state.columns[key] = msg.columns
	state.expanded[key] = true
	m.rebuildBrowser()
	return nil
}

func (m *Model) rebuildBrowser() {
	items := []browserItem{}
	for _, connection := range m.profiles {
		state := m.ensureCatalog(connection.ID)
		items = append(items, browserItem{
			kind: browserProfile, label: connection.Name, profileID: connection.ID, isExpanded: state.isExpanded,
		})
		if !state.isExpanded {
			continue
		}
		for _, schema := range state.schemas {
			label := schema
			if containsSchema([]string{schema}, connection.Driver, connection.DefaultSchema) {
				label += "  (default)"
			}
			items = append(items, browserItem{
				kind: browserSchema, level: 1, label: label, profileID: connection.ID,
				schema: schema, isExpanded: state.expanded[schema],
			})
			if !state.expanded[schema] {
				continue
			}
			for _, relation := range state.relations[schema] {
				key := catalogKey(schema, relation.Name)
				items = append(items, browserItem{
					kind: browserRelation, level: 2, label: relation.Name, profileID: connection.ID,
					schema: schema, relation: relation.Name, isExpanded: state.expanded[key],
				})
				if !state.expanded[key] {
					continue
				}
				for _, column := range state.columns[key] {
					label := column.Name + "  " + column.Type
					if column.Key != "" {
						label += " " + column.Key
					}
					items = append(items, browserItem{
						kind: browserColumn, level: 3, label: label, profileID: connection.ID,
						schema: schema, relation: relation.Name,
					})
				}
			}
		}
	}
	m.browserItems = items
	if m.browserCursor >= len(items) {
		m.browserCursor = max(0, len(items)-1)
	}
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
		return m.styles.header.Render("tui-db  SQL workbench")
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
		keys = "Arrows/WASD move  Enter detail  c/C copy  f find  ? help"
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
		title = "Quit tui-db?"
		body = "Open query editors are session-only and will be discarded."
		footer = "y quit  n/Esc cancel"
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
  Ctrl+Shift+F          format selection/current statement
  Ctrl+Space            open SQL autocomplete
  Ctrl+Shift+Space      start/clear a query selection
  Up/Down, Enter/Tab    navigate and accept autocomplete
  Esc                   close autocomplete
  F8 / Shift+F8         next / previous diagnostic
  Ctrl+C / Ctrl+G       cancel active query

Results
  Arrows / WASD         move active cell
  PgUp / PgDn           move one result page
  Home / End            first / last column
  Ctrl+Home / Ctrl+End  first / last result
  Enter                 open full cell detail
  c / Shift+C           copy cell / row
  r / f / g             rerun / find / go to row

Connections
  Enter                 connect or expand selected item
  a / e / c / d         add, edit, copy, delete
  t / x / r             test, disconnect, refresh

Application
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

func (m *Model) newTabForSelection() tea.Cmd {
	connection, ok := m.selectedProfile()
	if !ok {
		m.setError(errors.New("select a connection first"))
		return nil
	}
	m.addTab(connection)
	return m.prepareConnectionCmd(connection)
}

func (m *Model) ensureTab(connection profile.Connection) {
	for index, tab := range m.tabs {
		if tab.connection.ID == connection.ID {
			m.activeTab = index
			m.setFocus(focusEditor)
			return
		}
	}
	m.addTab(connection)
}

func (m *Model) addTab(connection profile.Connection) {
	editor := textarea.New()
	editor.Placeholder = "Write SQL here…"
	editor.ShowLineNumbers = true
	editor.SetVirtualCursor(true)
	editor.SetValue(defaultSQL(connection.Driver))
	document := sqleditor.NewDocument(editor.Value())
	analysis := sqleditor.Analyze(editor.Value(), dialectFor(connection.Driver), document.Version())
	_ = document.Apply(analysis)
	tab := &queryTab{
		id:         m.nextTabID,
		connection: connection,
		editor:     editor,
		table:      newResultTable(connection.EffectiveMaxColumnWidth()),
		statusText: "Ready",
		document:   document,
		analysis:   analysis,
		format:     sqleditor.DefaultFormatOptions(),
	}
	m.nextTabID++
	m.tabs = append(m.tabs, tab)
	m.activeTab = len(m.tabs) - 1
	m.setFocus(focusEditor)
	m.resize()
}

func (m *Model) requestCloseTab() tea.Cmd {
	tab := m.currentTab()
	if tab == nil {
		return nil
	}
	if tab.isRunning || strings.TrimSpace(tab.editor.Value()) != "" {
		m.mode = modeConfirmClose
		return nil
	}
	m.closeActiveTab()
	return nil
}

func (m *Model) closeActiveTab() {
	if m.activeTab < 0 || m.activeTab >= len(m.tabs) {
		return
	}
	tab := m.tabs[m.activeTab]
	if tab.cancel != nil {
		tab.cancel()
	}
	m.tabs = append(m.tabs[:m.activeTab], m.tabs[m.activeTab+1:]...)
	if len(m.tabs) == 0 {
		m.activeTab = -1
		m.setFocus(focusBrowser)
		return
	}
	if m.activeTab >= len(m.tabs) {
		m.activeTab = len(m.tabs) - 1
	}
	m.setFocus(focusEditor)
}

func (m *Model) switchTab(delta int) {
	if len(m.tabs) < 2 {
		return
	}
	m.activeTab = (m.activeTab + delta + len(m.tabs)) % len(m.tabs)
	m.setFocus(m.focus)
}

func (m *Model) cycleFocus(delta int) {
	order := []focus{focusEditor, focusResults}
	if m.isBrowserOpen {
		order = []focus{focusBrowser, focusEditor, focusResults}
	}
	index := 0
	for i, item := range order {
		if item == m.focus {
			index = i
			break
		}
	}
	index = (index + delta + len(order)) % len(order)
	m.setFocus(order[index])
}

func (m *Model) setFocus(next focus) {
	if next != focusEditor {
		if tab := m.currentTab(); tab != nil {
			tab.completion.isOpen = false
		}
	}
	m.focus = next
	for _, tab := range m.tabs {
		tab.editor.Blur()
		tab.table.Blur()
	}
	if tab := m.currentTab(); tab != nil {
		if next == focusEditor {
			tab.editor.Focus()
		}
		if next == focusResults {
			tab.table.Focus()
		}
	}
}

func (m *Model) currentTab() *queryTab {
	if m.activeTab < 0 || m.activeTab >= len(m.tabs) {
		return nil
	}
	return m.tabs[m.activeTab]
}

func (m *Model) tabByID(id int) *queryTab {
	for _, tab := range m.tabs {
		if tab.id == id {
			return tab
		}
	}
	return nil
}

func (m *Model) syncTabConnections() {
	for _, tab := range m.tabs {
		if connection, ok := m.profileByID(tab.connection.ID); ok {
			tab.connection = connection
		}
	}
}

func (m *Model) selectedProfile() (profile.Connection, bool) {
	if m.browserCursor < 0 || m.browserCursor >= len(m.browserItems) {
		return profile.Connection{}, false
	}
	return m.profileByID(m.browserItems[m.browserCursor].profileID)
}

func (m *Model) profileByID(id string) (profile.Connection, bool) {
	for _, connection := range m.profiles {
		if connection.ID == id {
			return connection, true
		}
	}
	return profile.Connection{}, false
}

func (m *Model) ensureCatalog(id string) *catalogState {
	if state, ok := m.catalog[id]; ok {
		return state
	}
	state := &catalogState{
		schemas: []string{}, expanded: map[string]bool{},
		relations: map[string][]database.Relation{}, columns: map[string][]database.Column{},
	}
	m.catalog[id] = state
	return state
}

func (m *Model) setError(err error) {
	m.statusText = activity.Redact(err.Error())
	m.isStatusError = true
}

func (m *Model) hasDirtyTabs() bool {
	for _, tab := range m.tabs {
		if tab.isRunning || strings.TrimSpace(tab.editor.Value()) != "" {
			return true
		}
	}
	return false
}

func (m *Model) cancelAll() {
	for _, tab := range m.tabs {
		if tab.cancel != nil {
			tab.cancel()
		}
	}
}

func (m *Model) loadProfilesCmd() tea.Cmd {
	return func() tea.Msg {
		profiles, err := m.store.List()
		return profilesLoadedMsg{profiles: profiles, err: err}
	}
}

func (m *Model) saveProfileCmd(connection profile.Connection, password string) tea.Cmd {
	return func() tea.Msg {
		if err := m.store.Save(connection); err != nil {
			return profileSavedMsg{connection: connection, err: err}
		}
		if password != "" {
			if err := m.secrets.Set(connection.ID, password); err != nil {
				return profileSavedMsg{connection: connection, err: err}
			}
		}
		if err := m.manager.Disconnect(connection.ID); err != nil {
			return profileSavedMsg{connection: connection, err: err}
		}
		return profileSavedMsg{connection: connection}
	}
}

func (m *Model) deleteProfileCmd(id string) tea.Cmd {
	return func() tea.Msg {
		secretErr := m.secrets.Delete(id)
		disconnectErr := m.manager.Disconnect(id)
		_, storeErr := m.store.Delete(id)
		return profileDeletedMsg{profileID: id, err: errors.Join(storeErr, secretErr, disconnectErr)}
	}
}

func (m *Model) prepareConnectionCmd(connection profile.Connection) tea.Cmd {
	if connection.Driver == profile.DriverSQLite {
		return m.loadSchemasCmd(connection)
	}
	return func() tea.Msg {
		_, err := m.secrets.Get(connection.ID)
		if err != nil {
			if errors.Is(err, profile.ErrSecretNotFound) {
				return passwordNeededMsg{connection: connection, action: passwordConnect}
			}
			return schemasLoadedMsg{profileID: connection.ID, err: err}
		}
		return m.loadSchemasCmd(connection)()
	}
}

func (m *Model) prepareTestCmd(connection profile.Connection) tea.Cmd {
	if connection.Driver == profile.DriverSQLite {
		return m.testConnectionCmd(connection)
	}
	return func() tea.Msg {
		_, err := m.secrets.Get(connection.ID)
		if err != nil {
			if errors.Is(err, profile.ErrSecretNotFound) {
				return passwordNeededMsg{connection: connection, action: passwordTest}
			}
			return connectionTestedMsg{connection: connection, err: err}
		}
		return m.testConnectionCmd(connection)()
	}
}

func (m *Model) testConnectionCmd(connection profile.Connection) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, err := m.manager.Connection(ctx, connection)
		return connectionTestedMsg{connection: connection, err: err}
	}
}

func (m *Model) disconnectCmd(connection profile.Connection) tea.Cmd {
	return func() tea.Msg {
		err := m.manager.Disconnect(connection.ID)
		return connectionDisconnectedMsg{connection: connection, err: err}
	}
}

func (m *Model) loadSchemasCmd(connection profile.Connection) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		schemas, err := m.inspector.Schemas(ctx, connection)
		return schemasLoadedMsg{profileID: connection.ID, schemas: schemas, err: err}
	}
}

func (m *Model) loadRelationsCmd(connection profile.Connection, schema string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		relations, err := m.inspector.Relations(ctx, connection, schema)
		return relationsLoadedMsg{profileID: connection.ID, schema: schema, relations: relations, err: err}
	}
}

func (m *Model) loadColumnsCmd(connection profile.Connection, schema, relation string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		columns, err := m.inspector.Columns(ctx, connection, schema, relation)
		return columnsLoadedMsg{
			profileID: connection.ID, schema: schema, relation: relation, columns: columns, err: err,
		}
	}
}

func (m *Model) loadLogsCmd() tea.Cmd {
	return func() tea.Msg {
		entries, err := m.activity.Entries(activity.DefaultReadMax)
		return logsLoadedMsg{entries: entries, err: err}
	}
}

func statementAtCursor(editor textarea.Model) (string, bool) {
	value := editor.Value()
	offset, ok := cursorOffset(editor)
	if !ok {
		return "", false
	}
	span, ok := sqlscan.At(value, offset)
	return span.Text, ok
}

func cursorOffset(editor textarea.Model) (int, bool) {
	value := editor.Value()
	lines := strings.Split(value, "\n")
	line := min(editor.Line(), len(lines)-1)
	if line < 0 {
		return 0, false
	}
	offset := 0
	for i := range line {
		offset += len(lines[i]) + 1
	}
	runes := []rune(lines[line])
	column := min(editor.Column(), len(runes))
	offset += len(string(runes[:column]))
	return offset, true
}

func selectedStatement(tab *queryTab) (string, bool) {
	statement, _, ok := selectedStatementWithOffset(tab)
	return statement, ok
}

func selectedStatementWithOffset(tab *queryTab) (string, int, bool) {
	if tab.selection == nil {
		value := tab.editor.Value()
		cursor, ok := cursorOffset(tab.editor)
		if !ok {
			return "", 0, false
		}
		span, ok := sqlscan.At(value, cursor)
		return span.Text, span.Start, ok
	}
	cursor, ok := cursorOffset(tab.editor)
	if !ok || cursor == *tab.selection {
		return "", 0, false
	}
	start, end := *tab.selection, cursor
	if start > end {
		start, end = end, start
	}
	value := tab.editor.Value()
	if start < 0 || end > len(value) {
		return "", 0, false
	}
	spans := sqlscan.Statements(value[start:end])
	if len(spans) != 1 {
		return "", 0, false
	}
	return spans[0].Text, start + spans[0].Start, true
}

func defaultSQL(driver profile.Driver) string {
	switch driver {
	case profile.DriverSQLServer:
		return "SELECT TOP (100) *\nFROM your_table;"
	case profile.DriverSQLite, profile.DriverPostgres:
		return "SELECT *\nFROM your_table\nLIMIT 100;"
	default:
		return ""
	}
}

func catalogKey(schema, relation string) string {
	return schema + "\x00" + relation
}

func containsSchema(schemas []string, driver profile.Driver, target string) bool {
	_, ok := findSchema(schemas, driver, target)
	return ok
}

func findSchema(schemas []string, driver profile.Driver, target string) (string, bool) {
	for _, schema := range schemas {
		if schema == target || driver == profile.DriverSQLServer && strings.EqualFold(schema, target) {
			return schema, true
		}
	}
	return "", false
}

func formatDuration(duration time.Duration) string {
	if duration < time.Microsecond {
		return duration.Round(time.Nanosecond).String()
	}
	if duration < time.Millisecond {
		return duration.Round(time.Microsecond).String()
	}
	if duration < time.Second {
		return duration.Round(time.Millisecond).String()
	}
	return duration.Round(time.Millisecond).String()
}

func truncate(value string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(strings.ReplaceAll(value, "\n", " "))
	if len(runes) <= width {
		return string(runes)
	}
	if width == 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}

var _ tea.Model = (*Model)(nil)

// RequestID returns the active request identifier for focused model tests.
func (m *Model) RequestID() string {
	tab := m.currentTab()
	if tab == nil {
		return ""
	}
	return strconv.Itoa(tab.requestID)
}
