package tui

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/lucasfguimares/charta-tui/internal/activity"
	"github.com/lucasfguimares/charta-tui/internal/profile"
)

func (m *Model) loadProfilesCmd() tea.Cmd {
	return func() tea.Msg {
		profiles, err := m.store.List()
		return profilesLoadedMsg{profiles: profiles, err: err}
	}
}

func (m *Model) saveProfileCmd(connection profile.Connection, password string) tea.Cmd {
	return func() tea.Msg {
		if err := m.store.Save(connection); err != nil {
			return profileSavedMsg{connection: connection, err: err}
		}
		if password != "" {
			if err := m.secrets.Set(connection.ID, password); err != nil {
				return profileSavedMsg{connection: connection, err: err}
			}
		}
		if err := m.manager.Disconnect(connection.ID); err != nil {
			return profileSavedMsg{connection: connection, err: err}
		}
		return profileSavedMsg{connection: connection}
	}
}

func (m *Model) deleteProfileCmd(id string) tea.Cmd {
	return func() tea.Msg {
		secretErr := m.secrets.Delete(id)
		disconnectErr := m.manager.Disconnect(id)
		_, storeErr := m.store.Delete(id)
		return profileDeletedMsg{profileID: id, err: errors.Join(storeErr, secretErr, disconnectErr)}
	}
}

func (m *Model) prepareConnectionCmd(connection profile.Connection) tea.Cmd {
	if connection.Driver == profile.DriverSQLite {
		return m.loadSchemasCmd(connection)
	}
	return func() tea.Msg {
		_, err := m.secrets.Get(connection.ID)
		if err != nil {
			if errors.Is(err, profile.ErrSecretNotFound) {
				return passwordNeededMsg{connection: connection, action: passwordConnect}
			}
			return schemasLoadedMsg{profileID: connection.ID, err: err}
		}
		return m.loadSchemasCmd(connection)()
	}
}

func (m *Model) prepareTestCmd(connection profile.Connection) tea.Cmd {
	if connection.Driver == profile.DriverSQLite {
		return m.testConnectionCmd(connection)
	}
	return func() tea.Msg {
		_, err := m.secrets.Get(connection.ID)
		if err != nil {
			if errors.Is(err, profile.ErrSecretNotFound) {
				return passwordNeededMsg{connection: connection, action: passwordTest}
			}
			return connectionTestedMsg{connection: connection, err: err}
		}
		return m.testConnectionCmd(connection)()
	}
}

func (m *Model) testConnectionCmd(connection profile.Connection) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, err := m.manager.Connection(ctx, connection)
		return connectionTestedMsg{connection: connection, err: err}
	}
}

func (m *Model) disconnectCmd(connection profile.Connection) tea.Cmd {
	return func() tea.Msg {
		err := m.manager.Disconnect(connection.ID)
		return connectionDisconnectedMsg{connection: connection, err: err}
	}
}

func (m *Model) loadSchemasCmd(connection profile.Connection) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		schemas, err := m.inspector.Schemas(ctx, connection)
		return schemasLoadedMsg{profileID: connection.ID, schemas: schemas, err: err}
	}
}

func (m *Model) loadRelationsCmd(connection profile.Connection, schema string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		relations, err := m.inspector.Relations(ctx, connection, schema)
		return relationsLoadedMsg{profileID: connection.ID, schema: schema, relations: relations, err: err}
	}
}

func (m *Model) loadColumnsCmd(connection profile.Connection, schema, relation string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		columns, err := m.inspector.Columns(ctx, connection, schema, relation)
		return columnsLoadedMsg{
			profileID: connection.ID, schema: schema, relation: relation, columns: columns, err: err,
		}
	}
}

func (m *Model) loadLogsCmd() tea.Cmd {
	return func() tea.Msg {
		entries, err := m.activity.Entries(activity.DefaultReadMax)
		return logsLoadedMsg{entries: entries, err: err}
	}
}
