// Package tui implements the interactive database workbench.
package tui

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/lucasfguimares/charta-tui/internal/activity"
	"github.com/lucasfguimares/charta-tui/internal/database"
	"github.com/lucasfguimares/charta-tui/internal/profile"
	"github.com/lucasfguimares/charta-tui/internal/queryhistory"
	"github.com/lucasfguimares/charta-tui/internal/querylibrary"
	"github.com/lucasfguimares/charta-tui/internal/sqleditor"
	"github.com/lucasfguimares/charta-tui/internal/sqlscan"
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
	modeColumnInspector
	modeRowInspector
	modeResultSearch
	modeGotoRow
	modeHistory
	modeHistorySearch
	modeLibrary
	modeLibrarySearch
	modeFavoriteForm
	modeSnippetForm
	modeConfirmHistoryClear
)

type profileRepository interface {
	List() ([]profile.Connection, error)
	Save(profile.Connection) error
	Delete(string) (bool, error)
}

type queryTab struct {
	id                  int
	connection          profile.Connection
	editor              textarea.Model
	table               *resultTableModel
	result              database.Result
	isRunning           bool
	isTrusted           bool
	requestID           int
	cancel              context.CancelFunc
	statusText          string
	selection           *int
	startedAt           time.Time
	lastSQL             string
	lastSQLBase         int
	document            *sqleditor.Document
	analysis            sqleditor.Analysis
	diagnostic          int
	format              sqleditor.FormatOptions
	completion          autocompleteState
	placeholders        []querylibrary.Placeholder
	placeholderAwaiting bool
	placeholderIndex    int
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
	automatic bool
	err       error
}

type autocompleteDueMsg struct {
	tabID     int
	requestID uint64
	version   uint64
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

type historyLoadedMsg struct {
	entries []queryhistory.Entry
	err     error
}

type historyChangedMsg struct{ err error }
type historyRecordedMsg struct {
	tabID int
	err   error
}

type libraryLoadedMsg struct {
	favorites []querylibrary.Favorite
	snippets  []querylibrary.Snippet
	err       error
}

type libraryChangedMsg struct{ err error }

// Model owns the entire interactive application state.
type Model struct {
	store     profileRepository
	secrets   profile.SecretStore
	manager   *database.Manager
	inspector *database.Inspector
	runner    *database.Runner
	activity  *activity.Log
	history   *queryhistory.Store
	library   *querylibrary.Store

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

	form                     profileForm
	passwordInput            textinput.Model
	resultInput              textinput.Model
	pendingConnection        profile.Connection
	pendingPasswordAct       passwordAction
	pendingSQL               string
	pendingRunScript         bool
	pendingDeleteID          string
	logs                     viewport.Model
	cellDetail               viewport.Model
	cellDetailText           string
	rowInspector             rowInspectorModel
	columnInspector          columnInspectorModel
	columnStatisticsCache    map[columnCacheKey]columnStatisticsCacheEntry
	columnInspectorRequestID uint64
	autocompleteCache        map[string]*autocompleteCatalogState
	autocompleteRequestID    uint64
	historyEntries           []queryhistory.Entry
	historyCursor            int
	historyFilter            queryhistory.Filter
	historyInput             textinput.Model
	historyPeriod            int
	historyError             string
	libraryFavorites         []querylibrary.Favorite
	librarySnippets          []querylibrary.Snippet
	libraryCursor            int
	librarySection           int
	librarySearch            string
	libraryError             string
	libraryInput             textinput.Model
	favoriteForm             favoriteForm
	snippetForm              snippetForm
}

// New creates the root Bubble Tea model with explicitly wired services.
func New(
	store profileRepository,
	secrets profile.SecretStore,
	manager *database.Manager,
	inspector *database.Inspector,
	runner *database.Runner,
	activityLog *activity.Log,
	historyStore *queryhistory.Store,
	libraryStore *querylibrary.Store,
) *Model {
	passwordInput := textinput.New()
	passwordInput.Prompt = "Password: "
	passwordInput.EchoMode = textinput.EchoPassword
	passwordInput.SetWidth(42)
	resultInput := textinput.New()
	resultInput.SetWidth(42)
	historyInput := textinput.New()
	historyInput.Prompt = "Search SQL: "
	historyInput.SetWidth(52)
	libraryInput := textinput.New()
	libraryInput.Prompt = "Search: "
	libraryInput.SetWidth(52)

	model := &Model{
		store:                 store,
		secrets:               secrets,
		manager:               manager,
		inspector:             inspector,
		runner:                runner,
		activity:              activityLog,
		history:               historyStore,
		library:               libraryStore,
		profiles:              []profile.Connection{},
		catalog:               map[string]*catalogState{},
		browserItems:          []browserItem{},
		tabs:                  []*queryTab{},
		activeTab:             -1,
		nextTabID:             1,
		focus:                 focusBrowser,
		mode:                  modeWorkspace,
		isBrowserOpen:         true,
		statusText:            "Loading connections…",
		styles:                defaultStyles(),
		passwordInput:         passwordInput,
		resultInput:           resultInput,
		logs:                  viewport.New(),
		cellDetail:            viewport.New(),
		historyInput:          historyInput,
		libraryInput:          libraryInput,
		librarySnippets:       querylibrary.BuiltInSnippets(),
		autocompleteCache:     map[string]*autocompleteCatalogState{},
		columnStatisticsCache: map[columnCacheKey]columnStatisticsCacheEntry{},
	}
	return model
}

// Init loads saved profiles without blocking the initial render.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.loadProfilesCmd(), m.loadLibraryCmd())
}

// Update applies input and asynchronous service results.
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
			m.closeAutocomplete(tab, false)
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

func statementAtCursor(editor textarea.Model, dialect sqleditor.Dialect) (string, bool) {
	value := editor.Value()
	offset, ok := cursorOffset(editor)
	if !ok {
		return "", false
	}
	statement, ok := sqleditor.StatementAtOffset(value, offset, dialect)
	return statement.Text, ok
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
		statement, ok := sqleditor.StatementAtOffset(value, cursor, dialectFor(tab.connection.Driver))
		return statement.Text, statement.Range.Start.Offset, ok
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
	statements := sqleditor.Statements(value[start:end], dialectFor(tab.connection.Driver))
	if len(statements) != 1 {
		return "", 0, false
	}
	return statements[0].Text, start + statements[0].Range.Start.Offset, true
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

func invalidatesSchemaMetadata(sql string, dialect sqleditor.Dialect) bool {
	for _, statement := range sqleditor.Statements(sql, dialect) {
		switch sqlscan.Analyze(statement.Text).FirstKeyword {
		case "CREATE", "ALTER", "DROP", "TRUNCATE", "ATTACH", "DETACH", "PRAGMA":
			return true
		}
	}
	return false
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
