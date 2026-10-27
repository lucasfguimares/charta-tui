package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/lucasfguimares/tui-db/internal/activity"
	"github.com/lucasfguimares/tui-db/internal/database"
	"github.com/lucasfguimares/tui-db/internal/profile"
	"github.com/lucasfguimares/tui-db/internal/sqleditor"
)

const (
	explorerLoadDelay   = 150 * time.Millisecond
	explorerSelectLimit = 100
	explorerWideWidth   = 100
)

type explorerPane int

const (
	explorerSchemas explorerPane = iota
	explorerRelations
	explorerColumns
)

// explorerModel is the full-screen schema explorer: Miller columns over the
// per-profile catalog cache, loaded lazily as the cursor moves.
type explorerModel struct {
	profileID    string
	pane         explorerPane
	cursor       [3]int
	filter       [3]string
	filterInput  textinput.Model
	loadVersion  uint64
	loading      map[string]bool
	marked       map[string]bool
	target       [3]string
	isConnecting bool
	needsConnect bool
	errorText    string
	find         explorerFind
	view         explorerView
}

type explorerLoadDueMsg struct{ version uint64 }

func newExplorerFilterInput() textinput.Model {
	input := textinput.New()
	input.Prompt = "Filter: "
	input.SetWidth(28)
	return input
}

// openExplorerForWorkspace opens the explorer on the active tab's connection,
// falling back to the browser selection.
func (m *Model) openExplorerForWorkspace() tea.Cmd {
	if tab := m.currentTab(); tab != nil && !tab.isDetached && tab.connection.ID != "" {
		return m.openExplorer(tab.connection.ID, "", "", "")
	}
	if connection, ok := m.selectedProfile(); ok {
		return m.openExplorer(connection.ID, "", "", "")
	}
	m.setError(errors.New("add or select a connection to explore its schemas"))
	return nil
}

// openExplorerForBrowserItem opens the explorer positioned on the sidebar selection.
func (m *Model) openExplorerForBrowserItem() tea.Cmd {
	if m.browserCursor < 0 || m.browserCursor >= len(m.browserItems) {
		return nil
	}
	item := m.browserItems[m.browserCursor]
	return m.openExplorer(item.profileID, item.schema, item.relation, item.column)
}

// openExplorer shows the explorer for a profile. It never prompts for a
// password: without stored credentials it waits for an explicit Enter.
func (m *Model) openExplorer(profileID, schema, relation, column string) tea.Cmd {
	connection, ok := m.profileByID(profileID)
	if !ok {
		m.setError(errors.New("connection profile not found"))
		return nil
	}
	if schema == "" {
		schema = connection.DefaultSchema
	}
	input := m.explorer.filterInput
	if input.Prompt == "" {
		input = newExplorerFilterInput()
	}
	find := explorerFind{input: m.explorer.find.input}
	if m.explorer.profileID == profileID {
		find.query, find.kind = m.explorer.find.query, m.explorer.find.kind
	}
	m.explorer = explorerModel{
		profileID:   profileID,
		find:        find,
		filterInput: input,
		loading:     map[string]bool{},
		marked:      map[string]bool{},
		target:      [3]string{schema, relation, column},
	}
	switch {
	case column != "":
		m.explorer.pane = explorerColumns
	case relation != "":
		m.explorer.pane = explorerRelations
	}
	m.mode = modeExplorer
	state := m.ensureCatalog(profileID)
	if len(state.schemas) == 0 {
		return m.explorerSchemasCmd(connection)
	}
	m.applyExplorerTarget()
	return m.explorerLoadCmd()
}

// explorerSchemasCmd loads schemas without ever prompting: a missing secret is
// reported back so the explorer can ask for an explicit connect.
func (m *Model) explorerSchemasCmd(connection profile.Connection) tea.Cmd {
	if m.inspector == nil {
		return nil
	}
	m.explorer.isConnecting = true
	m.explorer.needsConnect = false
	m.explorer.errorText = ""
	secrets := m.secrets
	return func() tea.Msg {
		if connection.Driver != profile.DriverSQLite && secrets != nil {
			if _, err := secrets.Get(connection.ID); err != nil {
				return schemasLoadedMsg{profileID: connection.ID, err: err, isExplorer: true}
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		schemas, err := m.inspector.Schemas(ctx, connection)
		return schemasLoadedMsg{profileID: connection.ID, schemas: schemas, err: err, isExplorer: true}
	}
}

func (m *Model) isExplorerOpen() bool {
	return m.mode == modeExplorer || m.mode == modeExplorerFilter || m.mode == modeExplorerFind
}

// refreshExplorerFindIfOpen re-indexes after catalog data arrives during a find.
func (m *Model) refreshExplorerFindIfOpen(profileID string) {
	if m.mode == modeExplorerFind && m.explorer.profileID == profileID {
		m.refreshExplorerFind()
	}
}

func (m *Model) handleExplorerSchemas(msg schemasLoadedMsg) tea.Cmd {
	if msg.profileID != m.explorer.profileID {
		return nil
	}
	m.explorer.isConnecting = false
	if msg.err != nil {
		if errors.Is(msg.err, profile.ErrSecretNotFound) {
			m.explorer.needsConnect = true
			return nil
		}
		m.explorer.errorText = "Loading schemas failed: " + activity.Redact(msg.err.Error())
		return nil
	}
	state := m.ensureCatalog(msg.profileID)
	state.schemas = msg.schemas
	m.rebuildBrowser()
	m.applyExplorerTarget()
	m.refreshExplorerFindIfOpen(msg.profileID)
	return m.explorerLoadCmd()
}

func (m *Model) handleExplorerRelations(msg relationsLoadedMsg) tea.Cmd {
	delete(m.explorer.loading, "r\x00"+msg.profileID+"\x00"+msg.schema)
	if msg.err != nil {
		m.explorer.errorText = "Loading relations failed: " + activity.Redact(msg.err.Error())
		return nil
	}
	m.ensureCatalog(msg.profileID).relations[msg.schema] = msg.relations
	m.rebuildBrowser()
	m.applyExplorerTarget()
	m.refreshExplorerFindIfOpen(msg.profileID)
	return m.explorerLoadCmd()
}

func (m *Model) handleExplorerColumns(msg columnsLoadedMsg) tea.Cmd {
	key := catalogKey(msg.schema, msg.relation)
	delete(m.explorer.loading, "c\x00"+msg.profileID+"\x00"+key)
	if msg.err != nil {
		m.explorer.errorText = "Loading columns failed: " + activity.Redact(msg.err.Error())
		return nil
	}
	m.ensureCatalog(msg.profileID).columns[key] = msg.columns
	m.rebuildBrowser()
	m.applyExplorerTarget()
	m.refreshExplorerFindIfOpen(msg.profileID)
	return m.explorerLoadCmd()
}

func (m *Model) scheduleExplorerLoad() tea.Cmd {
	m.explorer.loadVersion++
	version := m.explorer.loadVersion
	return tea.Tick(explorerLoadDelay, func(time.Time) tea.Msg { return explorerLoadDueMsg{version: version} })
}

func (m *Model) handleExplorerLoadDue(msg explorerLoadDueMsg) tea.Cmd {
	if !m.isExplorerOpen() || msg.version != m.explorer.loadVersion {
		return nil
	}
	return m.explorerLoadCmd()
}

// explorerLoadCmd requests the first missing level under the current selection.
func (m *Model) explorerLoadCmd() tea.Cmd {
	if !m.isExplorerOpen() {
		return nil
	}
	connection, ok := m.profileByID(m.explorer.profileID)
	if !ok {
		return nil
	}
	state := m.ensureCatalog(connection.ID)
	schema, ok := m.explorerSelectedSchema()
	if !ok {
		return nil
	}
	if _, loaded := state.relations[schema]; !loaded {
		loadingKey := "r\x00" + connection.ID + "\x00" + schema
		if m.explorer.loading[loadingKey] {
			return nil
		}
		m.explorer.loading[loadingKey] = true
		load := m.loadRelationsCmd(connection, schema)
		return func() tea.Msg {
			msg := load().(relationsLoadedMsg)
			msg.isExplorer = true
			return msg
		}
	}
	relation, ok := m.explorerSelectedRelation()
	if !ok {
		return nil
	}
	key := catalogKey(schema, relation.Name)
	if _, loaded := state.columns[key]; loaded {
		return m.explorerDetailsLoadCmd(connection, schema, relation.Name)
	}
	loadingKey := "c\x00" + connection.ID + "\x00" + key
	if m.explorer.loading[loadingKey] {
		return nil
	}
	m.explorer.loading[loadingKey] = true
	load := m.loadColumnsCmd(connection, schema, relation.Name)
	return func() tea.Msg {
		msg := load().(columnsLoadedMsg)
		msg.isExplorer = true
		return msg
	}
}

// applyExplorerTarget moves the cursors onto requested names once their level is loaded.
func (m *Model) applyExplorerTarget() {
	if name := m.explorer.target[explorerSchemas]; name != "" {
		schemas := m.explorerSchemas()
		if len(schemas) == 0 {
			return
		}
		for index, schema := range schemas {
			if strings.EqualFold(schema, name) {
				m.explorer.cursor[explorerSchemas] = index
				break
			}
		}
		m.explorer.target[explorerSchemas] = ""
	}
	if name := m.explorer.target[explorerRelations]; name != "" {
		schema, _ := m.explorerSelectedSchema()
		if _, loaded := m.ensureCatalog(m.explorer.profileID).relations[schema]; !loaded {
			return
		}
		for index, relation := range m.explorerRelations() {
			if relation.Name == name {
				m.explorer.cursor[explorerRelations] = index
				break
			}
		}
		m.explorer.target[explorerRelations] = ""
	}
	if name := m.explorer.target[explorerColumns]; name != "" {
		columns := m.explorerColumns()
		if len(columns) == 0 {
			return
		}
		for index, column := range columns {
			if column.Name == name {
				m.explorer.cursor[explorerColumns] = index
				break
			}
		}
		m.explorer.target[explorerColumns] = ""
	}
}

func (m *Model) explorerSchemas() []string {
	state, ok := m.catalog[m.explorer.profileID]
	if !ok {
		return nil
	}
	filter := strings.ToLower(m.explorer.filter[explorerSchemas])
	result := make([]string, 0, len(state.schemas))
	for _, schema := range state.schemas {
		if filter == "" || strings.Contains(strings.ToLower(schema), filter) {
			result = append(result, schema)
		}
	}
	return result
}

func (m *Model) explorerRelations() []database.Relation {
	schema, ok := m.explorerSelectedSchema()
	if !ok {
		return nil
	}
	filter := strings.ToLower(m.explorer.filter[explorerRelations])
	relations := m.catalog[m.explorer.profileID].relations[schema]
	result := make([]database.Relation, 0, len(relations))
	for _, relation := range relations {
		if filter == "" || strings.Contains(strings.ToLower(relation.Name), filter) {
			result = append(result, relation)
		}
	}
	return result
}

func (m *Model) explorerColumns() []database.Column {
	schema, ok := m.explorerSelectedSchema()
	if !ok {
		return nil
	}
	relation, ok := m.explorerSelectedRelation()
	if !ok {
		return nil
	}
	filter := strings.ToLower(m.explorer.filter[explorerColumns])
	columns := m.catalog[m.explorer.profileID].columns[catalogKey(schema, relation.Name)]
	result := make([]database.Column, 0, len(columns))
	for _, column := range columns {
		if filter == "" || strings.Contains(strings.ToLower(column.Name), filter) {
			result = append(result, column)
		}
	}
	return result
}

func (m *Model) explorerSelectedSchema() (string, bool) {
	schemas := m.explorerSchemas()
	index := m.explorer.cursor[explorerSchemas]
	if index < 0 || index >= len(schemas) {
		return "", false
	}
	return schemas[index], true
}

func (m *Model) explorerSelectedRelation() (database.Relation, bool) {
	relations := m.explorerRelations()
	index := m.explorer.cursor[explorerRelations]
	if index < 0 || index >= len(relations) {
		return database.Relation{}, false
	}
	return relations[index], true
}

func (m *Model) explorerSelectedColumn() (database.Column, bool) {
	if m.explorer.view != explorerViewColumns {
		return database.Column{}, false
	}
	columns := m.explorerColumns()
	index := m.explorer.cursor[explorerColumns]
	if index < 0 || index >= len(columns) {
		return database.Column{}, false
	}
	return columns[index], true
}

func (m *Model) explorerPaneLength(pane explorerPane) int {
	switch pane {
	case explorerSchemas:
		return len(m.explorerSchemas())
	case explorerRelations:
		return len(m.explorerRelations())
	}
	switch m.explorer.view {
	case explorerViewIndexes:
		details, _ := m.explorerSelectedDetails()
		return len(details.Indexes)
	case explorerViewDefinition:
		return len(m.explorerDefinitionLines())
	default:
		return len(m.explorerColumns())
	}
}

// moveExplorerCursor moves the focused pane's cursor and resets deeper panes.
func (m *Model) moveExplorerCursor(position int) tea.Cmd {
	pane := m.explorer.pane
	length := m.explorerPaneLength(pane)
	if length == 0 {
		return nil
	}
	position = min(max(0, position), length-1)
	if position == m.explorer.cursor[pane] {
		return nil
	}
	m.explorer.cursor[pane] = position
	return m.resetExplorerBelow(pane)
}

func (m *Model) resetExplorerBelow(pane explorerPane) tea.Cmd {
	if pane == explorerColumns {
		return nil
	}
	for deeper := pane + 1; deeper <= explorerColumns; deeper++ {
		m.explorer.cursor[deeper] = 0
		m.explorer.filter[deeper] = ""
	}
	m.explorer.marked = map[string]bool{}
	m.explorer.errorText = ""
	return m.scheduleExplorerLoad()
}

func (m *Model) handleExplorerKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	pane := m.explorer.pane
	page := max(1, m.explorerPaneHeight()-3)
	switch key {
	case "esc":
		if m.explorer.filter[pane] != "" {
			m.explorer.filter[pane] = ""
			m.explorer.cursor[pane] = 0
			return m.resetExplorerBelow(pane)
		}
		m.mode = modeWorkspace
		return nil
	case "up", "k":
		return m.moveExplorerCursor(m.explorer.cursor[pane] - 1)
	case "down", "j":
		return m.moveExplorerCursor(m.explorer.cursor[pane] + 1)
	case "pgup":
		return m.moveExplorerCursor(m.explorer.cursor[pane] - page)
	case "pgdown":
		return m.moveExplorerCursor(m.explorer.cursor[pane] + page)
	case "home", "g":
		return m.moveExplorerCursor(0)
	case "end", "G", "shift+g":
		return m.moveExplorerCursor(m.explorerPaneLength(pane) - 1)
	case "left", "h", "shift+tab":
		if pane > explorerSchemas {
			m.explorer.pane--
		}
		return nil
	case "right", "l", "tab":
		if pane < explorerColumns {
			m.explorer.pane++
		}
		return m.explorerLoadCmd()
	case "enter":
		if m.explorer.needsConnect || m.explorer.errorText != "" && len(m.explorerSchemas()) == 0 {
			connection, ok := m.profileByID(m.explorer.profileID)
			if !ok {
				return nil
			}
			m.explorer.needsConnect = false
			m.explorer.isConnecting = true
			return m.prepareConnectionCmd(connection)
		}
		if pane == explorerColumns {
			return m.openExplorerInspector()
		}
		m.explorer.pane++
		return m.explorerLoadCmd()
	case "f", "ctrl+f":
		return m.openExplorerFind()
	case "/":
		m.explorer.filterInput.SetValue(m.explorer.filter[pane])
		m.explorer.filterInput.CursorEnd()
		m.mode = modeExplorerFilter
		return m.explorer.filterInput.Focus()
	case "space":
		if column, ok := m.explorerSelectedColumn(); ok && pane == explorerColumns {
			if m.explorer.marked[column.Name] {
				delete(m.explorer.marked, column.Name)
			} else {
				m.explorer.marked[column.Name] = true
			}
			return m.moveExplorerCursor(m.explorer.cursor[pane] + 1)
		}
	case "s":
		return m.explorerInsertSelect()
	case "y":
		return m.explorerCopy(false)
	case "Y", "shift+y":
		return m.explorerCopy(true)
	case "c":
		if pane == explorerColumns {
			return m.openExplorerInspector()
		}
	case "r":
		return m.refreshExplorer()
	case "d":
		m.cycleExplorerView()
		return m.explorerLoadCmd()
	}
	return nil
}

func (m *Model) handleExplorerFilterKey(msg tea.KeyPressMsg) tea.Cmd {
	pane := m.explorer.pane
	switch msg.String() {
	case "esc", "enter":
		m.explorer.filterInput.Blur()
		m.mode = modeExplorer
		return nil
	case "up":
		return m.moveExplorerCursor(m.explorer.cursor[pane] - 1)
	case "down":
		return m.moveExplorerCursor(m.explorer.cursor[pane] + 1)
	}
	before := m.explorer.filterInput.Value()
	updated, cmd := m.explorer.filterInput.Update(msg)
	m.explorer.filterInput = updated
	if value := m.explorer.filterInput.Value(); value != before {
		m.explorer.filter[pane] = strings.TrimSpace(value)
		m.explorer.cursor[pane] = 0
		return tea.Batch(cmd, m.resetExplorerBelow(pane))
	}
	return cmd
}

func (m *Model) refreshExplorer() tea.Cmd {
	connection, ok := m.profileByID(m.explorer.profileID)
	if !ok {
		return nil
	}
	state := m.ensureCatalog(connection.ID)
	m.explorer.errorText = ""
	schema, hasSchema := m.explorerSelectedSchema()
	relation, hasRelation := m.explorerSelectedRelation()
	switch {
	case m.explorer.pane == explorerSchemas || !hasSchema:
		if hasSchema {
			m.explorer.target[explorerSchemas] = schema
		}
		state.schemas = []string{}
		return m.explorerSchemasCmd(connection)
	case m.explorer.pane == explorerRelations || !hasRelation:
		if hasRelation {
			m.explorer.target[explorerRelations] = relation.Name
		}
		delete(state.relations, schema)
		for key := range state.details {
			if strings.HasPrefix(key, schema+"\x00") {
				delete(state.details, key)
			}
		}
	default:
		if column, ok := m.explorerSelectedColumn(); ok {
			m.explorer.target[explorerColumns] = column.Name
		}
		delete(state.columns, catalogKey(schema, relation.Name))
		delete(state.details, catalogKey(schema, relation.Name))
	}
	delete(m.autocompleteCache, connection.ID)
	return m.explorerLoadCmd()
}

// explorerSelectSQL builds a row-limited SELECT for a relation in the
// connection's dialect, quoting identifiers only when required.
func explorerSelectSQL(connection profile.Connection, schema, relation string, columns []string) string {
	dialect := dialectFor(connection.Driver)
	target := sqleditor.QuoteIdentifier(dialect, relation)
	if schema != "" {
		target = sqleditor.QuoteIdentifier(dialect, schema) + "." + target
	}
	selected := "*"
	if len(columns) > 0 {
		quoted := make([]string, len(columns))
		for index, column := range columns {
			quoted[index] = sqleditor.QuoteIdentifier(dialect, column)
		}
		selected = strings.Join(quoted, ", ")
	}
	if connection.Driver == profile.DriverSQLServer {
		return fmt.Sprintf("SELECT TOP (%d) %s\nFROM %s;", explorerSelectLimit, selected, target)
	}
	return fmt.Sprintf("SELECT %s\nFROM %s\nLIMIT %d;", selected, target, explorerSelectLimit)
}

func (m *Model) explorerInsertSelect() tea.Cmd {
	connection, ok := m.profileByID(m.explorer.profileID)
	if !ok {
		return nil
	}
	schema, hasSchema := m.explorerSelectedSchema()
	relation, hasRelation := m.explorerSelectedRelation()
	if !hasSchema || !hasRelation || m.explorer.pane == explorerSchemas {
		m.explorer.errorText = "Select a table or view first"
		return nil
	}
	var columns []string
	if m.explorer.pane == explorerColumns {
		for _, column := range m.catalog[connection.ID].columns[catalogKey(schema, relation.Name)] {
			if m.explorer.marked[column.Name] {
				columns = append(columns, column.Name)
			}
		}
		if len(columns) == 0 {
			if column, ok := m.explorerSelectedColumn(); ok {
				columns = []string{column.Name}
			}
		}
	}
	statement := explorerSelectSQL(connection, schema, relation.Name, columns)

	m.mode = modeWorkspace
	m.ensureTab(connection)
	tab := m.currentTab()
	if tab == nil {
		return nil
	}
	current := tab.editor.Value()
	value, cursor := statement, 0
	if trimmed := strings.TrimSpace(current); trimmed != "" && trimmed != strings.TrimSpace(defaultSQL(connection.Driver)) {
		value = strings.TrimRight(current, "\n") + "\n\n" + statement
		cursor = len(value) - len(statement)
	}
	applyTabSQL(tab, value, cursor)
	m.setFocus(focusEditor)
	tab.statusText = "Inserted SELECT for " + schema + "." + relation.Name + " — Ctrl+Enter runs it"
	return scheduleSQLAnalysis(tab.id, tab.document.Version())
}

func (m *Model) explorerCopy(columnList bool) tea.Cmd {
	schema, hasSchema := m.explorerSelectedSchema()
	if !hasSchema {
		return nil
	}
	relation, hasRelation := m.explorerSelectedRelation()
	value := schema
	switch {
	case columnList && hasRelation:
		columns := m.catalog[m.explorer.profileID].columns[catalogKey(schema, relation.Name)]
		names := make([]string, len(columns))
		for index, column := range columns {
			names[index] = column.Name
		}
		value = strings.Join(names, ", ")
	case m.explorer.pane == explorerColumns && m.explorer.view == explorerViewColumns:
		if column, ok := m.explorerSelectedColumn(); ok {
			value = relation.Name + "." + column.Name
		}
	case m.explorer.pane != explorerSchemas && hasRelation:
		value = schema + "." + relation.Name
	}
	if value == "" {
		return nil
	}
	m.statusText = "Copied " + truncate(value, 60)
	m.isStatusError = false
	return tea.SetClipboard(value)
}

// openExplorerInspector opens the column inspector for the selected catalog column.
func (m *Model) openExplorerInspector() tea.Cmd {
	connection, ok := m.profileByID(m.explorer.profileID)
	if !ok {
		return nil
	}
	schema, _ := m.explorerSelectedSchema()
	relation, _ := m.explorerSelectedRelation()
	column, ok := m.explorerSelectedColumn()
	if !ok {
		return nil
	}
	m.cancelColumnStatistics()
	m.columnInspectorRequestID++
	metadata := column
	resultColumn := database.ResultColumn{
		Name: column.Name, DatabaseType: column.Type, Nullable: column.Nullable, NullableKnown: true,
		Length: column.Length, HasLength: column.HasLength,
		Precision: column.Precision, Scale: column.Scale, HasDecimalSize: column.HasDecimalSize,
		Ordinal: column.Ordinal, Key: column.Key,
		SourceSchema: schema, SourceTable: relation.Name, SourceColumn: column.Name,
		ReferenceSchema: column.ReferenceSchema, ReferenceTable: column.ReferenceTable, ReferenceColumn: column.ReferenceColumn,
	}
	inspector := columnInspectorModel{
		requestID:   m.columnInspectorRequestID,
		columnIndex: m.explorer.cursor[explorerColumns],
		column:      resultColumn,
		metadata:    &metadata,
		viewport:    viewport.New(),
		connection:  connection,
		isCatalog:   true,
		returnMode:  modeExplorer,
	}
	if cached, ok := m.columnStatisticsCache[columnKeyFor(connection.ID, resultColumn)]; ok && time.Since(cached.loadedAt) <= columnStatisticsTTL {
		statistics := cached.statistics
		inspector.exact = &statistics
		inspector.exactLoadedAt = cached.loadedAt
	}
	m.columnInspector = inspector
	m.mode = modeColumnInspector
	m.resizeColumnInspector()
	m.refreshColumnInspectorContent()
	return nil
}

func (m *Model) navigateExplorerInspector(index int) tea.Cmd {
	length := len(m.explorerColumns())
	if length == 0 {
		return nil
	}
	index = min(max(0, index), length-1)
	if index == m.columnInspector.columnIndex {
		return nil
	}
	m.explorer.cursor[explorerColumns] = index
	return m.openExplorerInspector()
}

// Rendering

// explorerVisiblePanes returns the first visible pane and how many fit: all
// three on wide terminals, otherwise a sliding window of two around the focus.
func (m *Model) explorerVisiblePanes() (explorerPane, int) {
	if m.width >= explorerWideWidth {
		return explorerSchemas, 3
	}
	return min(m.explorer.pane, explorerRelations), 2
}

// explorerPaneWidths returns the outer width of each visible pane (0 when hidden).
func (m *Model) explorerPaneWidths() [3]int {
	available := max(40, m.width-2)
	first, count := m.explorerVisiblePanes()
	shares := [3]int{18, 30, 52}
	if count == 2 {
		shares = [3]int{36, 36, 64}
		if first == explorerSchemas {
			shares[1] = 64
		}
	}
	var widths [3]int
	used := 0
	last := first + explorerPane(count) - 1
	for pane := first; pane <= last; pane++ {
		widths[pane] = available * shares[pane] / 100
		if pane == last {
			widths[pane] = available - used
		}
		used += widths[pane]
	}
	return widths
}

func (m *Model) explorerPaneHeight() int {
	height := m.height - 3 // header, detail line, footer
	if m.mode == modeExplorerFilter || m.explorer.filter[m.explorer.pane] != "" {
		height--
	}
	return max(5, height)
}

func (m *Model) renderExplorer() string {
	if m.mode == modeExplorerFind {
		return m.renderExplorerFind()
	}
	connection, _ := m.profileByID(m.explorer.profileID)
	height := m.explorerPaneHeight()

	first, count := m.explorerVisiblePanes()
	widths := m.explorerPaneWidths()
	panes := make([]string, 0, count)
	for pane := first; pane < first+explorerPane(count); pane++ {
		panes = append(panes, m.renderExplorerPane(pane, widths[pane], height))
	}

	lines := []string{
		m.renderExplorerHeader(connection),
		lipgloss.JoinHorizontal(lipgloss.Top, panes...),
		m.renderExplorerDetail(),
	}
	if m.mode == modeExplorerFilter || m.explorer.filter[m.explorer.pane] != "" {
		lines = append(lines, m.renderExplorerFilterBar())
	}
	footer := "←/→ pane  ↑/↓ PgUp/PgDn move  f find  / filter  Enter open  d indexes/definition  s SELECT  y/Y copy  r refresh  Esc close"
	if m.explorer.pane == explorerColumns {
		footer = "←/→ pane  ↑/↓ move  f find  d view  Space mark  s SELECT marked  Enter/c column stats  y/Y copy  / filter  Esc close"
	}
	lines = append(lines, m.styles.dim.Render(ansi.Truncate(footer, max(10, m.width-2), "…")))
	return m.styles.app.Render(strings.Join(lines, "\n"))
}

func (m *Model) renderExplorerHeader(connection profile.Connection) string {
	crumbs := []string{connection.Name}
	if schema, ok := m.explorerSelectedSchema(); ok {
		crumbs = append(crumbs, schema)
		if relation, ok := m.explorerSelectedRelation(); ok {
			crumbs = append(crumbs, relation.Name)
		}
	}
	header := m.styles.header.Render("Schema explorer") + "  " + m.styles.dim.Render(strings.Join(crumbs, " › "))
	return ansi.Truncate(header, max(10, m.width-2), "…")
}

func (m *Model) renderExplorerPane(pane explorerPane, width, height int) string {
	inner := max(4, width-2)
	rows := max(1, height-2)
	var title string
	var body []string
	switch pane {
	case explorerSchemas:
		title, body = m.explorerSchemaLines(inner)
	case explorerRelations:
		title, body = m.explorerRelationLines(inner)
	default:
		switch m.explorer.view {
		case explorerViewIndexes:
			title, body = m.explorerIndexLines(inner)
		case explorerViewDefinition:
			title, body = m.explorerDefinitionView(inner)
		default:
			title, body = m.explorerColumnLines(inner)
		}
	}
	if filter := m.explorer.filter[pane]; filter != "" {
		title += "  /" + filter
	}
	lines := []string{m.styles.header.Render(ansi.Truncate(title, inner, "…"))}
	header, items := 0, body
	if pane == explorerColumns && len(body) > 0 && m.explorerColumnsHaveHeader() {
		lines = append(lines, body[0])
		header, items = 1, body[1:]
	}
	visible := max(1, rows-1-header)
	cursor := m.explorer.cursor[pane]
	start := 0
	if cursor >= visible {
		start = cursor - visible + 1
	}
	end := min(len(items), start+visible)
	for index := start; index < end; index++ {
		line := items[index]
		if index == cursor && m.explorerPaneHasItems(pane) {
			style := m.styles.selected
			if pane != m.explorer.pane {
				style = lipgloss.NewStyle().Background(lipgloss.Color("237"))
			}
			line = style.Width(inner).Render(ansi.Strip(line))
		}
		lines = append(lines, line)
	}
	style := m.styles.panel
	if pane == m.explorer.pane {
		style = m.styles.focusedPanel
	}
	return style.Width(width).Height(height).Render(strings.Join(lines, "\n"))
}

func (m *Model) explorerPaneHasItems(pane explorerPane) bool {
	return m.explorerPaneLength(pane) > 0
}

func (m *Model) explorerColumnsHaveHeader() bool {
	switch m.explorer.view {
	case explorerViewIndexes:
		details, _ := m.explorerSelectedDetails()
		return len(details.Indexes) > 0
	case explorerViewDefinition:
		return false
	default:
		return len(m.explorerColumns()) > 0
	}
}

func (m *Model) explorerSchemaLines(width int) (string, []string) {
	schemas := m.explorerSchemas()
	title := fmt.Sprintf("Schemas (%d)", len(schemas))
	switch {
	case m.explorer.isConnecting:
		return title, []string{m.styles.dim.Render("Connecting…")}
	case m.explorer.needsConnect:
		return title, []string{m.styles.warning.Render("Password required"), m.styles.dim.Render("Enter to connect")}
	case len(schemas) == 0 && m.explorer.filter[explorerSchemas] != "":
		return title, []string{m.styles.dim.Render("No match")}
	case len(schemas) == 0:
		return title, []string{m.styles.dim.Render("Not loaded — Enter to connect")}
	}
	connection, _ := m.profileByID(m.explorer.profileID)
	lines := make([]string, len(schemas))
	for index, schema := range schemas {
		suffix := ""
		if containsSchema([]string{schema}, connection.Driver, connection.DefaultSchema) {
			suffix = "default"
		}
		lines[index] = explorerLabel(schema, suffix, width, m.styles.dim)
	}
	return title, lines
}

func (m *Model) explorerRelationLines(width int) (string, []string) {
	relations := m.explorerRelations()
	title := fmt.Sprintf("Relations (%d)", len(relations))
	schema, ok := m.explorerSelectedSchema()
	if !ok {
		return "Relations", []string{}
	}
	state := m.catalog[m.explorer.profileID]
	if _, loaded := state.relations[schema]; !loaded {
		return "Relations", []string{m.styles.dim.Render("Loading…")}
	}
	if len(relations) == 0 {
		return title, []string{m.styles.dim.Render("No tables or views")}
	}
	lines := make([]string, len(relations))
	for index, relation := range relations {
		kind := m.styles.accent.Render("T")
		if isViewRelation(relation) {
			kind = m.styles.info.Render("V")
		}
		suffix := ""
		if columns, loaded := state.columns[catalogKey(schema, relation.Name)]; loaded {
			suffix = fmt.Sprintf("%d col", len(columns))
		}
		if details, ok := m.relationDetails(schema, relation.Name); ok {
			if summary := relationSizeSummary(details); summary != "" {
				suffix = summary
			}
		}
		lines[index] = kind + " " + explorerLabel(relation.Name, suffix, width-2, m.styles.dim)
	}
	return title, lines
}

func (m *Model) explorerColumnLines(width int) (string, []string) {
	schema, hasSchema := m.explorerSelectedSchema()
	relation, hasRelation := m.explorerSelectedRelation()
	if !hasSchema || !hasRelation {
		return "Columns", []string{}
	}
	title := "Columns · " + schema + "." + relation.Name
	if _, loaded := m.catalog[m.explorer.profileID].columns[catalogKey(schema, relation.Name)]; !loaded {
		return title, []string{m.styles.dim.Render("Loading…")}
	}
	columns := m.explorerColumns()
	title += fmt.Sprintf(" (%d)", len(columns))
	if len(columns) == 0 {
		return title, []string{m.styles.dim.Render("No columns")}
	}
	widths := explorerColumnWidths(width)
	header := m.styles.dim.Render(explorerGridRow(widths, " ", "#", "name", "type", "null", "key", "default"))
	lines := []string{header}
	details, _ := m.relationDetails(schema, relation.Name)
	indexed := indexedLeadColumns(details)
	for _, column := range columns {
		marker := " "
		if m.explorer.marked[column.Name] {
			marker = "•"
		}
		null := "no"
		if column.Nullable {
			null = "yes"
		}
		row := explorerGridRow(widths, marker, fmt.Sprint(column.Ordinal), sanitizeInline(column.Name),
			formatColumnType(column), null, withIndexMarker(column.Key, indexed[column.Name]), sanitizeInline(column.Default))
		lines = append(lines, row)
	}
	return title, lines
}

// explorerColumnWidths splits the grid: marker, #, name, type, null, key, default.
func explorerColumnWidths(width int) [7]int {
	fixed := 1 + 3 + 4 + 5 + 6 // marker, #, null, key, separators
	rest := max(12, width-fixed)
	name := rest * 35 / 100
	columnType := rest * 30 / 100
	return [7]int{1, 3, name, columnType, 4, 5, max(0, rest-name-columnType)}
}

func explorerGridRow(widths [7]int, values ...string) string {
	alignments := [7]cellAlignment{alignLeft, alignRight, alignLeft, alignLeft, alignLeft, alignLeft, alignLeft}
	cells := make([]string, len(values))
	for index, value := range values {
		cells[index] = fitCell(value, widths[index], alignments[index])
	}
	return cells[0] + strings.Join(cells[1:], " ")
}

func explorerLabel(name, suffix string, width int, dim lipgloss.Style) string {
	if suffix == "" || width < len(suffix)+4 {
		return fitCell(name, width, alignLeft)
	}
	nameWidth := width - len(suffix) - 1
	return fitCell(name, nameWidth, alignLeft) + " " + dim.Render(suffix)
}

func isViewRelation(relation database.Relation) bool {
	return strings.Contains(strings.ToUpper(relation.Type), "VIEW")
}

// formatColumnType renders a column type with its length or precision when the
// catalog reports them separately.
func formatColumnType(column database.Column) string {
	value := sanitizeInline(column.Type)
	if strings.Contains(value, "(") {
		return value
	}
	switch {
	case column.HasDecimalSize:
		return fmt.Sprintf("%s(%d,%d)", value, column.Precision, column.Scale)
	case column.HasLength && column.Length > 0:
		return fmt.Sprintf("%s(%d)", value, column.Length)
	}
	return value
}

func (m *Model) renderExplorerDetail() string {
	width := max(10, m.width-2)
	if m.explorer.errorText != "" {
		return m.styles.error.Render(ansi.Truncate(m.explorer.errorText, width, "…"))
	}
	if m.explorer.needsConnect {
		return m.styles.warning.Render(ansi.Truncate("Password required — press Enter to connect", width, "…"))
	}
	if m.explorer.isConnecting {
		return m.styles.dim.Render("Connecting…")
	}
	detail := ""
	switch m.explorer.pane {
	case explorerSchemas:
		if schema, ok := m.explorerSelectedSchema(); ok {
			detail = "schema " + schema
			if relations, loaded := m.catalog[m.explorer.profileID].relations[schema]; loaded {
				tables, views := countRelationKinds(relations)
				detail += fmt.Sprintf(" · %d table(s) · %d view(s)", tables, views)
			}
		}
	case explorerRelations:
		if relation, ok := m.explorerSelectedRelation(); ok {
			schema, _ := m.explorerSelectedSchema()
			kind := "table"
			if isViewRelation(relation) {
				kind = "view"
			}
			detail = schema + "." + relation.Name + " · " + kind
			if columns, loaded := m.catalog[m.explorer.profileID].columns[catalogKey(schema, relation.Name)]; loaded {
				keys := []string{}
				for _, column := range columns {
					if strings.Contains(column.Key, "PK") {
						keys = append(keys, column.Name)
					}
				}
				detail += fmt.Sprintf(" · %d column(s)", len(columns))
				if len(keys) > 0 {
					detail += " · PK (" + strings.Join(keys, ", ") + ")"
				}
			}
			if details, ok := m.relationDetails(schema, relation.Name); ok {
				if summary := relationSizeSummary(details); summary != "" {
					detail += " · " + summary
				}
				detail += fmt.Sprintf(" · %d index(es)", len(details.Indexes))
			}
		}
	default:
		if column, ok := m.explorerSelectedColumn(); ok {
			detail = formatColumnDetail(column)
		}
	}
	return m.styles.info.Render(ansi.Truncate(detail, width, "…"))
}

func formatColumnDetail(column database.Column) string {
	parts := []string{sanitizeInline(column.Name), formatColumnType(column)}
	if column.Nullable {
		parts = append(parts, "NULL")
	} else {
		parts = append(parts, "NOT NULL")
	}
	if strings.Contains(column.Key, "PK") {
		parts = append(parts, "PK")
	}
	if column.ReferenceTable != "" {
		reference := column.ReferenceTable + "(" + column.ReferenceColumn + ")"
		if column.ReferenceSchema != "" {
			reference = column.ReferenceSchema + "." + reference
		}
		parts = append(parts, "FK → "+reference)
	}
	defaultValue := "—"
	if column.Default != "" {
		defaultValue = sanitizeInline(column.Default)
	}
	return strings.Join(append(parts, "default "+defaultValue), " · ")
}

func countRelationKinds(relations []database.Relation) (int, int) {
	views := 0
	for _, relation := range relations {
		if isViewRelation(relation) {
			views++
		}
	}
	return len(relations) - views, views
}

func (m *Model) renderExplorerFilterBar() string {
	pane := m.explorer.pane
	input := m.explorer.filterInput.View()
	if m.mode != modeExplorerFilter {
		input = "Filter: " + m.explorer.filter[pane]
	}
	parts := []string{input}
	if count := m.explorerPaneLength(pane); count == 0 {
		parts = append(parts, m.styles.error.Render("no matches"))
	} else {
		parts = append(parts, m.styles.accent.Render(fmt.Sprintf("%d match(es)", count)))
	}
	help := "Enter/Esc keep filter  ↑/↓ move"
	if m.mode != modeExplorerFilter {
		help = "/ edit filter  Esc clear"
	}
	parts = append(parts, m.styles.dim.Render(help))
	return ansi.Truncate(strings.Join(parts, "   "), max(10, m.width-2), "…")
}
