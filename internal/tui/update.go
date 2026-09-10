package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/lucasfguimares/charta-tui/internal/activity"
	"github.com/lucasfguimares/charta-tui/internal/sqleditor"
)

func (m *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil
	case profilesLoadedMsg:
		if msg.err != nil {
			m.setError(msg.err)
			return m, nil
		}
		m.profiles = msg.profiles
		m.syncTabConnections()
		m.rebuildBrowser()
		if len(m.profiles) == 0 {
			m.statusText = "No connections yet — press a to add one"
		} else {
			m.statusText = fmt.Sprintf("%d connection(s) loaded", len(m.profiles))
		}
		return m, nil
	case profileSavedMsg:
		if msg.err != nil {
			m.mode = modeProfile
			m.form.errorText = msg.err.Error()
			return m, nil
		}
		m.mode = modeWorkspace
		delete(m.catalog, msg.connection.ID)
		delete(m.autocompleteCache, msg.connection.ID)
		m.invalidateColumnCaches(msg.connection.ID)
		m.statusText = fmt.Sprintf("Saved %s", msg.connection.Name)
		m.isStatusError = false
		return m, m.loadProfilesCmd()
	case profileDeletedMsg:
		delete(m.catalog, msg.profileID)
		delete(m.autocompleteCache, msg.profileID)
		m.invalidateColumnCaches(msg.profileID)
		if msg.err != nil {
			m.setError(msg.err)
			return m, m.loadProfilesCmd()
		}
		m.statusText = "Connection deleted"
		return m, m.loadProfilesCmd()
	case passwordNeededMsg:
		m.pendingConnection = msg.connection
		m.pendingPasswordAct = msg.action
		m.passwordInput.SetValue("")
		m.passwordInput.Focus()
		m.mode = modePassword
		return m, nil
	case schemasLoadedMsg:
		return m, m.handleSchemas(msg)
	case connectionTestedMsg:
		if msg.err != nil {
			m.setError(msg.err)
			return m, nil
		}
		m.statusText = fmt.Sprintf("Connection to %s succeeded", msg.connection.Name)
		m.isStatusError = false
		return m, nil
	case connectionDisconnectedMsg:
		if msg.err != nil {
			m.setError(msg.err)
			return m, nil
		}
		delete(m.catalog, msg.connection.ID)
		delete(m.autocompleteCache, msg.connection.ID)
		m.invalidateColumnCaches(msg.connection.ID)
		m.rebuildBrowser()
		m.statusText = fmt.Sprintf("Disconnected from %s", msg.connection.Name)
		m.isStatusError = false
		return m, nil
	case relationsLoadedMsg:
		return m, m.handleRelations(msg)
	case columnsLoadedMsg:
		return m, m.handleColumns(msg)
	case queryFinishedMsg:
		return m, m.handleQueryFinished(msg)
	case queryTickMsg:
		tab := m.tabByID(msg.tabID)
		if tab == nil || !tab.isRunning || tab.requestID != msg.requestID {
			return m, nil
		}
		return m, queryTickCmd(msg.tabID, msg.requestID)
	case sqlAnalysisDueMsg:
		tab := m.tabByID(msg.tabID)
		if tab == nil || tab.document == nil || tab.document.Version() != msg.version {
			return m, nil
		}
		text, dialect := tab.document.Text(), dialectFor(tab.connection.Driver)
		return m, func() tea.Msg {
			return sqlAnalysisMsg{tabID: msg.tabID, result: sqleditor.Analyze(text, dialect, msg.version)}
		}
	case sqlAnalysisMsg:
		tab := m.tabByID(msg.tabID)
		if tab == nil || tab.document == nil || !tab.document.Apply(msg.result) {
			return m, nil
		}
		tab.analysis = msg.result
		if len(tab.analysis.Diagnostics) == 0 {
			tab.diagnostic = 0
		} else if tab.diagnostic >= len(tab.analysis.Diagnostics) {
			tab.diagnostic = len(tab.analysis.Diagnostics) - 1
		}
		return m, nil
	case autocompleteCatalogMsg:
		return m, m.handleAutocompleteCatalog(msg)
	case autocompleteDueMsg:
		return m, m.handleAutocompleteDue(msg)
	case autocompleteResultMsg:
		return m, m.handleAutocompleteResult(msg)
	case columnMetadataLoadedMsg:
		return m, m.handleColumnMetadataLoaded(msg)
	case columnStatisticsLoadedMsg:
		return m, m.handleColumnStatisticsLoaded(msg)
	case logsLoadedMsg:
		m.renderLogs(msg)
		return m, nil
	case historyLoadedMsg:
		m.applyHistory(msg)
		return m, nil
	case historyChangedMsg:
		if msg.err != nil {
			m.setError(msg.err)
			return m, nil
		}
		return m, m.loadHistoryCmd()
	case historyRecordedMsg:
		if msg.err != nil {
			if tab := m.tabByID(msg.tabID); tab != nil {
				tab.statusText = "Saving query history failed: " + activity.Redact(msg.err.Error())
			}
		}
		return m, nil
	case libraryLoadedMsg:
		m.applyLibrary(msg)
		return m, nil
	case libraryChangedMsg:
		if msg.err != nil {
			if m.mode == modeFavoriteForm {
				m.favoriteForm.errorText = msg.err.Error()
			} else if m.mode == modeSnippetForm {
				m.snippetForm.errorText = msg.err.Error()
			} else {
				m.setError(msg.err)
			}
			return m, nil
		}
		m.mode = modeLibrary
		return m, m.loadLibraryCmd()
	case tea.KeyPressMsg:
		return m, m.handleKey(msg)
	}

	return m, m.updateFocused(message)
}

// View renders the responsive workspace or current modal screen.
