package tui

import (
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/lucasfguimares/tui-db/internal/profile"
	"github.com/lucasfguimares/tui-db/internal/sqleditor"
	"github.com/lucasfguimares/tui-db/internal/workspace"
)

func newSessionStore(t *testing.T) *workspace.Store {
	t.Helper()
	store, err := workspace.Open(filepath.Join(t.TempDir(), "workspace.json"))
	if err != nil {
		t.Fatalf("workspace.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// restoreModel builds a model and drives profile loading and session restore.
func restoreModel(t *testing.T, store *workspace.Store, profiles ...profile.Connection) (*Model, tea.Cmd) {
	t.Helper()
	model := New(nil, nil, nil, nil, nil, nil, nil, nil, store)
	if _, cmd := model.Update(profilesLoadedMsg{profiles: profiles}); cmd == nil {
		t.Fatal("profilesLoadedMsg did not request the session")
	}
	_, cmd := model.Update(model.loadSessionCmd()())
	return model, cmd
}

func TestRestoreSessionKeepsTabsWithoutConnecting(t *testing.T) {
	t.Parallel()

	store := newSessionStore(t)
	postgres := profile.Connection{ID: "pg", Name: "Primary", Driver: profile.DriverPostgres}
	saved := workspace.Snapshot{
		Tabs: []workspace.Tab{
			{ConnectionID: "pg", SQL: "SELECT 1;\nSELECT 2;", Cursor: workspace.Position{Line: 2, Column: 4}},
			{ConnectionID: "removed", SQL: "SELECT * FROM keep_me;", Cursor: workspace.Position{Line: 1, Column: 1}},
		},
		ActiveTab: 1,
	}
	if _, err := store.Save(1, saved); err != nil {
		t.Fatal(err)
	}

	model, cmd := restoreModel(t, store, postgres)
	if cmd != nil {
		t.Fatal("restoring the session returned a command; it must not connect or prompt")
	}
	if model.mode != modeWorkspace || len(model.tabs) != 2 || model.activeTab != 1 {
		t.Fatalf("mode = %v, tabs = %d, active = %d", model.mode, len(model.tabs), model.activeTab)
	}
	first := model.tabs[0]
	if first.connection.ID != "pg" || first.isDetached {
		t.Fatalf("first tab connection = %+v", first.connection)
	}
	offset, _ := cursorOffset(first.editor)
	if want := sqleditor.OffsetAt(first.editor.Value(), 2, 4); offset != want {
		t.Fatalf("cursor offset = %d, want %d", offset, want)
	}
	orphan := model.tabs[1]
	if !orphan.isDetached || orphan.connection.ID != "" || orphan.connection.Name != missingConnectionName {
		t.Fatalf("orphan tab = %+v detached = %v", orphan.connection, orphan.isDetached)
	}
	if orphan.editor.Value() != "SELECT * FROM keep_me;" {
		t.Fatalf("orphan text = %q", orphan.editor.Value())
	}
	if got := model.snapshot().Tabs[1].ConnectionID; got != "removed" {
		t.Fatalf("orphan snapshot connection = %q, want original id", got)
	}
	if cmd := model.requestRun(); cmd != nil || !model.isStatusError {
		t.Fatal("running a tab without connection did not fail")
	}

	model.profiles = append(model.profiles, profile.Connection{ID: "lite", Name: "Local", Driver: profile.DriverSQLite})
	model.rebuildBrowser()
	model.browserCursor = len(model.browserItems) - 1
	model.handleKey(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if orphan.isDetached || orphan.connection.ID != "lite" || orphan.editor.Value() != "SELECT * FROM keep_me;" {
		t.Fatalf("reassociated tab = %+v detached = %v", orphan.connection, orphan.isDetached)
	}
}

func TestRestoreSessionNeverTrustsTabs(t *testing.T) {
	t.Parallel()

	store := newSessionStore(t)
	postgres := profile.Connection{ID: "pg", Name: "Primary", Driver: profile.DriverPostgres}
	writer, _ := restoreModel(t, store, postgres)
	writer.addTab(postgres, "DELETE FROM users;")
	writer.tabs[0].isTrusted = true
	writer.saveSessionNow()

	reader, _ := restoreModel(t, store, postgres)
	if len(reader.tabs) != 1 {
		t.Fatalf("restored tabs = %d", len(reader.tabs))
	}
	if reader.tabs[0].isTrusted {
		t.Fatal("restored tab is trusted")
	}
}

func TestSessionSaveDueDiscardsStaleVersions(t *testing.T) {
	t.Parallel()

	store := newSessionStore(t)
	postgres := profile.Connection{ID: "pg", Name: "Primary", Driver: profile.DriverPostgres}
	model, _ := restoreModel(t, store, postgres)
	model.addTab(postgres, "SELECT 1;")
	tab := model.tabs[0]
	stale := tab.document.Version()
	current := tab.document.SetText("SELECT 2;")

	if cmd := model.handleSessionSaveDue(sessionSaveDueMsg{tabID: tab.id, version: stale}); cmd != nil {
		t.Fatal("stale debounce version scheduled a save")
	}
	cmd := model.handleSessionSaveDue(sessionSaveDueMsg{tabID: tab.id, version: current})
	if cmd == nil {
		t.Fatal("current debounce version did not save")
	}
	if msg, ok := cmd().(sessionSavedMsg); !ok || msg.err != nil {
		t.Fatalf("save result = %#v", msg)
	}
}

func TestQuitConfirmation(t *testing.T) {
	t.Parallel()

	postgres := profile.Connection{ID: "pg", Name: "Primary", Driver: profile.DriverPostgres}
	ctrlQ := tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl}

	t.Run("persisted session quits without confirmation", func(t *testing.T) {
		t.Parallel()
		model, _ := restoreModel(t, newSessionStore(t), postgres)
		model.addTab(postgres, "SELECT 1;")
		cmd := model.handleKey(ctrlQ)
		if model.mode == modeConfirmQuit || cmd == nil {
			t.Fatalf("mode = %v, cmd = %v", model.mode, cmd)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatal("ctrl+q did not quit")
		}
	})

	t.Run("running query asks for confirmation", func(t *testing.T) {
		t.Parallel()
		model, _ := restoreModel(t, newSessionStore(t), postgres)
		model.addTab(postgres, "SELECT 1;")
		model.tabs[0].isRunning = true
		if cmd := model.handleKey(ctrlQ); cmd != nil || model.mode != modeConfirmQuit {
			t.Fatalf("mode = %v, cmd = %v", model.mode, cmd)
		}
	})

	t.Run("unpersisted text asks for confirmation", func(t *testing.T) {
		t.Parallel()
		model := New(nil, nil, nil, nil, nil, nil, nil, nil, nil)
		if _, cmd := model.Update(profilesLoadedMsg{profiles: []profile.Connection{postgres}}); cmd != nil {
			t.Fatal("model without session store requested a session")
		}
		model.addTab(postgres, "SELECT 1;")
		if cmd := model.handleKey(ctrlQ); cmd != nil || model.mode != modeConfirmQuit {
			t.Fatalf("mode = %v, cmd = %v", model.mode, cmd)
		}
	})
}

func TestSecondInstanceRestoresWithoutSaving(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "workspace.json")
	owner, err := workspace.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	if _, err := owner.Save(1, workspace.Snapshot{Tabs: []workspace.Tab{{ConnectionID: "pg", SQL: "SELECT 1;"}}}); err != nil {
		t.Fatal(err)
	}
	second, err := workspace.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })

	postgres := profile.Connection{ID: "pg", Name: "Primary", Driver: profile.DriverPostgres}
	model, _ := restoreModel(t, second, postgres)
	if len(model.tabs) != 1 || model.isSessionPersisted() || model.sessionNotice == "" {
		t.Fatalf("tabs = %d, persisted = %v, notice = %q", len(model.tabs), model.isSessionPersisted(), model.sessionNotice)
	}
	model.addTab(postgres, "SELECT 2;")
	if cmd := model.saveSessionCmd(); cmd != nil {
		t.Fatal("non-owner instance scheduled a session save")
	}
}
