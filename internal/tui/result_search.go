package tui

import (
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
)

type resultCell struct{ row, column int }

// resultSearch holds the live find state of a result grid. Matches are kept in
// row-major order and indexed for constant-time lookup while rendering.
type resultSearch struct {
	query   string
	matches []resultCell
	index   map[resultCell]int
	anchor  resultCell
}

func (cell resultCell) before(other resultCell) bool {
	return cell.row < other.row || cell.row == other.row && cell.column < other.column
}

// BeginSearch remembers the active cell so incremental matching can return to it.
func (table *resultTableModel) BeginSearch() {
	table.search.anchor = resultCell{row: table.cursorRow, column: table.cursorColumn}
}

// SetSearchQuery recomputes matches and moves to the first match at or after
// the anchor, or back to the anchor when nothing matches.
func (table *resultTableModel) SetSearchQuery(query string) (int, int) {
	table.computeMatches(query)
	if len(table.search.matches) == 0 {
		table.cursorRow, table.cursorColumn = table.search.anchor.row, table.search.anchor.column
		table.clampSelection()
		table.ensureSelectionVisible()
		return table.SearchStatus()
	}
	anchor := table.search.anchor
	index := sort.Search(len(table.search.matches), func(i int) bool { return !table.search.matches[i].before(anchor) })
	table.selectMatch(index % len(table.search.matches))
	return table.SearchStatus()
}

// NextMatch moves to the next (delta > 0) or previous match relative to the
// active cell, wrapping around the result set.
func (table *resultTableModel) NextMatch(delta int) (int, int) {
	matches := table.search.matches
	if len(matches) == 0 {
		return 0, 0
	}
	cursor := resultCell{row: table.cursorRow, column: table.cursorColumn}
	if delta > 0 {
		index := sort.Search(len(matches), func(i int) bool { return cursor.before(matches[i]) })
		table.selectMatch(index % len(matches))
	} else {
		index := sort.Search(len(matches), func(i int) bool { return !matches[i].before(cursor) }) - 1
		table.selectMatch((index + len(matches)) % len(matches))
	}
	return table.SearchStatus()
}

// SearchStatus returns the 1-based position of the active cell among the
// matches (0 when the active cell is not a match) and the match count.
func (table *resultTableModel) SearchStatus() (int, int) {
	position, ok := table.search.index[resultCell{row: table.cursorRow, column: table.cursorColumn}]
	if !ok {
		return 0, len(table.search.matches)
	}
	return position + 1, len(table.search.matches)
}

// HasSearch reports whether a non-empty find query is active.
func (table *resultTableModel) HasSearch() bool { return table.search.query != "" }

// ClearSearch removes the find query and its highlights.
func (table *resultTableModel) ClearSearch() { table.search = resultSearch{} }

func (table *resultTableModel) computeMatches(query string) {
	anchor := table.search.anchor
	table.search = resultSearch{query: strings.ToLower(strings.TrimSpace(query)), anchor: anchor}
	if table.search.query == "" {
		return
	}
	table.search.index = map[resultCell]int{}
	for row, values := range table.result.Rows {
		for column := range min(len(values), len(table.result.Columns)) {
			cell := formatResultValue(values[column], table.result.Columns[column])
			if strings.Contains(strings.ToLower(cell.detail), table.search.query) {
				match := resultCell{row: row, column: column}
				table.search.index[match] = len(table.search.matches)
				table.search.matches = append(table.search.matches, match)
			}
		}
	}
}

func (table *resultTableModel) selectMatch(index int) {
	match := table.search.matches[index]
	table.cursorRow, table.cursorColumn = match.row, match.column
	table.selectionScope = resultSelectionCell
	table.ensureSelectionVisible()
}

func (table *resultTableModel) isMatch(row, column int) bool {
	_, ok := table.search.index[resultCell{row: row, column: column}]
	return ok
}

// highlightMatch emphasizes the matched text inside an already fitted cell. When
// the match is not visible (truncated or only in the full value), the whole cell
// is tinted instead.
func (table *resultTableModel) highlightMatch(fitted string, base lipgloss.Style) string {
	query := table.search.query
	lower := strings.ToLower(fitted)
	if len(lower) == len(fitted) {
		if start := strings.Index(lower, query); start >= 0 {
			end := start + len(query)
			return base.Render(fitted[:start]) + table.matchStyle.Render(fitted[start:end]) + base.Render(fitted[end:])
		}
	}
	return table.matchCellStyle.Render(fitted)
}
