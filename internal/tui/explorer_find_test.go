package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/lucasfguimares/tui-db/internal/database"
	"github.com/lucasfguimares/tui-db/internal/sqleditor"
)

func typeInto(model *Model, text string) {
	for _, r := range text {
		model.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func TestSearchExplorerRanksHits(t *testing.T) {
	t.Parallel()

	index := []explorerHit{
		{kind: hitColumn, schema: "public", relation: "orders", column: "customer_id", name: "customer_id"},
		{kind: hitTable, schema: "public", relation: "customers", name: "customers"},
		{kind: hitColumn, schema: "public", relation: "notes", column: "customer", name: "customer"},
		{kind: hitView, schema: "public", relation: "best_customers", name: "best_customers"},
		{kind: hitSchema, schema: "customer", name: "customer"},
	}
	hits, truncated := searchExplorer(index, "Customer", findAllKind)
	got := make([]string, len(hits))
	for i, hit := range hits {
		got[i] = string(hit.kind) + ":" + hit.qualifiedName()
	}
	want := []string{
		"C:public.notes.customer", "S:customer", // exact: column before schema
		"T:public.customers", "C:public.orders.customer_id", // prefix: relation before column
		"V:public.best_customers", // substring
	}
	if truncated || strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("hits = %v, want %v", got, want)
	}
	if hits, _ := searchExplorer(index, "customer", findColumnKind); len(hits) != 2 {
		t.Fatalf("column filter hits = %d, want 2", len(hits))
	}
	if hits, _ := searchExplorer(index, "  ", findAllKind); hits != nil {
		t.Fatal("blank query returned hits")
	}
}

func TestSearchExplorerCapsResults(t *testing.T) {
	t.Parallel()

	index := make([]explorerHit, explorerFindLimit+10)
	for i := range index {
		index[i] = explorerHit{kind: hitTable, schema: "s", relation: "t", name: "t"}
	}
	hits, truncated := searchExplorer(index, "t", findAllKind)
	if len(hits) != explorerFindLimit || !truncated {
		t.Fatalf("hits = %d, truncated = %v", len(hits), truncated)
	}
}

func TestExplorerFindMergesCatalogsAndJumps(t *testing.T) {
	t.Parallel()

	model := newExplorerTestModel(t)
	model.ensureAutocompleteCache("pg").catalog = sqleditor.Catalog{Schemas: []sqleditor.SchemaMetadata{
		{Name: "public", Relations: []sqleditor.RelationMetadata{
			{Schema: "public", Name: "orders", Kind: "BASE TABLE", Columns: []sqleditor.ColumnMetadata{{Name: "customer_id", SQLType: "integer"}}},
		}},
		{Name: "audit", Relations: []sqleditor.RelationMetadata{
			{Schema: "audit", Name: "customer_events", Kind: "BASE TABLE"},
		}},
	}}
	model.ensureAutocompleteCache("pg").isLoaded = true
	model.openExplorer("pg", "", "", "")
	model.handleKey(explorerKey("f"))
	if model.mode != modeExplorerFind {
		t.Fatalf("mode = %v", model.mode)
	}
	typeInto(model, "cust")
	hits := model.explorer.find.hits
	names := map[string]int{}
	for _, hit := range hits {
		names[hit.qualifiedName()]++
	}
	if names["public.orders.customer_id"] != 1 || names["audit.customer_events"] != 1 || names["public.customers"] != 1 {
		t.Fatalf("hits = %v", names)
	}
	view := ansi.Strip(model.View().Content)
	for _, expected := range []string{"Results (", "Find: cust", "catalog: complete", "Tab kind: all"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("find view does not contain %q:\n%s", expected, view)
		}
	}

	for index, hit := range hits {
		if hit.qualifiedName() == "public.orders.customer_id" {
			model.explorer.find.cursor = index
		}
	}
	if !strings.Contains(ansi.Strip(model.View().Content), "public.orders.customer_id · integer · NOT NULL · FK → public.customers(id)") {
		t.Fatal("detail line does not use the explorer column metadata")
	}
	model.handleKey(explorerKey("enter"))
	column, _ := model.explorerSelectedColumn()
	relation, _ := model.explorerSelectedRelation()
	if model.mode != modeExplorer || model.explorer.pane != explorerColumns || relation.Name != "orders" || column.Name != "customer_id" {
		t.Fatalf("jump landed on %s.%s pane %v mode %v", relation.Name, column.Name, model.explorer.pane, model.mode)
	}
}

func TestExplorerFindKindFilterAndEscape(t *testing.T) {
	t.Parallel()

	model := newExplorerTestModel(t)
	model.openExplorer("pg", "public", "customers", "")
	model.handleKey(explorerKey("f"))
	typeInto(model, "id")
	all := len(model.explorer.find.hits)
	model.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if model.explorer.find.kind != findRelationKind || len(model.explorer.find.hits) != 0 {
		t.Fatalf("kind = %d, hits = %d", model.explorer.find.kind, len(model.explorer.find.hits))
	}
	model.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if len(model.explorer.find.hits) != all {
		t.Fatalf("column hits = %d, want %d", len(model.explorer.find.hits), all)
	}
	model.handleKey(explorerKey("esc"))
	if relation, _ := model.explorerSelectedRelation(); model.mode != modeExplorer || relation.Name != "customers" {
		t.Fatalf("Esc moved the explorer: %v %q", model.mode, relation.Name)
	}
	model.handleKey(explorerKey("f"))
	if model.explorer.find.input.Value() != "id" {
		t.Fatalf("query was not kept: %q", model.explorer.find.input.Value())
	}
}

func TestExplorerFindRefreshesWhenCatalogArrives(t *testing.T) {
	t.Parallel()

	model := newExplorerTestModel(t)
	model.inspector = database.NewInspector(nil)
	model.openExplorer("pg", "", "", "")
	if cmd := model.openExplorerFind(); cmd == nil {
		t.Fatal("find did not start loading the full catalog")
	}
	if !strings.Contains(ansi.Strip(model.View().Content), "catalog: indexing…") {
		t.Fatal("find does not show that the catalog is indexing")
	}
	typeInto(model, "ledger")
	if len(model.explorer.find.hits) != 0 {
		t.Fatal("unexpected hits before the catalog arrived")
	}
	requestID := model.ensureAutocompleteCache("pg").requestID
	model.Update(autocompleteCatalogMsg{profileID: "pg", requestID: requestID, catalog: sqleditor.Catalog{
		Schemas: []sqleditor.SchemaMetadata{{Name: "finance", Relations: []sqleditor.RelationMetadata{{Schema: "finance", Name: "ledger"}}}},
	}})
	if len(model.explorer.find.hits) != 1 || model.explorer.find.hits[0].qualifiedName() != "finance.ledger" {
		t.Fatalf("hits after catalog = %+v", model.explorer.find.hits)
	}
}
