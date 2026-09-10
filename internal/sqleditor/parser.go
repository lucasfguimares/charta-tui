package sqleditor

import (
	"sort"
	"strings"
	"unicode"
)

// Parser performs fast structural validation. Dialect-specific full parsers can
// implement DiagnosticProvider and be composed with this provider later.
type Parser struct{ Dialect Dialect }

type DiagnosticProvider interface {
	Diagnostics(sql string, tokens []Token) []Diagnostic
}

func (p Parser) Diagnostics(sql string, tokens []Token) []Diagnostic {
	d := p.Dialect
	if d == nil {
		d = ANSI()
	}
	diagnostics := []Diagnostic{}
	sig := significant(tokens)
	stack := []Token{}
	for _, token := range sig {
		if token.Text == "(" {
			stack = append(stack, token)
		}
		if token.Text == ")" {
			if len(stack) == 0 {
				diagnostics = append(diagnostics, diagnostic(Error, "Unmatched closing parenthesis", "SQL Parser", token.Range.Start, token.Range.End))
			} else {
				stack = stack[:len(stack)-1]
			}
		}
	}
	for _, token := range stack {
		diagnostics = append(diagnostics, diagnostic(Error, "Unclosed parenthesis", "SQL Parser", token.Range.Start, token.Range.End))
	}

	for i, token := range sig {
		upper := strings.ToUpper(token.Text)
		prev, next := Token{}, Token{}
		if i > 0 {
			prev = sig[i-1]
		}
		if i+1 < len(sig) {
			next = sig[i+1]
		}
		if token.Text == "," && (i == 0 || i == len(sig)-1 || prev.Text == "," || next.Text == "," || isClause(next)) {
			diagnostics = append(diagnostics, diagnostic(Error, "Invalid comma", "SQL Parser", token.Range.Start, token.Range.End))
		}
		if isBinaryOperator(token) && (i == len(sig)-1 || next.Text == ")" || next.Text == ";" || next.Text == ",") {
			diagnostics = append(diagnostics, diagnostic(Error, "Expected expression after '"+token.Text+"'", "SQL Parser", token.Range.Start, token.Range.End))
		}
		if (upper == "WHERE" || upper == "HAVING" || upper == "ON" || upper == "WHEN" || upper == "THEN" || upper == "ELSE" || upper == "SET" || upper == "VALUES") &&
			(i == len(sig)-1 || next.Text == ";" || next.Text == ")" || isClause(next)) {
			diagnostics = append(diagnostics, diagnostic(Error, "Expected expression after "+upper, "SQL Parser", token.Range.Start, token.Range.End))
		}
		if (upper == "FROM" || upper == "JOIN" || upper == "INTO" || upper == "UPDATE" || upper == "TABLE") &&
			(i == len(sig)-1 || next.Text == ";" || next.Text == ")" || isClause(next)) {
			diagnostics = append(diagnostics, diagnostic(Error, "Expected relation after "+upper, "SQL Parser", token.Range.Start, token.Range.End))
		}
		if upper == "SELECT" && (i == len(sig)-1 || next.Text == ";" || strings.EqualFold(next.Text, "FROM")) {
			diagnostics = append(diagnostics, diagnostic(Error, "Expected select expression", "SQL Parser", token.Range.Start, token.Range.End))
		}
		if upper == "TOP" && !d.Supports("top") {
			diagnostics = append(diagnostics, diagnostic(Error, "TOP is not valid in the "+d.DisplayName()+" dialect", "SQL Dialect", token.Range.Start, token.Range.End))
		}
		if upper == "LIMIT" && !d.Supports("limit") {
			diagnostics = append(diagnostics, diagnostic(Error, "LIMIT is not valid in the "+d.DisplayName()+" dialect", "SQL Dialect", token.Range.Start, token.Range.End))
		}
	}
	diagnostics = append(diagnostics, clauseOrderDiagnostics(sig)...)
	sort.SliceStable(diagnostics, func(i, j int) bool { return diagnostics[i].Range.Start.Offset < diagnostics[j].Range.Start.Offset })
	return uniqueDiagnostics(diagnostics)
}

func significant(tokens []Token) []Token {
	result := make([]Token, 0, len(tokens))
	for _, token := range tokens {
		if token.Kind != TokenWhitespace && token.Kind != TokenComment {
			result = append(result, token)
		}
	}
	return result
}

func isBinaryOperator(token Token) bool {
	if token.Kind == TokenOperator {
		return token.Text != "*"
	}
	switch strings.ToUpper(token.Text) {
	case "AND", "OR", "LIKE", "IN", "IS", "BETWEEN":
		return true
	}
	return false
}

func isClause(token Token) bool {
	switch strings.ToUpper(token.Text) {
	case "FROM", "WHERE", "JOIN", "HAVING", "ORDER", "GROUP", "UNION", "LIMIT", "RETURNING", "ON":
		return true
	}
	return false
}

func clauseOrderDiagnostics(tokens []Token) []Diagnostic {
	result := []Diagnostic{}
	level, phase := 0, 0
	order := map[string]int{"SELECT": 1, "FROM": 2, "WHERE": 3, "GROUP": 4, "HAVING": 5, "ORDER": 6, "LIMIT": 7, "RETURNING": 8}
	for _, token := range tokens {
		if token.Text == "(" {
			level++
			continue
		}
		if token.Text == ")" {
			level--
			continue
		}
		if token.Text == ";" || strings.EqualFold(token.Text, "UNION") {
			phase = 0
			continue
		}
		if level != 0 {
			continue
		}
		current, ok := order[strings.ToUpper(token.Text)]
		if !ok {
			continue
		}
		if current < phase && strings.ToUpper(token.Text) != "SELECT" {
			result = append(result, diagnostic(Error, "Invalid clause order near "+strings.ToUpper(token.Text), "SQL Parser", token.Range.Start, token.Range.End))
		} else if current > phase {
			phase = current
		}
	}
	return result
}

func uniqueDiagnostics(values []Diagnostic) []Diagnostic {
	seen := map[string]bool{}
	result := make([]Diagnostic, 0, len(values))
	for _, d := range values {
		key := d.Message + "\x00" + string(rune(d.Range.Start.Offset))
		if !seen[key] {
			seen[key] = true
			result = append(result, d)
		}
	}
	return result
}

// Analyze lexes, validates, and resolves statements in one worker-friendly pass.
func Analyze(sql string, dialect Dialect, version uint64) Analysis {
	lexer := Lexer{Dialect: dialect}
	tokens, diagnostics := lexer.Lex(sql)
	diagnostics = append(diagnostics, (Parser{Dialect: dialect}).Diagnostics(sql, tokens)...)
	return Analysis{Version: version, Tokens: tokens, Diagnostics: uniqueDiagnostics(diagnostics), Statements: ResolveStatements(sql, tokens)}
}

func ResolveStatements(sql string, tokens []Token) []Statement {
	result := []Statement{}
	start := 0
	depth := 0
	for _, token := range tokens {
		if token.Kind == TokenComment || token.Kind == TokenString {
			continue
		}
		if token.Text == "(" {
			depth++
		}
		if token.Text == ")" && depth > 0 {
			depth--
		}
		if token.Text == ";" && depth == 0 {
			if statement, ok := trimStatement(sql, start, token.Range.End.Offset); ok {
				result = append(result, statement)
			}
			start = token.Range.End.Offset
		}
	}
	if statement, ok := trimStatement(sql, start, len(sql)); ok {
		result = append(result, statement)
	}
	return result
}

// Statements lexes SQL with the active dialect and resolves its top-level
// statements. Editor rendering, formatting and execution use this single
// boundary implementation so semicolons inside dialect-specific literals are
// handled consistently.
func Statements(sql string, dialect Dialect) []Statement {
	if dialect == nil {
		dialect = ANSI()
	}
	tokens, _ := (Lexer{Dialect: dialect}).Lex(sql)
	return ResolveStatements(sql, tokens)
}

// StatementAtOffset resolves the statement nearest the byte offset in SQL.
func StatementAtOffset(sql string, offset int, dialect Dialect) (Statement, bool) {
	if offset < 0 {
		offset = 0
	}
	if offset > len(sql) {
		offset = len(sql)
	}
	return StatementAt(Statements(sql, dialect), offset)
}

func StatementAt(statements []Statement, offset int) (Statement, bool) {
	for i, statement := range statements {
		if offset >= statement.Range.Start.Offset && offset <= statement.Range.End.Offset {
			return statement, true
		}
		if offset < statement.Range.Start.Offset {
			if i > 0 {
				return statements[i-1], true
			}
			return statement, true
		}
	}
	if len(statements) > 0 {
		return statements[len(statements)-1], true
	}
	return Statement{}, false
}

func trimStatement(sql string, start, end int) (Statement, bool) {
	for start < end {
		r, size := utf8Decode(sql[start:])
		if !unicode.IsSpace(r) {
			break
		}
		start += size
	}
	for end > start {
		r, size := utf8DecodeLast(sql[:end])
		if !unicode.IsSpace(r) {
			break
		}
		end -= size
	}
	if start >= end || strings.Trim(strings.TrimSpace(sql[start:end]), ";") == "" {
		return Statement{}, false
	}
	return Statement{Range: Range{Start: PositionAt(sql, start), End: PositionAt(sql, end)}, Text: sql[start:end]}, true
}
