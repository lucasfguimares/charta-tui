package sqleditor

import (
	"strings"
	"testing"
)

func TestLexerClassifiesMultilineSQLWithoutTouchingLiteralContents(t *testing.T) {
	sql := "-- SELECT is text\nSELECT COUNT(u.id), 'FROM; WHERE', 123, @id\nFROM dbo.users AS u /* JOIN ignored */;"
	tokens, diagnostics := (Lexer{Dialect: TSQL()}).Lex(sql)
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	want := map[string]TokenKind{"SELECT": TokenKeyword, "COUNT": TokenFunction, "'FROM; WHERE'": TokenString, "123": TokenNumber, "@id": TokenParameter, "dbo": TokenSchema, "users": TokenTable, "u": TokenAlias}
	for text, kind := range want {
		found := false
		for _, token := range tokens {
			if token.Text == text && token.Kind == kind {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing %q as %s in %#v", text, kind, tokens)
		}
	}
	if got := joinTokenText(tokens); got != sql {
		t.Fatalf("lexer changed SQL:\n%q\nwant %q", got, sql)
	}
}

func TestLexerSupportsCommentsQuotedIdentifiersAndUnicode(t *testing.T) {
	sql := "SELECT [Nome Completo], \"ação\", `grupo` /* multi\nline */ FROM usuários;"
	tokens, diagnostics := (Lexer{Dialect: SQLite()}).Lex(sql)
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	for _, text := range []string{"[Nome Completo]", "\"ação\"", "`grupo`"} {
		if !hasToken(tokens, text, TokenDelimitedIdentifier) {
			t.Errorf("missing delimited identifier %s", text)
		}
	}
}

func TestDiagnosticsForIncompleteAndUnclosedConstructs(t *testing.T) {
	tests := []struct{ sql, message string }{
		{"SELECT * FROM usr WHERE usr_cod =", "Expected expression after '='"},
		{"SELECT (1", "Unclosed parenthesis"},
		{"SELECT 'FROM", "Unclosed string literal"},
		{"SELECT 1 /* x", "Unclosed block comment"},
		{"SELECT a,,b FROM t", "Invalid comma"},
		{"SELECT FROM t", "Expected select expression"},
	}
	for _, test := range tests {
		analysis := Analyze(test.sql, TSQL(), 1)
		if !hasDiagnostic(analysis.Diagnostics, test.message) {
			t.Errorf("Analyze(%q) = %#v; missing %q", test.sql, analysis.Diagnostics, test.message)
		}
	}
}

func TestDialectSpecificValidation(t *testing.T) {
	if hasDiagnostic(Analyze("SELECT TOP 10 * FROM usr;", TSQL(), 1).Diagnostics, "TOP is not valid") {
		t.Fatal("TOP rejected by T-SQL")
	}
	if !hasDiagnostic(Analyze("SELECT TOP 10 * FROM usr;", PostgreSQL(), 1).Diagnostics, "TOP is not valid") {
		t.Fatal("TOP accepted by PostgreSQL")
	}
	if !hasDiagnostic(Analyze("SELECT * FROM usr LIMIT 10;", TSQL(), 1).Diagnostics, "LIMIT is not valid") {
		t.Fatal("LIMIT accepted by T-SQL")
	}
	if hasDiagnostic(Analyze("SELECT * FROM usr LIMIT 10;", SQLite(), 1).Diagnostics, "LIMIT is not valid") {
		t.Fatal("LIMIT rejected by SQLite")
	}
}

func TestStatementResolverIgnoresSemicolonsInStringsAndComments(t *testing.T) {
	sql := "SELECT 'a;b'; -- ;\nSELECT 2; /* ; */\nSELECT 3"
	analysis := Analyze(sql, PostgreSQL(), 1)
	if len(analysis.Statements) != 3 {
		t.Fatalf("statements = %#v", analysis.Statements)
	}
	second, ok := StatementAt(analysis.Statements, strings.Index(sql, "SELECT 2")+2)
	if !ok || !strings.Contains(second.Text, "SELECT 2") {
		t.Fatalf("StatementAt = %#v, %v", second, ok)
	}
}

func TestFormatterProducesReadableSQLAndPreservesTokens(t *testing.T) {
	before := "select u.usr_cod,u.usr_nome from usr u left join grp g on g.grp_cod=u.usr_grp where u.usr_status='A' order by u.usr_nome;"
	formatter := Formatter{Dialect: TSQL(), Options: DefaultFormatOptions()}
	after, err := formatter.Format(before)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"SELECT\n", "    u.usr_cod,\n", "FROM usr", "LEFT JOIN grp", "    ON g.grp_cod = u.usr_grp", "WHERE u.usr_status = 'A'", "ORDER BY u.usr_nome;"} {
		if !strings.Contains(after, fragment) {
			t.Errorf("formatted SQL missing %q:\n%s", fragment, after)
		}
	}
	if got, want := semanticTokens(after, TSQL()), semanticTokens(before, TSQL()); strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("semantic tokens changed:\n%#v\nwant %#v\nSQL:\n%s", got, want, after)
	}
}

func TestFormatterHandlesCTECaseSubqueryAndUnion(t *testing.T) {
	sql := "with dados as (select id,case when ativo=1 then 'S' else 'N' end status from usr) select * from dados union all select * from (select * from old_usr) x;"
	after, err := (Formatter{Dialect: TSQL(), Options: DefaultFormatOptions()}).Format(sql)
	if err != nil {
		t.Fatal(err)
	}
	if semantic := strings.Join(semanticTokens(after, TSQL()), "|"); semantic != strings.Join(semanticTokens(sql, TSQL()), "|") {
		t.Fatalf("semantics changed:\n%s", after)
	}
	for _, value := range []string{"WITH", "CASE", "WHEN", "UNION ALL"} {
		if !strings.Contains(after, value) {
			t.Errorf("missing %s:\n%s", value, after)
		}
	}
}

func TestDocumentDiscardsStaleAnalysis(t *testing.T) {
	doc := NewDocument("SELECT 1")
	old := Analyze(doc.Text(), SQLite(), doc.Version())
	doc.SetText("SELECT 2")
	if doc.Apply(old) {
		t.Fatal("stale analysis was accepted")
	}
	current := Analyze(doc.Text(), SQLite(), doc.Version())
	if !doc.Apply(current) {
		t.Fatal("current analysis was rejected")
	}
}

func TestDatabaseDiagnosticAndDelimiterMatching(t *testing.T) {
	sql := "SELECT (\n  1\n);"
	analysis := Analyze(sql, TSQL(), 1)
	open := strings.Index(sql, "(")
	close := strings.Index(sql, ")")
	if got, ok := MatchDelimiter(analysis.Tokens, open); !ok || got != close {
		t.Fatalf("MatchDelimiter = %d, %v", got, ok)
	}
	diagnostic := DatabaseDiagnostic(sql, DatabaseError{Message: "Incorrect syntax", Code: "102", Line: 2, Column: 3})
	if diagnostic.Source != "Database" || diagnostic.Range.Start.Line != 2 || diagnostic.Range.Start.Column != 3 {
		t.Fatalf("diagnostic = %#v", diagnostic)
	}
}

func TestBracketMatchingAndServerPositionWithUnicode(t *testing.T) {
	sql := "SELECT [ação]\nFROM usr;"
	analysis := Analyze(sql, TSQL(), 1)
	open, close := strings.Index(sql, "["), strings.Index(sql, "]")
	if got, ok := MatchDelimiter(analysis.Tokens, open); !ok || got != close {
		t.Fatalf("bracket match = %d, %v", got, ok)
	}
	position := strings.Index(sql, "FROM")
	runePosition := len([]rune(sql[:position])) + 1
	diagnostic := DatabaseDiagnostic(sql, DatabaseError{Message: "syntax error", Position: runePosition})
	if diagnostic.Range.Start.Offset != position {
		t.Fatalf("unicode position offset = %d, want %d", diagnostic.Range.Start.Offset, position)
	}
	near := DatabaseDiagnostic(sql, DatabaseError{Message: "Incorrect syntax near 'usr'.", Line: 2})
	if near.Range.Start.Column != 6 {
		t.Fatalf("near diagnostic = %#v", near)
	}
}

func TestLargeScript(t *testing.T) {
	sql := strings.Repeat("SELECT COUNT(*) FROM tabela WHERE id = @id;\n", 3000)
	analysis := Analyze(sql, TSQL(), 7)
	if analysis.Version != 7 || len(analysis.Statements) != 3000 {
		t.Fatalf("analysis = version %d statements %d", analysis.Version, len(analysis.Statements))
	}
}

func TestQualifyRelations(t *testing.T) {
	tests := []struct{ name, sql, schema, want string }{
		{name: "select and join", sql: "SELECT * FROM usr u JOIN grp g ON g.id = u.grp_id;", schema: "tenant", want: "SELECT * FROM [tenant].usr u JOIN [tenant].grp g ON g.id = u.grp_id;"},
		{name: "already qualified", sql: "SELECT * FROM audit.usr;", schema: "tenant", want: "SELECT * FROM audit.usr;"},
		{name: "cte", sql: "WITH usr AS (SELECT * FROM source_usr) SELECT * FROM usr;", schema: "tenant", want: "WITH usr AS (SELECT * FROM [tenant].source_usr) SELECT * FROM usr;"},
		{name: "cte column list", sql: "WITH usr(id) AS (SELECT id FROM source_usr) SELECT * FROM usr;", schema: "tenant", want: "WITH usr(id) AS (SELECT id FROM [tenant].source_usr) SELECT * FROM usr;"},
		{name: "update alias", sql: "UPDATE u SET name = 'x' FROM users u WHERE u.id = 1;", schema: "tenant", want: "UPDATE u SET name = 'x' FROM [tenant].users u WHERE u.id = 1;"},
		{name: "delete alias", sql: "DELETE u FROM users u WHERE u.id = 1;", schema: "tenant", want: "DELETE u FROM [tenant].users u WHERE u.id = 1;"},
		{name: "merge without into", sql: "MERGE users AS u USING source AS s ON s.id = u.id WHEN MATCHED THEN DELETE;", schema: "tenant", want: "MERGE [tenant].users AS u USING [tenant].source AS s ON s.id = u.id WHEN MATCHED THEN DELETE;"},
		{name: "comments and strings", sql: "SELECT 'FROM usr' FROM usr -- FROM grp", schema: "my schema", want: "SELECT 'FROM usr' FROM [my schema].usr -- FROM grp"},
		{name: "built in table function", sql: "SELECT * FROM OPENJSON(@json);", schema: "tenant", want: "SELECT * FROM OPENJSON(@json);"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := QualifyRelations(test.sql, TSQL(), test.schema); got != test.want {
				t.Fatalf("QualifyRelations() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestQualifyRelationsSQLite(t *testing.T) {
	if got := QualifyRelations("SELECT * FROM events;", SQLite(), "archive"); got != `SELECT * FROM "archive".events;` {
		t.Fatalf("QualifyRelations(SQLite) = %q", got)
	}
}

func joinTokenText(tokens []Token) string {
	var b strings.Builder
	for _, token := range tokens {
		b.WriteString(token.Text)
	}
	return b.String()
}
func hasToken(tokens []Token, text string, kind TokenKind) bool {
	for _, token := range tokens {
		if token.Text == text && token.Kind == kind {
			return true
		}
	}
	return false
}
func hasDiagnostic(values []Diagnostic, message string) bool {
	for _, value := range values {
		if strings.Contains(value.Message, message) {
			return true
		}
	}
	return false
}
func semanticTokens(sql string, dialect Dialect) []string {
	tokens, _ := (Lexer{Dialect: dialect}).Lex(sql)
	result := []string{}
	for _, token := range tokens {
		if token.Kind != TokenWhitespace {
			text := token.Text
			if token.Kind == TokenKeyword {
				text = strings.ToUpper(text)
			}
			result = append(result, text)
		}
	}
	return result
}
