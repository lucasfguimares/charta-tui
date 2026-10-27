package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/lucasfguimares/tui-db/internal/activity"
	"github.com/lucasfguimares/tui-db/internal/database"
	"github.com/lucasfguimares/tui-db/internal/profile"
	"github.com/lucasfguimares/tui-db/internal/sqleditor"
)

// explorerView selects what the third explorer pane shows for a relation.
type explorerView int

const (
	explorerViewColumns explorerView = iota
	explorerViewIndexes
	explorerViewDefinition
)

type relationDetailsLoadedMsg struct {
	profileID string
	schema    string
	relation  string
	details   database.RelationDetails
	err       error
}

func (m *Model) loadRelationDetailsCmd(connection profile.Connection, schema, relation string) tea.Cmd {
	inspector := m.inspector
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		details, err := inspector.RelationDetails(ctx, connection, schema, relation)
		return relationDetailsLoadedMsg{profileID: connection.ID, schema: schema, relation: relation, details: details, err: err}
	}
}

func (m *Model) handleRelationDetailsLoaded(msg relationDetailsLoadedMsg) tea.Cmd {
	key := catalogKey(msg.schema, msg.relation)
	delete(m.explorer.loading, "d\x00"+msg.profileID+"\x00"+key)
	if msg.err != nil {
		m.explorer.errorText = "Loading relation details failed: " + activity.Redact(msg.err.Error())
		return nil
	}
	state := m.ensureCatalog(msg.profileID)
	if state.details == nil {
		state.details = map[string]database.RelationDetails{}
	}
	state.details[key] = msg.details
	m.refreshExplorerFindIfOpen(msg.profileID)
	return nil
}

// explorerDetailsLoadCmd requests details for the selected relation once its columns are known.
func (m *Model) explorerDetailsLoadCmd(connection profile.Connection, schema, relation string) tea.Cmd {
	if m.inspector == nil {
		return nil
	}
	key := catalogKey(schema, relation)
	if _, loaded := m.catalog[connection.ID].details[key]; loaded {
		return nil
	}
	loadingKey := "d\x00" + connection.ID + "\x00" + key
	if m.explorer.loading[loadingKey] {
		return nil
	}
	m.explorer.loading[loadingKey] = true
	return m.loadRelationDetailsCmd(connection, schema, relation)
}

func (m *Model) explorerSelectedDetails() (database.RelationDetails, bool) {
	schema, ok := m.explorerSelectedSchema()
	if !ok {
		return database.RelationDetails{}, false
	}
	relation, ok := m.explorerSelectedRelation()
	if !ok {
		return database.RelationDetails{}, false
	}
	return m.relationDetails(schema, relation.Name)
}

func (m *Model) relationDetails(schema, relation string) (database.RelationDetails, bool) {
	state, ok := m.catalog[m.explorer.profileID]
	if !ok || state.details == nil {
		return database.RelationDetails{}, false
	}
	details, ok := state.details[catalogKey(schema, relation)]
	return details, ok
}

func (m *Model) cycleExplorerView() {
	m.explorer.view = (m.explorer.view + 1) % 3
	m.explorer.cursor[explorerColumns] = 0
	m.explorer.filter[explorerColumns] = ""
	if m.explorer.pane != explorerColumns {
		m.explorer.pane = explorerColumns
	}
}

// explorerDefinitionLines returns the view SQL wrapped to the columns pane width.
func (m *Model) explorerDefinitionLines() []string {
	details, ok := m.explorerSelectedDetails()
	if !ok || details.Definition == "" {
		return nil
	}
	width := max(10, m.explorerPaneWidths()[explorerColumns]-2)
	text := strings.ReplaceAll(strings.ReplaceAll(details.Definition, "\r\n", "\n"), "\t", "    ")
	return strings.Split(ansi.Wrap(text, width, ""), "\n")
}

func (m *Model) explorerIndexLines(width int) (string, []string) {
	schema, _ := m.explorerSelectedSchema()
	relation, _ := m.explorerSelectedRelation()
	title := "Indexes · " + schema + "." + relation.Name
	details, ok := m.explorerSelectedDetails()
	if !ok {
		return title, []string{m.styles.dim.Render("Loading…")}
	}
	title += fmt.Sprintf(" (%d)", len(details.Indexes))
	if len(details.Indexes) == 0 {
		return title, []string{m.styles.dim.Render("No indexes")}
	}
	nameWidth := max(10, width*40/100)
	kindWidth := 7
	columnsWidth := max(0, width-nameWidth-kindWidth-2)
	lines := []string{m.styles.dim.Render(
		fitCell("name", nameWidth, alignLeft) + " " + fitCell("kind", kindWidth, alignLeft) + " " + fitCell("columns", columnsWidth, alignLeft),
	)}
	for _, index := range details.Indexes {
		kind := ""
		switch {
		case index.IsPrimary:
			kind = "primary"
		case index.IsUnique:
			kind = "unique"
		}
		lines = append(lines, fitCell(sanitizeInline(index.Name), nameWidth, alignLeft)+" "+
			fitCell(kind, kindWidth, alignLeft)+" "+fitCell(strings.Join(index.Columns, ", "), columnsWidth, alignLeft))
	}
	return title, lines
}

func (m *Model) explorerDefinitionView(width int) (string, []string) {
	schema, _ := m.explorerSelectedSchema()
	relation, _ := m.explorerSelectedRelation()
	title := "Definition · " + schema + "." + relation.Name
	details, ok := m.explorerSelectedDetails()
	switch {
	case !ok:
		return title, []string{m.styles.dim.Render("Loading…")}
	case !isViewRelation(relation):
		return title, []string{m.styles.dim.Render("Not a view — d shows columns or indexes")}
	case details.Definition == "":
		return title, []string{m.styles.dim.Render("Definition unavailable")}
	}
	connection, _ := m.profileByID(m.explorer.profileID)
	return title, highlightSQLLines(m.explorerDefinitionLines(), dialectFor(connection.Driver), width, m.styles)
}

// highlightSQLLines renders SQL with the editor's token colors, one string per line.
func highlightSQLLines(lines []string, dialect sqleditor.Dialect, width int, s styles) []string {
	text := strings.Join(lines, "\n")
	analysis := sqleditor.Analyze(text, dialect, 1)
	result := make([]string, len(lines))
	for index, value := range lines {
		line := []rune(value)
		decorations := make([]charDecoration, len(line)+1)
		for i := range decorations {
			decorations[i].kind = sqleditor.TokenIdentifier
		}
		decorateTokens(decorations, index+1, analysis.Tokens)
		if len(line) > width {
			line, decorations = line[:width], decorations[:width]
		}
		result[index] = renderDecorated(line, decorations, s)
	}
	return result
}

// indexedLeadColumns returns columns that lead a non-primary index.
func indexedLeadColumns(details database.RelationDetails) map[string]bool {
	result := map[string]bool{}
	for _, index := range details.Indexes {
		if !index.IsPrimary && len(index.Columns) > 0 {
			result[index.Columns[0]] = true
		}
	}
	return result
}

func withIndexMarker(key string, indexed bool) string {
	switch {
	case !indexed:
		return key
	case key == "":
		return "IX"
	default:
		return key + "/IX"
	}
}

func relationSizeSummary(details database.RelationDetails) string {
	parts := []string{}
	if details.HasApproximateRows {
		parts = append(parts, "~"+formatApproximateCount(details.ApproximateRows)+" rows")
	}
	if details.HasTotalBytes {
		parts = append(parts, formatBytes(details.TotalBytes))
	}
	return strings.Join(parts, " · ")
}

// formatApproximateCount renders 950, 12.3k, 4.2M, 1.1B.
func formatApproximateCount(value int64) string {
	switch {
	case value < 1_000:
		return fmt.Sprint(value)
	case value < 1_000_000:
		return trimDecimal(float64(value)/1_000) + "k"
	case value < 1_000_000_000:
		return trimDecimal(float64(value)/1_000_000) + "M"
	default:
		return trimDecimal(float64(value)/1_000_000_000) + "B"
	}
}

// formatBytes renders binary sizes such as 512 B, 4.1 KiB, 3.2 GiB.
func formatBytes(value int64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	size := float64(value)
	unit := 0
	for size >= 1024 && unit < len(units)-1 {
		size /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d B", value)
	}
	return trimDecimal(size) + " " + units[unit]
}

func trimDecimal(value float64) string {
	if value >= 100 {
		return fmt.Sprintf("%.0f", value)
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", value), ".0")
}
