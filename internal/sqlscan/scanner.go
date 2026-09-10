// Package sqlscan finds SQL statement boundaries and classifies execution risk.
package sqlscan

import (
	"strings"
	"unicode"
)

// Analysis describes how a statement should be executed and guarded.
type Analysis struct {
	FirstKeyword string
	IsRisky      bool
	ReturnsRows  bool
}

// Analyze conservatively marks anything other than an unambiguous read as risky.
func Analyze(sql string) Analysis {
	words := words(mask(sql))
	if len(words) == 0 {
		return Analysis{IsRisky: true}
	}
	first := words[0]
	readKeywords := map[string]bool{
		"SELECT":   true,
		"SHOW":     true,
		"EXPLAIN":  true,
		"DESCRIBE": true,
		"DESC":     true,
		"VALUES":   true,
	}
	mutatingKeywords := map[string]bool{
		"INSERT":   true,
		"UPDATE":   true,
		"DELETE":   true,
		"MERGE":    true,
		"CREATE":   true,
		"ALTER":    true,
		"DROP":     true,
		"TRUNCATE": true,
		"REPLACE":  true,
		"GRANT":    true,
		"REVOKE":   true,
		"CALL":     true,
		"EXEC":     true,
		"EXECUTE":  true,
		"VACUUM":   true,
		"ANALYZE":  true,
		"ATTACH":   true,
		"DETACH":   true,
		"PRAGMA":   true,
		"SET":      true,
		"USE":      true,
		"INTO":     true,
		"COPY":     true,
	}

	isRisky := !readKeywords[first]
	returnsRows := readKeywords[first]
	if first == "WITH" {
		isRisky = false
		returnsRows = true
	}
	if first == "PRAGMA" {
		returnsRows = true
	}
	for _, word := range words {
		if mutatingKeywords[word] {
			isRisky = true
		}
		if word == "RETURNING" || word == "OUTPUT" {
			returnsRows = true
		}
	}
	return Analysis{FirstKeyword: first, IsRisky: isRisky, ReturnsRows: returnsRows}
}

func words(masked string) []string {
	return strings.FieldsFunc(strings.ToUpper(masked), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
}

func mask(sql string) string {
	result := []byte(sql)
	const (
		stateNormal = iota
		stateSingle
		stateDouble
		stateBracket
		stateBacktick
		stateLineComment
		stateBlockComment
		stateDollar
	)
	state := stateNormal
	blockDepth := 0
	dollarTag := ""

	for i := 0; i < len(sql); i++ {
		switch state {
		case stateNormal:
			switch {
			case sql[i] == '\'':
				state = stateSingle
				result[i] = ' '
			case sql[i] == '"':
				state = stateDouble
				result[i] = ' '
			case sql[i] == '[':
				state = stateBracket
				result[i] = ' '
			case sql[i] == '`':
				state = stateBacktick
				result[i] = ' '
			case i+1 < len(sql) && sql[i:i+2] == "--":
				state = stateLineComment
				result[i], result[i+1] = ' ', ' '
				i++
			case i+1 < len(sql) && sql[i:i+2] == "/*":
				state = stateBlockComment
				blockDepth = 1
				result[i], result[i+1] = ' ', ' '
				i++
			case sql[i] == '$':
				if tag, ok := dollarAt(sql, i); ok {
					state = stateDollar
					dollarTag = tag
					for j := range len(tag) {
						result[i+j] = ' '
					}
					i += len(tag) - 1
				}
			}
		case stateSingle:
			result[i] = ' '
			if sql[i] == '\'' {
				if i+1 < len(sql) && sql[i+1] == '\'' {
					result[i+1] = ' '
					i++
				} else {
					state = stateNormal
				}
			}
		case stateDouble:
			result[i] = ' '
			if sql[i] == '"' {
				if i+1 < len(sql) && sql[i+1] == '"' {
					result[i+1] = ' '
					i++
				} else {
					state = stateNormal
				}
			}
		case stateBracket:
			result[i] = ' '
			if sql[i] == ']' {
				if i+1 < len(sql) && sql[i+1] == ']' {
					result[i+1] = ' '
					i++
				} else {
					state = stateNormal
				}
			}
		case stateBacktick:
			result[i] = ' '
			if sql[i] == '`' {
				state = stateNormal
			}
		case stateLineComment:
			if sql[i] == '\n' {
				state = stateNormal
			} else {
				result[i] = ' '
			}
		case stateBlockComment:
			result[i] = ' '
			if i+1 < len(sql) && sql[i:i+2] == "/*" {
				blockDepth++
				result[i+1] = ' '
				i++
			} else if i+1 < len(sql) && sql[i:i+2] == "*/" {
				blockDepth--
				result[i+1] = ' '
				i++
				if blockDepth == 0 {
					state = stateNormal
				}
			}
		case stateDollar:
			result[i] = ' '
			if strings.HasPrefix(sql[i:], dollarTag) {
				for j := range len(dollarTag) {
					result[i+j] = ' '
				}
				i += len(dollarTag) - 1
				state = stateNormal
			}
		}
	}
	return string(result)
}

func dollarAt(sql string, start int) (string, bool) {
	for i := start + 1; i < len(sql); i++ {
		if sql[i] == '$' {
			return sql[start : i+1], true
		}
		isTagChar := sql[i] == '_' || sql[i] >= 'a' && sql[i] <= 'z' ||
			sql[i] >= 'A' && sql[i] <= 'Z' || sql[i] >= '0' && sql[i] <= '9'
		if !isTagChar {
			return "", false
		}
	}
	return "", false
}
