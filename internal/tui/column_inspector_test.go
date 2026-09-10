package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/lucasfguimares/charta-tui/internal/database"
	"github.com/lucasfguimares/charta-tui/internal/profile"
)

func TestCalculateLocalColumnStatistics(t *testing.T) {
	t.Parallel()

	result := database.Result{
		Columns:     []database.ResultColumn{{Name: "value"}},
		Rows:        [][]any{{"one"}, {nil}, {""}, {"one"}},
		IsTruncated: true,
	}
	statistics := calculateLocalColumnStatistics(result, 0)
	if statistics.loadedRows != 4 || statistics.nonNullCount != 3 || statistics.nullCount != 1 ||
		statistics.distinctCount != 2 || !statistics.hasDistinctCount || statistics.emptyStringCount != 1 ||
		!statistics.isTruncated {
		t.Fatalf("local statistics = %#v", statistics)
	}
	if statistics.minimumLength != 0 || statistics.maximumLength != 3 || statistics.lengthCount != 3 {
		t.Fatalf("local lengths = %#v", statistics)
	}
}

func TestColumnInspectorRendersMetadataAndStatistics(t *testing.T) {
	t.Parallel()

	column := database.ResultColumn{
		Name: "usr_psv", DatabaseType: "INT", Nullable: true, NullableKnown: true,
		Key: "FK", SourceSchema: "dbo", SourceTable: "usr", SourceColumn: "usr_psv",
		ReferenceSchema: "dbo", ReferenceTable: "psv", ReferenceColumn: "psv_cod",
	}
	table := newResultTable(20)
	table.SetSize(70, 8)
	table.SetResult(database.Result{Columns: []database.ResultColumn{column}, Rows: [][]any{{int64(1)}, {nil}}})
	table.selectionScope = resultSelectionColumn
	tab := &queryTab{
		id: 3, connection: profile.Connection{ID: "connection-1"}, table: table, editor: textarea.New(),
	}
	model := &Model{
		tabs: []*queryTab{tab}, activeTab: 0, width: 100, height: 30, focus: focusResults,
		mode: modeWorkspace, styles: defaultStyles(), catalog: map[string]*catalogState{},
		columnStatisticsCache: map[columnCacheKey]columnStatisticsCacheEntry{},
	}
	if cmd := model.openColumnInspector(tab); cmd != nil {
		t.Fatal("column inspector scheduled metadata without an inspector service")
	}
	statistics := database.ColumnStatistics{
		TotalRows: 401, NullCount: 14, DistinctCount: 387, HasDistinctCount: true, Duration: 2 * time.Millisecond,
	}
	model.columnInspector.exact = &statistics
	model.columnInspector.exactLoadedAt = time.Now()
	model.refreshColumnInspectorContent()
	view := ansi.Strip(model.renderColumnInspector())
	for _, expected := range []string{
		"usr_psv", "Type: INT", "Nullable: YES", "PK: NO", "FK: dbo.psv.psv_cod",
		"Loaded result (2 row(s))", "Distinct: 1", "Rows: 401", "NULL: 14", "Distinct: 387",
	} {
		if !strings.Contains(view, expected) {
			t.Fatalf("column inspector missing %q:\n%s", expected, view)
		}
	}
}

func TestColumnInspectorKeyboardEntryAndNavigation(t *testing.T) {
	t.Parallel()

	table := newResultTable(20)
	table.SetSize(70, 8)
	table.SetResult(database.Result{
		Columns: []database.ResultColumn{{Name: "id"}, {Name: "name"}},
		Rows:    [][]any{{int64(1), "Alice"}},
	})
	table.Focus()
	table.HandleNavigation("up")
	tab := &queryTab{id: 1, connection: profile.Connection{ID: "connection"}, table: table, editor: textarea.New()}
	model := &Model{
		tabs: []*queryTab{tab}, activeTab: 0, width: 100, height: 30,
		focus: focusResults, mode: modeWorkspace, styles: defaultStyles(), catalog: map[string]*catalogState{},
		columnStatisticsCache: map[columnCacheKey]columnStatisticsCacheEntry{},
	}
	model.handleResultKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if model.mode != modeColumnInspector || model.columnInspector.columnIndex != 0 {
		t.Fatalf("Enter opened mode=%v column=%d", model.mode, model.columnInspector.columnIndex)
	}
	model.handleColumnInspectorKey(tea.KeyPressMsg{Code: tea.KeyRight})
	if model.columnInspector.columnIndex != 1 || table.cursorColumn != 1 {
		t.Fatalf("Right selected inspector=%d table=%d", model.columnInspector.columnIndex, table.cursorColumn)
	}
	model.handleColumnInspectorKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if model.mode != modeWorkspace || !table.IsColumnSelected() {
		t.Fatalf("Esc returned mode=%v scope=%v", model.mode, table.selectionScope)
	}
}

func TestColumnInspectorDiscardsStaleMessages(t *testing.T) {
	t.Parallel()

	column := database.ResultColumn{SourceSchema: "main", SourceTable: "users", SourceColumn: "id"}
	model := &Model{
		tabs:      []*queryTab{{id: 2, connection: profile.Connection{ID: "connection"}, table: newResultTable(20)}},
		activeTab: 0, catalog: map[string]*catalogState{},
		columnInspector:       columnInspectorModel{tabID: 2, requestID: 7, column: column, isStatisticsLoading: true},
		columnStatisticsCache: map[columnCacheKey]columnStatisticsCacheEntry{},
	}
	model.handleColumnStatisticsLoaded(columnStatisticsLoadedMsg{
		tabID: 2, requestID: 6, key: columnKeyFor("connection", column),
		statistics: database.ColumnStatistics{TotalRows: 99},
	})
	if model.columnInspector.exact != nil || !model.columnInspector.isStatisticsLoading {
		t.Fatalf("stale statistics changed inspector: %#v", model.columnInspector)
	}
}

func TestColumnInspectorNormalizesAsyncErrors(t *testing.T) {
	t.Parallel()

	column := database.ResultColumn{SourceSchema: "main", SourceTable: "users", SourceColumn: "id"}
	tab := &queryTab{id: 2, connection: profile.Connection{ID: "connection"}, table: newResultTable(20)}
	model := &Model{
		tabs: []*queryTab{tab}, activeTab: 0, mode: modeColumnInspector,
		catalog: map[string]*catalogState{}, styles: defaultStyles(),
		columnInspector:       columnInspectorModel{tabID: 2, requestID: 7, column: column, isStatisticsLoading: true},
		columnStatisticsCache: map[columnCacheKey]columnStatisticsCacheEntry{},
	}
	key := columnKeyFor("connection", column)
	model.handleColumnStatisticsLoaded(columnStatisticsLoadedMsg{
		tabID: 2, requestID: 7, key: key, err: context.Canceled,
	})
	if model.columnInspector.statisticsError != "Exact statistics cancelled" {
		t.Fatalf("cancel error = %q", model.columnInspector.statisticsError)
	}
	model.columnInspector.isStatisticsLoading = true
	model.handleColumnStatisticsLoaded(columnStatisticsLoadedMsg{
		tabID: 2, requestID: 7, key: key, err: context.DeadlineExceeded,
	})
	if model.columnInspector.statisticsError != "Exact statistics timed out" {
		t.Fatalf("timeout error = %q", model.columnInspector.statisticsError)
	}
	model.columnInspector.isStatisticsLoading = true
	model.handleColumnStatisticsLoaded(columnStatisticsLoadedMsg{
		tabID: 2, requestID: 7, key: key, err: fmt.Errorf("password=secret rejected"),
	})
	if strings.Contains(model.columnInspector.statisticsError, "secret") {
		t.Fatalf("statistics error leaked a credential: %q", model.columnInspector.statisticsError)
	}
}

func TestColumnStatisticsCacheIsBoundedAndConnectionScoped(t *testing.T) {
	t.Parallel()

	model := &Model{columnStatisticsCache: map[columnCacheKey]columnStatisticsCacheEntry{}}
	for index := range columnStatisticsCacheLimit + 1 {
		model.storeColumnStatistics(columnCacheKey{
			connectionID: "one", table: "users", column: fmt.Sprintf("column_%03d", index),
		}, columnStatisticsCacheEntry{loadedAt: time.Unix(int64(index+1), 0)})
	}
	other := columnCacheKey{connectionID: "two", table: "users", column: "id"}
	model.storeColumnStatistics(other, columnStatisticsCacheEntry{loadedAt: time.Now()})
	if len(model.columnStatisticsCache) != columnStatisticsCacheLimit {
		t.Fatalf("cache size = %d, want %d", len(model.columnStatisticsCache), columnStatisticsCacheLimit)
	}
	model.invalidateColumnCaches("one")
	if len(model.columnStatisticsCache) != 1 {
		t.Fatalf("connection invalidation left %d entries", len(model.columnStatisticsCache))
	}
	if _, ok := model.columnStatisticsCache[other]; !ok {
		t.Fatal("connection invalidation removed another connection's statistics")
	}
}

func BenchmarkLocalColumnStatistics(b *testing.B) {
	result := numberedResult(localDistinctLimit, 3)
	b.ReportAllocs()
	for b.Loop() {
		_ = calculateLocalColumnStatistics(result, 1)
	}
}
