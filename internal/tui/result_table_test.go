package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/lucasfguimares/charta-tui/internal/database"
)

func TestResultTableAlignment(t *testing.T) {
	t.Parallel()

	result := database.Result{
		Columns: []database.ResultColumn{
			{Name: "id", DatabaseType: "INTEGER"},
			{Name: "cidade", DatabaseType: "TEXT"},
			{Name: "descrição", DatabaseType: "TEXT"},
		},
		Rows: [][]any{{int64(7), "Recife", "界界"}},
	}
	table := newResultTable(24)
	table.SetSize(70, 8)
	table.SetResult(result)
	lines := strings.Split(ansi.Strip(table.View()), "\n")
	if len(lines) < 2 {
		t.Fatalf("View() lines = %d, want at least 2", len(lines))
	}
	headerCells := strings.Split(strings.TrimPrefix(lines[0], "  "), resultColumnSeparator)
	rowCells := strings.Split(strings.TrimPrefix(lines[1], "> "), resultColumnSeparator)
	if len(headerCells) != len(rowCells) {
		t.Fatalf("header cells = %d, row cells = %d", len(headerCells), len(rowCells))
	}
	for column := range headerCells {
		if ansi.StringWidth(headerCells[column]) != ansi.StringWidth(rowCells[column]) {
			t.Errorf(
				"column %d widths: header=%d row=%d\nheader=%q\nrow=%q",
				column,
				ansi.StringWidth(headerCells[column]),
				ansi.StringWidth(rowCells[column]),
				headerCells[column],
				rowCells[column],
			)
		}
	}
}

func TestResultTableHeaderHasDistinctBackground(t *testing.T) {
	t.Parallel()

	table := newResultTable(20)
	table.SetSize(50, 6)
	table.SetResult(database.Result{
		Columns: []database.ResultColumn{{Name: "id"}, {Name: "name"}},
		Rows:    [][]any{{int64(1), "Alice"}},
	})
	lines := strings.Split(table.View(), "\n")
	if len(lines) < 2 || !strings.Contains(lines[0], "48;5;24m") {
		t.Fatalf("header has no distinct background: %q", lines[0])
	}
	if strings.Contains(lines[1], "48;5;24m") {
		t.Fatalf("body row inherited header background: %q", lines[1])
	}
}

func TestResultFormatting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		value      any
		columnType string
		want       string
		alignment  cellAlignment
	}{
		{name: "null", value: nil, want: "<NULL>"},
		{name: "empty string", value: "", want: `""`},
		{name: "zero", value: int64(0), want: "0", alignment: alignRight},
		{name: "boolean", value: true, want: "true"},
		{name: "json", value: "{\n \"ok\": true }", columnType: "JSONB", want: `{"ok":true}`},
		{name: "binary", value: []byte{0x00, 0xff}, columnType: "BLOB", want: "0x00ff (2 B)"},
		{name: "ansi stripped", value: "\x1b[31mred\x1b[0m", want: "red"},
		{name: "newline escaped", value: "a\nb", want: `a\nb`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cell := formatResultValue(test.value, database.ResultColumn{DatabaseType: test.columnType})
			if cell.display != test.want {
				t.Fatalf("display = %q, want %q", cell.display, test.want)
			}
			if cell.alignment != test.alignment {
				t.Fatalf("alignment = %v, want %v", cell.alignment, test.alignment)
			}
		})
	}
}

func TestFitCellUsesVisualWidthAndTruncates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		value     string
		width     int
		alignment cellAlignment
		want      string
	}{
		{name: "wide unicode", value: "界", width: 4, want: "界  "},
		{name: "truncated", value: "abcdef", width: 4, want: "abc…"},
		{name: "right aligned", value: "7", width: 4, alignment: alignRight, want: "   7"},
		{name: "ansi aware", value: "\x1b[31mred\x1b[0m", width: 5, want: "\x1b[31mred\x1b[0m  "},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := fitCell(test.value, test.width, test.alignment)
			if got != test.want {
				t.Fatalf("fitCell() = %q, want %q", got, test.want)
			}
			if ansi.StringWidth(got) != test.width {
				t.Fatalf("visual width = %d, want %d", ansi.StringWidth(got), test.width)
			}
		})
	}
}

func TestResultColumnMarkers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		key    string
		marker string
	}{
		{name: "common", marker: "account_id"},
		{name: "primary key", key: "PK", marker: "[PK] account_id"},
		{name: "foreign key", key: "FK", marker: "[FK] account_id"},
		{name: "primary and foreign", key: "PK/FK", marker: "[PK/FK] account_id"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := resultColumnTitle(database.ResultColumn{Name: "account_id", Key: test.key})
			if got != test.marker {
				t.Fatalf("resultColumnTitle() = %q, want %q", got, test.marker)
			}
		})
	}
}

func TestResultTableNavigationKeepsCellVisible(t *testing.T) {
	t.Parallel()

	table := newResultTable(12)
	table.SetSize(24, 8)
	table.SetResult(numberedResult(100, 12))

	for range 11 {
		table.HandleNavigation("right")
	}
	if table.cursorColumn != 11 || table.columnOffset == 0 {
		t.Fatalf("horizontal position = cursor %d offset %d", table.cursorColumn, table.columnOffset)
	}
	layout := table.columnLayout()
	if !layout.contains(table.cursorColumn) {
		t.Fatalf("selected column %d outside layout %#v", table.cursorColumn, layout.columns)
	}

	table.HandleNavigation("ctrl+end")
	if table.cursorRow != 99 || table.rowOffset == 0 {
		t.Fatalf("vertical position = cursor %d offset %d", table.cursorRow, table.rowOffset)
	}
	rowStart, rowEnd := table.visibleRowRange()
	if table.cursorRow+1 < rowStart || table.cursorRow+1 > rowEnd {
		t.Fatalf("selected row %d outside viewport %d..%d", table.cursorRow+1, rowStart, rowEnd)
	}

	table.HandleNavigation("home")
	table.HandleNavigation("ctrl+home")
	if table.cursorRow != 0 || table.cursorColumn != 0 || table.rowOffset != 0 || table.columnOffset != 0 {
		t.Fatalf(
			"home position = row %d col %d offsets %d,%d",
			table.cursorRow,
			table.cursorColumn,
			table.rowOffset,
			table.columnOffset,
		)
	}

	table.HandleNavigation("S")
	table.HandleNavigation("D")
	if table.cursorRow != 1 || table.cursorColumn != 1 {
		t.Fatalf("WASD position = %d,%d, want 1,1", table.cursorRow, table.cursorColumn)
	}
}

func TestResultTableHeaderSelectionAndFrozenColumns(t *testing.T) {
	t.Parallel()

	table := newResultTable(12)
	table.SetSize(42, 8)
	table.SetResult(numberedResult(4, 6))
	table.Focus()

	if !table.HandleNavigation("up") || !table.IsColumnSelected() {
		t.Fatal("Up from the first row did not select the header")
	}
	table.HandleNavigation("right")
	if table.cursorColumn != 1 || !table.ToggleFrozenColumns() || table.frozenColumnCount != 2 {
		t.Fatalf("frozen selection = column %d count %d", table.cursorColumn, table.frozenColumnCount)
	}
	layout := table.columnLayout()
	if len(layout.columns) < 3 || !layout.columns[0].isFrozen || !layout.columns[1].isFrozen || !layout.columns[2].hasDivider {
		t.Fatalf("unexpected frozen layout: %#v", layout)
	}
	view := ansi.Strip(table.View())
	lines := strings.Split(view, "\n")
	if len(lines) < 2 {
		t.Fatalf("result table has fewer than two lines:\n%s", view)
	}
	headerDivider := strings.Index(lines[0], resultFrozenDivider)
	rowDivider := strings.Index(lines[1], resultFrozenDivider)
	if headerDivider < 0 || rowDivider < 0 ||
		ansi.StringWidth(lines[0][:headerDivider]) != ansi.StringWidth(lines[1][:rowDivider]) {
		t.Fatalf("header and row divider are not synchronized:\n%s", view)
	}

	if !table.HandleNavigation("down") || table.IsColumnSelected() {
		t.Fatal("Down from the header did not return to the result body")
	}
}

func TestResultTableFrozenColumnsStayVisibleWhileScrollableColumnsMove(t *testing.T) {
	t.Parallel()

	table := newResultTable(10)
	table.SetSize(40, 8)
	table.SetResult(numberedResult(3, 8))
	table.selectionScope = resultSelectionColumn
	table.cursorColumn = 1
	if !table.ToggleFrozenColumns() {
		t.Fatal("could not freeze the first two columns")
	}
	for range 6 {
		table.HandleNavigation("right")
	}
	layout := table.columnLayout()
	if !layout.contains(0) || !layout.contains(1) || !layout.contains(7) {
		t.Fatalf("frozen/selected columns are not visible: %#v", layout)
	}
	if layout.scrollStart <= table.frozenColumnCount {
		t.Fatalf("scrollable offset did not move: %#v", layout)
	}
}

func TestResultTableFrozenColumnsResetWhenResultShapeChanges(t *testing.T) {
	t.Parallel()

	table := newResultTable(12)
	table.SetSize(50, 8)
	result := numberedResult(2, 4)
	table.SetResult(result)
	table.selectionScope = resultSelectionColumn
	table.cursorColumn = 1
	if !table.ToggleFrozenColumns() {
		t.Fatal("could not freeze columns")
	}
	table.SetResult(result)
	if table.frozenColumnCount != 2 {
		t.Fatalf("same result shape reset frozen count to %d", table.frozenColumnCount)
	}
	changed := numberedResult(2, 4)
	changed.Columns[1].Name = "changed"
	table.SetResult(changed)
	if table.frozenColumnCount != 0 || table.IsColumnSelected() {
		t.Fatalf("changed result shape preserved count=%d scope=%v", table.frozenColumnCount, table.selectionScope)
	}
}

func TestResultTableResizePreservesSelection(t *testing.T) {
	t.Parallel()

	table := newResultTable(20)
	table.SetSize(80, 12)
	table.SetResult(numberedResult(50, 8))
	table.GoToRow(40)
	table.cursorColumn = 7
	table.ensureSelectionVisible()
	table.SetSize(18, 5)
	if table.cursorRow != 39 || table.cursorColumn != 7 {
		t.Fatalf("selection after resize = %d,%d, want 39,7", table.cursorRow, table.cursorColumn)
	}
	rowStart, rowEnd := table.visibleRowRange()
	layout := table.columnLayout()
	if table.cursorRow+1 < rowStart || table.cursorRow+1 > rowEnd ||
		!layout.contains(table.cursorColumn) {
		t.Fatalf("selection is outside resized viewport")
	}
}

func TestResultTableNarrowResizeKeepsSelectedColumnVisible(t *testing.T) {
	t.Parallel()

	table := newResultTable(20)
	table.SetSize(100, 10)
	table.SetResult(numberedResult(20, 8))
	table.selectionScope = resultSelectionColumn
	table.cursorColumn = 2
	if !table.ToggleFrozenColumns() {
		t.Fatal("could not freeze the first three columns")
	}
	table.SetSize(18, 6)
	if !table.columnLayout().contains(table.cursorColumn) {
		t.Fatalf("selected column %d is hidden after resize: %#v", table.cursorColumn, table.columnLayout())
	}
	if table.frozenColumnCount >= 3 {
		t.Fatalf("narrow viewport retained an unusable frozen prefix of %d columns", table.frozenColumnCount)
	}
}

func TestResultTableVirtualizesLargeDataset(t *testing.T) {
	t.Parallel()

	table := newResultTable(16)
	table.SetSize(60, 8)
	table.SetResult(numberedResult(10_000, 3))
	view := ansi.Strip(table.View())
	if strings.Contains(view, "row-9999") {
		t.Fatal("View() rendered a row outside the viewport")
	}
	if lines := strings.Count(view, "\n") + 1; lines > 8 {
		t.Fatalf("View() lines = %d, want at most 8", lines)
	}
}

func TestResultTableEmptyAndSmallTerminal(t *testing.T) {
	t.Parallel()

	table := newResultTable(40)
	table.SetSize(10, 4)
	table.SetResult(database.Result{
		Columns: []database.ResultColumn{{Name: "very_long_column_name"}},
		Rows:    [][]any{},
	})
	view := ansi.Strip(table.View())
	if !strings.Contains(view, "Empty") {
		t.Fatalf("empty View() = %q", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if ansi.StringWidth(line) > 10 {
			t.Fatalf("line width = %d, want <= 10: %q", ansi.StringWidth(line), line)
		}
	}
	table.SetSize(80, 6)
	if view = ansi.Strip(table.View()); !strings.Contains(view, "Rows: 0") {
		t.Fatalf("empty View() at normal width = %q", view)
	}
}

func TestResultFooter(t *testing.T) {
	t.Parallel()

	table := newResultTable(20)
	table.SetSize(240, 8)
	result := numberedResult(1_248, 37)
	result.Duration = 183 * time.Millisecond
	result.FetchDuration = 42 * time.Millisecond
	result.Limit = 10_000
	result.IsTruncated = true
	table.SetResult(result)
	table.GoToRow(18)
	table.cursorColumn = 6
	table.ensureSelectionVisible()
	footer := ansi.Strip(table.renderFooter())
	for _, expected := range []string{
		"Partial", "Rows: 1,248+", "Columns: 37", "Query: 183ms", "Fetch: 42ms",
		"Cell: 18,7", "View:", "Limit: 10,000 reached",
	} {
		if !strings.Contains(footer, expected) {
			t.Errorf("footer %q does not contain %q", footer, expected)
		}
	}
}

func TestResultTableSearchGoToAndClipboard(t *testing.T) {
	t.Parallel()

	table := newResultTable(20)
	table.SetSize(60, 8)
	table.SetResult(database.Result{
		Columns: []database.ResultColumn{{Name: "id"}, {Name: "name"}},
		Rows:    [][]any{{int64(1), "Alice"}, {int64(2), "Bob"}},
	})
	if !table.Search("bob") || table.cursorRow != 1 || table.cursorColumn != 1 {
		t.Fatalf("Search() position = %d,%d", table.cursorRow, table.cursorColumn)
	}
	if !table.GoToRow(1) || table.cursorRow != 0 {
		t.Fatalf("GoToRow() cursor = %d", table.cursorRow)
	}
	cell, ok := table.CellClipboard()
	if !ok || cell != "Alice" {
		t.Fatalf("CellClipboard() = %q, %v", cell, ok)
	}
	row, ok := table.RowClipboard()
	if !ok || row != "1\tAlice" {
		t.Fatalf("RowClipboard() = %q, %v", row, ok)
	}
}

func TestResultTableStates(t *testing.T) {
	t.Parallel()

	table := newResultTable(20)
	table.SetSize(80, 8)
	table.SetRunning(time.Now().Add(-2 * time.Second))
	if table.state != resultRunning || !strings.Contains(ansi.Strip(table.View()), "Running query") {
		t.Fatalf("running state View() = %q", table.View())
	}

	table.SetResult(database.Result{Columns: []database.ResultColumn{{Name: "id"}}, Rows: [][]any{}})
	if table.state != resultEmpty {
		t.Fatalf("empty state = %v", table.state)
	}
	table.SetResult(database.Result{
		Columns: []database.ResultColumn{{Name: "id"}},
		Rows:    [][]any{{int64(1)}},
	})
	if table.state != resultReady {
		t.Fatalf("ready state = %v", table.state)
	}
	table.SetResult(database.Result{
		Columns:     []database.ResultColumn{{Name: "id"}},
		Rows:        [][]any{{int64(1)}},
		IsTruncated: true,
	})
	if table.state != resultPartial {
		t.Fatalf("partial state = %v", table.state)
	}

	table.SetError(database.Result{}, database.ErrorDetails{
		Message:   "cancelled",
		Duration:  time.Millisecond,
		Cancelled: true,
	})
	if table.state != resultCancelled || !strings.Contains(ansi.Strip(table.View()), "cancelled") {
		t.Fatalf("cancelled state View() = %q", table.View())
	}
	table.SetError(database.Result{}, database.ErrorDetails{
		Message:  "syntax error",
		Code:     "42601",
		Position: 8,
		Duration: 3 * time.Millisecond,
	})
	view := ansi.Strip(table.View())
	for _, expected := range []string{"Query failed", "syntax error", "Code: 42601", "Position: 8", "Time: 3ms"} {
		if !strings.Contains(view, expected) {
			t.Errorf("failed View() %q does not contain %q", view, expected)
		}
	}
}

func numberedResult(rows, columns int) database.Result {
	result := database.Result{
		Columns: make([]database.ResultColumn, columns),
		Rows:    make([][]any, rows),
	}
	for column := range columns {
		result.Columns[column] = database.ResultColumn{Name: fmt.Sprintf("column_%d", column+1)}
	}
	for row := range rows {
		result.Rows[row] = make([]any, columns)
		for column := range columns {
			result.Rows[row][column] = fmt.Sprintf("row-%04d-col-%02d", row, column)
		}
	}
	return result
}

func BenchmarkResultTableViewLargeDataset(b *testing.B) {
	table := newResultTable(24)
	table.SetSize(100, 24)
	table.SetResult(numberedResult(100_000, 12))
	table.selectionScope = resultSelectionColumn
	table.cursorColumn = 1
	table.ToggleFrozenColumns()
	table.cursorColumn = 11
	table.ensureSelectionVisible()
	b.ReportAllocs()
	for b.Loop() {
		_ = table.View()
	}
}
