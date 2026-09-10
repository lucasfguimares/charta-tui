package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/lucasfguimares/charta-tui/internal/database"
)

const rowInspectorPreviewLines = 3

type rowInspectorField struct {
	name string
	cell formattedCell
}

type rowInspectorModel struct {
	fields        []rowInspectorField
	rendered      [][]string
	rowNumber     int
	cursor        int
	lineOffset    int
	width         int
	height        int
	expandedField int
}

func newRowInspector(columns []database.ResultColumn, row []any, rowIndex int) rowInspectorModel {
	fields := make([]rowInspectorField, len(columns))
	for index, column := range columns {
		var value any
		if index < len(row) {
			value = row[index]
		}
		fields[index] = rowInspectorField{
			name: sanitizeInline(column.Name),
			cell: formatResultValue(value, column),
		}
	}
	inspector := rowInspectorModel{
		fields: fields, rowNumber: rowIndex + 1, expandedField: -1,
	}
	inspector.SetSize(80, 20)
	return inspector
}

func (inspector *rowInspectorModel) SetSize(width, height int) {
	inspector.width = max(1, width)
	inspector.height = max(1, height)
	inspector.rebuild()
	inspector.ensureVisible()
}

func (inspector *rowInspectorModel) HandleNavigation(key string) bool {
	if len(inspector.fields) == 0 {
		return false
	}
	previous := inspector.cursor
	switch key {
	case "up", "k":
		inspector.cursor--
	case "down", "j":
		inspector.cursor++
	case "pgup":
		if inspector.scrollExpandedField(-1) {
			return true
		}
		inspector.cursor -= max(1, inspector.height/2)
	case "pgdown":
		if inspector.scrollExpandedField(1) {
			return true
		}
		inspector.cursor += max(1, inspector.height/2)
	case "home":
		inspector.cursor = 0
	case "end":
		inspector.cursor = len(inspector.fields) - 1
	default:
		return false
	}
	inspector.cursor = min(max(0, inspector.cursor), len(inspector.fields)-1)
	if inspector.cursor == previous {
		return false
	}
	inspector.ensureVisible()
	return true
}

func (inspector *rowInspectorModel) ToggleExpanded() bool {
	if inspector.cursor < 0 || inspector.cursor >= len(inspector.fields) {
		return false
	}
	if inspector.expandedField == inspector.cursor {
		inspector.expandedField = -1
	} else {
		inspector.expandedField = inspector.cursor
	}
	inspector.rebuild()
	inspector.ensureVisible()
	return true
}

func (inspector *rowInspectorModel) ValueClipboard() (string, bool) {
	if inspector.cursor < 0 || inspector.cursor >= len(inspector.fields) {
		return "", false
	}
	return inspector.fields[inspector.cursor].cell.clipboard, true
}

func (inspector *rowInspectorModel) FieldClipboard() (string, bool) {
	if inspector.cursor < 0 || inspector.cursor >= len(inspector.fields) {
		return "", false
	}
	field := inspector.fields[inspector.cursor]
	return field.name + "\t" + field.cell.clipboard, true
}

func (inspector *rowInspectorModel) View(styles styles) string {
	if len(inspector.fields) == 0 {
		return styles.dim.Render("No fields in this row.")
	}
	lines := make([]string, 0, inspector.height)
	lineIndex := 0
	for fieldIndex := 0; fieldIndex < len(inspector.rendered) && len(lines) < inspector.height; fieldIndex++ {
		for _, line := range inspector.rendered[fieldIndex] {
			if lineIndex < inspector.lineOffset {
				lineIndex++
				continue
			}
			if len(lines) >= inspector.height {
				break
			}
			line = ansi.Truncate(line, inspector.width, "…")
			if fieldIndex == inspector.cursor {
				line = styles.selected.Width(inspector.width).Render(line)
			}
			lines = append(lines, line)
			lineIndex++
		}
	}
	for len(lines) < inspector.height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (inspector *rowInspectorModel) rebuild() {
	labelWidth := 8
	for _, field := range inspector.fields {
		labelWidth = max(labelWidth, ansi.StringWidth(field.name))
	}
	labelWidth = min(labelWidth, max(8, inspector.width/3))
	valueWidth := max(1, inspector.width-labelWidth-2)
	inspector.rendered = make([][]string, len(inspector.fields))
	for index, field := range inspector.fields {
		value := field.cell.detail
		if field.cell.isNull {
			value = "<NULL>"
		}
		wrapped := strings.Split(ansi.Hardwrap(value, valueWidth, true), "\n")
		if inspector.expandedField != index && len(wrapped) > rowInspectorPreviewLines {
			wrapped = append(wrapped[:rowInspectorPreviewLines], "… (Enter expand)")
		}
		lines := make([]string, len(wrapped))
		for lineIndex, current := range wrapped {
			label := ""
			if lineIndex == 0 {
				label = field.name
			}
			lines[lineIndex] = fitCell(label, labelWidth, alignLeft) + "  " + current
		}
		inspector.rendered[index] = lines
	}
}

func (inspector *rowInspectorModel) ensureVisible() {
	if len(inspector.rendered) == 0 {
		inspector.cursor, inspector.lineOffset = 0, 0
		return
	}
	inspector.cursor = min(max(0, inspector.cursor), len(inspector.rendered)-1)
	start, end := inspector.fieldLineRange(inspector.cursor)
	if start < inspector.lineOffset {
		inspector.lineOffset = start
	}
	if end > inspector.lineOffset+inspector.height {
		if end-start >= inspector.height {
			inspector.lineOffset = start
		} else {
			inspector.lineOffset = end - inspector.height
		}
	}
	inspector.lineOffset = min(max(0, inspector.lineOffset), max(0, inspector.totalLineCount()-inspector.height))
}

func (inspector *rowInspectorModel) scrollExpandedField(direction int) bool {
	if inspector.expandedField != inspector.cursor || inspector.cursor < 0 || inspector.cursor >= len(inspector.rendered) {
		return false
	}
	start, end := inspector.fieldLineRange(inspector.cursor)
	maximumOffset := max(start, end-inspector.height)
	next := inspector.lineOffset + direction*max(1, inspector.height-1)
	next = min(max(start, next), maximumOffset)
	if next == inspector.lineOffset {
		return false
	}
	inspector.lineOffset = next
	return true
}

func (inspector *rowInspectorModel) fieldLineRange(field int) (int, int) {
	start := 0
	for index := 0; index < field && index < len(inspector.rendered); index++ {
		start += len(inspector.rendered[index])
	}
	if field < 0 || field >= len(inspector.rendered) {
		return start, start
	}
	return start, start + len(inspector.rendered[field])
}

func (inspector *rowInspectorModel) totalLineCount() int {
	total := 0
	for _, lines := range inspector.rendered {
		total += len(lines)
	}
	return total
}
