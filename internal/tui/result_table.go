package tui

import (
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/lucasfguimares/tui-db/internal/database"
)

const (
	resultColumnSeparator = " │ "
	resultFrozenDivider   = " ║ "
)

type resultState int

const (
	resultIdle resultState = iota
	resultRunning
	resultEmpty
	resultReady
	resultPartial
	resultCancelled
	resultFailed
)

type resultTableConfig struct {
	MinimumColumnWidth int
	MaximumColumnWidth int
}

type resultSelectionScope int

const (
	resultSelectionCell resultSelectionScope = iota
	resultSelectionColumn
)

type visibleResultColumn struct {
	index      int
	width      int
	isFrozen   bool
	hasDivider bool
}

type resultColumnLayout struct {
	columns     []visibleResultColumn
	scrollStart int
	scrollEnd   int
}

func (layout resultColumnLayout) contains(columnIndex int) bool {
	for _, column := range layout.columns {
		if column.index == columnIndex {
			return true
		}
	}
	return false
}

func defaultResultTableConfig(maximumColumnWidth int) resultTableConfig {
	if maximumColumnWidth <= 0 {
		maximumColumnWidth = 40
	}
	return resultTableConfig{
		MinimumColumnWidth: 8,
		MaximumColumnWidth: maximumColumnWidth,
	}
}

type resultTableModel struct {
	config resultTableConfig
	result database.Result
	state  resultState
	error  database.ErrorDetails

	columnWidths      []int
	cursorRow         int
	cursorColumn      int
	rowOffset         int
	columnOffset      int
	frozenColumnCount int
	selectionScope    resultSelectionScope
	width             int
	height            int
	focused           bool
	startedAt         time.Time
	layoutTime        time.Duration

	headerStyle       lipgloss.Style
	activeHeaderStyle lipgloss.Style
	activeRowStyle    lipgloss.Style
	activeCellStyle   lipgloss.Style
	dimStyle          lipgloss.Style
	errorStyle        lipgloss.Style
	nullStyle         lipgloss.Style
}

func newResultTable(maximumColumnWidth int) *resultTableModel {
	return &resultTableModel{
		config:            defaultResultTableConfig(maximumColumnWidth),
		state:             resultIdle,
		columnWidths:      []int{},
		headerStyle:       lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("24")),
		activeHeaderStyle: lipgloss.NewStyle().Bold(true).Underline(true).Foreground(lipgloss.Color("229")).Background(lipgloss.Color("24")),
		activeRowStyle:    lipgloss.NewStyle().Background(lipgloss.Color("235")),
		activeCellStyle:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("24")),
		dimStyle:          lipgloss.NewStyle().Foreground(lipgloss.Color("242")),
		errorStyle:        lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203")),
		nullStyle:         lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("214")),
	}
}

func (table *resultTableModel) SetSize(width, height int) {
	table.width = max(1, width)
	table.height = max(1, height)
	table.clampSelection()
	table.clampFrozenColumns()
	table.ensureSelectionVisible()
}

func (table *resultTableModel) Focus() {
	table.focused = true
}

func (table *resultTableModel) Blur() {
	table.focused = false
}

func (table *resultTableModel) SetRunning(startedAt time.Time) {
	table.state = resultRunning
	table.startedAt = startedAt
	table.error = database.ErrorDetails{}
}

func (table *resultTableModel) SetResult(result database.Result) {
	started := time.Now()
	columnWidths := calculateResultColumnWidths(result, table.config)
	table.SetResultWithLayout(result, columnWidths, time.Since(started))
}

func (table *resultTableModel) SetResultWithLayout(
	result database.Result,
	columnWidths []int,
	layoutTime time.Duration,
) {
	preserveFrozenColumns := sameResultColumns(table.result.Columns, result.Columns)
	table.result = result
	table.error = database.ErrorDetails{}
	table.state = resultReady
	if len(result.Columns) == 0 || len(result.Rows) == 0 {
		table.state = resultEmpty
	}
	if result.IsTruncated {
		table.state = resultPartial
	}
	table.columnWidths = append(table.columnWidths[:0], columnWidths...)
	if !preserveFrozenColumns {
		table.frozenColumnCount = 0
		table.selectionScope = resultSelectionCell
	}
	table.clampSelection()
	table.clampFrozenColumns()
	table.ensureSelectionVisible()
	table.layoutTime = layoutTime
}

func (table *resultTableModel) SetError(
	result database.Result,
	details database.ErrorDetails,
) {
	started := time.Now()
	columnWidths := calculateResultColumnWidths(result, table.config)
	table.SetErrorWithLayout(result, details, columnWidths, time.Since(started))
}

func (table *resultTableModel) SetErrorWithLayout(
	result database.Result,
	details database.ErrorDetails,
	columnWidths []int,
	layoutTime time.Duration,
) {
	table.result = result
	table.error = details
	if details.Cancelled {
		table.state = resultCancelled
	} else {
		table.state = resultFailed
	}
	table.columnWidths = append(table.columnWidths[:0], columnWidths...)
	table.clampSelection()
	table.ensureSelectionVisible()
	table.layoutTime = layoutTime
}

func (table *resultTableModel) HandleNavigation(key string) bool {
	if len(table.result.Columns) == 0 {
		return false
	}
	previousRow, previousColumn, previousScope := table.cursorRow, table.cursorColumn, table.selectionScope
	switch key {
	case "up", "w", "W", "shift+w":
		if table.selectionScope == resultSelectionColumn {
			return true
		}
		if table.cursorRow == 0 {
			table.selectionScope = resultSelectionColumn
		} else {
			table.cursorRow--
		}
	case "down", "s", "S", "shift+s":
		if table.selectionScope == resultSelectionColumn {
			table.selectionScope = resultSelectionCell
		} else {
			table.cursorRow++
		}
	case "left", "a", "A", "shift+a":
		table.cursorColumn--
	case "right", "d", "D", "shift+d":
		table.cursorColumn++
	case "pgup":
		if table.selectionScope == resultSelectionColumn {
			return true
		}
		table.cursorRow -= max(1, table.bodyHeight())
	case "pgdown":
		if table.selectionScope == resultSelectionColumn {
			table.selectionScope = resultSelectionCell
		} else {
			table.cursorRow += max(1, table.bodyHeight())
		}
	case "home":
		table.cursorColumn = 0
	case "end":
		table.cursorColumn = len(table.result.Columns) - 1
	case "ctrl+home":
		table.cursorRow = 0
		table.selectionScope = resultSelectionCell
	case "ctrl+end":
		table.cursorRow = max(0, len(table.result.Rows)-1)
		table.selectionScope = resultSelectionCell
	default:
		return false
	}
	table.clampSelection()
	table.ensureSelectionVisible()
	return table.cursorRow != previousRow || table.cursorColumn != previousColumn || table.selectionScope != previousScope
}

func (table *resultTableModel) IsColumnSelected() bool {
	return table.selectionScope == resultSelectionColumn
}

func (table *resultTableModel) ToggleFrozenColumns() bool {
	if !table.IsColumnSelected() || table.cursorColumn < 0 || table.cursorColumn >= len(table.result.Columns) {
		return false
	}
	next := table.cursorColumn + 1
	if table.cursorColumn < table.frozenColumnCount {
		next = table.cursorColumn
	}
	if !table.canFreeze(next) {
		return false
	}
	table.frozenColumnCount = next
	table.clampSelection()
	table.ensureSelectionVisible()
	return true
}

func (table *resultTableModel) ClearFrozenColumns() bool {
	if table.frozenColumnCount == 0 {
		return false
	}
	table.frozenColumnCount = 0
	table.columnOffset = 0
	table.ensureSelectionVisible()
	return true
}

func (table *resultTableModel) Search(query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	rowCount, columnCount := len(table.result.Rows), len(table.result.Columns)
	if query == "" || rowCount == 0 || columnCount == 0 {
		return false
	}
	total := rowCount * columnCount
	start := table.cursorRow*columnCount + table.cursorColumn
	for step := 1; step <= total; step++ {
		position := (start + step) % total
		row, column := position/columnCount, position%columnCount
		if column >= len(table.result.Rows[row]) {
			continue
		}
		cell := formatResultValue(table.result.Rows[row][column], table.result.Columns[column])
		if strings.Contains(strings.ToLower(cell.detail), query) {
			table.cursorRow, table.cursorColumn = row, column
			table.ensureSelectionVisible()
			return true
		}
	}
	return false
}

func (table *resultTableModel) GoToRow(row int) bool {
	if row < 1 || row > len(table.result.Rows) {
		return false
	}
	table.cursorRow = row - 1
	table.ensureSelectionVisible()
	return true
}

func (table *resultTableModel) SelectedCell() (formattedCell, bool) {
	if table.cursorRow < 0 || table.cursorRow >= len(table.result.Rows) ||
		table.cursorColumn < 0 || table.cursorColumn >= len(table.result.Columns) ||
		table.cursorColumn >= len(table.result.Rows[table.cursorRow]) {
		return formattedCell{}, false
	}
	return formatResultValue(
		table.result.Rows[table.cursorRow][table.cursorColumn],
		table.result.Columns[table.cursorColumn],
	), true
}

func (table *resultTableModel) SelectedColumn() (database.ResultColumn, bool) {
	if table.cursorColumn < 0 || table.cursorColumn >= len(table.result.Columns) {
		return database.ResultColumn{}, false
	}
	return table.result.Columns[table.cursorColumn], true
}

func (table *resultTableModel) SelectedRow() ([]database.ResultColumn, []any, int, bool) {
	if table.cursorRow < 0 || table.cursorRow >= len(table.result.Rows) {
		return nil, nil, 0, false
	}
	return table.result.Columns, table.result.Rows[table.cursorRow], table.cursorRow, true
}

func (table *resultTableModel) CellClipboard() (string, bool) {
	cell, ok := table.SelectedCell()
	return cell.clipboard, ok
}

func (table *resultTableModel) RowClipboard() (string, bool) {
	if table.cursorRow < 0 || table.cursorRow >= len(table.result.Rows) {
		return "", false
	}
	record := make([]string, len(table.result.Columns))
	for column := range table.result.Columns {
		if column >= len(table.result.Rows[table.cursorRow]) {
			continue
		}
		record[column] = formatResultValue(
			table.result.Rows[table.cursorRow][column],
			table.result.Columns[column],
		).clipboard
	}
	var output strings.Builder
	writer := csv.NewWriter(&output)
	writer.Comma = '\t'
	if err := writer.Write(record); err != nil {
		return "", false
	}
	writer.Flush()
	return strings.TrimSuffix(output.String(), "\n"), writer.Error() == nil
}

func (table *resultTableModel) View() string {
	switch table.state {
	case resultRunning:
		return table.renderRunning()
	case resultFailed, resultCancelled:
		return table.renderError()
	case resultIdle:
		return table.dimStyle.Render("Run a query to see results.")
	}
	if len(table.result.Columns) == 0 {
		return table.renderCommandResult()
	}
	return table.renderGrid()
}

func (table *resultTableModel) renderGrid() string {
	layout := table.columnLayout()
	lines := make([]string, 0, table.height)
	lines = append(lines, table.renderHeader(layout))

	bodyHeight := table.bodyHeight()
	startRow := min(table.rowOffset, len(table.result.Rows))
	endRow := min(len(table.result.Rows), startRow+bodyHeight)
	for row := startRow; row < endRow; row++ {
		lines = append(lines, table.renderRow(row, layout))
	}
	for range max(0, bodyHeight-(endRow-startRow)) {
		lines = append(lines, "")
	}

	if table.showColumnDetail() {
		lines = append(lines, table.renderColumnDetail())
	}
	if table.showCellDetail() {
		lines = append(lines, table.renderCellDetail())
	}
	if table.showShortcuts() {
		lines = append(lines, table.renderShortcuts())
	}
	lines = append(lines, strings.Split(table.renderFooter(), "\n")...)
	return strings.Join(lines, "\n")
}

func (table *resultTableModel) renderHeader(layout resultColumnLayout) string {
	marker := "  "
	if table.focused && table.IsColumnSelected() {
		marker = "> "
	}
	var line strings.Builder
	line.WriteString(table.headerStyle.Render(marker))
	for position, visible := range layout.columns {
		if position > 0 {
			separator := resultColumnSeparator
			if visible.hasDivider {
				separator = resultFrozenDivider
			}
			line.WriteString(table.headerStyle.Render(separator))
		}
		title := resultColumnTitle(table.result.Columns[visible.index])
		style := table.headerStyle
		if table.focused && table.IsColumnSelected() && visible.index == table.cursorColumn {
			style = table.activeHeaderStyle
		}
		line.WriteString(style.Render(fitCell(title, visible.width, alignLeft)))
	}
	return line.String()
}

func (table *resultTableModel) renderRow(row int, layout resultColumnLayout) string {
	cells := make([]string, 0, len(layout.columns))
	for _, visible := range layout.columns {
		column := visible.index
		var cell formattedCell
		if column < len(table.result.Rows[row]) {
			cell = formatResultValue(table.result.Rows[row][column], table.result.Columns[column])
		}
		width := visible.width
		isActiveCell := table.focused && !table.IsColumnSelected() && row == table.cursorRow && column == table.cursorColumn
		value := fitCell(cell.display, width, cell.alignment)
		if isActiveCell && width >= 2 {
			value = "[" + fitCell(cell.display, width-2, cell.alignment) + "]"
			value = table.activeCellStyle.Render(value)
		} else if cell.isNull {
			value = table.nullStyle.Render(value)
		} else if row == table.cursorRow && !table.IsColumnSelected() {
			value = table.activeRowStyle.Render(value)
		}
		separator := ""
		if len(cells) > 0 {
			separator = resultColumnSeparator
			if visible.hasDivider {
				separator = resultFrozenDivider
			}
			if row == table.cursorRow && !table.IsColumnSelected() {
				separator = table.activeRowStyle.Render(separator)
			}
		}
		cells = append(cells, separator+value)
	}
	marker := "  "
	if row == table.cursorRow && !table.IsColumnSelected() {
		marker = "> "
	}
	return marker + strings.Join(cells, "")
}

func (table *resultTableModel) renderColumnDetail() string {
	column, ok := table.SelectedColumn()
	if !ok {
		return ""
	}
	details := []string{"Column: " + resultColumnTitle(column)}
	if column.DatabaseType != "" {
		details = append(details, column.DatabaseType)
	}
	if column.NullableKnown {
		if column.Nullable {
			details = append(details, "NULL")
		} else {
			details = append(details, "NOT NULL")
		}
	} else {
		details = append(details, "nullability unknown")
	}
	if column.SourceTable != "" {
		source := column.SourceTable + "." + column.SourceColumn
		if column.SourceSchema != "" {
			source = column.SourceSchema + "." + source
		}
		details = append(details, "source "+source)
	}
	if column.ReferenceTable != "" {
		reference := column.ReferenceTable + "." + column.ReferenceColumn
		if column.ReferenceSchema != "" {
			reference = column.ReferenceSchema + "." + reference
		}
		details = append(details, "FK -> "+reference)
	}
	if column.HasLength {
		details = append(details, "length "+strconv.FormatInt(column.Length, 10))
	}
	if column.HasDecimalSize {
		details = append(details, fmt.Sprintf("precision %d scale %d", column.Precision, column.Scale))
	}
	if column.Ordinal > 0 {
		details = append(details, "position "+strconv.Itoa(column.Ordinal))
	}
	return table.dimStyle.Render(ansi.Truncate(sanitizeInline(strings.Join(details, " | ")), table.width, "…"))
}

func (table *resultTableModel) renderCellDetail() string {
	cell, ok := table.SelectedCell()
	if !ok {
		return table.dimStyle.Render("Value: —")
	}
	return table.dimStyle.Render(ansi.Truncate("Value: "+sanitizeInline(cell.detail), table.width, "…"))
}

func (table *resultTableModel) renderShortcuts() string {
	shortcuts := "Arrows/WASD move  Enter detail  v row  c cell  C row  f find  g row  ? help"
	if table.IsColumnSelected() {
		shortcuts = "←/→ column  Enter inspect  Space freeze  u unfreeze  ↓ rows  ? help"
	}
	return table.dimStyle.Render(ansi.Truncate(shortcuts, table.width, "…"))
}

func (table *resultTableModel) renderFooter() string {
	rowStart, rowEnd := table.visibleRowRange()
	layout := table.columnLayout()
	rowCount := formatCount(len(table.result.Rows))
	if table.result.IsTruncated {
		rowCount += "+"
	}
	status := "Ready"
	if table.state == resultPartial {
		status = "Partial"
	} else if table.state == resultEmpty {
		status = "Empty"
	} else if table.state == resultFailed {
		status = "Failed"
	} else if table.state == resultCancelled {
		status = "Cancelled"
	}
	cellRow, cellColumn := table.cursorRow+1, table.cursorColumn+1
	if len(table.result.Rows) == 0 {
		cellRow = 0
	}
	if len(table.result.Columns) == 0 {
		cellColumn = 0
	}
	execution := []string{
		status,
		"Rows: " + rowCount,
		"Columns: " + formatCount(len(table.result.Columns)),
		"Query: " + formatDuration(table.result.Duration),
		"Fetch: " + formatDuration(table.result.FetchDuration),
	}
	if table.result.Limit > 0 {
		limit := "Limit: " + formatCount(table.result.Limit)
		if table.result.IsTruncated {
			limit += " reached"
		}
		execution = append(execution, limit)
	}
	position := []string{
		"Render: " + formatDuration(table.layoutTime),
		fmt.Sprintf("Cell: %d,%d", cellRow, cellColumn),
		fmt.Sprintf("View: %d–%d", rowStart, rowEnd),
		fmt.Sprintf("Frozen: %d", table.frozenColumnCount),
	}
	if layout.scrollEnd > layout.scrollStart {
		position = append(position, fmt.Sprintf("Cols: %d–%d", layout.scrollStart+1, layout.scrollEnd))
	}
	return ansi.Truncate(strings.Join(execution, " | "), table.width, "…") + "\n" +
		ansi.Truncate(strings.Join(position, " | "), table.width, "…")
}

func (table *resultTableModel) renderRunning() string {
	frames := `|/-\`
	elapsed := time.Since(table.startedAt)
	frame := frames[int(elapsed/(100*time.Millisecond))%len(frames)]
	line := fmt.Sprintf("%c Running query… %s", frame, formatDuration(elapsed))
	footer := fmt.Sprintf("Running | Elapsed: %s | Ctrl+C/Ctrl+G cancel", formatDuration(elapsed))
	return table.renderWithFixedFooter(
		[]string{ansi.Truncate(line, table.width, "…")},
		table.dimStyle.Render(ansi.Truncate(footer, table.width, "…")),
	)
}

func (table *resultTableModel) renderError() string {
	title := "Query failed"
	style := table.errorStyle
	if table.state == resultCancelled {
		title = "Query cancelled"
		style = table.dimStyle
	}
	lines := []string{style.Render(title), ansi.Truncate(sanitizeInline(table.error.Message), table.width, "…")}
	details := []string{}
	if table.error.Code != "" {
		details = append(details, "Code: "+table.error.Code)
	}
	if table.error.Line > 0 {
		details = append(details, "Line: "+strconv.Itoa(table.error.Line))
	}
	if table.error.Position > 0 {
		details = append(details, "Position: "+strconv.Itoa(table.error.Position))
	}
	details = append(details, "Time: "+formatDuration(table.error.Duration))
	lines = append(lines, table.dimStyle.Render(strings.Join(details, " | ")))
	if len(table.result.Rows) > 0 {
		lines = append(lines, table.dimStyle.Render(
			fmt.Sprintf("Partial data retained: %d row(s)", len(table.result.Rows)),
		))
	}
	return table.renderWithFixedFooter(lines, table.renderFooter())
}

func (table *resultTableModel) renderCommandResult() string {
	message := "Query completed without a result set."
	if table.result.AffectedRows >= 0 {
		message = fmt.Sprintf("Statement completed — %d row(s) affected.", table.result.AffectedRows)
	}
	return table.renderWithFixedFooter(
		[]string{ansi.Truncate(message, table.width, "…")},
		table.renderFooter(),
	)
}

func (table *resultTableModel) renderWithFixedFooter(lines []string, footer string) string {
	footerLines := strings.Split(footer, "\n")
	if table.height <= len(footerLines) {
		return strings.Join(footerLines[len(footerLines)-table.height:], "\n")
	}
	contentHeight := table.height - len(footerLines)
	lines = lines[:min(len(lines), contentHeight)]
	for len(lines) < contentHeight {
		lines = append(lines, "")
	}
	lines = append(lines, footerLines...)
	return strings.Join(lines, "\n")
}

func calculateResultColumnWidths(result database.Result, config resultTableConfig) []int {
	columnWidths := make([]int, len(result.Columns))
	for column := range result.Columns {
		width := ansi.StringWidth(resultColumnTitle(result.Columns[column]))
		for _, row := range result.Rows {
			if column >= len(row) {
				continue
			}
			cell := formatResultValue(row[column], result.Columns[column])
			width = max(width, ansi.StringWidth(cell.display))
		}
		columnWidths[column] = min(
			config.MaximumColumnWidth,
			max(config.MinimumColumnWidth, width),
		)
	}
	return columnWidths
}

func (table *resultTableModel) columnLayout() resultColumnLayout {
	if len(table.result.Columns) == 0 {
		return resultColumnLayout{}
	}
	columnCount := len(table.result.Columns)
	frozenCount := min(max(0, table.frozenColumnCount), columnCount)
	available := max(1, table.width-2)
	layout := resultColumnLayout{
		columns:     make([]visibleResultColumn, 0, columnCount),
		scrollStart: frozenCount,
		scrollEnd:   frozenCount,
	}

	scrollStart := frozenCount
	if frozenCount < columnCount {
		scrollStart = min(max(frozenCount, table.columnOffset), columnCount-1)
		layout.scrollStart = scrollStart
	}

	frozenBudget := available
	if frozenCount > 0 && scrollStart < columnCount {
		frozenBudget -= ansi.StringWidth(resultFrozenDivider) + table.config.MinimumColumnWidth
	}
	if frozenCount > 0 {
		widths := fitResultColumnWidths(
			table.columnWidths[:frozenCount],
			max(1, frozenBudget),
			table.config.MinimumColumnWidth,
			ansi.StringWidth(resultColumnSeparator),
		)
		for index, width := range widths {
			layout.columns = append(layout.columns, visibleResultColumn{
				index: index, width: width, isFrozen: true,
			})
		}
	}

	used := 0
	for position, column := range layout.columns {
		if position > 0 {
			used += ansi.StringWidth(resultColumnSeparator)
		}
		used += column.width
	}
	for column := scrollStart; column < columnCount; column++ {
		separatorWidth := 0
		hasDivider := false
		if len(layout.columns) > 0 {
			separatorWidth = ansi.StringWidth(resultColumnSeparator)
			if column == scrollStart && frozenCount > 0 {
				separatorWidth = ansi.StringWidth(resultFrozenDivider)
				hasDivider = true
			}
		}
		remaining := available - used - separatorWidth
		if remaining <= 0 {
			break
		}
		width := min(table.columnWidths[column], remaining)
		if width < table.config.MinimumColumnWidth && column > scrollStart {
			break
		}
		layout.columns = append(layout.columns, visibleResultColumn{
			index: column, width: max(1, width), hasDivider: hasDivider,
		})
		used += separatorWidth + width
		layout.scrollEnd = column + 1
		if width < table.columnWidths[column] {
			break
		}
	}
	return layout
}

func fitResultColumnWidths(widths []int, budget, minimumWidth, separatorWidth int) []int {
	result := append([]int(nil), widths...)
	used := max(0, len(result)-1) * separatorWidth
	for _, width := range result {
		used += width
	}
	for index := len(result) - 1; index >= 0 && used > budget; index-- {
		reduction := min(max(0, result[index]-minimumWidth), used-budget)
		result[index] -= reduction
		used -= reduction
	}
	for index := len(result) - 1; index >= 0 && used > budget; index-- {
		reduction := min(max(0, result[index]-1), used-budget)
		result[index] -= reduction
		used -= reduction
	}
	for len(result) > 0 && used > budget {
		last := len(result) - 1
		used -= result[last]
		if last > 0 {
			used -= separatorWidth
		}
		result = result[:last]
	}
	return result
}

func (table *resultTableModel) visibleRowRange() (int, int) {
	if len(table.result.Rows) == 0 {
		return 0, 0
	}
	start := min(table.rowOffset, len(table.result.Rows)-1)
	end := min(len(table.result.Rows), start+table.bodyHeight())
	return start + 1, end
}

func (table *resultTableModel) ensureSelectionVisible() {
	bodyHeight := max(1, table.bodyHeight())
	if table.cursorRow < table.rowOffset {
		table.rowOffset = table.cursorRow
	}
	if table.cursorRow >= table.rowOffset+bodyHeight {
		table.rowOffset = table.cursorRow - bodyHeight + 1
	}
	if table.cursorColumn < table.frozenColumnCount {
		return
	}
	if table.cursorColumn < table.columnOffset {
		table.columnOffset = table.cursorColumn
	}
	for range max(1, len(table.result.Columns)) {
		layout := table.columnLayout()
		if layout.contains(table.cursorColumn) || table.columnOffset >= table.cursorColumn {
			break
		}
		table.columnOffset++
	}
}

func (table *resultTableModel) clampSelection() {
	table.cursorRow = min(max(0, table.cursorRow), max(0, len(table.result.Rows)-1))
	table.cursorColumn = min(max(0, table.cursorColumn), max(0, len(table.result.Columns)-1))
	table.rowOffset = min(max(0, table.rowOffset), max(0, len(table.result.Rows)-1))
	minimumOffset := min(table.frozenColumnCount, len(table.result.Columns))
	maximumOffset := max(minimumOffset, len(table.result.Columns)-1)
	table.columnOffset = min(max(minimumOffset, table.columnOffset), maximumOffset)
}

func (table *resultTableModel) clampFrozenColumns() {
	table.frozenColumnCount = min(max(0, table.frozenColumnCount), len(table.result.Columns))
	for table.frozenColumnCount > 0 && !table.canFreeze(table.frozenColumnCount) {
		table.frozenColumnCount--
	}
	table.clampSelection()
}

func (table *resultTableModel) canFreeze(count int) bool {
	if count < 0 || count > len(table.result.Columns) {
		return false
	}
	if count == 0 {
		return true
	}
	minimumWidth := count*table.config.MinimumColumnWidth + max(0, count-1)*ansi.StringWidth(resultColumnSeparator)
	if count < len(table.result.Columns) {
		minimumWidth += ansi.StringWidth(resultFrozenDivider) + table.config.MinimumColumnWidth
	}
	return minimumWidth <= max(1, table.width-2)
}

func sameResultColumns(left, right []database.ResultColumn) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Name != right[index].Name ||
			left[index].SourceDatabase != right[index].SourceDatabase ||
			left[index].SourceSchema != right[index].SourceSchema ||
			left[index].SourceTable != right[index].SourceTable ||
			left[index].SourceColumn != right[index].SourceColumn {
			return false
		}
	}
	return true
}

func (table *resultTableModel) bodyHeight() int {
	overhead := 3 // fixed header and two-line footer
	if table.showShortcuts() {
		overhead++
	}
	if table.showColumnDetail() {
		overhead++
	}
	if table.showCellDetail() {
		overhead++
	}
	return max(0, table.height-overhead)
}

func (table *resultTableModel) showShortcuts() bool {
	return table.height >= 4
}

func (table *resultTableModel) showColumnDetail() bool {
	return table.height >= 7
}

func (table *resultTableModel) showCellDetail() bool {
	return table.height >= 6
}

func resultColumnTitle(column database.ResultColumn) string {
	name := sanitizeInline(column.Name)
	if column.Key == "" {
		return name
	}
	return "[" + column.Key + "] " + name
}

func formatCount(value int) string {
	digits := strconv.Itoa(value)
	for index := len(digits) - 3; index > 0; index -= 3 {
		digits = digits[:index] + "," + digits[index:]
	}
	return digits
}
