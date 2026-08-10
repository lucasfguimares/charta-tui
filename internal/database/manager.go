// Package database manages SQL sessions, metadata inspection, and query execution.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lucasfguimares/tui-db/internal/profile"
)

// SecretReader resolves the password for a connection profile.
type SecretReader interface {
	Get(profileID string) (string, error)
}

type session struct {
	profile profile.Connection
	db      *sql.DB
}

// Manager lazily opens and reuses bounded connection pools.
type Manager struct {
	secrets  SecretReader
	mu       sync.Mutex
	sessions map[string]session
}

// NewManager creates an empty session manager.
func NewManager(secrets SecretReader) *Manager {
	return &Manager{
		secrets:  secrets,
		sessions: map[string]session{},
	}
}

// Connection returns a tested pool, opening it on first use.
func (m *Manager) Connection(ctx context.Context, connection profile.Connection) (*sql.DB, error) {
	if err := connection.Validate(); err != nil {
		return nil, err
	}

	m.mu.Lock()
	if existing, ok := m.sessions[connection.ID]; ok && existing.profile == connection {
		db := existing.db
		m.mu.Unlock()
		return db, nil
	}
	if existing, ok := m.sessions[connection.ID]; ok {
		delete(m.sessions, connection.ID)
		_ = existing.db.Close()
	}
	m.mu.Unlock()

	driverName, dataSource, err := m.dataSource(connection)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(driverName, dataSource)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	configurePool(db, connection.Driver)

	pingTimeout := 10 * time.Second
	configuredTimeout := time.Duration(connection.QueryTimeoutSeconds) * time.Second
	if configuredTimeout < pingTimeout {
		pingTimeout = configuredTimeout
	}
	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connecting to database: %w", err)
	}

	m.mu.Lock()
	if existing, ok := m.sessions[connection.ID]; ok {
		m.mu.Unlock()
		_ = db.Close()
		return existing.db, nil
	}
	m.sessions[connection.ID] = session{profile: connection, db: db}
	m.mu.Unlock()
	return db, nil
}

// Disconnect closes one profile's pool.
func (m *Manager) Disconnect(profileID string) error {
	m.mu.Lock()
	existing, ok := m.sessions[profileID]
	if ok {
		delete(m.sessions, profileID)
	}
	m.mu.Unlock()
	if !ok {
		return nil
	}
	if err := existing.db.Close(); err != nil {
		return fmt.Errorf("closing database: %w", err)
	}
	return nil
}

// Close closes every active connection pool.
func (m *Manager) Close() error {
	m.mu.Lock()
	sessions := make([]session, 0, len(m.sessions))
	for _, existing := range m.sessions {
		sessions = append(sessions, existing)
	}
	m.sessions = map[string]session{}
	m.mu.Unlock()

	errs := []error{}
	for _, existing := range sessions {
		if err := existing.db.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing %q: %w", existing.profile.Name, err))
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) dataSource(connection profile.Connection) (string, string, error) {
	if connection.Driver == profile.DriverSQLite {
		path := filepath.ToSlash(connection.SQLitePath)
		query := url.Values{}
		if connection.SQLiteReadOnly {
			query.Set("mode", "ro")
		}
		dsn := "file:" + path
		if encoded := query.Encode(); encoded != "" {
			dsn += "?" + encoded
		}
		return "sqlite3", dsn, nil
	}

	password, err := m.secrets.Get(connection.ID)
	if err != nil {
		return "", "", err
	}
	host := net.JoinHostPort(connection.Host, fmt.Sprintf("%d", connection.Port))

	switch connection.Driver {
	case profile.DriverPostgres:
		query := url.Values{}
		query.Set("sslmode", postgresTLSMode(connection.TLS.Mode))
		if connection.DefaultSchema != "" {
			query.Set("search_path", postgresSearchPath(connection.DefaultSchema))
		}
		if connection.TLS.CAFile != "" {
			query.Set("sslrootcert", connection.TLS.CAFile)
		}
		u := url.URL{
			Scheme:   "postgres",
			User:     url.UserPassword(connection.Username, password),
			Host:     host,
			Path:     connection.Database,
			RawQuery: query.Encode(),
		}
		return "pgx", u.String(), nil
	case profile.DriverSQLServer:
		query := url.Values{}
		query.Set("database", connection.Database)
		query.Set("app name", "tui-db")
		applySQLServerTLS(query, connection.TLS)
		u := url.URL{
			Scheme:   "sqlserver",
			User:     url.UserPassword(connection.Username, password),
			Host:     host,
			RawQuery: query.Encode(),
		}
		return "sqlserver", u.String(), nil
	default:
		return "", "", errors.New("database: unsupported driver")
	}
}

func postgresSearchPath(schema string) string {
	quoted := `"` + strings.ReplaceAll(schema, `"`, `""`) + `"`
	if schema == "public" {
		return quoted
	}
	return quoted + ", public"
}

func configurePool(db *sql.DB, driver profile.Driver) {
	if driver == profile.DriverSQLite {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		return
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxIdleTime(5 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)
}

func postgresTLSMode(mode profile.TLSMode) string {
	switch mode {
	case profile.TLSModeDisable:
		return "disable"
	case profile.TLSModeRequire:
		return "require"
	case profile.TLSModeVerifyCA:
		return "verify-ca"
	case profile.TLSModeDefault, profile.TLSModeVerifyFull:
		return "verify-full"
	default:
		return "verify-full"
	}
}

func applySQLServerTLS(query url.Values, tls profile.TLS) {
	switch tls.Mode {
	case profile.TLSModeDisable:
		query.Set("encrypt", "disable")
	case profile.TLSModeRequire:
		query.Set("encrypt", "true")
		query.Set("TrustServerCertificate", "true")
	default:
		query.Set("encrypt", "true")
		query.Set("TrustServerCertificate", "false")
	}
	if strings.TrimSpace(tls.CAFile) != "" {
		query.Set("certificate", tls.CAFile)
	}
	if strings.TrimSpace(tls.ServerName) != "" {
		query.Set("hostNameInCertificate", tls.ServerName)
	}
}
