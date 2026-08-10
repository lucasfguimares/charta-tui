// Package profile defines and persists database connection profiles.
package profile

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
)

const (
	DefaultQueryTimeoutSeconds = 30
	DefaultRowLimit            = 1000
	DefaultMaxColumnWidth      = 40
)

// Driver identifies a supported SQL engine.
type Driver string

const (
	DriverUnknown   Driver = ""
	DriverPostgres  Driver = "postgres"
	DriverSQLServer Driver = "sqlserver"
	DriverSQLite    Driver = "sqlite"
)

// TLSMode controls transport security for network database connections.
type TLSMode string

const (
	TLSModeDefault    TLSMode = "default"
	TLSModeDisable    TLSMode = "disable"
	TLSModeRequire    TLSMode = "require"
	TLSModeVerifyCA   TLSMode = "verify-ca"
	TLSModeVerifyFull TLSMode = "verify-full"
)

// TLS contains engine-independent certificate settings.
type TLS struct {
	Mode       TLSMode `json:"mode"`
	CAFile     string  `json:"ca_file,omitempty"`
	ServerName string  `json:"server_name,omitempty"`
}

// Connection describes a saved database connection without its password.
type Connection struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Driver              Driver `json:"driver"`
	Host                string `json:"host,omitempty"`
	Port                uint16 `json:"port,omitempty"`
	Database            string `json:"database,omitempty"`
	DefaultSchema       string `json:"default_schema,omitempty"`
	Username            string `json:"username,omitempty"`
	SQLitePath          string `json:"sqlite_path,omitempty"`
	SQLiteReadOnly      bool   `json:"sqlite_read_only,omitempty"`
	TLS                 TLS    `json:"tls"`
	QueryTimeoutSeconds int    `json:"query_timeout_seconds"`
	RowLimit            int    `json:"row_limit"`
	MaxColumnWidth      int    `json:"max_column_width,omitempty"`
}

// NewConnection returns a profile populated with secure engine defaults.
func NewConnection(driver Driver) (Connection, error) {
	id, err := newID()
	if err != nil {
		return Connection{}, fmt.Errorf("generating profile id: %w", err)
	}

	connection := Connection{
		ID:                  id,
		Driver:              driver,
		QueryTimeoutSeconds: DefaultQueryTimeoutSeconds,
		RowLimit:            DefaultRowLimit,
		MaxColumnWidth:      DefaultMaxColumnWidth,
		TLS: TLS{
			Mode: TLSModeVerifyFull,
		},
	}

	switch driver {
	case DriverPostgres:
		connection.Name = "PostgreSQL"
		connection.Host = "localhost"
		connection.Port = 5432
		connection.DefaultSchema = NativeDefaultSchema(driver)
	case DriverSQLServer:
		connection.Name = "SQL Server"
		connection.Host = "localhost"
		connection.Port = 1433
		connection.DefaultSchema = NativeDefaultSchema(driver)
	case DriverSQLite:
		connection.Name = "SQLite"
		connection.DefaultSchema = NativeDefaultSchema(driver)
		connection.TLS.Mode = TLSModeDefault
	default:
		return Connection{}, errors.New("profile: unsupported driver")
	}

	return connection, nil
}

// Validate checks that a profile can be saved or opened safely.
func (c Connection) Validate() error {
	if strings.TrimSpace(c.ID) == "" {
		return errors.New("profile: id is required")
	}
	if strings.TrimSpace(c.Name) == "" {
		return errors.New("profile: name is required")
	}
	if c.QueryTimeoutSeconds < 1 || c.QueryTimeoutSeconds > 86400 {
		return errors.New("profile: query timeout must be between 1 and 86400 seconds")
	}
	if c.RowLimit < 1 || c.RowLimit > 100000 {
		return errors.New("profile: row limit must be between 1 and 100000")
	}
	if c.MaxColumnWidth != 0 && (c.MaxColumnWidth < 8 || c.MaxColumnWidth > 200) {
		return errors.New("profile: maximum column width must be between 8 and 200")
	}
	if strings.ContainsAny(c.DefaultSchema, "\x00\r\n") {
		return errors.New("profile: default schema contains invalid characters")
	}
	if c.DefaultSchema != strings.TrimSpace(c.DefaultSchema) {
		return errors.New("profile: default schema cannot start or end with whitespace")
	}

	switch c.Driver {
	case DriverPostgres, DriverSQLServer:
		if strings.TrimSpace(c.Host) == "" {
			return errors.New("profile: host is required")
		}
		if strings.ContainsAny(c.Host, "\x00\r\n") {
			return errors.New("profile: host contains invalid characters")
		}
		if c.Port == 0 {
			return errors.New("profile: port is required")
		}
		if strings.TrimSpace(c.Database) == "" {
			return errors.New("profile: database is required")
		}
		if strings.TrimSpace(c.Username) == "" {
			return errors.New("profile: username is required")
		}
		if net.ParseIP(c.Host) == nil && strings.Contains(c.Host, ":") {
			return errors.New("profile: invalid host")
		}
		if err := c.TLS.validate(); err != nil {
			return err
		}
	case DriverSQLite:
		if strings.TrimSpace(c.SQLitePath) == "" {
			return errors.New("profile: sqlite path is required")
		}
		if strings.ContainsRune(c.SQLitePath, '\x00') {
			return errors.New("profile: sqlite path contains invalid characters")
		}
		if filepath.Clean(c.SQLitePath) == "." {
			return errors.New("profile: sqlite path must name a database file")
		}
	default:
		return errors.New("profile: unsupported driver")
	}

	return nil
}

// NativeDefaultSchema returns the conventional schema for a new connection.
// An empty DefaultSchema on a persisted profile keeps the database user's
// native resolution rules for backward compatibility.
func NativeDefaultSchema(driver Driver) string {
	switch driver {
	case DriverPostgres:
		return "public"
	case DriverSQLServer:
		return "dbo"
	case DriverSQLite:
		return "main"
	default:
		return ""
	}
}

// EffectiveMaxColumnWidth preserves compatibility with profiles created
// before column width became configurable.
func (c Connection) EffectiveMaxColumnWidth() int {
	if c.MaxColumnWidth == 0 {
		return DefaultMaxColumnWidth
	}
	return c.MaxColumnWidth
}

func (t TLS) validate() error {
	switch t.Mode {
	case TLSModeDefault, TLSModeDisable, TLSModeRequire, TLSModeVerifyCA, TLSModeVerifyFull:
	default:
		return errors.New("profile: unsupported tls mode")
	}
	if strings.ContainsRune(t.CAFile, '\x00') || strings.ContainsRune(t.ServerName, '\x00') {
		return errors.New("profile: tls settings contain invalid characters")
	}
	return nil
}

func newID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("reading random bytes: %w", err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := make([]byte, 36)
	hex.Encode(encoded[0:8], raw[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], raw[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], raw[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], raw[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], raw[10:16])
	return string(encoded), nil
}
