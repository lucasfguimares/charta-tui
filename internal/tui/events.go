package tui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/lucasfguimares/charta-tui/internal/activity"
	"github.com/lucasfguimares/charta-tui/internal/database"
	"github.com/lucasfguimares/charta-tui/internal/profile"
	"github.com/lucasfguimares/charta-tui/internal/queryhistory"
	"github.com/lucasfguimares/charta-tui/internal/sqleditor"
	"github.com/lucasfguimares/charta-tui/internal/sqlscan"
)

func (m *Model) openBrowserItem() tea.Cmd {
	if m.browserCursor < 0 || m.browserCursor >= len(m.browserItems) {
		return nil
	}
	item := m.browserItems[m.browserCursor]
	connection, ok := m.profileByID(item.profileID)
	if !ok {
		return nil
	}
	state := m.ensureCatalog(item.profileID)
	switch item.kind {
	case browserProfile:
		m.ensureTab(connection)
		if state.isExpanded {
			state.isExpanded = false
			m.rebuildBrowser()
			return nil
		}
		if len(state.schemas) == 0 {
			return m.prepareConnectionCmd(connection)
		}
		state.isExpanded = true
		m.rebuildBrowser()
	case browserSchema:
		if state.expanded[item.schema] {
			state.expanded[item.schema] = false
			m.rebuildBrowser()
			return nil
		}
		if _, loaded := state.relations[item.schema]; !loaded {
			return m.loadRelationsCmd(connection, item.schema)
		}
		state.expanded[item.schema] = true
		m.rebuildBrowser()
	case browserRelation:
		key := catalogKey(item.schema, item.relation)
		if state.expanded[key] {
			state.expanded[key] = false
			m.rebuildBrowser()
			return nil
		}
		if _, loaded := state.columns[key]; !loaded {
			return m.loadColumnsCmd(connection, item.schema, item.relation)
		}
		state.expanded[key] = true
		m.rebuildBrowser()
	}
	return nil
}

func (m *Model) collapseBrowserItem() {
	if m.browserCursor < 0 || m.browserCursor >= len(m.browserItems) {
		return
	}
	item := m.browserItems[m.browserCursor]
	state := m.ensureCatalog(item.profileID)
	switch item.kind {
	case browserProfile:
		state.isExpanded = false
	case browserSchema:
		state.expanded[item.schema] = false
	case browserRelation:
		state.expanded[catalogKey(item.schema, item.relation)] = false
	}
	m.rebuildBrowser()
}

func (m *Model) handleQueryFinished(msg queryFinishedMsg) tea.Cmd {
	tab := m.tabByID(msg.tabID)
	if tab == nil || tab.requestID != msg.requestID {
		return nil
	}
	if msg.result.Duration <= 0 && !tab.startedAt.IsZero() {
		msg.result.Duration = time.Since(tab.startedAt)
		if msg.result.Duration <= 0 {
			msg.result.Duration = time.Nanosecond
		}
	}
	tab.isRunning = false
	if tab.cancel != nil {
		tab.cancel()
		tab.cancel = nil
	}
	tab.result = msg.result
	if msg.err != nil {
		details := database.DescribeError(msg.err, msg.result.Duration)
		details.Message = activity.Redact(details.Message)
		if !details.Cancelled && !details.TimedOut {
			diagnostic := sqleditor.DatabaseDiagnostic(tab.lastSQL, sqleditor.DatabaseError{
				Message: details.Message, Code: details.Code, Line: details.Line,
				Column: details.Column, Position: details.Position,
			})
			base := tab.lastSQLBase
			if base < 0 || base+len(tab.lastSQL) > len(tab.editor.Value()) || tab.editor.Value()[base:base+len(tab.lastSQL)] != tab.lastSQL {
				base = strings.Index(tab.editor.Value(), tab.lastSQL)
			}
			if base >= 0 {
				diagnostic.Range.Start = sqleditor.PositionAt(tab.editor.Value(), base+diagnostic.Range.Start.Offset)
				diagnostic.Range.End = sqleditor.PositionAt(tab.editor.Value(), base+diagnostic.Range.End.Offset)
			}
			tab.analysis.Diagnostics = append(tab.analysis.Diagnostics, diagnostic)
			tab.diagnostic = len(tab.analysis.Diagnostics) - 1
			setEditorCursor(&tab.editor, diagnostic.Range.Start.Offset)
		}
		tab.table.SetErrorWithLayout(
			msg.result,
			details,
			msg.columnWidths,
			msg.layoutDuration,
		)
		if details.Cancelled {
			tab.statusText = fmt.Sprintf("Query cancelled after %s", formatDuration(msg.result.Duration))
			m.isStatusError = false
		} else {
			tab.statusText = "Query failed"
			m.isStatusError = true
		}
		m.activity.Record(context.Background(), activity.Event{
			Level: slog.LevelError, Message: "query failed", Connection: tab.connection.Name,
			Engine: string(tab.connection.Driver), Duration: msg.result.Duration, Error: msg.err,
		})
		return m.recordHistoryCmd(tab, msg.result, statusForError(details.Cancelled), details.Message)
	}
	rows := int64(len(msg.result.Rows))
	if len(msg.result.Columns) == 0 {
		rows = msg.result.AffectedRows
	}
	tab.statusText = fmt.Sprintf("Completed in %s — %d row(s)", formatDuration(msg.result.Duration), rows)
	if msg.result.IsTruncated {
		tab.statusText += " (truncated)"
	}
	tab.table.SetResultWithLayout(msg.result, msg.columnWidths, msg.layoutDuration)
	analysis := sqlscan.Analyze(tab.lastSQL)
	if analysis.IsRisky {
		m.invalidateColumnCaches(tab.connection.ID)
	}
	if invalidatesSchemaMetadata(tab.lastSQL, dialectFor(tab.connection.Driver)) {
		delete(m.catalog, tab.connection.ID)
		delete(m.autocompleteCache, tab.connection.ID)
	}
	m.isStatusError = false
	if m.focus == focusResults {
		tab.table.Focus()
	}
	m.activity.Record(context.Background(), activity.Event{
		Level: slog.LevelInfo, Message: "query completed", Connection: tab.connection.Name,
		Engine: string(tab.connection.Driver), Duration: msg.result.Duration, Rows: rows,
	})
	return m.recordHistoryCmd(tab, msg.result, queryhistory.StatusSuccess, "")
}

func queryTickCmd(tabID, requestID int) tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg {
		return queryTickMsg{tabID: tabID, requestID: requestID}
	})
}

func (m *Model) handleSchemas(msg schemasLoadedMsg) tea.Cmd {
	if msg.err != nil {
		if errors.Is(msg.err, profile.ErrSecretNotFound) {
			if connection, ok := m.profileByID(msg.profileID); ok {
				m.mode = modePassword
				m.pendingConnection = connection
				m.pendingPasswordAct = passwordConnect
				m.passwordInput.SetValue("")
				return m.passwordInput.Focus()
			}
		}
		m.setError(msg.err)
		return nil
	}
	state := m.ensureCatalog(msg.profileID)
	state.schemas = msg.schemas
	state.isExpanded = true
	m.rebuildBrowser()
	if connection, ok := m.profileByID(msg.profileID); ok {
		defaultSchema, schemaFound := findSchema(msg.schemas, connection.Driver, connection.DefaultSchema)
		if connection.DefaultSchema != "" && !schemaFound {
			m.setError(fmt.Errorf("default schema %q was not found in %s", connection.DefaultSchema, connection.Name))
			return nil
		}
		m.ensureTab(connection)
		m.statusText = fmt.Sprintf("Connected to %s", connection.Name)
		if connection.DefaultSchema != "" {
			m.statusText += fmt.Sprintf(" (schema %s)", connection.DefaultSchema)
		}
		m.activity.Record(context.Background(), activity.Event{
			Level: slog.LevelInfo, Message: "connection opened", Connection: connection.Name,
			Engine: string(connection.Driver),
		})
		if connection.DefaultSchema != "" {
			if _, loaded := state.relations[defaultSchema]; !loaded {
				return m.loadRelationsCmd(connection, defaultSchema)
			}
			state.expanded[defaultSchema] = true
			m.rebuildBrowser()
		}
	}
	return nil
}

func (m *Model) handleRelations(msg relationsLoadedMsg) tea.Cmd {
	if msg.err != nil {
		m.setError(msg.err)
		return nil
	}
	state := m.ensureCatalog(msg.profileID)
	state.relations[msg.schema] = msg.relations
	state.expanded[msg.schema] = true
	m.rebuildBrowser()
	return nil
}

func (m *Model) handleColumns(msg columnsLoadedMsg) tea.Cmd {
	if msg.err != nil {
		m.setError(msg.err)
		return nil
	}
	state := m.ensureCatalog(msg.profileID)
	key := catalogKey(msg.schema, msg.relation)
	state.columns[key] = msg.columns
	state.expanded[key] = true
	m.rebuildBrowser()
	return nil
}

func (m *Model) rebuildBrowser() {
	items := []browserItem{}
	for _, connection := range m.profiles {
		state := m.ensureCatalog(connection.ID)
		items = append(items, browserItem{
			kind: browserProfile, label: connection.Name, profileID: connection.ID, isExpanded: state.isExpanded,
		})
		if !state.isExpanded {
			continue
		}
		for _, schema := range state.schemas {
			label := schema
			if containsSchema([]string{schema}, connection.Driver, connection.DefaultSchema) {
				label += "  (default)"
			}
			items = append(items, browserItem{
				kind: browserSchema, level: 1, label: label, profileID: connection.ID,
				schema: schema, isExpanded: state.expanded[schema],
			})
			if !state.expanded[schema] {
				continue
			}
			for _, relation := range state.relations[schema] {
				key := catalogKey(schema, relation.Name)
				items = append(items, browserItem{
					kind: browserRelation, level: 2, label: relation.Name, profileID: connection.ID,
					schema: schema, relation: relation.Name, isExpanded: state.expanded[key],
				})
				if !state.expanded[key] {
					continue
				}
				for _, column := range state.columns[key] {
					label := column.Name + "  " + column.Type
					if column.Key != "" {
						label += " " + column.Key
					}
					items = append(items, browserItem{
						kind: browserColumn, level: 3, label: label, profileID: connection.ID,
						schema: schema, relation: relation.Name,
					})
				}
			}
		}
	}
	m.browserItems = items
	if m.browserCursor >= len(items) {
		m.browserCursor = max(0, len(items)-1)
	}
}
