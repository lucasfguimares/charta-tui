package sqleditor

import (
	"sort"
	"strings"
)

// QualifyRelations applies an application-level default schema to relation
// names that are genuinely unqualified. It leaves CTEs, aliases, temporary
// objects, strings, comments and already-qualified names untouched.
//
// PostgreSQL should normally use its session search_path instead. This helper
// exists for engines such as SQL Server and SQLite that cannot select an
// arbitrary default schema for the current session.
func QualifyRelations(sql string, dialect Dialect, schema string) string {
	if strings.TrimSpace(sql) == "" || schema == "" {
		return sql
	}
	tokens, diagnostics := (Lexer{Dialect: dialect}).Lex(sql)
	for _, item := range diagnostics {
		if item.Severity == Error {
			return sql
		}
	}
	statements := ResolveStatements(sql, tokens)
	ctes := cteNamesByStatement(tokens, statements)
	aliases := relationAliasesByStatement(tokens, statements)
	prefix := quoteSchema(dialect, schema) + "."
	type insertion struct {
		offset int
		text   string
	}
	insertions := []insertion{}
	sig := significantIndexes(tokens)
	for position, index := range sig {
		token := tokens[index]
		if token.Kind != TokenTable {
			continue
		}
		statementIndex := statementIndexAt(statements, token.Range.Start.Offset)
		name := normalizedIdentifier(token.Text)
		if name == "" || ctes[statementIndex][strings.ToLower(name)] {
			continue
		}
		if position > 0 && tokens[sig[position-1]].Text == "." {
			continue
		}
		if strings.HasPrefix(token.Text, "#") || strings.HasPrefix(token.Text, "@") {
			continue
		}
		if position > 0 && (strings.EqualFold(tokens[sig[position-1]].Text, "UPDATE") || strings.EqualFold(tokens[sig[position-1]].Text, "DELETE")) && aliases[statementIndex][strings.ToLower(name)] {
			continue
		}
		insertions = append(insertions, insertion{offset: token.Range.Start.Offset, text: prefix})
	}
	if len(insertions) == 0 {
		return sql
	}
	var result strings.Builder
	result.Grow(len(sql) + len(insertions)*len(prefix))
	start := 0
	for _, item := range insertions {
		result.WriteString(sql[start:item.offset])
		result.WriteString(item.text)
		start = item.offset
	}
	result.WriteString(sql[start:])
	return result.String()
}

func quoteSchema(dialect Dialect, schema string) string {
	if dialect != nil {
		switch dialect.ID() {
		case "tsql":
			return "[" + strings.ReplaceAll(schema, "]", "]]") + "]"
		case "mysql":
			return "`" + strings.ReplaceAll(schema, "`", "``") + "`"
		}
	}
	return `"` + strings.ReplaceAll(schema, `"`, `""`) + `"`
}

func significantIndexes(tokens []Token) []int {
	result := make([]int, 0, len(tokens))
	for index, token := range tokens {
		if token.Kind != TokenWhitespace && token.Kind != TokenComment {
			result = append(result, index)
		}
	}
	return result
}

func cteNamesByStatement(tokens []Token, statements []Statement) map[int]map[string]bool {
	result := map[int]map[string]bool{}
	for index := range statements {
		result[index] = map[string]bool{}
	}
	sig := significantIndexes(tokens)
	for position, tokenIndex := range sig {
		if !strings.EqualFold(tokens[tokenIndex].Text, "WITH") {
			continue
		}
		statementIndex := statementIndexAt(statements, tokens[tokenIndex].Range.Start.Offset)
		if statementIndex < 0 {
			continue
		}
		depth := 0
		for cursor := position + 1; cursor < len(sig); cursor++ {
			current := tokens[sig[cursor]]
			if statementIndexAt(statements, current.Range.Start.Offset) != statementIndex {
				break
			}
			if current.Text == "(" {
				depth++
				continue
			}
			if current.Text == ")" {
				depth--
				continue
			}
			if depth == 0 && strings.EqualFold(current.Text, "SELECT") {
				break
			}
			if depth == 0 && isCTEDeclaration(tokens, sig, cursor) {
				result[statementIndex][strings.ToLower(normalizedIdentifier(current.Text))] = true
			}
		}
	}
	return result
}

func isCTEDeclaration(tokens []Token, sig []int, cursor int) bool {
	if cursor+2 < len(sig) && strings.EqualFold(tokens[sig[cursor+1]].Text, "AS") && tokens[sig[cursor+2]].Text == "(" {
		return true
	}
	if cursor+1 >= len(sig) || tokens[sig[cursor+1]].Text != "(" {
		return false
	}
	depth := 0
	for position := cursor + 1; position < len(sig); position++ {
		switch tokens[sig[position]].Text {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				return position+2 < len(sig) && strings.EqualFold(tokens[sig[position+1]].Text, "AS") && tokens[sig[position+2]].Text == "("
			}
		}
	}
	return false
}

func relationAliasesByStatement(tokens []Token, statements []Statement) map[int]map[string]bool {
	result := map[int]map[string]bool{}
	for index := range statements {
		result[index] = map[string]bool{}
	}
	for _, token := range tokens {
		if token.Kind == TokenAlias {
			index := statementIndexAt(statements, token.Range.Start.Offset)
			if index < 0 {
				continue
			}
			result[index][strings.ToLower(normalizedIdentifier(token.Text))] = true
		}
	}
	return result
}

func statementIndexAt(statements []Statement, offset int) int {
	index := sort.Search(len(statements), func(index int) bool {
		return statements[index].Range.End.Offset >= offset
	})
	if index < len(statements) && offset >= statements[index].Range.Start.Offset {
		return index
	}
	return -1
}

func normalizedIdentifier(value string) string {
	if len(value) >= 2 {
		switch {
		case value[0] == '[' && value[len(value)-1] == ']':
			return strings.ReplaceAll(value[1:len(value)-1], "]]", "]")
		case value[0] == '"' && value[len(value)-1] == '"':
			return strings.ReplaceAll(value[1:len(value)-1], `""`, `"`)
		case value[0] == '`' && value[len(value)-1] == '`':
			return strings.ReplaceAll(value[1:len(value)-1], "``", "`")
		}
	}
	return value
}
