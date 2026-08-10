package sqleditor

import "strings"

// DatabaseError is a driver-neutral input produced by the database package.
type DatabaseError struct {
	Message  string
	Code     string
	Line     int
	Column   int
	Position int // one-based byte/rune position when supplied by the driver
}

func DatabaseDiagnostic(sql string, value DatabaseError) Diagnostic {
	offset := 0
	if value.Position > 0 {
		offset = runeOffset(sql, value.Position-1)
	} else if value.Line > 0 {
		offset = OffsetAt(sql, value.Line, max(1, value.Column))
	}
	if value.Position == 0 && value.Column == 0 {
		if near := nearToken(value.Message); near != "" {
			lineStart := OffsetAt(sql, max(1, value.Line), 1)
			lineEnd := strings.IndexByte(sql[lineStart:], '\n')
			if lineEnd < 0 {
				lineEnd = len(sql) - lineStart
			}
			if relative := strings.Index(sql[lineStart:lineStart+lineEnd], near); relative >= 0 {
				offset = lineStart + relative
			}
		}
	}
	position := PositionAt(sql, offset)
	end := position
	if offset < len(sql) {
		_, size := utf8Decode(sql[offset:])
		end = PositionAt(sql, offset+size)
	}
	message := value.Message
	if message == "" {
		message = "Database rejected the SQL statement"
	}
	return Diagnostic{Severity: Error, Message: message, Source: "Database", Code: value.Code, Range: Range{Start: position, End: end}}
}

func runeOffset(text string, runeIndex int) int {
	if runeIndex <= 0 {
		return 0
	}
	count := 0
	for offset := range text {
		if count == runeIndex {
			return offset
		}
		count++
	}
	return len(text)
}

func nearToken(message string) string {
	lower := strings.ToLower(message)
	index := strings.Index(lower, "near '")
	if index < 0 {
		index = strings.Index(lower, "at or near \"")
		if index < 0 {
			return ""
		}
		start := index + len("at or near \"")
		end := strings.IndexByte(message[start:], '"')
		if end < 0 {
			return ""
		}
		return message[start : start+end]
	}
	start := index + len("near '")
	end := strings.IndexByte(message[start:], '\'')
	if end < 0 {
		return ""
	}
	return message[start : start+end]
}
