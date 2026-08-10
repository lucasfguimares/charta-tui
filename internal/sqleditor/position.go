package sqleditor

import "unicode/utf8"

func PositionAt(text string, offset int) Position {
	if offset < 0 {
		offset = 0
	}
	if offset > len(text) {
		offset = len(text)
	}
	line, column := 1, 1
	for _, r := range text[:offset] {
		if r == '\n' {
			line, column = line+1, 1
		} else {
			column++
		}
	}
	return Position{Offset: offset, Line: line, Column: column}
}

func OffsetAt(text string, line, column int) int {
	if line < 1 {
		line = 1
	}
	if column < 1 {
		column = 1
	}
	currentLine, currentColumn := 1, 1
	for offset, r := range text {
		if currentLine == line && currentColumn >= column {
			return offset
		}
		if r == '\n' {
			if currentLine == line {
				return offset
			}
			currentLine++
			currentColumn = 1
		} else {
			currentColumn++
		}
	}
	return len(text)
}

func utf8Decode(text string) (rune, int)     { return utf8.DecodeRuneInString(text) }
func utf8DecodeLast(text string) (rune, int) { return utf8.DecodeLastRuneInString(text) }

// MatchDelimiter returns the matching delimiter offset for (), and [] when
// brackets are not part of a quoted identifier.
func MatchDelimiter(tokens []Token, offset int) (int, bool) {
	type entry struct {
		text   string
		offset int
	}
	stack := []entry{}
	for _, token := range tokens {
		if token.Kind == TokenDelimitedIdentifier && len(token.Text) >= 2 && token.Text[0] == '[' && token.Text[len(token.Text)-1] == ']' {
			open, close := token.Range.Start.Offset, token.Range.End.Offset-1
			if offset == open {
				return close, true
			}
			if offset == close {
				return open, true
			}
			continue
		}
		if token.Kind == TokenString || token.Kind == TokenComment || token.Kind == TokenDelimitedIdentifier {
			continue
		}
		if token.Text == "(" {
			stack = append(stack, entry{token.Text, token.Range.Start.Offset})
		}
		if token.Text == ")" {
			if len(stack) == 0 {
				continue
			}
			open := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if offset == open.offset {
				return token.Range.Start.Offset, true
			}
			if offset == token.Range.Start.Offset {
				return open.offset, true
			}
		}
	}
	return 0, false
}
