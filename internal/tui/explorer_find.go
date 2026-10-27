package tui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/lucasfguimares/tui-db/internal/database"
)

const explorerFindLimit = 500

const (
	hitSchema = 'S'
	hitTable  = 'T'
	hitView   = 'V'
	hitColumn = 'C'
)

const (
	findAllKind = iota
	findRelationKind
	findColumnKind
	findSchemaKind
)

var explorerFindKindNames = map[int]string{
	findAllKind: "all", findRelationKind: "tables/views", findColumnKind: "columns", findSchemaKind: "schemas",
}

// explorerHit is one searchable catalog object.
type explorerHit struct {
	kind     byte
	schema   string
	relation string
	column   string
	detail   string
	name     string
	rank     int
	metadata *database.Column
}

func (hit explorerHit) qualifiedName() string {
	parts := []string{hit.schema}
	if hit.relation != "" {
		parts = append(parts, hit.relation)
	}
	if hit.column != "" {
		parts = append(parts, hit.column)
	}
	return strings.Join(parts, ".")
}

// explorerFind is the global, in-memory object search of the explorer.
type explorerFind struct {
	input       textinput.Model
	query       string
	kind        int
	index       []explorerHit
	hits        []explorerHit
	cursor      int
	isTruncated bool
}

func newExplorerFindInput() textinput.Model {
	input := textinput.New()
	input.Prompt = "Find: "
	input.SetWidth(28)
	return input
}

// openExplorerFind shows the global find and starts loading the full catalog
// (shared with autocomplete) when it is not cached yet.
func (m *Model) openExplorerFind() tea.Cmd {
	if m.explorer.find.input.Prompt == "" {
		m.explorer.find.input = newExplorerFindInput()
	}
	m.explorer.find.input.SetValue(m.explorer.find.query)
	m.explorer.find.input.CursorEnd()
	m.mode = modeExplorerFind
	m.refreshExplorerFind()
	cmds := []tea.Cmd{m.explorer.find.input.Focus()}
	if connection, ok := m.profileByID(m.explorer.profileID); ok && !m.explorer.needsConnect && m.inspector != nil {
		cmds = append(cmds, m.startAutocompleteCatalogCmd(connection, true))
	}
	return tea.Batch(cmds...)
}

// refreshExplorerFind rebuilds the object index from every cached catalog and reruns the query.
func (m *Model) refreshExplorerFind() {
	m.explorer.find.index = m.buildExplorerIndex(m.explorer.profileID)
	m.searchExplorerFind()
}

func (m *Model) searchExplorerFind() {
	find := &m.explorer.find
	find.hits, find.isTruncated = searchExplorer(find.index, find.query, find.kind)
	find.cursor = min(find.cursor, max(0, len(find.hits)-1))
}

// buildExplorerIndex merges the explorer/browser catalog cache with the full
// autocomplete catalog, preferring the richer explorer column metadata.
func (m *Model) buildExplorerIndex(profileID string) []explorerHit {
	seen := map[string]bool{}
	index := []explorerHit{}
	add := func(hit explorerHit) {
		key := string(hit.kind) + "\x00" + strings.ToLower(hit.qualifiedName())
		if hit.kind == hitView {
			key = string(hitTable) + key[1:]
		}
		if seen[key] {
			return
		}
		seen[key] = true
		index = append(index, hit)
	}
	if state, ok := m.catalog[profileID]; ok {
		for _, schema := range state.schemas {
			add(explorerHit{kind: hitSchema, schema: schema, name: schema, detail: "schema"})
			for _, relation := range state.relations[schema] {
				kind, detail := byte(hitTable), "table"
				if isViewRelation(relation) {
					kind, detail = hitView, "view"
				}
				columns, loaded := state.columns[catalogKey(schema, relation.Name)]
				if loaded {
					detail = fmt.Sprintf("%s · %d column(s)", detail, len(columns))
				}
				if details, ok := state.details[catalogKey(schema, relation.Name)]; ok {
					if summary := relationSizeSummary(details); summary != "" {
						detail += " · " + summary
					}
					detail += fmt.Sprintf(" · %d index(es)", len(details.Indexes))
				}
				add(explorerHit{kind: kind, schema: schema, relation: relation.Name, name: relation.Name, detail: detail})
				for index := range columns {
					column := columns[index]
					add(explorerHit{
						kind: hitColumn, schema: schema, relation: relation.Name, column: column.Name, name: column.Name,
						detail: explorerColumnSummary(column), metadata: &column,
					})
				}
			}
		}
	}
	if cache, ok := m.autocompleteCache[profileID]; ok {
		for _, schema := range cache.catalog.Schemas {
			add(explorerHit{kind: hitSchema, schema: schema.Name, name: schema.Name, detail: "schema"})
			for _, relation := range schema.Relations {
				kind, detail := byte(hitTable), "table"
				if strings.Contains(strings.ToUpper(relation.Kind), "VIEW") {
					kind, detail = hitView, "view"
				}
				if len(relation.Columns) > 0 {
					detail = fmt.Sprintf("%s · %d column(s)", detail, len(relation.Columns))
				}
				add(explorerHit{kind: kind, schema: schema.Name, relation: relation.Name, name: relation.Name, detail: detail})
				for _, column := range relation.Columns {
					key := ""
					switch {
					case column.IsPrimaryKey && column.IsForeignKey:
						key = "PK/FK"
					case column.IsPrimaryKey:
						key = "PK"
					case column.IsForeignKey:
						key = "FK"
					}
					add(explorerHit{
						kind: hitColumn, schema: schema.Name, relation: relation.Name, column: column.Name, name: column.Name,
						detail: explorerColumnSummary(database.Column{
							Name: column.Name, Type: column.SQLType, Nullable: column.IsNullable || !column.HasNullable, Key: key,
						}),
					})
				}
			}
		}
	}
	return index
}

func explorerColumnSummary(column database.Column) string {
	parts := []string{formatColumnType(column)}
	if !column.Nullable {
		parts = append(parts, "NOT NULL")
	}
	if column.Key != "" {
		parts = append(parts, column.Key)
	}
	if column.ReferenceTable != "" {
		parts = append(parts, "→ "+column.ReferenceTable)
	}
	return strings.Join(parts, "  ")
}

// searchExplorer ranks matching hits: exact name, then prefix, then substring;
// relations before columns before schemas; then shorter, then alphabetical names.
func searchExplorer(index []explorerHit, query string, kind int) ([]explorerHit, bool) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, false
	}
	hits := []explorerHit{}
	for _, hit := range index {
		if !matchesFindKind(hit.kind, kind) {
			continue
		}
		name := strings.ToLower(hit.name)
		switch {
		case name == query:
			hit.rank = 0
		case strings.HasPrefix(name, query):
			hit.rank = 1
		case strings.Contains(name, query):
			hit.rank = 2
		default:
			continue
		}
		hits = append(hits, hit)
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if kindOrder(a.kind) != kindOrder(b.kind) {
			return kindOrder(a.kind) < kindOrder(b.kind)
		}
		aName, bName := a.qualifiedName(), b.qualifiedName()
		if len(aName) != len(bName) {
			return len(aName) < len(bName)
		}
		return strings.ToLower(aName) < strings.ToLower(bName)
	})
	if len(hits) > explorerFindLimit {
		return hits[:explorerFindLimit], true
	}
	return hits, false
}

func matchesFindKind(hit byte, kind int) bool {
	switch kind {
	case findRelationKind:
		return hit == hitTable || hit == hitView
	case findColumnKind:
		return hit == hitColumn
	case findSchemaKind:
		return hit == hitSchema
	default:
		return true
	}
}

func kindOrder(kind byte) int {
	switch kind {
	case hitTable, hitView:
		return 0
	case hitColumn:
		return 1
	default:
		return 2
	}
}

func (m *Model) handleExplorerFindKey(msg tea.KeyPressMsg) tea.Cmd {
	find := &m.explorer.find
	page := max(1, m.explorerPaneHeight()-3)
	switch msg.String() {
	case "esc":
		find.input.Blur()
		m.mode = modeExplorer
		return nil
	case "enter":
		return m.jumpToExplorerHit()
	case "up":
		find.cursor = max(0, find.cursor-1)
		return nil
	case "down":
		find.cursor = min(max(0, len(find.hits)-1), find.cursor+1)
		return nil
	case "pgup":
		find.cursor = max(0, find.cursor-page)
		return nil
	case "pgdown":
		find.cursor = min(max(0, len(find.hits)-1), find.cursor+page)
		return nil
	case "tab":
		find.kind = (find.kind + 1) % len(explorerFindKindNames)
		find.cursor = 0
		m.searchExplorerFind()
		return nil
	}
	before := find.input.Value()
	updated, cmd := find.input.Update(msg)
	find.input = updated
	if value := find.input.Value(); value != before {
		find.query = value
		find.cursor = 0
		m.searchExplorerFind()
	}
	return cmd
}

// jumpToExplorerHit closes the find and positions the panes on the selected object.
func (m *Model) jumpToExplorerHit() tea.Cmd {
	find := &m.explorer.find
	if find.cursor < 0 || find.cursor >= len(find.hits) {
		return nil
	}
	hit := find.hits[find.cursor]
	find.input.Blur()
	m.mode = modeExplorer
	m.explorer.filter = [3]string{}
	m.explorer.cursor = [3]int{}
	m.explorer.marked = map[string]bool{}
	m.explorer.errorText = ""
	m.explorer.target = [3]string{hit.schema, hit.relation, hit.column}
	switch hit.kind {
	case hitColumn:
		m.explorer.view = explorerViewColumns
		m.explorer.pane = explorerColumns
	case hitTable, hitView:
		m.explorer.pane = explorerRelations
	default:
		m.explorer.pane = explorerSchemas
	}
	m.applyExplorerTarget()
	return m.explorerLoadCmd()
}

func (m *Model) explorerCatalogCoverage() string {
	cache, ok := m.autocompleteCache[m.explorer.profileID]
	switch {
	case !ok:
		return "catalog: cached only"
	case cache.isLoading:
		return "catalog: indexing…"
	case cache.isLoaded:
		return "catalog: complete"
	default:
		return "catalog: partial"
	}
}

func (m *Model) renderExplorerFind() string {
	connection, _ := m.profileByID(m.explorer.profileID)
	find := &m.explorer.find
	width := max(40, m.width-2)
	height := m.explorerPaneHeight()
	inner := max(10, width-2)

	title := m.styles.header.Render("Schema explorer") + "  " +
		m.styles.dim.Render(connection.Name+" › find "+fmt.Sprintf("%q", strings.TrimSpace(find.query)))
	coverage := m.styles.dim.Render(m.explorerCatalogCoverage())
	gap := max(1, width-ansi.StringWidth(title)-ansi.StringWidth(coverage))
	header := ansi.Truncate(title+strings.Repeat(" ", gap)+coverage, width, "…")

	count := fmt.Sprintf("%d", len(find.hits))
	if find.isTruncated {
		count += "+"
	}
	lines := []string{m.styles.header.Render("Results (" + count + ")")}
	switch {
	case strings.TrimSpace(find.query) == "":
		lines = append(lines, m.styles.dim.Render("Type to search schemas, tables, views, and columns."))
	case len(find.hits) == 0:
		lines = append(lines, m.styles.dim.Render("No matching objects in the loaded catalog."))
	}
	visible := max(1, height-3)
	start := 0
	if find.cursor >= visible {
		start = find.cursor - visible + 1
	}
	end := min(len(find.hits), start+visible)
	nameWidth := max(20, inner*55/100)
	for index := start; index < end; index++ {
		lines = append(lines, m.renderExplorerHit(find.hits[index], index == find.cursor, inner, nameWidth))
	}
	pane := m.styles.focusedPanel.Width(width).Height(height).Render(strings.Join(lines, "\n"))

	detail := ""
	if find.cursor < len(find.hits) {
		hit := find.hits[find.cursor]
		detail = hit.qualifiedName() + " · " + hit.detail
		if hit.metadata != nil {
			detail = hit.schema + "." + hit.relation + "." + formatColumnDetail(*hit.metadata)
		}
	}
	detailLine := m.styles.info.Render(ansi.Truncate(detail, width, "…"))

	counter := m.styles.error.Render("no matches")
	if len(find.hits) > 0 {
		counter = m.styles.accent.Render(fmt.Sprintf("%d/%s", find.cursor+1, count))
	}
	if strings.TrimSpace(find.query) == "" {
		counter = ""
	}
	bar := strings.Join([]string{
		find.input.View(), counter,
		m.styles.dim.Render("Enter jump  ↑/↓ move  Tab kind: " + explorerFindKindNames[find.kind] + "  Esc close"),
	}, "   ")
	return m.styles.app.Render(strings.Join([]string{header, pane, detailLine, ansi.Truncate(bar, width, "…")}, "\n"))
}

func (m *Model) renderExplorerHit(hit explorerHit, isSelected bool, width, nameWidth int) string {
	kind := m.styles.dim.Render(string(hit.kind))
	switch hit.kind {
	case hitTable:
		kind = m.styles.accent.Render("T")
	case hitView:
		kind = m.styles.info.Render("V")
	case hitColumn:
		kind = m.styles.identifier.Render("C")
	}
	name := hit.qualifiedName()
	qualified := fitCell(name, nameWidth, alignLeft)
	detail := fitCell(hit.detail, max(0, width-nameWidth-4), alignLeft)
	if isSelected {
		return m.styles.selected.Width(width).Render(ansi.Strip(kind + " " + qualified + "  " + detail))
	}
	if width := ansi.StringWidth(name); width <= nameWidth {
		qualified = m.highlightName(name, hit) + strings.Repeat(" ", nameWidth-width)
	}
	return kind + " " + qualified + "  " + m.styles.dim.Render(detail)
}

// highlightName emphasizes the query inside the object's own (last) name segment.
func (m *Model) highlightName(qualified string, hit explorerHit) string {
	query := strings.ToLower(strings.TrimSpace(m.explorer.find.query))
	lowerName := strings.ToLower(hit.name)
	offset := strings.Index(lowerName, query)
	if query == "" || offset < 0 || len(lowerName) != len(hit.name) || !strings.HasSuffix(qualified, hit.name) {
		return qualified
	}
	start := len(qualified) - len(hit.name) + offset
	end := start + len(query)
	match := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(lipgloss.Color("220"))
	return qualified[:start] + match.Render(qualified[start:end]) + qualified[end:]
}
