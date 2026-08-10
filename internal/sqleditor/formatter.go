package sqleditor

import (
	"strings"
	"unicode"
)

// Formatter is a token-based whitespace formatter. Because every non-whitespace
// token is copied verbatim, literals, identifiers, parameters and operators keep
// their original semantics.
type Formatter struct {
	Dialect Dialect
	Options FormatOptions
}

func (f Formatter) Format(sql string) (string, error) {
	d := f.Dialect
	if d == nil {
		d = ANSI()
	}
	options := f.Options
	if options.IndentSize <= 0 {
		options = DefaultFormatOptions()
	}
	tokens, diagnostics := (Lexer{Dialect: d}).Lex(sql)
	for _, item := range diagnostics {
		if item.Severity == Error {
			return sql, &FormatError{Diagnostic: item}
		}
	}
	tokens = withoutWhitespace(tokens)
	if len(tokens) == 0 {
		return strings.TrimSpace(sql), nil
	}

	w := formatWriter{options: options, lineStart: true}
	indent, parenDepth, selectDepth := 0, 0, -1
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		upper := strings.ToUpper(token.Text)
		prev := Token{}
		if i > 0 {
			prev = tokens[i-1]
		}
		next := Token{}
		if i+1 < len(tokens) {
			next = tokens[i+1]
		}

		if token.Kind == TokenComment {
			if strings.HasPrefix(token.Text, "--") {
				w.space()
				w.write(token.Text)
				w.newline(indent)
				continue
			}
			if !w.lineStart {
				w.space()
			}
			w.write(token.Text)
			if strings.Contains(token.Text, "\n") {
				w.newline(indent)
			} else {
				w.space()
			}
			continue
		}

		phrase, consumed := clausePhrase(tokens, i)
		if phrase != "" {
			upper = phrase
			i += consumed
			if upper == "SELECT" {
				if !w.lineStart && prev.Text != "(" {
					w.newline(indent)
				}
				w.write(keywordCase(upper, options.KeywordCase))
				selectDepth = parenDepth
				if options.ColumnsOnNewLine {
					w.newline(indent + 1)
				} else {
					w.space()
				}
				continue
			}
			if isMainClause(upper) {
				if upper == "UNION" || upper == "UNION ALL" {
					if indent > 0 {
						indent--
					}
				}
				w.newline(indent)
				w.write(keywordCase(upper, options.KeywordCase))
				if upper == "FROM" {
					selectDepth = -1
				}
				w.space()
				continue
			}
			if isJoinClause(upper) {
				if options.BreakJoin {
					w.newline(indent)
				} else {
					w.space()
				}
				w.write(keywordCase(upper, options.KeywordCase))
				w.space()
				continue
			}
		}

		switch {
		case token.Text == "(":
			if needsSpaceBeforeParen(prev) {
				w.space()
			}
			w.write("(")
			parenDepth++
			if strings.EqualFold(next.Text, "SELECT") || strings.EqualFold(next.Text, "WITH") {
				indent++
				w.newline(indent)
			}
		case token.Text == ")":
			parenDepth--
			if !w.lineStart && (strings.EqualFold(prev.Text, "SELECT") || prev.Text == ",") {
				w.newline(max(0, indent-1))
			}
			if indent > parenDepth {
				indent = max(0, indent-1)
				if !w.lineStart {
					w.newline(indent)
				}
			}
			w.write(")")
		case token.Text == ".":
			w.write(".")
		case token.Text == ",":
			if options.CommaStyle == CommaLeading && selectDepth == parenDepth {
				w.newline(indent + 1)
				w.write(", ")
			} else {
				w.write(",")
				if selectDepth == parenDepth && options.ColumnsOnNewLine {
					w.newline(indent + 1)
				} else {
					w.space()
				}
			}
		case token.Text == ";":
			w.write(";")
			if i+1 < len(tokens) {
				w.newline(0)
				w.newline(0)
			}
			indent, selectDepth = 0, -1
		case token.Kind == TokenOperator || isWordOperator(upper):
			w.space()
			w.write(keywordOrOriginal(token, upper, options.KeywordCase))
			w.space()
		case (upper == "AND" || upper == "OR") && options.BreakBoolean:
			w.newline(indent + 1)
			w.write(keywordCase(upper, options.KeywordCase))
			w.space()
		case upper == "ON":
			w.newline(indent + 1)
			w.write(keywordCase(upper, options.KeywordCase))
			w.space()
		case upper == "WHEN" || upper == "ELSE":
			w.newline(indent + 1)
			w.write(keywordCase(upper, options.KeywordCase))
			w.space()
		case upper == "THEN":
			w.space()
			w.write(keywordCase(upper, options.KeywordCase))
			w.space()
		case upper == "CASE":
			w.write(keywordCase(upper, options.KeywordCase))
			indent++
			w.space()
		case upper == "END":
			indent = max(0, indent-1)
			w.newline(indent)
			w.write(keywordCase(upper, options.KeywordCase))
		default:
			if shouldSeparate(prev, token) {
				w.space()
			}
			w.write(keywordOrOriginal(token, upper, options.KeywordCase))
		}
	}
	return strings.TrimSpace(w.String()), nil
}

type FormatError struct{ Diagnostic Diagnostic }

func (e *FormatError) Error() string { return e.Diagnostic.String() }

type formatWriter struct {
	strings.Builder
	options   FormatOptions
	lineStart bool
	lastSpace bool
}

func (w *formatWriter) indentation(level int) string {
	if w.options.UseTabs {
		return strings.Repeat("\t", level)
	}
	return strings.Repeat(" ", level*w.options.IndentSize)
}
func (w *formatWriter) write(value string) {
	if value == "" {
		return
	}
	w.Builder.WriteString(value)
	w.lineStart = false
	w.lastSpace = unicode.IsSpace(rune(value[len(value)-1]))
}
func (w *formatWriter) space() {
	if !w.lineStart && !w.lastSpace {
		w.Builder.WriteByte(' ')
		w.lastSpace = true
	}
}
func (w *formatWriter) newline(indent int) {
	value := strings.TrimRight(w.Builder.String(), " \t")
	if len(value) != w.Builder.Len() {
		w.Builder.Reset()
		w.Builder.WriteString(value)
	}
	if w.Builder.Len() > 0 && !strings.HasSuffix(w.Builder.String(), "\n") {
		w.Builder.WriteByte('\n')
	}
	w.Builder.WriteString(w.indentation(max(0, indent)))
	w.lineStart = true
	w.lastSpace = false
}

func withoutWhitespace(tokens []Token) []Token {
	result := make([]Token, 0, len(tokens))
	for _, t := range tokens {
		if t.Kind != TokenWhitespace {
			result = append(result, t)
		}
	}
	return result
}
func keywordCase(value string, style KeywordCase) string {
	if style == KeywordLower {
		return strings.ToLower(value)
	}
	if style == KeywordUpper {
		return strings.ToUpper(value)
	}
	return value
}
func keywordOrOriginal(token Token, upper string, style KeywordCase) string {
	if token.Kind == TokenKeyword {
		return keywordCase(upper, style)
	}
	return token.Text
}
func isWordOperator(value string) bool {
	switch value {
	case "LIKE", "ILIKE", "IN", "IS", "BETWEEN":
		return true
	}
	return false
}
func needsSpaceBeforeParen(previous Token) bool {
	return previous.Kind == TokenKeyword && !strings.EqualFold(previous.Text, "IN") || previous.Kind == TokenTable || previous.Kind == TokenAlias
}
func shouldSeparate(previous, current Token) bool {
	if previous.Text == "" || previous.Text == "(" || previous.Text == "." || current.Text == "." || current.Text == ")" || current.Text == ";" || current.Text == "," {
		return false
	}
	return true
}
func isMainClause(value string) bool {
	switch value {
	case "FROM", "WHERE", "GROUP BY", "ORDER BY", "HAVING", "LIMIT", "OFFSET", "RETURNING", "VALUES", "SET", "UNION", "UNION ALL":
		return true
	}
	return false
}
func isJoinClause(value string) bool {
	return strings.HasSuffix(value, "JOIN") || value == "CROSS APPLY" || value == "OUTER APPLY"
}

func clausePhrase(tokens []Token, index int) (string, int) {
	upper := strings.ToUpper(tokens[index].Text)
	if upper == "SELECT" || upper == "FROM" || upper == "WHERE" || upper == "HAVING" || upper == "LIMIT" || upper == "OFFSET" || upper == "RETURNING" || upper == "VALUES" || upper == "SET" {
		return upper, 0
	}
	if index+1 < len(tokens) {
		next := strings.ToUpper(tokens[index+1].Text)
		pair := upper + " " + next
		switch pair {
		case "GROUP BY", "ORDER BY", "UNION ALL", "LEFT JOIN", "RIGHT JOIN", "FULL JOIN", "INNER JOIN", "CROSS JOIN", "OUTER APPLY", "CROSS APPLY":
			return pair, 1
		}
	}
	if upper == "JOIN" || upper == "UNION" {
		return upper, 0
	}
	return "", 0
}
