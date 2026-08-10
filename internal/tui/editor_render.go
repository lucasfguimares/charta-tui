package tui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/lucasfguimares/tui-db/internal/sqleditor"
)

type charDecoration struct {
	kind          sqleditor.TokenKind
	statement     bool
	selection     bool
	diagnostic    sqleditor.Severity
	hasDiagnostic bool
	match         bool
	cursor        bool
}

func (m *Model) renderSQLEditor(tab *queryTab, width, height int) string {
	if tab.editor.Value() == "" {
		return m.overlayAutocomplete(tab, tab.editor.View(), width, height, 0)
	}
	value := tab.editor.Value()
	lines := strings.Split(value, "\n")
	cursorOffset, _ := cursorOffset(tab.editor)
	statement, hasStatement := sqleditor.StatementAt(tab.analysis.Statements, cursorOffset)
	matchOffset, hasMatch := sqleditor.MatchDelimiter(tab.analysis.Tokens, cursorOffset)
	if !hasMatch && cursorOffset > 0 {
		matchOffset, hasMatch = sqleditor.MatchDelimiter(tab.analysis.Tokens, cursorOffset-1)
	}
	selectionStart, selectionEnd := -1, -1
	if tab.selection != nil && *tab.selection != cursorOffset {
		selectionStart, selectionEnd = *tab.selection, cursorOffset
		if selectionStart > selectionEnd {
			selectionStart, selectionEnd = selectionEnd, selectionStart
		}
	}

	panelHeight := height
	if len(tab.analysis.Diagnostics) > 0 {
		panelHeight = max(1, height-1)
	}
	start := max(0, tab.editor.ScrollYOffset())
	if start >= len(lines) {
		start = max(0, len(lines)-1)
	}
	cursorLine := tab.editor.Line()
	if cursorLine < start {
		start = cursorLine
	}
	if cursorLine >= start+panelHeight {
		start = cursorLine - panelHeight + 1
	}
	end := min(len(lines), start+panelHeight)
	digits := len(fmt.Sprint(max(1, len(lines))))
	result := make([]string, 0, height)
	for lineIndex := start; lineIndex < end; lineIndex++ {
		lineNo := lineIndex + 1
		line := []rune(lines[lineIndex])
		decorations := make([]charDecoration, len(line)+1)
		for i := range decorations {
			decorations[i].kind = sqleditor.TokenIdentifier
		}
		decorateTokens(decorations, lineNo, tab.analysis.Tokens)
		if hasStatement {
			decorateRange(decorations, lineNo, statement.Range, func(d *charDecoration) { d.statement = true })
		}
		if selectionStart >= 0 {
			decorateRange(decorations, lineNo, sqleditor.Range{Start: sqleditor.PositionAt(value, selectionStart), End: sqleditor.PositionAt(value, selectionEnd)}, func(d *charDecoration) { d.selection = true })
		}
		for _, diagnostic := range tab.analysis.Diagnostics {
			severity := diagnostic.Severity
			decorateRange(decorations, lineNo, diagnostic.Range, func(d *charDecoration) { d.hasDiagnostic = true; d.diagnostic = severity })
		}
		if hasMatch {
			matchPosition := sqleditor.PositionAt(value, matchOffset)
			if matchPosition.Line == lineNo && matchPosition.Column-1 < len(decorations) {
				decorations[matchPosition.Column-1].match = true
			}
		}
		if lineIndex == tab.editor.Line() && tab.editor.Column() < len(decorations) {
			decorations[tab.editor.Column()].cursor = m.focus == focusEditor
		}

		marker := " "
		if diagnostic, ok := mostSevereOnLine(tab.analysis.Diagnostics, lineNo); ok {
			marker = diagnostic.Severity.Marker()
		}
		gutter := m.styles.dim.Render(fmt.Sprintf("%s %*d │ ", marker, digits, lineNo))
		available := max(1, width-lipgloss.Width(gutter))
		if len(line) > available {
			line = line[:available]
			decorations = decorations[:available]
		}
		body := renderDecorated(line, decorations, m.styles)
		if lineIndex == tab.editor.Line() && tab.editor.Column() >= len(line) && len(line) < available && m.focus == focusEditor {
			body += lipgloss.NewStyle().Reverse(true).Render(" ")
		}
		result = append(result, gutter+body)
	}
	for len(result) < panelHeight {
		result = append(result, "")
	}
	if len(tab.analysis.Diagnostics) > 0 {
		index := min(tab.diagnostic, len(tab.analysis.Diagnostics)-1)
		current := tab.analysis.Diagnostics[index]
		counts := diagnosticCounts(tab.analysis.Diagnostics)
		line := fmt.Sprintf("%s  Errors: %d | Warnings: %d | Current: %d/%d", current.String(), counts[0], counts[1], index+1, len(tab.analysis.Diagnostics))
		result = append(result, severityStyle(m.styles, current.Severity).Render(truncate(line, width)))
	}
	return m.overlayAutocomplete(tab, strings.Join(result, "\n"), width, height, start)
}

func (m *Model) overlayAutocomplete(tab *queryTab, editor string, width, height, firstLine int) string {
	if tab == nil || !tab.completion.isOpen || width < 34 || height < 3 {
		return editor
	}
	popupWidth := min(68, max(30, width-4))
	popup := m.renderAutocomplete(tab, popupWidth)
	popupHeight := lipgloss.Height(popup)
	gutterWidth := len(fmt.Sprint(max(1, len(strings.Split(tab.editor.Value(), "\n"))))) + 5
	x := min(max(0, gutterWidth+tab.editor.Column()), max(0, width-lipgloss.Width(popup)))
	y := tab.editor.Line() - firstLine + 1
	if y+popupHeight > height {
		y = max(0, tab.editor.Line()-firstLine-popupHeight)
	}
	canvas := lipgloss.NewCanvas(width, height)
	canvas.Compose(lipgloss.NewLayer(editor))
	canvas.Compose(lipgloss.NewLayer(popup).X(x).Y(y).Z(1))
	return canvas.Render()
}

func decorateTokens(chars []charDecoration, line int, tokens []sqleditor.Token) {
	for _, token := range tokens {
		if line < token.Range.Start.Line || line > token.Range.End.Line {
			continue
		}
		start, end := 0, len(chars)
		if line == token.Range.Start.Line {
			start = token.Range.Start.Column - 1
		}
		if line == token.Range.End.Line {
			end = token.Range.End.Column - 1
		}
		start, end = max(0, start), min(len(chars), max(start, end))
		for i := start; i < end; i++ {
			chars[i].kind = token.Kind
		}
	}
}

func decorateRange(chars []charDecoration, line int, value sqleditor.Range, apply func(*charDecoration)) {
	if line < value.Start.Line || line > value.End.Line {
		return
	}
	start, end := 0, len(chars)
	if line == value.Start.Line {
		start = value.Start.Column - 1
	}
	if line == value.End.Line {
		end = value.End.Column - 1
	}
	if end <= start {
		end = start + 1
	}
	for i := max(0, start); i < min(len(chars), end); i++ {
		apply(&chars[i])
	}
}

func renderDecorated(line []rune, decorations []charDecoration, styles styles) string {
	if len(line) == 0 {
		return ""
	}
	var result strings.Builder
	start := 0
	for i := 1; i <= len(line); i++ {
		if i < len(line) && decorations[i] == decorations[start] {
			continue
		}
		style := tokenStyle(styles, decorations[start].kind)
		if decorations[start].statement {
			style = style.Background(lipgloss.Color("234"))
		}
		if decorations[start].selection {
			style = style.Background(lipgloss.Color("24"))
		}
		if decorations[start].match {
			style = style.Background(lipgloss.Color("58")).Bold(true)
		}
		if decorations[start].hasDiagnostic {
			style = style.Underline(true)
			if decorations[start].diagnostic == sqleditor.Error {
				style = style.Foreground(lipgloss.Color("203"))
			}
		}
		if decorations[start].cursor {
			style = style.Reverse(true)
		}
		result.WriteString(style.Render(string(line[start:i])))
		start = i
	}
	return result.String()
}

func tokenStyle(s styles, kind sqleditor.TokenKind) lipgloss.Style {
	switch kind {
	case sqleditor.TokenKeyword:
		return s.keyword
	case sqleditor.TokenFunction:
		return s.function
	case sqleditor.TokenString:
		return s.string
	case sqleditor.TokenNumber:
		return s.number
	case sqleditor.TokenOperator:
		return s.operator
	case sqleditor.TokenComment:
		return s.comment
	case sqleditor.TokenSchema:
		return s.schema
	case sqleditor.TokenTable:
		return s.table
	case sqleditor.TokenAlias:
		return s.alias
	case sqleditor.TokenParameter:
		return s.parameter
	case sqleditor.TokenDelimitedIdentifier:
		return s.accent
	default:
		return s.identifier
	}
}

func mostSevereOnLine(values []sqleditor.Diagnostic, line int) (sqleditor.Diagnostic, bool) {
	matches := []sqleditor.Diagnostic{}
	for _, value := range values {
		if line >= value.Range.Start.Line && line <= value.Range.End.Line {
			matches = append(matches, value)
		}
	}
	if len(matches) == 0 {
		return sqleditor.Diagnostic{}, false
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].Severity < matches[j].Severity })
	return matches[0], true
}
func diagnosticCounts(values []sqleditor.Diagnostic) [3]int {
	var result [3]int
	for _, value := range values {
		if int(value.Severity) < len(result) {
			result[value.Severity]++
		}
	}
	return result
}
func severityStyle(s styles, value sqleditor.Severity) lipgloss.Style {
	if value == sqleditor.Error {
		return s.error
	}
	if value == sqleditor.Warning {
		return s.warning
	}
	return s.info
}

func cursorStatus(tab *queryTab) string {
	return fmt.Sprintf("Ln %d, Col %d", tab.editor.Line()+1, tab.editor.Column()+1)
}
