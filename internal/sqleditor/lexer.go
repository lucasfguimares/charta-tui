package sqleditor

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Lexer is safe for concurrent use; it owns no mutable document state.
type Lexer struct{ Dialect Dialect }

func (l Lexer) Lex(sql string) ([]Token, []Diagnostic) {
	d := l.Dialect
	if d == nil {
		d = ANSI()
	}
	tokens := make([]Token, 0, len(sql)/4)
	diagnostics := []Diagnostic{}
	line, column := 1, 1
	pos := func(offset int) Position { return Position{Offset: offset, Line: line, Column: column} }
	advance := func(text string) {
		for _, r := range text {
			if r == '\n' {
				line, column = line+1, 1
			} else {
				column++
			}
		}
	}
	emit := func(kind TokenKind, start int, startPos Position, end int) {
		text := sql[start:end]
		advance(text)
		tokens = append(tokens, Token{Kind: kind, Text: text, Range: Range{Start: startPos, End: pos(end)}})
	}

	for i := 0; i < len(sql); {
		start, startPos := i, pos(i)
		r, size := utf8.DecodeRuneInString(sql[i:])
		switch {
		case unicode.IsSpace(r):
			for i += size; i < len(sql); {
				rr, ss := utf8.DecodeRuneInString(sql[i:])
				if !unicode.IsSpace(rr) {
					break
				}
				i += ss
			}
			emit(TokenWhitespace, start, startPos, i)
		case strings.HasPrefix(sql[i:], "--"):
			i += 2
			for i < len(sql) && sql[i] != '\n' {
				_, ss := utf8.DecodeRuneInString(sql[i:])
				i += ss
			}
			emit(TokenComment, start, startPos, i)
		case strings.HasPrefix(sql[i:], "/*"):
			i += 2
			depth := 1
			for i < len(sql) && depth > 0 {
				if strings.HasPrefix(sql[i:], "/*") {
					depth++
					i += 2
				} else if strings.HasPrefix(sql[i:], "*/") {
					depth--
					i += 2
				} else {
					_, ss := utf8.DecodeRuneInString(sql[i:])
					i += ss
				}
			}
			emit(TokenComment, start, startPos, i)
			if depth > 0 {
				diagnostics = append(diagnostics, diagnostic(Error, "Unclosed block comment", "SQL Lexer", startPos, pos(i)))
			}
		case r == '\'' || (r == '$' && d.Supports("dollar_strings") && dollarTag(sql[i:]) != ""):
			if r == '$' {
				tag := dollarTag(sql[i:])
				i += len(tag)
				closeAt := strings.Index(sql[i:], tag)
				if closeAt < 0 {
					i = len(sql)
					emit(TokenString, start, startPos, i)
					diagnostics = append(diagnostics, diagnostic(Error, "Unclosed dollar-quoted string", "SQL Lexer", startPos, pos(i)))
					continue
				}
				i += closeAt + len(tag)
				emit(TokenString, start, startPos, i)
				continue
			}
			i += size
			closed := false
			for i < len(sql) {
				if sql[i] == '\'' {
					i++
					if i < len(sql) && sql[i] == '\'' {
						i++
						continue
					}
					closed = true
					break
				}
				_, ss := utf8.DecodeRuneInString(sql[i:])
				i += ss
			}
			emit(TokenString, start, startPos, i)
			if !closed {
				diagnostics = append(diagnostics, diagnostic(Error, "Unclosed string literal", "SQL Lexer", startPos, pos(i)))
			}
		case r == '"' || r == '`' || r == '[':
			close := byte(r)
			if r == '[' {
				close = ']'
			}
			i += size
			closed := false
			for i < len(sql) {
				if sql[i] == close {
					i++
					if i < len(sql) && sql[i] == close {
						i++
						continue
					}
					closed = true
					break
				}
				_, ss := utf8.DecodeRuneInString(sql[i:])
				i += ss
			}
			emit(TokenDelimitedIdentifier, start, startPos, i)
			if !closed {
				diagnostics = append(diagnostics, diagnostic(Error, "Unclosed delimited identifier", "SQL Lexer", startPos, pos(i)))
			}
		case unicode.IsDigit(r):
			i += size
			for i < len(sql) {
				rr, ss := utf8.DecodeRuneInString(sql[i:])
				if !(unicode.IsDigit(rr) || unicode.IsLetter(rr) || rr == '.' || rr == '_') {
					break
				}
				i += ss
			}
			emit(TokenNumber, start, startPos, i)
		case r == '#' && d.Supports("hash_identifiers") && i+1 < len(sql) && (sql[i+1] == '#' || isASCIIIdent(sql[i+1])):
			i += size
			if i < len(sql) && sql[i] == '#' {
				i++
			}
			for i < len(sql) {
				rr, ss := utf8.DecodeRuneInString(sql[i:])
				if !isIdentPart(rr) {
					break
				}
				i += ss
			}
			emit(TokenIdentifier, start, startPos, i)
		case isParameterStart(sql, i, d):
			i += size
			for i < len(sql) {
				rr, ss := utf8.DecodeRuneInString(sql[i:])
				if !isIdentPart(rr) {
					break
				}
				i += ss
			}
			emit(TokenParameter, start, startPos, i)
		case isIdentStart(r):
			i += size
			for i < len(sql) {
				rr, ss := utf8.DecodeRuneInString(sql[i:])
				if !isIdentPart(rr) {
					break
				}
				i += ss
			}
			text := sql[start:i]
			kind := TokenIdentifier
			if d.IsKeyword(text) {
				kind = TokenKeyword
			} else if d.IsFunction(text) {
				kind = TokenFunction
			}
			emit(kind, start, startPos, i)
		case strings.ContainsRune("(),.;", r):
			i += size
			emit(TokenPunctuation, start, startPos, i)
		case strings.ContainsRune("+-*/%=<>!|&^~:", r):
			i += size
			if i < len(sql) && isOperatorPair(sql[start:i+1]) {
				i++
			}
			emit(TokenOperator, start, startPos, i)
		default:
			i += size
			emit(TokenInvalid, start, startPos, i)
			diagnostics = append(diagnostics, diagnostic(Error, "Unexpected token "+sql[start:i], "SQL Lexer", startPos, pos(i)))
		}
	}
	classifyIdentifiers(tokens)
	return tokens, diagnostics
}

func diagnostic(severity Severity, message, source string, start, end Position) Diagnostic {
	return Diagnostic{Severity: severity, Message: message, Source: source, Range: Range{Start: start, End: end}}
}

func dollarTag(text string) string {
	if len(text) < 2 || text[0] != '$' {
		return ""
	}
	for i := 1; i < len(text); i++ {
		if text[i] == '$' {
			return text[:i+1]
		}
		if !(text[i] == '_' || text[i] >= 'a' && text[i] <= 'z' || text[i] >= 'A' && text[i] <= 'Z' || text[i] >= '0' && text[i] <= '9') {
			return ""
		}
	}
	return ""
}

func isIdentStart(r rune) bool { return r == '_' || unicode.IsLetter(r) || r >= utf8.RuneSelf }
func isIdentPart(r rune) bool  { return isIdentStart(r) || unicode.IsDigit(r) || r == '$' }
func isOperatorPair(value string) bool {
	switch value {
	case "<=", ">=", "<>", "!=", "||", "&&", "::", ":=", "->", "=>":
		return true
	}
	return false
}
func isParameterStart(sql string, i int, d Dialect) bool {
	if sql[i] == '?' {
		return true
	}
	if sql[i] == '@' {
		return i+1 < len(sql) && isASCIIIdent(sql[i+1])
	}
	if sql[i] == ':' {
		return i+1 < len(sql) && sql[i+1] != ':' && isASCIIIdent(sql[i+1])
	}
	if sql[i] == '$' && i+1 < len(sql) && sql[i+1] >= '0' && sql[i+1] <= '9' {
		return true
	}
	return false
}
func isASCIIIdent(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

func classifyIdentifiers(tokens []Token) {
	sig := make([]int, 0, len(tokens))
	for i := range tokens {
		if tokens[i].Kind != TokenWhitespace && tokens[i].Kind != TokenComment {
			sig = append(sig, i)
		}
	}
	for p, index := range sig {
		if tokens[index].Kind != TokenIdentifier && tokens[index].Kind != TokenDelimitedIdentifier {
			continue
		}
		prev := ""
		if p > 0 {
			prev = strings.ToUpper(tokens[sig[p-1]].Text)
		}
		next := ""
		if p+1 < len(sig) {
			next = tokens[sig[p+1]].Text
		}
		isRelationStart := prev == "FROM" || prev == "JOIN" || prev == "UPDATE" || prev == "DELETE" || prev == "MERGE" || prev == "INTO" || prev == "TABLE" || prev == "VIEW" || prev == "USING" || prev == "EXEC" || prev == "EXECUTE"
		if isRelationStart {
			if next == "." {
				tokens[index].Kind = TokenSchema
			} else {
				tokens[index].Kind = TokenTable
			}
		}
		if prev == "." && p >= 2 && tokens[sig[p-2]].Kind == TokenSchema {
			tokens[index].Kind = TokenTable
		}
		if prev == "AS" {
			tokens[index].Kind = TokenAlias
		}
		if p > 0 && tokens[sig[p-1]].Kind == TokenTable && prev != "AS" && next != "." {
			tokens[index].Kind = TokenAlias
		}
		if next == "." && tokens[index].Kind == TokenIdentifier {
			tokens[index].Kind = TokenAlias
		}
		if p+1 < len(sig) && tokens[sig[p+1]].Text == "(" && tokens[index].Kind == TokenIdentifier {
			tokens[index].Kind = TokenFunction
		}
	}
}
