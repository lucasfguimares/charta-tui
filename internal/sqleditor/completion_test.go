package sqleditor

import (
	"slices"
	"strings"
	"testing"
)

func TestCompleteQualifiedAliasOnlyReturnsItsColumns(t *testing.T) {
	t.Parallel()

	sql, cursor := cursorSQL("SELECT u.|\nFROM usr AS u JOIN grp AS g ON g.grp_cod = u.usr_grp")
	result := Complete(sql, cursor, TSQL(), testCatalog())
	labels := completionLabels(result.Items)
	want := []string{"usr_cod", "usr_grp", "usr_nome", "usr_status"}
	if !slices.Equal(labels, want) {
		t.Fatalf("Complete() labels = %#v, want %#v", labels, want)
	}
	for _, item := range result.Items {
		if item.Kind != CompletionColumn {
			t.Fatalf("qualified completion contains %s: %#v", item.Kind, item)
		}
	}
}

func TestCompleteUsesCursorClauseToPrioritizeRelationsAndExpressions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		sql        string
		wantKinds  []CompletionKind
		rejectKind CompletionKind
	}{
		{name: "from", sql: "SELECT * FROM |", wantKinds: []CompletionKind{CompletionTable, CompletionView, CompletionSchema}, rejectKind: CompletionColumn},
		{name: "join", sql: "SELECT * FROM usr u JOIN |", wantKinds: []CompletionKind{CompletionTable, CompletionView, CompletionSchema}, rejectKind: CompletionColumn},
		{name: "select", sql: "SELECT | FROM usr u", wantKinds: []CompletionKind{CompletionColumn, CompletionAlias, CompletionFunction}, rejectKind: CompletionSchema},
		{name: "where", sql: "SELECT * FROM usr u WHERE |", wantKinds: []CompletionKind{CompletionColumn, CompletionAlias}, rejectKind: CompletionSchema},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sql, cursor := cursorSQL(test.sql)
			items := Complete(sql, cursor, TSQL(), testCatalog()).Items
			for _, kind := range test.wantKinds {
				if !hasCompletionKind(items, kind) {
					t.Errorf("missing %s in %#v", kind, items)
				}
			}
			if hasCompletionKind(items, test.rejectKind) {
				t.Errorf("unexpected %s in %#v", test.rejectKind, items)
			}
		})
	}
}

func TestCompleteResolvesSchemaCTEAndSubqueryScopes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		sql  string
		want []string
	}{
		{name: "schema", sql: "SELECT * FROM audit.|", want: []string{"usr_history"}},
		{name: "cte explicit columns", sql: "WITH recent(id, label) AS (SELECT usr_cod, usr_nome FROM usr) SELECT r.| FROM recent r", want: []string{"id", "label"}},
		{name: "cte projected columns", sql: "WITH recent AS (SELECT usr_cod, usr_nome AS label FROM usr) SELECT r.| FROM recent r", want: []string{"label", "usr_cod"}},
		{name: "subquery", sql: "SELECT x.| FROM (SELECT usr_cod AS id, usr_nome FROM usr) x", want: []string{"id", "usr_nome"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sql, cursor := cursorSQL(test.sql)
			if got := completionLabels(Complete(sql, cursor, TSQL(), testCatalog()).Items); !slices.Equal(got, test.want) {
				t.Fatalf("labels = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestCompleteFiltersCaseInsensitivelyAndReturnsReplacementRange(t *testing.T) {
	t.Parallel()

	sql, cursor := cursorSQL("SELECT u.UsR_N| FROM usr u")
	result := Complete(sql, cursor, TSQL(), testCatalog())
	if got := completionLabels(result.Items); !slices.Equal(got, []string{"usr_nome"}) {
		t.Fatalf("labels = %#v", got)
	}
	if sql[result.Replace.Start.Offset:result.Replace.End.Offset] != "UsR_N" {
		t.Fatalf("replacement = %q", sql[result.Replace.Start.Offset:result.Replace.End.Offset])
	}
	if result.Items[0].MatchStart != 0 || result.Items[0].MatchLength != 5 {
		t.Fatalf("match = %d,%d", result.Items[0].MatchStart, result.Items[0].MatchLength)
	}
}

func TestCompleteReplacesIdentifierSuffixAndResolvesCorrelatedAlias(t *testing.T) {
	t.Parallel()

	sql, cursor := cursorSQL("SELECT u.usr_n|ome FROM usr u")
	result := Complete(sql, cursor, TSQL(), testCatalog())
	if got := sql[result.Replace.Start.Offset:result.Replace.End.Offset]; got != "usr_nome" {
		t.Fatalf("replacement range = %q, want full identifier", got)
	}

	sql, cursor = cursorSQL("SELECT (SELECT o.| FROM grp g WHERE g.grp_cod = o.usr_grp) FROM usr o")
	if got := completionLabels(Complete(sql, cursor, TSQL(), testCatalog()).Items); !slices.Equal(got, []string{"usr_cod", "usr_grp", "usr_nome", "usr_status"}) {
		t.Fatalf("correlated alias labels = %#v", got)
	}
}

func TestCompletePrioritizesStatementStartingKeyword(t *testing.T) {
	t.Parallel()

	result := Complete("IN", 2, ANSI(), Catalog{})
	if len(result.Items) == 0 || result.Items[0].Label != "INSERT" {
		t.Fatalf("Complete(\"IN\") first item = %#v, want INSERT", result.Items)
	}
}

func testCatalog() Catalog {
	return Catalog{DefaultSchema: "dbo", Schemas: []SchemaMetadata{
		{Name: "dbo", Relations: []RelationMetadata{
			{Schema: "dbo", Name: "grp", Kind: "BASE TABLE", Columns: []ColumnMetadata{{Name: "grp_cod", SQLType: "INT", IsPrimaryKey: true}, {Name: "grp_nome", SQLType: "VARCHAR", IsNullable: true}}},
			{Schema: "dbo", Name: "usr", Kind: "BASE TABLE", Columns: []ColumnMetadata{{Name: "usr_cod", SQLType: "INT", IsPrimaryKey: true}, {Name: "usr_nome", SQLType: "VARCHAR"}, {Name: "usr_status", SQLType: "CHAR"}, {Name: "usr_grp", SQLType: "INT", IsForeignKey: true}}},
			{Schema: "dbo", Name: "active_usr", Kind: "VIEW", Columns: []ColumnMetadata{{Name: "usr_cod", SQLType: "INT"}}},
		}},
		{Name: "audit", Relations: []RelationMetadata{{Schema: "audit", Name: "usr_history", Kind: "VIEW", Columns: []ColumnMetadata{{Name: "usr_cod", SQLType: "INT"}}}}},
	}}
}

func cursorSQL(value string) (string, int) {
	offset := strings.IndexByte(value, '|')
	if offset < 0 {
		panic("test SQL has no cursor marker")
	}
	return value[:offset] + value[offset+1:], offset
}

func completionLabels(items []CompletionItem) []string {
	labels := make([]string, len(items))
	for index, item := range items {
		labels[index] = item.Label
	}
	return labels
}

func hasCompletionKind(items []CompletionItem, kind CompletionKind) bool {
	for _, item := range items {
		if item.Kind == kind {
			return true
		}
	}
	return false
}
