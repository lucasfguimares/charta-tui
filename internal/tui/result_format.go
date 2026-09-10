package tui

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/lucasfguimares/charta-tui/internal/database"
)

type cellAlignment int

const (
	alignLeft cellAlignment = iota
	alignRight
)

type formattedCell struct {
	display   string
	detail    string
	clipboard string
	alignment cellAlignment
	isNull    bool
}

func formatResultValue(value any, column database.ResultColumn) formattedCell {
	if value == nil {
		return formattedCell{
			display:   "<NULL>",
			detail:    "NULL (SQL null)",
			clipboard: "NULL",
			isNull:    true,
		}
	}

	switch typed := value.(type) {
	case string:
		return formatStringValue(typed, column)
	case []byte:
		return formatBytesValue(typed, column)
	case time.Time:
		value := typed.Format(time.RFC3339Nano)
		return formattedCell{display: value, detail: value, clipboard: value}
	case bool:
		value := strconv.FormatBool(typed)
		return formattedCell{display: value, detail: value, clipboard: value}
	case int:
		return numericCell(strconv.Itoa(typed))
	case int8:
		return numericCell(strconv.FormatInt(int64(typed), 10))
	case int16:
		return numericCell(strconv.FormatInt(int64(typed), 10))
	case int32:
		return numericCell(strconv.FormatInt(int64(typed), 10))
	case int64:
		return numericCell(strconv.FormatInt(typed, 10))
	case uint:
		return numericCell(strconv.FormatUint(uint64(typed), 10))
	case uint8:
		return numericCell(strconv.FormatUint(uint64(typed), 10))
	case uint16:
		return numericCell(strconv.FormatUint(uint64(typed), 10))
	case uint32:
		return numericCell(strconv.FormatUint(uint64(typed), 10))
	case uint64:
		return numericCell(strconv.FormatUint(typed, 10))
	case float32:
		return numericCell(strconv.FormatFloat(float64(typed), 'g', -1, 32))
	case float64:
		return numericCell(strconv.FormatFloat(typed, 'g', -1, 64))
	default:
		value := fmt.Sprint(typed)
		if isNumericDatabaseType(column.DatabaseType) {
			return numericCell(value)
		}
		return formattedCell{
			display:   sanitizeInline(value),
			detail:    sanitizeDetail(value),
			clipboard: value,
		}
	}
}

func formatStringValue(value string, column database.ResultColumn) formattedCell {
	if value == "" {
		return formattedCell{display: `""`, detail: `"" (empty string)`, clipboard: ""}
	}
	display := value
	if isJSONDatabaseType(column.DatabaseType) {
		display = compactJSON([]byte(value))
	} else if isTemporalDatabaseType(column.DatabaseType) {
		display = normalizeTimeString(value)
	}
	return formattedCell{
		display:   sanitizeInline(display),
		detail:    sanitizeDetail(display),
		clipboard: value,
	}
}

func formatBytesValue(value []byte, column database.ResultColumn) formattedCell {
	if isJSONDatabaseType(column.DatabaseType) && json.Valid(value) {
		compacted := compactJSON(value)
		return formattedCell{
			display:   sanitizeInline(compacted),
			detail:    sanitizeDetail(compacted),
			clipboard: string(value),
		}
	}
	if !isBinaryDatabaseType(column.DatabaseType) && utf8.Valid(value) {
		return formatStringValue(string(value), column)
	}

	const previewBytes = 16
	preview := value
	if len(preview) > previewBytes {
		preview = preview[:previewBytes]
	}
	display := "0x" + hex.EncodeToString(preview)
	if len(preview) < len(value) {
		display += "…"
	}
	display += fmt.Sprintf(" (%d B)", len(value))
	full := "0x" + hex.EncodeToString(value)
	return formattedCell{display: display, detail: full, clipboard: full}
}

func numericCell(value string) formattedCell {
	return formattedCell{
		display:   value,
		detail:    value,
		clipboard: value,
		alignment: alignRight,
	}
}

func compactJSON(value []byte) string {
	var output bytes.Buffer
	if err := json.Compact(&output, value); err != nil {
		return string(value)
	}
	return output.String()
}

func normalizeTimeString(value string) string {
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			if layout == "2006-01-02" {
				return parsed.Format("2006-01-02")
			}
			return parsed.Format(time.RFC3339Nano)
		}
	}
	return value
}

func sanitizeInline(value string) string {
	value = ansi.Strip(value)
	var output strings.Builder
	for _, current := range value {
		switch current {
		case '\n':
			output.WriteString(`\n`)
		case '\r':
			output.WriteString(`\r`)
		case '\t':
			output.WriteString(`\t`)
		default:
			if unicode.IsControl(current) {
				output.WriteRune('�')
				continue
			}
			output.WriteRune(current)
		}
	}
	return output.String()
}

func sanitizeDetail(value string) string {
	value = ansi.Strip(value)
	return strings.Map(func(current rune) rune {
		if current == '\n' || current == '\r' || current == '\t' || !unicode.IsControl(current) {
			return current
		}
		return '�'
	}, value)
}

func isJSONDatabaseType(databaseType string) bool {
	typeName := strings.ToUpper(databaseType)
	return strings.Contains(typeName, "JSON")
}

func isBinaryDatabaseType(databaseType string) bool {
	typeName := strings.ToUpper(databaseType)
	return strings.Contains(typeName, "BINARY") || strings.Contains(typeName, "BLOB") ||
		strings.Contains(typeName, "BYTEA") || strings.Contains(typeName, "IMAGE")
}

func isTemporalDatabaseType(databaseType string) bool {
	typeName := strings.ToUpper(databaseType)
	return strings.Contains(typeName, "DATE") || strings.Contains(typeName, "TIME")
}

func isNumericDatabaseType(databaseType string) bool {
	typeName := strings.ToUpper(databaseType)
	names := []string{
		"INT", "DECIMAL", "NUMERIC", "REAL", "FLOAT", "DOUBLE", "MONEY", "SERIAL",
	}
	for _, name := range names {
		if strings.Contains(typeName, name) {
			return true
		}
	}
	return false
}

func fitCell(value string, width int, alignment cellAlignment) string {
	if width <= 0 {
		return ""
	}
	value = ansi.Truncate(value, width, "…")
	padding := max(0, width-ansi.StringWidth(value))
	if alignment == alignRight {
		return strings.Repeat(" ", padding) + value
	}
	return value + strings.Repeat(" ", padding)
}
