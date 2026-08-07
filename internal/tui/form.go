package tui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/lucasfguimares/tui-db/internal/profile"
)

type formField struct {
	key   string
	label string
	input textinput.Model
}

type profileForm struct {
	connection profile.Connection
	fields     []formField
	focused    int
	isNew      bool
	errorText  string
}

func newProfileForm(connection profile.Connection, isNew bool) profileForm {
	form := profileForm{connection: connection, isNew: isNew}
	form.rebuild("")
	return form
}

func (f *profileForm) rebuild(password string) {
	values := map[string]string{
		"name":         f.connection.Name,
		"host":         f.connection.Host,
		"port":         strconv.Itoa(int(f.connection.Port)),
		"database":     f.connection.Database,
		"username":     f.connection.Username,
		"path":         f.connection.SQLitePath,
		"read_only":    strconv.FormatBool(f.connection.SQLiteReadOnly),
		"tls":          string(f.connection.TLS.Mode),
		"ca_file":      f.connection.TLS.CAFile,
		"server_name":  f.connection.TLS.ServerName,
		"timeout":      strconv.Itoa(f.connection.QueryTimeoutSeconds),
		"row_limit":    strconv.Itoa(f.connection.RowLimit),
		"column_width": strconv.Itoa(f.connection.EffectiveMaxColumnWidth()),
		"password":     password,
	}
	definitions := [][3]string{{"name", "Name", "connection name"}}
	if f.connection.Driver == profile.DriverSQLite {
		definitions = append(definitions,
			[3]string{"path", "Database file", "/path/to/database.sqlite"},
			[3]string{"read_only", "Read only", "true or false"},
		)
	} else {
		definitions = append(definitions,
			[3]string{"host", "Host", "localhost"},
			[3]string{"port", "Port", "port"},
			[3]string{"database", "Database", "database name"},
			[3]string{"username", "Username", "database user"},
			[3]string{"password", "Password", "stored in keyring"},
			[3]string{"tls", "TLS mode", "verify-full, verify-ca, require, disable"},
			[3]string{"ca_file", "CA file", "optional certificate path"},
			[3]string{"server_name", "TLS server name", "optional override"},
		)
	}
	definitions = append(definitions,
		[3]string{"timeout", "Timeout (seconds)", "30"},
		[3]string{"row_limit", "Row limit", "1000"},
		[3]string{"column_width", "Maximum column width", "40"},
	)

	f.fields = make([]formField, 0, len(definitions))
	for _, definition := range definitions {
		input := textinput.New()
		input.Prompt = ""
		input.Placeholder = definition[2]
		input.SetValue(values[definition[0]])
		input.SetWidth(48)
		if definition[0] == "password" {
			input.EchoMode = textinput.EchoPassword
		}
		f.fields = append(f.fields, formField{key: definition[0], label: definition[1], input: input})
	}
	if f.focused >= len(f.fields) {
		f.focused = len(f.fields) - 1
	}
	f.focusCurrent()
}

func (f *profileForm) focusCurrent() tea.Cmd {
	cmds := []tea.Cmd{}
	for i := range f.fields {
		if i == f.focused {
			cmds = append(cmds, f.fields[i].input.Focus())
		} else {
			f.fields[i].input.Blur()
		}
	}
	return tea.Batch(cmds...)
}

func (f *profileForm) update(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	switch key {
	case "tab", "down", "enter":
		f.focused = (f.focused + 1) % len(f.fields)
		return f.focusCurrent()
	case "shift+tab", "up":
		f.focused--
		if f.focused < 0 {
			f.focused = len(f.fields) - 1
		}
		return f.focusCurrent()
	case "ctrl+d":
		f.applyValues()
		password := f.value("password")
		switch f.connection.Driver {
		case profile.DriverPostgres:
			f.connection.Driver = profile.DriverSQLServer
			f.connection.Port = 1433
		case profile.DriverSQLServer:
			f.connection.Driver = profile.DriverSQLite
		case profile.DriverSQLite:
			f.connection.Driver = profile.DriverPostgres
			f.connection.Host = "localhost"
			f.connection.Port = 5432
			f.connection.TLS.Mode = profile.TLSModeVerifyFull
		default:
			f.connection.Driver = profile.DriverPostgres
		}
		f.rebuild(password)
		return nil
	}
	updated, cmd := f.fields[f.focused].input.Update(msg)
	f.fields[f.focused].input = updated
	return cmd
}

func (f *profileForm) applyValues() {
	f.connection.Name = strings.TrimSpace(f.value("name"))
	f.connection.Host = strings.TrimSpace(f.value("host"))
	f.connection.Database = strings.TrimSpace(f.value("database"))
	f.connection.Username = strings.TrimSpace(f.value("username"))
	f.connection.SQLitePath = strings.TrimSpace(f.value("path"))
	f.connection.SQLiteReadOnly, _ = strconv.ParseBool(f.value("read_only"))
	f.connection.TLS.Mode = profile.TLSMode(strings.TrimSpace(f.value("tls")))
	f.connection.TLS.CAFile = strings.TrimSpace(f.value("ca_file"))
	f.connection.TLS.ServerName = strings.TrimSpace(f.value("server_name"))
	if port, err := strconv.ParseUint(strings.TrimSpace(f.value("port")), 10, 16); err == nil {
		f.connection.Port = uint16(port)
	}
	if timeout, err := strconv.Atoi(strings.TrimSpace(f.value("timeout"))); err == nil {
		f.connection.QueryTimeoutSeconds = timeout
	}
	if rowLimit, err := strconv.Atoi(strings.TrimSpace(f.value("row_limit"))); err == nil {
		f.connection.RowLimit = rowLimit
	}
	if columnWidth, err := strconv.Atoi(strings.TrimSpace(f.value("column_width"))); err == nil {
		f.connection.MaxColumnWidth = columnWidth
	}
}

func (f *profileForm) validate() error {
	f.applyValues()
	if _, err := strconv.ParseUint(strings.TrimSpace(f.value("port")), 10, 16); f.connection.Driver != profile.DriverSQLite && err != nil {
		return errorsField("port", "must be an integer from 1 to 65535")
	}
	if _, err := strconv.ParseBool(strings.TrimSpace(f.value("read_only"))); f.connection.Driver == profile.DriverSQLite && err != nil {
		return errorsField("read only", "must be true or false")
	}
	if _, err := strconv.Atoi(strings.TrimSpace(f.value("timeout"))); err != nil {
		return errorsField("timeout", "must be an integer")
	}
	if _, err := strconv.Atoi(strings.TrimSpace(f.value("row_limit"))); err != nil {
		return errorsField("row limit", "must be an integer")
	}
	if _, err := strconv.Atoi(strings.TrimSpace(f.value("column_width"))); err != nil {
		return errorsField("maximum column width", "must be an integer")
	}
	return f.connection.Validate()
}

func (f *profileForm) value(key string) string {
	for _, field := range f.fields {
		if field.key == key {
			return field.input.Value()
		}
	}
	return ""
}

func (f *profileForm) clearPassword() {
	for index := range f.fields {
		if f.fields[index].key == "password" {
			f.fields[index].input.SetValue("")
			return
		}
	}
}

func errorsField(field, reason string) error {
	return fmt.Errorf("%s %s", field, reason)
}
