package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/lucasfguimares/charta-tui/internal/database"
)

func TestRowInspectorFormatsNavigatesAndCopiesFields(t *testing.T) {
	t.Parallel()

	inspector := newRowInspector(
		[]database.ResultColumn{{Name: "usr_cod", DatabaseType: "INTEGER"}, {Name: "usr_nome"}, {Name: "usr_psv"}},
		[]any{int64(312), "EVANDO", nil},
		4,
	)
	inspector.SetSize(44, 6)
	view := ansi.Strip(inspector.View(defaultStyles()))
	for _, expected := range []string{"usr_cod", "312", "usr_nome", "EVANDO"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("row inspector missing %q:\n%s", expected, view)
		}
	}
	inspector.HandleNavigation("end")
	view = ansi.Strip(inspector.View(defaultStyles()))
	if !strings.Contains(view, "<NULL>") {
		t.Fatalf("row inspector does not represent NULL explicitly:\n%s", view)
	}
	if value, ok := inspector.ValueClipboard(); !ok || value != "NULL" {
		t.Fatalf("ValueClipboard() = %q, %v", value, ok)
	}
	if field, ok := inspector.FieldClipboard(); !ok || field != "usr_psv\tNULL" {
		t.Fatalf("FieldClipboard() = %q, %v", field, ok)
	}
}

func TestRowInspectorWrapsAndExpandsLongValues(t *testing.T) {
	t.Parallel()

	value := strings.Repeat("long value ", 20)
	inspector := newRowInspector(
		[]database.ResultColumn{{Name: "description"}},
		[]any{value},
		0,
	)
	inspector.SetSize(32, 8)
	collapsed := ansi.Strip(inspector.View(defaultStyles()))
	if !strings.Contains(collapsed, "Enter expand") {
		t.Fatalf("long value has no expansion hint:\n%s", collapsed)
	}
	if !inspector.ToggleExpanded() {
		t.Fatal("ToggleExpanded() returned false")
	}
	if len(inspector.rendered[0]) <= rowInspectorPreviewLines+1 {
		t.Fatalf("expanded value has only %d lines", len(inspector.rendered[0]))
	}
	firstPage := ansi.Strip(inspector.View(defaultStyles()))
	if !inspector.HandleNavigation("pgdown") || inspector.lineOffset == 0 {
		t.Fatalf("PgDown did not scroll within expanded value; offset=%d", inspector.lineOffset)
	}
	if secondPage := ansi.Strip(inspector.View(defaultStyles())); secondPage == firstPage {
		t.Fatal("PgDown left the expanded value on the same page")
	}
}

func TestRowInspectorDoesNotMutateResultTablePosition(t *testing.T) {
	t.Parallel()

	table := newResultTable(12)
	table.SetSize(32, 7)
	table.SetResult(numberedResult(50, 8))
	table.GoToRow(40)
	table.cursorColumn = 7
	table.ensureSelectionVisible()
	before := []int{table.cursorRow, table.cursorColumn, table.rowOffset, table.columnOffset, table.frozenColumnCount}
	columns, row, rowIndex, ok := table.SelectedRow()
	if !ok {
		t.Fatal("SelectedRow() returned false")
	}
	inspector := newRowInspector(columns, row, rowIndex)
	inspector.HandleNavigation("end")
	after := []int{table.cursorRow, table.cursorColumn, table.rowOffset, table.columnOffset, table.frozenColumnCount}
	for index := range before {
		if before[index] != after[index] {
			t.Fatalf("table position changed from %v to %v", before, after)
		}
	}
}
