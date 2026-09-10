package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/lucasfguimares/charta-tui/internal/activity"
	"github.com/lucasfguimares/charta-tui/internal/database"
)

const (
	columnStatisticsTTL        = time.Minute
	columnStatisticsCacheLimit = 128
	localDistinctLimit         = 10_000
	maximumStatisticsTimeout   = 10 * time.Second
)

type columnCacheKey struct {
	connectionID string
	database     string
	schema       string
	table        string
	column       string
}

type localColumnStatistics struct {
	loadedRows       int
	nonNullCount     int
	nullCount        int
	emptyStringCount int
	distinctCount    int
	hasDistinctCount bool
	lengthCount      int
	minimumLength    int
	maximumLength    int
	totalLength      int64
	isTruncated      bool
}

type columnStatisticsCacheEntry struct {
	statistics database.ColumnStatistics
	loadedAt   time.Time
}

type columnInspectorModel struct {
	tabID               int
	requestID           uint64
	columnIndex         int
	column              database.ResultColumn
	metadata            *database.Column
	local               localColumnStatistics
	exact               *database.ColumnStatistics
	exactLoadedAt       time.Time
	isMetadataLoading   bool
	isStatisticsLoading bool
	metadataError       string
	statisticsError     string
	viewport            viewport.Model
	cancelStatistics    context.CancelFunc
}

type columnMetadataLoadedMsg struct {
	tabID     int
	requestID uint64
	key       columnCacheKey
	columns   []database.Column
	err       error
}

type columnStatisticsLoadedMsg struct {
	tabID      int
	requestID  uint64
	key        columnCacheKey
	statistics database.ColumnStatistics
	err        error
}

func (m *Model) openColumnInspector(tab *queryTab) tea.Cmd {
	column, ok := tab.table.SelectedColumn()
	if !ok {
		return nil
	}
	m.cancelColumnStatistics()
	m.columnInspectorRequestID++
	inspector := columnInspectorModel{
		tabID:       tab.id,
		requestID:   m.columnInspectorRequestID,
		columnIndex: tab.table.cursorColumn,
		column:      column,
		local:       calculateLocalColumnStatistics(tab.table.result, tab.table.cursorColumn),
		viewport:    viewport.New(),
	}
	key := columnKeyFor(tab.connection.ID, column)
	if cached, ok := m.columnStatisticsCache[key]; ok && time.Since(cached.loadedAt) <= columnStatisticsTTL {
		statistics := cached.statistics
		inspector.exact = &statistics
		inspector.exactLoadedAt = cached.loadedAt
	}
	metadata, isMetadataCached := m.cachedColumnMetadata(tab.connection.ID, column)
	if isMetadataCached {
		inspector.metadata = &metadata
	} else if key.hasSource() && m.inspector != nil {
		inspector.isMetadataLoading = true
	}
	m.columnInspector = inspector
	m.mode = modeColumnInspector
	m.resizeColumnInspector()
	m.refreshColumnInspectorContent()
	if inspector.isMetadataLoading {
		return m.loadColumnMetadataCmd(tab, key, inspector.requestID)
	}
	return nil
}

func (m *Model) renderColumnInspector() string {
	width := min(100, max(40, m.width-10))
	header := m.styles.header.Render(resultColumnTitle(m.columnInspector.column))
	position := m.styles.dim.Render(fmt.Sprintf(
		"Column %d of %d",
		m.columnInspector.columnIndex+1,
		m.columnCountForInspector(),
	))
	footer := m.styles.dim.Render("←/→ column  ↑/↓/PgUp/PgDn scroll  s exact stats  r refresh  c copy  Esc close")
	content := strings.Join([]string{header, position, "", m.columnInspector.viewport.View(), footer}, "\n")
	return m.center(m.styles.modal.Width(width).Render(content))
}

func (m *Model) handleColumnInspectorKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	switch key {
	case "esc":
		m.cancelColumnStatistics()
		m.mode = modeWorkspace
		return nil
	case "left":
		return m.navigateColumnInspector(-1)
	case "right":
		return m.navigateColumnInspector(1)
	case "home":
		return m.navigateColumnInspectorTo(0)
	case "end":
		return m.navigateColumnInspectorTo(m.columnCountForInspector() - 1)
	case "s":
		return m.loadColumnStatistics(false)
	case "r":
		return m.refreshColumnInspector()
	case "c":
		value := columnInspectorClipboard(m.columnInspector.column)
		if value == "" {
			return nil
		}
		if tab := m.currentTab(); tab != nil {
			tab.statusText = "Column identifier copied"
		}
		return tea.SetClipboard(value)
	case "ctrl+c", "ctrl+g":
		if m.columnInspector.isStatisticsLoading {
			m.cancelColumnStatistics()
			m.columnInspector.statisticsError = "Exact statistics cancelled"
			m.refreshColumnInspectorContent()
		}
		return nil
	}
	updated, cmd := m.columnInspector.viewport.Update(msg)
	m.columnInspector.viewport = updated
	return cmd
}

func (m *Model) navigateColumnInspector(delta int) tea.Cmd {
	return m.navigateColumnInspectorTo(m.columnInspector.columnIndex + delta)
}

func (m *Model) navigateColumnInspectorTo(column int) tea.Cmd {
	tab := m.currentTab()
	if tab == nil || len(tab.table.result.Columns) == 0 {
		return nil
	}
	column = min(max(0, column), len(tab.table.result.Columns)-1)
	if column == m.columnInspector.columnIndex {
		return nil
	}
	tab.table.cursorColumn = column
	tab.table.selectionScope = resultSelectionColumn
	tab.table.ensureSelectionVisible()
	return m.openColumnInspector(tab)
}

func (m *Model) refreshColumnInspector() tea.Cmd {
	tab := m.currentTab()
	if tab == nil {
		return nil
	}
	key := columnKeyFor(tab.connection.ID, m.columnInspector.column)
	delete(m.columnStatisticsCache, key)
	state := m.ensureCatalog(tab.connection.ID)
	delete(state.columns, catalogKey(key.schema, key.table))
	hadExact := m.columnInspector.exact != nil
	cmd := m.openColumnInspector(tab)
	if hadExact {
		return tea.Batch(cmd, m.loadColumnStatistics(true))
	}
	return cmd
}

func (m *Model) loadColumnStatistics(force bool) tea.Cmd {
	tab := m.currentTab()
	if tab == nil || m.inspector == nil {
		return nil
	}
	key := columnKeyFor(tab.connection.ID, m.columnInspector.column)
	if !key.hasSource() {
		m.columnInspector.statisticsError = "Exact statistics require a proven source table and column"
		m.refreshColumnInspectorContent()
		return nil
	}
	if !force {
		if cached, ok := m.columnStatisticsCache[key]; ok && time.Since(cached.loadedAt) <= columnStatisticsTTL {
			statistics := cached.statistics
			m.columnInspector.exact = &statistics
			m.columnInspector.exactLoadedAt = cached.loadedAt
			m.columnInspector.statisticsError = ""
			m.refreshColumnInspectorContent()
			return nil
		}
	}
	m.cancelColumnStatistics()
	timeout := time.Duration(tab.connection.QueryTimeoutSeconds) * time.Second
	if timeout <= 0 || timeout > maximumStatisticsTimeout {
		timeout = maximumStatisticsTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	m.columnInspector.cancelStatistics = cancel
	m.columnInspector.isStatisticsLoading = true
	m.columnInspector.statisticsError = ""
	m.refreshColumnInspectorContent()
	tabID, requestID := tab.id, m.columnInspector.requestID
	connection, column := tab.connection, m.columnInspector.column
	return func() tea.Msg {
		statistics, err := m.inspector.Statistics(ctx, connection, column)
		cancel()
		return columnStatisticsLoadedMsg{
			tabID: tabID, requestID: requestID, key: key, statistics: statistics, err: err,
		}
	}
}

func (m *Model) loadColumnMetadataCmd(tab *queryTab, key columnCacheKey, requestID uint64) tea.Cmd {
	connection := tab.connection
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		columns, err := m.inspector.Columns(ctx, connection, key.schema, key.table)
		return columnMetadataLoadedMsg{
			tabID: tab.id, requestID: requestID, key: key, columns: columns, err: err,
		}
	}
}

func (m *Model) handleColumnMetadataLoaded(msg columnMetadataLoadedMsg) tea.Cmd {
	if m.mode != modeColumnInspector || m.columnInspector.tabID != msg.tabID ||
		m.columnInspector.requestID != msg.requestID ||
		columnKeyFor(m.connectionIDForTab(msg.tabID), m.columnInspector.column) != msg.key {
		return nil
	}
	m.columnInspector.isMetadataLoading = false
	if msg.err != nil {
		m.columnInspector.metadataError = "Metadata unavailable: " + activity.Redact(msg.err.Error())
		m.refreshColumnInspectorContent()
		return nil
	}
	state := m.ensureCatalog(msg.key.connectionID)
	state.columns[catalogKey(msg.key.schema, msg.key.table)] = msg.columns
	if metadata, ok := findColumnMetadata(msg.columns, msg.key.column); ok {
		m.columnInspector.metadata = &metadata
	}
	m.columnInspector.metadataError = ""
	m.refreshColumnInspectorContent()
	return nil
}

func (m *Model) handleColumnStatisticsLoaded(msg columnStatisticsLoadedMsg) tea.Cmd {
	if m.mode != modeColumnInspector || m.columnInspector.tabID != msg.tabID ||
		m.columnInspector.requestID != msg.requestID ||
		columnKeyFor(m.connectionIDForTab(msg.tabID), m.columnInspector.column) != msg.key {
		return nil
	}
	m.columnInspector.isStatisticsLoading = false
	m.columnInspector.cancelStatistics = nil
	if msg.err != nil {
		switch {
		case errors.Is(msg.err, context.Canceled):
			m.columnInspector.statisticsError = "Exact statistics cancelled"
		case errors.Is(msg.err, context.DeadlineExceeded):
			m.columnInspector.statisticsError = "Exact statistics timed out"
		default:
			m.columnInspector.statisticsError = "Exact statistics failed: " + activity.Redact(msg.err.Error())
		}
		m.refreshColumnInspectorContent()
		return nil
	}
	loadedAt := time.Now()
	m.storeColumnStatistics(msg.key, columnStatisticsCacheEntry{statistics: msg.statistics, loadedAt: loadedAt})
	statistics := msg.statistics
	m.columnInspector.exact = &statistics
	m.columnInspector.exactLoadedAt = loadedAt
	m.columnInspector.statisticsError = ""
	m.refreshColumnInspectorContent()
	return nil
}

func (m *Model) refreshColumnInspectorContent() {
	inspector := &m.columnInspector
	column := inspector.column
	metadata := inspector.metadata
	columnType := column.DatabaseType
	nullable, nullableKnown := column.Nullable, column.NullableKnown
	key := column.Key
	if metadata != nil {
		if metadata.Type != "" {
			columnType = metadata.Type
		}
		nullable, nullableKnown = metadata.Nullable, true
		if metadata.Key != "" {
			key = metadata.Key
		}
	}
	lines := []string{
		"Type: " + firstNonEmptyText(columnType, "unknown"),
		"Nullable: " + nullableLabel(nullable, nullableKnown),
		"PK: " + yesNo(strings.Contains(key, "PK")),
	}
	referenceSchema, referenceTable, referenceColumn := column.ReferenceSchema, column.ReferenceTable, column.ReferenceColumn
	if metadata != nil && metadata.ReferenceTable != "" {
		referenceSchema, referenceTable, referenceColumn = metadata.ReferenceSchema, metadata.ReferenceTable, metadata.ReferenceColumn
	}
	if referenceTable != "" {
		reference := referenceTable + "." + referenceColumn
		if referenceSchema != "" {
			reference = referenceSchema + "." + reference
		}
		lines = append(lines, "FK: "+reference)
	} else {
		lines = append(lines, "FK: NO")
	}
	if source := columnInspectorClipboard(column); source != "" && source != column.Name {
		lines = append(lines, "Source: "+source)
	} else if column.SourceTable == "" {
		lines = append(lines, "Source: derived or unknown")
	}
	hasLength, length := column.HasLength, column.Length
	hasDecimalSize, precision, scale := column.HasDecimalSize, column.Precision, column.Scale
	if metadata != nil {
		if metadata.HasLength {
			hasLength, length = true, metadata.Length
		}
		if metadata.HasDecimalSize {
			hasDecimalSize, precision, scale = true, metadata.Precision, metadata.Scale
		}
	}
	if hasLength {
		lines = append(lines, "Length: "+strconv.FormatInt(length, 10))
	}
	if hasDecimalSize {
		lines = append(lines, fmt.Sprintf("Precision: %d  Scale: %d", precision, scale))
	}
	if metadata != nil && metadata.Default != "" {
		lines = append(lines, "Default: "+sanitizeInline(metadata.Default))
	}
	if inspector.isMetadataLoading {
		lines = append(lines, "Metadata: loading…")
	} else if inspector.metadataError != "" {
		lines = append(lines, inspector.metadataError)
	}

	local := inspector.local
	scope := fmt.Sprintf("Loaded result (%s row(s)", formatCount(local.loadedRows))
	if local.isTruncated {
		scope += ", truncated"
	}
	lines = append(lines, "", scope+")", "NULL: "+formatCount(local.nullCount), "Non-NULL: "+formatCount(local.nonNullCount))
	if local.hasDistinctCount {
		lines = append(lines, "Distinct: "+formatCount(local.distinctCount))
	} else {
		lines = append(lines, fmt.Sprintf("Distinct: not computed above %s loaded rows", formatCount(localDistinctLimit)))
	}
	if local.emptyStringCount > 0 {
		lines = append(lines, "Empty strings: "+formatCount(local.emptyStringCount))
	}
	if local.lengthCount > 0 {
		average := float64(local.totalLength) / float64(local.lengthCount)
		lines = append(lines, fmt.Sprintf("Value length: min %d  max %d  avg %.1f", local.minimumLength, local.maximumLength, average))
	}

	lines = append(lines, "", "Exact source statistics")
	switch {
	case inspector.isStatisticsLoading:
		lines = append(lines, "Loading… Ctrl+C/Ctrl+G cancels")
	case inspector.statisticsError != "":
		lines = append(lines, inspector.statisticsError)
	case inspector.exact != nil:
		lines = append(lines,
			"Rows: "+formatCount64(inspector.exact.TotalRows),
			"NULL: "+formatCount64(inspector.exact.NullCount),
		)
		if inspector.exact.HasDistinctCount {
			lines = append(lines, "Distinct: "+formatCount64(inspector.exact.DistinctCount))
		} else {
			lines = append(lines, "Distinct: unsupported for this database type")
		}
		lines = append(lines,
			"Duration: "+formatDuration(inspector.exact.Duration),
			"Loaded: "+inspector.exactLoadedAt.Local().Format("15:04:05"),
		)
	default:
		lines = append(lines, "Press s to scan the source relation (may be expensive).")
	}
	inspector.viewport.SetContent(strings.Join(lines, "\n"))
}

func calculateLocalColumnStatistics(result database.Result, column int) localColumnStatistics {
	statistics := localColumnStatistics{loadedRows: len(result.Rows), isTruncated: result.IsTruncated}
	distinct := map[string]struct{}{}
	statistics.hasDistinctCount = len(result.Rows) <= localDistinctLimit
	for _, row := range result.Rows {
		if column >= len(row) || row[column] == nil {
			statistics.nullCount++
			continue
		}
		value := row[column]
		statistics.nonNullCount++
		if text, ok := value.(string); ok && text == "" {
			statistics.emptyStringCount++
		}
		if statistics.hasDistinctCount {
			cell := formatResultValue(value, result.Columns[column])
			distinct[fmt.Sprintf("%T\x00%s", value, cell.clipboard)] = struct{}{}
		}
		length, hasLength := columnValueLength(value)
		if hasLength {
			if statistics.lengthCount == 0 {
				statistics.minimumLength = length
			}
			statistics.minimumLength = min(statistics.minimumLength, length)
			statistics.maximumLength = max(statistics.maximumLength, length)
			statistics.totalLength += int64(length)
			statistics.lengthCount++
		}
	}
	if statistics.hasDistinctCount {
		statistics.distinctCount = len(distinct)
	}
	return statistics
}

func columnValueLength(value any) (int, bool) {
	switch typed := value.(type) {
	case string:
		return len([]rune(typed)), true
	case []byte:
		return len(typed), true
	default:
		return 0, false
	}
}

func (m *Model) cachedColumnMetadata(connectionID string, column database.ResultColumn) (database.Column, bool) {
	if column.SourceTable == "" || column.SourceColumn == "" {
		return database.Column{}, false
	}
	state, ok := m.catalog[connectionID]
	if !ok {
		return database.Column{}, false
	}
	return findColumnMetadata(state.columns[catalogKey(column.SourceSchema, column.SourceTable)], column.SourceColumn)
}

func findColumnMetadata(columns []database.Column, name string) (database.Column, bool) {
	for _, column := range columns {
		if column.Name == name {
			return column, true
		}
	}
	return database.Column{}, false
}

func columnKeyFor(connectionID string, column database.ResultColumn) columnCacheKey {
	return columnCacheKey{
		connectionID: connectionID,
		database:     column.SourceDatabase,
		schema:       column.SourceSchema,
		table:        column.SourceTable,
		column:       column.SourceColumn,
	}
}

func (key columnCacheKey) hasSource() bool {
	return key.connectionID != "" && key.table != "" && key.column != ""
}

func columnInspectorClipboard(column database.ResultColumn) string {
	if column.SourceTable == "" || column.SourceColumn == "" {
		return column.Name
	}
	parts := []string{}
	if column.SourceDatabase != "" {
		parts = append(parts, column.SourceDatabase)
	}
	if column.SourceSchema != "" {
		parts = append(parts, column.SourceSchema)
	}
	parts = append(parts, column.SourceTable, column.SourceColumn)
	return strings.Join(parts, ".")
}

func nullableLabel(nullable, known bool) string {
	if !known {
		return "UNKNOWN"
	}
	if nullable {
		return "YES"
	}
	return "NO"
}

func yesNo(value bool) string {
	if value {
		return "YES"
	}
	return "NO"
}

func firstNonEmptyText(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func formatCount64(value int64) string {
	digits := strconv.FormatInt(value, 10)
	for index := len(digits) - 3; index > 0; index -= 3 {
		digits = digits[:index] + "," + digits[index:]
	}
	return digits
}

func (m *Model) resizeColumnInspector() {
	width := min(100, max(40, m.width-10))
	m.columnInspector.viewport.SetWidth(max(20, width-6))
	m.columnInspector.viewport.SetHeight(max(3, m.height-11))
}

func (m *Model) cancelColumnStatistics() {
	if m.columnInspector.cancelStatistics != nil {
		m.columnInspector.cancelStatistics()
		m.columnInspector.cancelStatistics = nil
	}
	m.columnInspector.isStatisticsLoading = false
}

func (m *Model) storeColumnStatistics(key columnCacheKey, entry columnStatisticsCacheEntry) {
	if m.columnStatisticsCache == nil {
		m.columnStatisticsCache = map[columnCacheKey]columnStatisticsCacheEntry{}
	}
	if _, replacing := m.columnStatisticsCache[key]; !replacing && len(m.columnStatisticsCache) >= columnStatisticsCacheLimit {
		var oldestKey columnCacheKey
		oldestTime := time.Now()
		for candidate, cached := range m.columnStatisticsCache {
			if cached.loadedAt.Before(oldestTime) {
				oldestKey, oldestTime = candidate, cached.loadedAt
			}
		}
		delete(m.columnStatisticsCache, oldestKey)
	}
	m.columnStatisticsCache[key] = entry
}

func (m *Model) invalidateColumnCaches(connectionID string) {
	for key := range m.columnStatisticsCache {
		if key.connectionID == connectionID {
			delete(m.columnStatisticsCache, key)
		}
	}
}

func (m *Model) connectionIDForTab(tabID int) string {
	if tab := m.tabByID(tabID); tab != nil {
		return tab.connection.ID
	}
	return ""
}

func (m *Model) columnCountForInspector() int {
	if tab := m.tabByID(m.columnInspector.tabID); tab != nil {
		return len(tab.table.result.Columns)
	}
	return 0
}
