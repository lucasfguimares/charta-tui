package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/lucasfguimares/tui-db/internal/database"
	"github.com/lucasfguimares/tui-db/internal/profile"
)

func newExplorerTestModel(t *testing.T) *Model {
	t.Helper()
	model := New(nil, profile.NewMemoryStore(), nil, nil, nil, nil, nil, nil, nil)
	model.width, model.height = 120, 40
	model.profiles = []profile.Connection{{ID: "pg", Name: "Primary", Driver: profile.DriverPostgres, DefaultSchema: "public"}}
	state := model.ensureCatalog("pg")
	state.schemas = []string{"audit", "public"}
	state.relations["public"] = []database.Relation{
		{Schema: "public", Name: "customers", Type: "BASE TABLE"},
		{Schema: "public", Name: "orders", Type: "BASE TABLE"},
		{Schema: "public", Name: "order_summary", Type: "VIEW"},
	}
	state.columns[catalogKey("public", "customers")] = []database.Column{
		{Name: "id", Type: "bigint", Key: "PK", Ordinal: 1},
		{Name: "name", Type: "varchar", HasLength: true, Length: 80, Nullable: true, Ordinal: 2},
	}
	state.columns[catalogKey("public", "orders")] = []database.Column{
		{Name: "id", Type: "bigint", Key: "PK", Ordinal: 1, Default: "nextval('orders_id_seq')"},
		{Name: "customer_id", Type: "integer", Key: "FK", Ordinal: 2,
			ReferenceSchema: "public", ReferenceTable: "customers", ReferenceColumn: "id"},
		{Name: "Order Note", Type: "text", Nullable: true, Ordinal: 3},
	}
	model.rebuildBrowser()
	return model
}

func explorerKey(text string) tea.KeyPressMsg {
	switch text {
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	}
	r := []rune(text)[0]
	return tea.KeyPressMsg{Code: r, Text: text}
}

func TestExplorerNavigatesCachedCatalog(t *testing.T) {
	t.Parallel()

	model := newExplorerTestModel(t)
	if cmd := model.openExplorer("pg", "", "", ""); cmd != nil {
		t.Fatal("opening on a fully cached catalog requested a load")
	}
	if model.mode != modeExplorer {
		t.Fatalf("mode = %v", model.mode)
	}
	if schema, _ := model.explorerSelectedSchema(); schema != "public" {
		t.Fatalf("selected schema = %q, want default schema", schema)
	}
	model.handleKey(explorerKey("enter"))
	model.handleKey(explorerKey("down"))
	if relation, _ := model.explorerSelectedRelation(); relation.Name != "orders" || model.explorer.pane != explorerRelations {
		t.Fatalf("relation = %q, pane = %v", relation.Name, model.explorer.pane)
	}
	model.handleKey(explorerKey("right"))
	model.handleKey(explorerKey("down"))
	column, _ := model.explorerSelectedColumn()
	if column.Name != "customer_id" {
		t.Fatalf("column = %q", column.Name)
	}
	view := ansi.Strip(model.View().Content)
	for _, expected := range []string{
		"Schemas (2)", "Relations (3)", "Columns · public.orders (3)", "# name",
		"customer_id · integer · NOT NULL · FK → public.customers(id) · default —",
	} {
		if !strings.Contains(view, expected) {
			t.Fatalf("view does not contain %q:\n%s", expected, view)
		}
	}
	model.handleKey(explorerKey("left"))
	if model.explorer.pane != explorerRelations {
		t.Fatalf("left did not return to relations: %v", model.explorer.pane)
	}
	model.handleKey(explorerKey("esc"))
	if model.mode != modeWorkspace {
		t.Fatalf("Esc did not close the explorer: %v", model.mode)
	}
}

func TestExplorerOpensAtBrowserItemAndCollapsesWhenNarrow(t *testing.T) {
	t.Parallel()

	model := newExplorerTestModel(t)
	model.openExplorer("pg", "public", "orders", "Order Note")
	if column, _ := model.explorerSelectedColumn(); column.Name != "Order Note" || model.explorer.pane != explorerColumns {
		t.Fatalf("column = %q, pane = %v", column.Name, model.explorer.pane)
	}
	model.width = 90
	view := ansi.Strip(model.renderExplorer())
	if strings.Contains(view, "Schemas (") || !strings.Contains(view, "Relations (3)") {
		t.Fatalf("narrow view should show relations and columns only:\n%s", view)
	}
}

func TestExplorerDebouncedLoadDropsStaleVersions(t *testing.T) {
	t.Parallel()

	model := newExplorerTestModel(t)
	model.openExplorer("pg", "", "", "")
	model.explorer.pane = explorerRelations
	if cmd := model.moveExplorerCursor(2); cmd == nil {
		t.Fatal("moving the relation cursor did not schedule a load")
	}
	stale := model.explorer.loadVersion
	model.moveExplorerCursor(1)
	if cmd := model.handleExplorerLoadDue(explorerLoadDueMsg{version: stale}); cmd != nil {
		t.Fatal("stale load version produced a command")
	}
	delete(model.catalog["pg"].columns, catalogKey("public", "orders"))
	if cmd := model.handleExplorerLoadDue(explorerLoadDueMsg{version: model.explorer.loadVersion}); cmd == nil {
		t.Fatal("current load version did not request missing columns")
	}
}

func TestExplorerFilter(t *testing.T) {
	t.Parallel()

	model := newExplorerTestModel(t)
	model.openExplorer("pg", "public", "", "")
	model.explorer.pane = explorerRelations
	model.handleKey(explorerKey("/"))
	for _, text := range []string{"o", "r", "d"} {
		model.handleKey(explorerKey(text))
	}
	if got := len(model.explorerRelations()); got != 2 {
		t.Fatalf("filtered relations = %d, want 2", got)
	}
	if !strings.Contains(ansi.Strip(model.renderExplorer()), "2 match(es)") {
		t.Fatal("filter bar does not show the match count")
	}
	model.handleKey(explorerKey("enter"))
	if model.mode != modeExplorer || model.explorer.filter[explorerRelations] != "ord" {
		t.Fatalf("mode = %v, filter = %q", model.mode, model.explorer.filter[explorerRelations])
	}
	model.handleKey(explorerKey("esc"))
	if model.mode != modeExplorer || len(model.explorerRelations()) != 3 {
		t.Fatalf("first Esc should clear the filter: mode = %v", model.mode)
	}
	model.handleKey(explorerKey("esc"))
	if model.mode != modeWorkspace {
		t.Fatal("second Esc did not close the explorer")
	}
}

func TestExplorerSelectSQL(t *testing.T) {
	t.Parallel()

	postgres := profile.Connection{Driver: profile.DriverPostgres}
	if got := explorerSelectSQL(postgres, "public", "orders", nil); got != "SELECT *\nFROM public.orders\nLIMIT 100;" {
		t.Errorf("postgres select = %q", got)
	}
	if got := explorerSelectSQL(postgres, "Sales", "orders", []string{"id", "Order Note"}); got != "SELECT id, \"Order Note\"\nFROM \"Sales\".orders\nLIMIT 100;" {
		t.Errorf("postgres quoted select = %q", got)
	}
	server := profile.Connection{Driver: profile.DriverSQLServer}
	if got := explorerSelectSQL(server, "dbo", "Order Lines", nil); got != "SELECT TOP (100) *\nFROM dbo.[Order Lines];" {
		t.Errorf("sql server select = %q", got)
	}
}

func TestExplorerInsertsMarkedColumnsIntoEditor(t *testing.T) {
	t.Parallel()

	model := newExplorerTestModel(t)
	model.openExplorer("pg", "public", "orders", "id")
	model.handleKey(explorerKey("space"))
	model.handleKey(explorerKey("down"))
	model.handleKey(explorerKey("space"))
	model.handleKey(explorerKey("s"))
	if model.mode != modeWorkspace || model.focus != focusEditor {
		t.Fatalf("mode = %v, focus = %v", model.mode, model.focus)
	}
	tab := model.currentTab()
	want := "SELECT id, \"Order Note\"\nFROM public.orders\nLIMIT 100;"
	if tab == nil || tab.editor.Value() != want {
		t.Fatalf("editor = %q, want %q", tab.editor.Value(), want)
	}
	if model.catalog["pg"].expanded["public"] {
		t.Fatal("explorer actions expanded the sidebar tree")
	}

	tab.editor.SetValue("SELECT 1;")
	model.openExplorer("pg", "public", "customers", "")
	model.handleKey(explorerKey("s"))
	if got := tab.editor.Value(); got != "SELECT 1;\n\nSELECT *\nFROM public.customers\nLIMIT 100;" {
		t.Fatalf("draft was not preserved: %q", got)
	}
}

func TestExplorerCopiesNames(t *testing.T) {
	t.Parallel()

	model := newExplorerTestModel(t)
	model.openExplorer("pg", "public", "orders", "customer_id")
	if cmd := model.explorerCopy(false); cmd == nil || model.statusText != "Copied orders.customer_id" {
		t.Fatalf("copy status = %q", model.statusText)
	}
	if cmd := model.explorerCopy(true); cmd == nil || model.statusText != "Copied id, customer_id, Order Note" {
		t.Fatalf("column list status = %q", model.statusText)
	}
}

func TestExplorerMessagesDoNotExpandSidebar(t *testing.T) {
	t.Parallel()

	model := newExplorerTestModel(t)
	model.openExplorer("pg", "audit", "", "")
	model.Update(relationsLoadedMsg{
		profileID: "pg", schema: "audit", isExplorer: true,
		relations: []database.Relation{{Schema: "audit", Name: "events", Type: "BASE TABLE"}},
	})
	state := model.catalog["pg"]
	if len(state.relations["audit"]) != 1 || state.expanded["audit"] {
		t.Fatalf("relations = %v, expanded = %v", state.relations["audit"], state.expanded["audit"])
	}
	model.Update(columnsLoadedMsg{profileID: "pg", schema: "audit", relation: "events", isExplorer: true, err: errors.New("boom")})
	if !strings.Contains(model.explorer.errorText, "boom") || model.mode != modeExplorer {
		t.Fatalf("error = %q, mode = %v", model.explorer.errorText, model.mode)
	}
}

func TestExplorerNeverPromptsForPassword(t *testing.T) {
	t.Parallel()

	model := newExplorerTestModel(t)
	model.inspector = database.NewInspector(nil)
	model.catalog["pg"].schemas = []string{}
	cmd := model.openExplorer("pg", "", "", "")
	if cmd == nil {
		t.Fatal("explorer did not try to load schemas")
	}
	msg, ok := cmd().(schemasLoadedMsg)
	if !ok || !msg.isExplorer {
		t.Fatalf("message = %#v", msg)
	}
	model.Update(msg)
	if model.mode != modeExplorer || !model.explorer.needsConnect {
		t.Fatalf("mode = %v, needsConnect = %v", model.mode, model.explorer.needsConnect)
	}
	if !strings.Contains(ansi.Strip(model.renderExplorer()), "Password required") {
		t.Fatal("explorer does not explain that a password is required")
	}
}

func TestExplorerColumnInspectorReturnsToExplorer(t *testing.T) {
	t.Parallel()

	model := newExplorerTestModel(t)
	model.openExplorer("pg", "public", "orders", "customer_id")
	model.handleKey(explorerKey("c"))
	if model.mode != modeColumnInspector || !model.columnInspector.isCatalog || model.columnInspector.returnMode != modeExplorer {
		t.Fatalf("mode = %v, inspector = %+v", model.mode, model.columnInspector)
	}
	if content := model.columnInspector.viewport.GetContent(); !strings.Contains(content, "FK: public.customers.id") || strings.Contains(content, "Loaded result") {
		t.Fatalf("inspector content = %q", content)
	}
	model.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	if model.columnInspector.column.Name != "Order Note" {
		t.Fatalf("inspector did not move to the next catalog column: %q", model.columnInspector.column.Name)
	}
	model.handleKey(explorerKey("esc"))
	if model.mode != modeExplorer {
		t.Fatalf("Esc returned to %v", model.mode)
	}
}

func withOrderDetails(model *Model) {
	model.catalog["pg"].details[catalogKey("public", "orders")] = database.RelationDetails{
		ApproximateRows: 12_345, HasApproximateRows: true, TotalBytes: 4_300_000, HasTotalBytes: true,
		Indexes: []database.Index{
			{Name: "orders_pkey", Columns: []string{"id"}, IsPrimary: true, IsUnique: true},
			{Name: "orders_customer_idx", Columns: []string{"customer_id", "id"}},
		},
	}
	model.catalog["pg"].details[catalogKey("public", "order_summary")] = database.RelationDetails{
		Definition: "SELECT customer_id, count(*) AS orders\nFROM orders\nGROUP BY customer_id",
	}
}

func TestExplorerShowsRelationDetails(t *testing.T) {
	t.Parallel()

	model := newExplorerTestModel(t)
	withOrderDetails(model)
	model.openExplorer("pg", "public", "orders", "")
	view := ansi.Strip(model.renderExplorer())
	for _, expected := range []string{"~12.3k rows · 4.1 MiB", "· 2 index(es)", "FK/IX"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("view does not contain %q:\n%s", expected, view)
		}
	}

	model.handleKey(explorerKey("d"))
	view = ansi.Strip(model.renderExplorer())
	if model.explorer.view != explorerViewIndexes || !strings.Contains(view, "Indexes · public.orders (2)") ||
		!strings.Contains(view, "orders_customer_idx") || !strings.Contains(view, "customer_id, id") || !strings.Contains(view, "primary") {
		t.Fatalf("indexes view:\n%s", view)
	}
	if _, ok := model.explorerSelectedColumn(); ok {
		t.Fatal("column actions are active outside the columns view")
	}

	model.handleKey(explorerKey("d"))
	if !strings.Contains(ansi.Strip(model.renderExplorer()), "Not a view") {
		t.Fatal("definition view of a table does not explain it is not a view")
	}
	model.explorer.pane = explorerRelations
	model.moveExplorerCursor(2)
	view = ansi.Strip(model.renderExplorer())
	if !strings.Contains(view, "Definition · public.order_summary") || !strings.Contains(view, "GROUP BY customer_id") {
		t.Fatalf("definition view:\n%s", view)
	}
	model.handleKey(explorerKey("d"))
	if model.explorer.view != explorerViewColumns {
		t.Fatalf("d did not cycle back to columns: %v", model.explorer.view)
	}
}

func TestExplorerLoadsDetailsAfterColumns(t *testing.T) {
	t.Parallel()

	model := newExplorerTestModel(t)
	model.inspector = database.NewInspector(nil)
	if cmd := model.openExplorer("pg", "public", "orders", ""); cmd == nil {
		t.Fatal("details were not requested for the selected relation")
	}
	if cmd := model.explorerLoadCmd(); cmd != nil {
		t.Fatal("details were requested twice while in flight")
	}
	model.Update(relationDetailsLoadedMsg{profileID: "pg", schema: "public", relation: "orders", err: errors.New("permission denied")})
	if model.mode != modeExplorer || !strings.Contains(model.explorer.errorText, "permission denied") {
		t.Fatalf("mode = %v, error = %q", model.mode, model.explorer.errorText)
	}
	model.Update(relationDetailsLoadedMsg{profileID: "pg", schema: "public", relation: "orders",
		details: database.RelationDetails{Indexes: []database.Index{{Name: "ix", Columns: []string{"customer_id"}}}}})
	if details, ok := model.relationDetails("public", "orders"); !ok || len(details.Indexes) != 1 {
		t.Fatalf("details = %+v, %v", details, ok)
	}
}

func TestExplorerSizeFormatting(t *testing.T) {
	t.Parallel()

	for value, want := range map[int64]string{950: "950", 12_345: "12.3k", 4_200_000: "4.2M", 150_000: "150k", 1_000: "1k"} {
		if got := formatApproximateCount(value); got != want {
			t.Errorf("formatApproximateCount(%d) = %q, want %q", value, got, want)
		}
	}
	for value, want := range map[int64]string{512: "512 B", 4_300_000: "4.1 MiB", 1024: "1 KiB"} {
		if got := formatBytes(value); got != want {
			t.Errorf("formatBytes(%d) = %q, want %q", value, got, want)
		}
	}
}
