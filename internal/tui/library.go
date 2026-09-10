package tui

import (
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/lucasfguimares/charta-tui/internal/querylibrary"
	"github.com/lucasfguimares/charta-tui/internal/sqleditor"
)

type favoriteForm struct {
	favorite   querylibrary.Favorite
	inputs     []textinput.Model
	sql        textarea.Model
	focus      int
	errorText  string
	returnMode mode
}

type snippetForm struct {
	snippet     querylibrary.Snippet
	trigger     textinput.Model
	description textinput.Model
	body        textarea.Model
	focus       int
	errorText   string
}

func (m *Model) loadLibraryCmd() tea.Cmd {
	if m.library == nil {
		return func() tea.Msg { return libraryLoadedMsg{snippets: querylibrary.BuiltInSnippets()} }
	}
	search := m.librarySearch
	return func() tea.Msg {
		favorites, favoriteErr := m.library.Favorites(search)
		snippets, snippetErr := m.library.Snippets(search)
		return libraryLoadedMsg{favorites: favorites, snippets: snippets, err: errors.Join(favoriteErr, snippetErr)}
	}
}

func (m *Model) applyLibrary(msg libraryLoadedMsg) {
	if msg.err != nil {
		m.libraryError = msg.err.Error()
		m.setError(msg.err)
		return
	}
	m.libraryError = ""
	m.libraryFavorites, m.librarySnippets = msg.favorites, msg.snippets
	if m.libraryCursor >= m.libraryItemCount() {
		m.libraryCursor = max(0, m.libraryItemCount()-1)
	}
}

func (m *Model) openLibrary() tea.Cmd {
	m.mode = modeLibrary
	m.libraryCursor = 0
	return m.loadLibraryCmd()
}

func (m *Model) handleLibraryKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if m.mode == modeLibrarySearch {
		if key == "esc" {
			m.libraryInput.Blur()
			m.mode = modeLibrary
			return nil
		}
		if key == "enter" {
			m.librarySearch = strings.TrimSpace(m.libraryInput.Value())
			m.libraryInput.Blur()
			m.mode = modeLibrary
			m.libraryCursor = 0
			return m.loadLibraryCmd()
		}
		updated, cmd := m.libraryInput.Update(msg)
		m.libraryInput = updated
		return cmd
	}
	switch key {
	case "esc", "ctrl+shift+p":
		m.mode = modeWorkspace
	case "tab", "left", "right":
		m.librarySection = (m.librarySection + 1) % 2
		m.libraryCursor = 0
	case "up", "k":
		if m.libraryCursor > 0 {
			m.libraryCursor--
		}
	case "down", "j":
		if m.libraryCursor < m.libraryItemCount()-1 {
			m.libraryCursor++
		}
	case "/":
		m.libraryInput.SetValue(m.librarySearch)
		m.libraryInput.Focus()
		m.mode = modeLibrarySearch
		return textinput.Blink
	case "enter":
		return m.openSelectedLibraryItem(false)
	case "ctrl+enter":
		return m.openSelectedLibraryItem(true)
	case "a":
		if m.librarySection == 1 {
			m.openSnippetForm(querylibrary.Snippet{})
			return nil
		}
	case "e":
		return m.editSelectedLibraryItem()
	case "delete", "backspace":
		return m.deleteSelectedLibraryItemCmd()
	}
	return nil
}

func (m *Model) libraryItemCount() int {
	if m.librarySection == 0 {
		return len(m.libraryFavorites)
	}
	return len(m.librarySnippets)
}

func (m *Model) openSelectedLibraryItem(run bool) tea.Cmd {
	if m.librarySection == 0 {
		if m.libraryCursor < 0 || m.libraryCursor >= len(m.libraryFavorites) {
			return nil
		}
		return m.loadFavorite(m.libraryFavorites[m.libraryCursor], run)
	}
	if run || m.libraryCursor < 0 || m.libraryCursor >= len(m.librarySnippets) {
		return nil
	}
	tab := m.currentTab()
	if tab == nil {
		m.setError(fmt.Errorf("open a query tab before inserting a snippet"))
		return nil
	}
	cursor, ok := cursorOffset(tab.editor)
	if !ok {
		return nil
	}
	before := tab.editor.Value()
	updated, placeholders := querylibrary.Insert(before, cursor, m.librarySnippets[m.libraryCursor])
	setTabSQLAt(tab, updated, placeholders, cursor+len(updated)-len(before))
	m.mode = modeWorkspace
	tab.statusText = "Snippet inserted"
	return nil
}

func (m *Model) loadFavorite(favorite querylibrary.Favorite, run bool) tea.Cmd {
	if favorite.ConnectionID != "" {
		connection, ok := m.profileByID(favorite.ConnectionID)
		if !ok {
			m.setError(fmt.Errorf("favorite connection is no longer available"))
			return nil
		}
		m.ensureTab(connection)
	} else if m.currentTab() == nil {
		m.setError(fmt.Errorf("select a connection before opening this favorite"))
		return nil
	}
	tab := m.currentTab()
	placeholders := querylibrary.FindPlaceholders(favorite.SQL)
	setTabSQLAt(tab, favorite.SQL, placeholders, len(favorite.SQL))
	if len(placeholders) > 0 {
		tab.statusText = "Loaded favorite " + favorite.Name + " — fill placeholders, then run"
	} else {
		tab.statusText = "Loaded favorite " + favorite.Name
	}
	m.mode = modeWorkspace
	m.setFocus(focusEditor)
	if run {
		return m.requestRunScript()
	}
	return nil
}

func (m *Model) editSelectedLibraryItem() tea.Cmd {
	if m.librarySection == 0 {
		if m.libraryCursor >= 0 && m.libraryCursor < len(m.libraryFavorites) {
			m.openFavoriteForm(m.libraryFavorites[m.libraryCursor])
			return nil
		}
		return nil
	}
	if m.libraryCursor >= 0 && m.libraryCursor < len(m.librarySnippets) {
		snippet := m.librarySnippets[m.libraryCursor]
		if snippet.IsBuiltIn {
			m.setError(fmt.Errorf("built-in snippets cannot be edited"))
			return nil
		}
		m.openSnippetForm(snippet)
	}
	return nil
}

func (m *Model) deleteSelectedLibraryItemCmd() tea.Cmd {
	if m.library == nil {
		return nil
	}
	if m.librarySection == 0 {
		if m.libraryCursor < 0 || m.libraryCursor >= len(m.libraryFavorites) {
			return nil
		}
		id := m.libraryFavorites[m.libraryCursor].ID
		return func() tea.Msg { _, err := m.library.DeleteFavorite(id); return libraryChangedMsg{err: err} }
	}
	if m.libraryCursor < 0 || m.libraryCursor >= len(m.librarySnippets) {
		return nil
	}
	snippet := m.librarySnippets[m.libraryCursor]
	if snippet.IsBuiltIn {
		m.setError(fmt.Errorf("built-in snippets cannot be deleted"))
		return nil
	}
	return func() tea.Msg { _, err := m.library.DeleteSnippet(snippet.ID); return libraryChangedMsg{err: err} }
}

func (m *Model) openFavoriteForm(favorite querylibrary.Favorite) {
	returnMode := m.mode
	tab := m.currentTab()
	if favorite.ID == "" && tab == nil {
		m.setError(fmt.Errorf("open a query tab before saving a favorite"))
		return
	}
	inputs := make([]textinput.Model, 3)
	labels := []string{"Name: ", "Description: ", "Tags (comma separated): "}
	values := []string{favorite.Name, favorite.Description, strings.Join(favorite.Tags, ", ")}
	for i := range inputs {
		inputs[i] = textinput.New()
		inputs[i].Prompt = labels[i]
		inputs[i].SetWidth(60)
		inputs[i].SetValue(values[i])
	}
	inputs[0].Focus()
	sql := favorite.SQL
	if favorite.ID == "" {
		sql = tab.editor.Value()
		favorite.ConnectionID = tab.connection.ID
	}
	sqlEditor := textarea.New()
	sqlEditor.Placeholder = "Favorite SQL"
	sqlEditor.ShowLineNumbers = true
	sqlEditor.SetWidth(68)
	sqlEditor.SetHeight(10)
	sqlEditor.SetValue(sql)
	m.favoriteForm = favoriteForm{favorite: favorite, sql: sqlEditor, inputs: inputs, returnMode: returnMode}
	m.librarySection = 0
	m.mode = modeFavoriteForm
}

func (m *Model) handleFavoriteFormKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if key == "esc" {
		m.mode = m.favoriteForm.returnMode
		return nil
	}
	if key == "ctrl+d" {
		if m.favoriteForm.favorite.ConnectionID == "" {
			if tab := m.currentTab(); tab != nil {
				m.favoriteForm.favorite.ConnectionID = tab.connection.ID
			}
		} else {
			m.favoriteForm.favorite.ConnectionID = ""
		}
		return nil
	}
	if key == "tab" || key == "shift+tab" {
		for i := range m.favoriteForm.inputs {
			m.favoriteForm.inputs[i].Blur()
		}
		m.favoriteForm.sql.Blur()
		delta := 1
		if key == "shift+tab" {
			delta = -1
		}
		m.favoriteForm.focus = (m.favoriteForm.focus + delta + 4) % 4
		if m.favoriteForm.focus == 3 {
			return m.favoriteForm.sql.Focus()
		}
		return m.favoriteForm.inputs[m.favoriteForm.focus].Focus()
	}
	if key == "ctrl+s" {
		favorite := m.favoriteForm.favorite
		favorite.Name = m.favoriteForm.inputs[0].Value()
		favorite.Description = m.favoriteForm.inputs[1].Value()
		favorite.SQL = m.favoriteForm.sql.Value()
		favorite.Tags = strings.Split(m.favoriteForm.inputs[2].Value(), ",")
		if m.library == nil {
			return nil
		}
		return func() tea.Msg { _, err := m.library.SaveFavorite(favorite); return libraryChangedMsg{err: err} }
	}
	if m.favoriteForm.focus == 3 {
		updated, cmd := m.favoriteForm.sql.Update(msg)
		m.favoriteForm.sql = updated
		return cmd
	}
	updated, cmd := m.favoriteForm.inputs[m.favoriteForm.focus].Update(msg)
	m.favoriteForm.inputs[m.favoriteForm.focus] = updated
	return cmd
}

func (m *Model) openSnippetForm(snippet querylibrary.Snippet) {
	trigger := textinput.New()
	trigger.Prompt = "Trigger: "
	trigger.SetWidth(60)
	trigger.SetValue(snippet.Trigger)
	trigger.Focus()
	description := textinput.New()
	description.Prompt = "Description: "
	description.SetWidth(60)
	description.SetValue(snippet.Description)
	body := textarea.New()
	body.Placeholder = "SQL snippet body with ${placeholders}"
	body.ShowLineNumbers = true
	body.SetWidth(68)
	body.SetHeight(12)
	body.SetValue(snippet.Body)
	m.snippetForm = snippetForm{snippet: snippet, trigger: trigger, description: description, body: body}
	m.mode = modeSnippetForm
}

func (m *Model) handleSnippetFormKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if key == "esc" {
		m.mode = modeLibrary
		return nil
	}
	if key == "tab" || key == "shift+tab" {
		m.blurSnippetForm()
		delta := 1
		if key == "shift+tab" {
			delta = -1
		}
		m.snippetForm.focus = (m.snippetForm.focus + delta + 3) % 3
		return m.focusSnippetForm()
	}
	if key == "ctrl+s" {
		snippet := m.snippetForm.snippet
		snippet.Trigger = m.snippetForm.trigger.Value()
		snippet.Description = m.snippetForm.description.Value()
		snippet.Body = m.snippetForm.body.Value()
		if m.library == nil {
			return nil
		}
		return func() tea.Msg { _, err := m.library.SaveSnippet(snippet); return libraryChangedMsg{err: err} }
	}
	var cmd tea.Cmd
	switch m.snippetForm.focus {
	case 0:
		m.snippetForm.trigger, cmd = m.snippetForm.trigger.Update(msg)
	case 1:
		m.snippetForm.description, cmd = m.snippetForm.description.Update(msg)
	default:
		m.snippetForm.body, cmd = m.snippetForm.body.Update(msg)
	}
	return cmd
}

func (m *Model) blurSnippetForm() {
	m.snippetForm.trigger.Blur()
	m.snippetForm.description.Blur()
	m.snippetForm.body.Blur()
}
func (m *Model) focusSnippetForm() tea.Cmd {
	switch m.snippetForm.focus {
	case 0:
		return m.snippetForm.trigger.Focus()
	case 1:
		return m.snippetForm.description.Focus()
	default:
		return m.snippetForm.body.Focus()
	}
}

func (m *Model) renderLibraryScreen() string {
	favoritesTitle, snippetsTitle := "Favorites", "Snippets"
	if m.librarySection == 0 {
		favoritesTitle = "[ Favorites ]"
	} else {
		snippetsTitle = "[ Snippets ]"
	}
	header := m.styles.header.Render(favoritesTitle+"   "+snippetsTitle) + "  " + m.styles.dim.Render(fmt.Sprintf("search=%q", m.librarySearch))
	lines := []string{}
	visible := max(1, (m.height-5)/2)
	start := 0
	if m.libraryCursor >= visible {
		start = m.libraryCursor - visible + 1
	}
	end := min(m.libraryItemCount(), start+visible)
	if m.librarySection == 0 {
		for i := start; i < end; i++ {
			favorite := m.libraryFavorites[i]
			line := "★ " + favorite.Name
			if len(favorite.Tags) > 0 {
				line += "  [" + strings.Join(favorite.Tags, ", ") + "]"
			}
			if i == m.libraryCursor {
				line = m.styles.selected.Width(max(20, m.width-4)).Render(line)
			}
			lines = append(lines, line)
			if favorite.Description != "" {
				lines = append(lines, "  "+m.styles.dim.Render(favorite.Description))
			}
		}
	} else {
		for i := start; i < end; i++ {
			snippet := m.librarySnippets[i]
			kind := "custom"
			if snippet.IsBuiltIn {
				kind = "built-in"
			}
			line := fmt.Sprintf("%-12s %-10s %s", snippet.Trigger, kind, snippet.Description)
			if i == m.libraryCursor {
				line = m.styles.selected.Width(max(20, m.width-4)).Render(line)
			}
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		lines = append(lines, m.styles.dim.Render("No items match the current search."))
	}
	if m.libraryError != "" {
		lines = append([]string{m.styles.error.Render(m.libraryError)}, lines...)
	}
	footer := m.styles.dim.Render("Tab section  Enter open/insert  Ctrl+Enter run favorite  / search  a add snippet  e edit  Delete remove  Esc return")
	if m.mode == modeLibrarySearch {
		footer = m.libraryInput.View() + "  " + m.styles.dim.Render("Enter apply  Esc cancel")
	}
	return m.styles.app.Render(lipgloss.JoinVertical(lipgloss.Left, header, strings.Join(lines, "\n"), footer))
}

func (m *Model) renderFavoriteForm() string {
	lines := []string{m.styles.header.Render("Save favorite"), ""}
	for i := range m.favoriteForm.inputs {
		lines = append(lines, m.favoriteForm.inputs[i].View())
	}
	connection := "any connection"
	if value, ok := m.profileByID(m.favoriteForm.favorite.ConnectionID); ok {
		connection = value.Name
	}
	lines = append(lines, "", "Connection: "+connection, "", m.favoriteForm.sql.View())
	if m.favoriteForm.errorText != "" {
		lines = append(lines, m.styles.error.Render(m.favoriteForm.errorText))
	}
	lines = append(lines, "", m.styles.dim.Render("Ctrl+S save  Ctrl+D toggle connection  Tab next field  Esc cancel"))
	return m.center(m.styles.modal.Width(76).Render(strings.Join(lines, "\n")))
}

func (m *Model) renderSnippetForm() string {
	content := []string{m.styles.header.Render("Custom snippet"), "", m.snippetForm.trigger.View(), m.snippetForm.description.View(), "", m.snippetForm.body.View(), "", m.styles.dim.Render("Use ${name} placeholders  Ctrl+S save  Tab next field  Esc cancel")}
	if m.snippetForm.errorText != "" {
		content = append(content, m.styles.error.Render(m.snippetForm.errorText))
	}
	return m.center(m.styles.modal.Width(76).Render(strings.Join(content, "\n")))
}

func setTabSQL(tab *queryTab, value string) {
	if tab == nil {
		return
	}
	applyTabSQL(tab, value, len(value))
	tab.placeholders = nil
	tab.placeholderAwaiting = false
}

func setTabSQLAt(tab *queryTab, value string, placeholders []querylibrary.Placeholder, fallbackCursor int) {
	if tab == nil {
		return
	}
	cursor := fallbackCursor
	if len(placeholders) > 0 {
		cursor = placeholders[0].Start + 2
	}
	applyTabSQL(tab, value, cursor)
	tab.placeholders = placeholders
	tab.placeholderIndex = 0
	tab.placeholderAwaiting = len(placeholders) > 0
}

func activatePlaceholders(tab *queryTab, value string, offset int) bool {
	placeholders := querylibrary.FindPlaceholders(value)
	if len(placeholders) == 0 {
		return false
	}
	for i := range placeholders {
		placeholders[i].Start += offset
		placeholders[i].End += offset
	}
	tab.placeholders = placeholders
	tab.placeholderIndex = 0
	tab.placeholderAwaiting = true
	setEditorCursor(&tab.editor, placeholders[0].Start+2)
	tab.statusText = "Fill placeholders before running — Tab navigates"
	return true
}

func applyTabSQL(tab *queryTab, value string, cursor int) {
	tab.editor.SetValue(value)
	setEditorCursor(&tab.editor, cursor)
	if tab.document == nil {
		tab.document = sqleditor.NewDocument(value)
	} else {
		tab.document.SetText(value)
	}
	version := tab.document.Version()
	tab.analysis = sqleditor.Analyze(value, dialectFor(tab.connection.Driver), version)
	_ = tab.document.Apply(tab.analysis)
	tab.selection = nil
	tab.completion.mode = autocompleteClosed
}

func (m *Model) handlePlaceholderKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	tab := m.currentTab()
	if tab == nil {
		return nil, false
	}
	if msg.Text != "" && tab.placeholderAwaiting && tab.placeholderIndex >= 0 && tab.placeholderIndex < len(tab.placeholders) {
		m.replaceCurrentPlaceholder(msg.Text)
		return nil, true
	}
	key := msg.String()
	if key != "tab" && key != "shift+tab" {
		return nil, false
	}
	if len(tab.placeholders) == 0 && key == "tab" {
		cursor, ok := cursorOffset(tab.editor)
		if !ok {
			return nil, false
		}
		updated, placeholders, expanded := querylibrary.Expand(tab.editor.Value(), cursor, m.librarySnippets)
		if !expanded {
			return nil, false
		}
		setTabSQLAt(tab, updated, placeholders, cursor)
		tab.statusText = "Snippet expanded — Tab navigates placeholders"
		return nil, true
	}
	if len(tab.placeholders) == 0 {
		return nil, false
	}
	if key == "shift+tab" {
		tab.placeholderIndex = (tab.placeholderIndex - 1 + len(tab.placeholders)) % len(tab.placeholders)
	} else if !tab.placeholderAwaiting {
		tab.placeholderIndex = (tab.placeholderIndex + 1) % len(tab.placeholders)
	} else {
		tab.placeholderIndex = (tab.placeholderIndex + 1) % len(tab.placeholders)
	}
	placeholder := tab.placeholders[tab.placeholderIndex]
	setEditorCursor(&tab.editor, placeholder.Start+2)
	tab.placeholderAwaiting = true
	return nil, true
}

func (m *Model) replaceCurrentPlaceholder(replacement string) {
	tab := m.currentTab()
	index := tab.placeholderIndex
	placeholder := tab.placeholders[index]
	value := tab.editor.Value()
	if placeholder.Start < 0 || placeholder.End > len(value) || placeholder.Start >= placeholder.End {
		tab.placeholders = nil
		return
	}
	updated := value[:placeholder.Start] + replacement + value[placeholder.End:]
	delta := len(replacement) - (placeholder.End - placeholder.Start)
	remaining := append([]querylibrary.Placeholder{}, tab.placeholders[:index]...)
	for _, candidate := range tab.placeholders[index+1:] {
		candidate.Start += delta
		candidate.End += delta
		remaining = append(remaining, candidate)
	}
	applyTabSQL(tab, updated, placeholder.Start+len(replacement))
	tab.placeholders = remaining
	tab.placeholderIndex = index - 1
	tab.placeholderAwaiting = false
}

func adjustPlaceholders(tab *queryTab, cursor, delta int) {
	if delta == 0 || len(tab.placeholders) == 0 {
		return
	}
	for i := range tab.placeholders {
		if cursor <= tab.placeholders[i].Start {
			tab.placeholders[i].Start += delta
			tab.placeholders[i].End += delta
			continue
		}
		if cursor < tab.placeholders[i].End {
			tab.placeholders = nil
			tab.placeholderAwaiting = false
			return
		}
	}
}
