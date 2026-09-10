package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/lucasfguimares/charta-tui/internal/database"
	"github.com/lucasfguimares/charta-tui/internal/profile"
	"github.com/lucasfguimares/charta-tui/internal/sqleditor"
	"golang.org/x/sync/errgroup"
)

const (
	autocompleteVisibleItems = 8
	autocompleteDelay        = 120 * time.Millisecond
)

type autocompleteMode uint8

const (
	autocompleteClosed autocompleteMode = iota
	autocompleteInline
	autocompleteList
)

type autocompleteState struct {
	mode          autocompleteMode
	selected      int
	result        sqleditor.CompletionResult
	requestID     uint64
	isCalculating bool
	dismissedSQL  string
	dismissedAt   int
}

type autocompleteCatalogState struct {
	catalog       sqleditor.Catalog
	isLoaded      bool
	isLoading     bool
	autoAttempted bool
	requestID     uint64
}

func (m *Model) openAutocomplete() tea.Cmd {
	tab := m.currentTab()
	if tab == nil || m.focus != focusEditor {
		return nil
	}
	tab.completion.mode = autocompleteList
	tab.completion.selected = 0
	tab.completion.dismissedSQL = ""
	completionCmd := m.refreshAutocompleteCmd(tab)
	return tea.Batch(completionCmd, m.startAutocompleteCatalogCmd(tab.connection, true))
}

func (m *Model) refreshAutocompleteCmd(tab *queryTab) tea.Cmd {
	if tab == nil || tab.completion.mode == autocompleteClosed {
		return nil
	}
	offset, ok := cursorOffset(tab.editor)
	if !ok {
		m.closeAutocomplete(tab, false)
		return nil
	}
	tab.completion.requestID++
	tab.completion.isCalculating = true
	tab.completion.selected = 0
	tab.completion.result = sqleditor.CompletionResult{}
	tabID, requestID := tab.id, tab.completion.requestID
	sql := tab.editor.Value()
	catalog := m.cachedAutocompleteCatalog(tab.connection)
	dialect := dialectFor(tab.connection.Driver)
	return func() tea.Msg {
		return autocompleteResultMsg{
			tabID: tabID, requestID: requestID,
			result: sqleditor.Complete(sql, offset, dialect, catalog),
		}
	}
}

func (m *Model) handleAutocompleteResult(msg autocompleteResultMsg) tea.Cmd {
	tab := m.tabByID(msg.tabID)
	if tab == nil || tab.completion.mode == autocompleteClosed || tab.completion.requestID != msg.requestID {
		return nil
	}
	tab.completion.result = msg.result
	tab.completion.isCalculating = false
	if len(tab.completion.result.Items) == 0 {
		tab.completion.selected = 0
	} else if tab.completion.selected >= len(tab.completion.result.Items) {
		tab.completion.selected = len(tab.completion.result.Items) - 1
	}
	if tab.completion.mode != autocompleteInline {
		return nil
	}
	if !m.inlineAutocompleteEligible(tab) {
		m.closeAutocomplete(tab, false)
		return nil
	}
	return m.startAutocompleteCatalogCmd(tab.connection, false)
}

func (m *Model) handleAutocompleteKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	tab := m.currentTab()
	if tab == nil || tab.completion.mode == autocompleteClosed {
		return nil, false
	}
	switch msg.String() {
	case "esc":
		m.closeAutocomplete(tab, true)
		return nil, true
	case "ctrl+space", "ctrl+@":
		return m.openAutocomplete(), true
	case "tab", "shift+tab":
		m.closeAutocomplete(tab, false)
		return nil, false
	}
	if tab.completion.mode == autocompleteInline {
		if key := msg.String(); key == "enter" || key == "space" {
			if _, ok := inlineCompletionItem(tab); ok {
				return m.acceptAutocomplete(tab), true
			}
		}
		return nil, false
	}
	switch msg.String() {
	case "up":
		if count := len(tab.completion.result.Items); count > 0 {
			tab.completion.selected = (tab.completion.selected - 1 + count) % count
		}
		return nil, true
	case "down":
		if count := len(tab.completion.result.Items); count > 0 {
			tab.completion.selected = (tab.completion.selected + 1) % count
		}
		return nil, true
	case "enter":
		return m.acceptAutocomplete(tab), true
	}
	return nil, false
}

func (m *Model) acceptAutocomplete(tab *queryTab) tea.Cmd {
	index := min(max(tab.completion.selected, 0), len(tab.completion.result.Items)-1)
	if tab.completion.mode == autocompleteInline {
		var ok bool
		index, ok = inlineCompletionItem(tab)
		if !ok {
			return nil
		}
	} else if len(tab.completion.result.Items) == 0 {
		m.closeAutocomplete(tab, false)
		return nil
	}
	item := tab.completion.result.Items[index]
	start := tab.completion.result.Replace.Start.Offset
	end := tab.completion.result.Replace.End.Offset
	value := tab.editor.Value()
	if start < 0 || end < start || end > len(value) {
		m.closeAutocomplete(tab, false)
		return nil
	}
	updated := value[:start] + item.InsertText + value[end:]
	tab.editor.SetValue(updated)
	setEditorCursor(&tab.editor, start+len(item.InsertText))
	m.closeAutocomplete(tab, false)
	if tab.document == nil {
		tab.document = sqleditor.NewDocument(value)
	}
	version := tab.document.SetText(updated)
	return scheduleSQLAnalysis(tab.id, version)
}

func (m *Model) scheduleInlineAutocomplete(tab *queryTab, version uint64) tea.Cmd {
	if tab == nil || m.mode != modeWorkspace || m.focus != focusEditor || len(tab.placeholders) > 0 || tab.selection != nil {
		if tab != nil {
			m.closeAutocomplete(tab, false)
		}
		return nil
	}
	m.closeAutocomplete(tab, false)
	requestID := tab.completion.requestID
	return tea.Tick(autocompleteDelay, func(time.Time) tea.Msg {
		return autocompleteDueMsg{tabID: tab.id, requestID: requestID, version: version}
	})
}

func (m *Model) handleAutocompleteDue(msg autocompleteDueMsg) tea.Cmd {
	tab := m.tabByID(msg.tabID)
	if tab == nil || tab != m.currentTab() || tab.completion.requestID != msg.requestID ||
		m.mode != modeWorkspace || m.focus != focusEditor || tab.document == nil ||
		tab.document.Version() != msg.version || len(tab.placeholders) > 0 || tab.selection != nil {
		return nil
	}
	offset, ok := cursorOffset(tab.editor)
	if !ok || tab.completion.dismissedSQL == tab.editor.Value() && tab.completion.dismissedAt == offset {
		return nil
	}
	tab.completion.mode = autocompleteInline
	tab.completion.selected = 0
	return m.refreshAutocompleteCmd(tab)
}

func (m *Model) closeAutocomplete(tab *queryTab, remember bool) {
	if tab == nil {
		return
	}
	if remember {
		tab.completion.dismissedSQL = tab.editor.Value()
		tab.completion.dismissedAt, _ = cursorOffset(tab.editor)
	}
	tab.completion.mode = autocompleteClosed
	tab.completion.result = sqleditor.CompletionResult{}
	tab.completion.isCalculating = false
	tab.completion.selected = 0
	tab.completion.requestID++
}

func (m *Model) inlineAutocompleteEligible(tab *queryTab) bool {
	if tab == nil || tab.completion.mode != autocompleteInline || len([]rune(tab.completion.result.Prefix)) < 2 ||
		tab != m.currentTab() || m.mode != modeWorkspace || m.focus != focusEditor ||
		len(tab.placeholders) > 0 || tab.selection != nil {
		return false
	}
	offset, ok := cursorOffset(tab.editor)
	return ok && tab.completion.result.Replace.End.Offset == offset
}

func inlineCompletionItem(tab *queryTab) (int, bool) {
	if tab == nil || tab.completion.mode != autocompleteInline {
		return 0, false
	}
	prefix := []rune(tab.completion.result.Prefix)
	for index, item := range tab.completion.result.Items {
		insert := []rune(item.InsertText)
		if len(insert) <= len(prefix) || !strings.EqualFold(string(insert[:len(prefix)]), string(prefix)) {
			continue
		}
		return index, true
	}
	return 0, false
}

func inlineCompletionSuffix(tab *queryTab) string {
	index, ok := inlineCompletionItem(tab)
	if !ok {
		return ""
	}
	prefix := []rune(tab.completion.result.Prefix)
	return string([]rune(tab.completion.result.Items[index].InsertText)[len(prefix):])
}

func (m *Model) ensureAutocompleteCache(profileID string) *autocompleteCatalogState {
	if m.autocompleteCache == nil {
		m.autocompleteCache = map[string]*autocompleteCatalogState{}
	}
	if state, ok := m.autocompleteCache[profileID]; ok {
		return state
	}
	state := &autocompleteCatalogState{}
	m.autocompleteCache[profileID] = state
	return state
}

func (m *Model) startAutocompleteCatalogCmd(connection profile.Connection, explicit bool) tea.Cmd {
	cache := m.ensureAutocompleteCache(connection.ID)
	if cache.isLoaded || cache.isLoading || m.inspector == nil || !explicit && cache.autoAttempted {
		return nil
	}
	if !explicit {
		cache.autoAttempted = true
	}
	cache.isLoading = true
	m.autocompleteRequestID++
	cache.requestID = m.autocompleteRequestID
	return m.loadAutocompleteCatalogCmd(connection, cache.requestID, !explicit)
}

func (m *Model) cachedAutocompleteCatalog(connection profile.Connection) sqleditor.Catalog {
	cache := m.ensureAutocompleteCache(connection.ID)
	if len(cache.catalog.Schemas) > 0 {
		catalog := cache.catalog
		catalog.DefaultSchema = connection.DefaultSchema
		return catalog
	}
	return m.browserCatalog(connection)
}

func (m *Model) browserCatalog(connection profile.Connection) sqleditor.Catalog {
	result := sqleditor.Catalog{DefaultSchema: connection.DefaultSchema}
	state, ok := m.catalog[connection.ID]
	if !ok {
		return result
	}
	for _, schemaName := range state.schemas {
		schema := sqleditor.SchemaMetadata{Name: schemaName}
		for _, relation := range state.relations[schemaName] {
			metadata := sqleditor.RelationMetadata{Schema: schemaName, Name: relation.Name, Kind: relation.Type}
			for _, column := range state.columns[catalogKey(schemaName, relation.Name)] {
				metadata.Columns = append(metadata.Columns, completionColumn(column))
			}
			schema.Relations = append(schema.Relations, metadata)
		}
		result.Schemas = append(result.Schemas, schema)
	}
	return result
}

func completionColumn(column database.Column) sqleditor.ColumnMetadata {
	upperKey := strings.ToUpper(column.Key)
	return sqleditor.ColumnMetadata{
		Name:         column.Name,
		SQLType:      column.Type,
		IsNullable:   column.Nullable,
		HasNullable:  true,
		IsPrimaryKey: strings.Contains(upperKey, "PK") || strings.Contains(upperKey, "PRIMARY"),
		IsForeignKey: strings.Contains(upperKey, "FK") || strings.Contains(upperKey, "FOREIGN"),
	}
}

func (m *Model) loadAutocompleteCatalogCmd(connection profile.Connection, requestID uint64, automatic bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		catalog, err := m.readAutocompleteCatalog(ctx, connection)
		return autocompleteCatalogMsg{profileID: connection.ID, requestID: requestID, catalog: catalog, automatic: automatic, err: err}
	}
}

func (m *Model) readAutocompleteCatalog(ctx context.Context, connection profile.Connection) (sqleditor.Catalog, error) {
	schemas, err := m.inspector.Schemas(ctx, connection)
	if err != nil {
		return sqleditor.Catalog{}, fmt.Errorf("loading autocomplete schemas: %w", err)
	}
	catalog := sqleditor.Catalog{DefaultSchema: connection.DefaultSchema, Schemas: make([]sqleditor.SchemaMetadata, len(schemas))}
	for index, schema := range schemas {
		catalog.Schemas[index].Name = schema
	}

	relationsGroup, relationsCtx := errgroup.WithContext(ctx)
	relationsGroup.SetLimit(4)
	for index := range catalog.Schemas {
		index := index
		relationsGroup.Go(func() error {
			relations, relationErr := m.inspector.Relations(relationsCtx, connection, catalog.Schemas[index].Name)
			if relationErr != nil {
				return fmt.Errorf("loading autocomplete relations for %s: %w", catalog.Schemas[index].Name, relationErr)
			}
			catalog.Schemas[index].Relations = make([]sqleditor.RelationMetadata, len(relations))
			for relationIndex, relation := range relations {
				catalog.Schemas[index].Relations[relationIndex] = sqleditor.RelationMetadata{
					Schema: relation.Schema, Name: relation.Name, Kind: relation.Type,
				}
			}
			return nil
		})
	}
	if err := relationsGroup.Wait(); err != nil {
		return catalog, err
	}

	columnsGroup, columnsCtx := errgroup.WithContext(ctx)
	columnsGroup.SetLimit(8)
	for schemaIndex := range catalog.Schemas {
		for relationIndex := range catalog.Schemas[schemaIndex].Relations {
			schemaIndex, relationIndex := schemaIndex, relationIndex
			columnsGroup.Go(func() error {
				relation := &catalog.Schemas[schemaIndex].Relations[relationIndex]
				columns, columnErr := m.inspector.Columns(columnsCtx, connection, relation.Schema, relation.Name)
				if columnErr != nil {
					return fmt.Errorf("loading autocomplete columns for %s.%s: %w", relation.Schema, relation.Name, columnErr)
				}
				relation.Columns = make([]sqleditor.ColumnMetadata, len(columns))
				for index, column := range columns {
					relation.Columns[index] = completionColumn(column)
				}
				return nil
			})
		}
	}
	err = columnsGroup.Wait()
	sortCatalog(&catalog)
	return catalog, err
}

func (m *Model) handleAutocompleteCatalog(msg autocompleteCatalogMsg) tea.Cmd {
	cache := m.ensureAutocompleteCache(msg.profileID)
	if cache.requestID != msg.requestID {
		return nil
	}
	cache.isLoading = false
	if len(msg.catalog.Schemas) > 0 {
		cache.catalog = msg.catalog
	}
	if msg.err != nil {
		m.setError(msg.err)
	} else {
		cache.isLoaded = true
	}
	commands := []tea.Cmd{}
	for _, tab := range m.tabs {
		if tab.connection.ID == msg.profileID && tab.completion.mode != autocompleteClosed {
			commands = append(commands, m.refreshAutocompleteCmd(tab))
		}
	}
	return tea.Batch(commands...)
}

func (m *Model) renderAutocomplete(tab *queryTab, width int) string {
	if tab == nil || tab.completion.mode != autocompleteList {
		return ""
	}
	items := tab.completion.result.Items
	start := 0
	if tab.completion.selected >= autocompleteVisibleItems {
		start = tab.completion.selected - autocompleteVisibleItems + 1
	}
	end := min(len(items), start+autocompleteVisibleItems)
	lines := make([]string, 0, autocompleteVisibleItems+2)
	if len(items) == 0 {
		message := "No matching suggestions"
		if cache := m.ensureAutocompleteCache(tab.connection.ID); cache.isLoading {
			message = "Loading database metadata…"
		} else if tab.completion.isCalculating {
			message = "Calculating suggestions…"
		}
		lines = append(lines, m.styles.dim.Render(message))
	} else {
		for index := start; index < end; index++ {
			lines = append(lines, m.renderAutocompleteItem(items[index], index == tab.completion.selected, width))
		}
	}
	footer := "↑/↓ navigate  Enter accept  Tab change focus  Esc close"
	if cache := m.ensureAutocompleteCache(tab.connection.ID); cache.isLoading {
		footer = "Loading metadata…  " + footer
	}
	lines = append(lines, m.styles.dim.Render(truncate(footer, max(20, width-2))))
	return m.styles.autocomplete.Width(max(30, width)).Render(strings.Join(lines, "\n"))
}

func (m *Model) renderAutocompleteItem(item sqleditor.CompletionItem, selected bool, width int) string {
	labelWidth := min(28, max(12, width-28))
	label := truncate(item.Label, labelWidth)
	label = highlightCompletionMatch(label, item.MatchStart, item.MatchLength, m.styles, selected)
	detail := item.Kind.String()
	if item.SQLType != "" {
		detail += "  " + item.SQLType
	}
	flags := []string{}
	if item.IsPrimaryKey {
		flags = append(flags, "PK")
	}
	if item.IsForeignKey {
		flags = append(flags, "FK")
	}
	if item.Kind == sqleditor.CompletionColumn && item.HasNullable {
		if item.IsNullable {
			flags = append(flags, "NULL")
		} else {
			flags = append(flags, "NOT NULL")
		}
	}
	if len(flags) > 0 {
		detail += "  " + strings.Join(flags, "/")
	}
	padding := strings.Repeat(" ", max(1, labelWidth-utf8.RuneCountInString(item.Label)+1))
	line := label + padding + truncate(detail, max(8, width-labelWidth-2))
	if selected {
		return m.styles.completionSelected.Width(width).Render(line)
	}
	return line
}

func highlightCompletionMatch(label string, start, length int, styles styles, selected bool) string {
	runes := []rune(label)
	start = min(max(start, 0), len(runes))
	end := min(len(runes), start+max(length, 0))
	if end <= start {
		return label
	}
	matchStyle := styles.completionMatch
	if selected {
		matchStyle = matchStyle.Background(lipgloss.Color("24"))
	}
	return string(runes[:start]) + matchStyle.Render(string(runes[start:end])) + string(runes[end:])
}

func sortCatalog(catalog *sqleditor.Catalog) {
	sort.SliceStable(catalog.Schemas, func(i, j int) bool {
		return strings.ToLower(catalog.Schemas[i].Name) < strings.ToLower(catalog.Schemas[j].Name)
	})
	for index := range catalog.Schemas {
		sort.SliceStable(catalog.Schemas[index].Relations, func(i, j int) bool {
			return strings.ToLower(catalog.Schemas[index].Relations[i].Name) < strings.ToLower(catalog.Schemas[index].Relations[j].Name)
		})
	}
}
