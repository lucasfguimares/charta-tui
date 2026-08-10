// Package queryhistory persists sanitized metadata about executed SQL.
package queryhistory

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	fileVersion  = 1
	DefaultLimit = 1000
	MaxLimit     = 100000
)

// Status is the terminal state of an execution.
type Status string

const (
	StatusUnknown   Status = ""
	StatusSuccess   Status = "success"
	StatusError     Status = "error"
	StatusCancelled Status = "cancelled"
)

// Entry describes one execution without connection credentials or result data.
type Entry struct {
	ID             string        `json:"id"`
	SQL            string        `json:"sql"`
	ConnectionID   string        `json:"connection_id,omitempty"`
	ConnectionName string        `json:"connection_name"`
	Database       string        `json:"database,omitempty"`
	ExecutedAt     time.Time     `json:"executed_at"`
	Duration       time.Duration `json:"duration"`
	Rows           int64         `json:"rows"`
	Status         Status        `json:"status"`
	Error          string        `json:"error,omitempty"`
}

// Filter selects history entries. Empty fields do not restrict results.
type Filter struct {
	Search       string
	ConnectionID string
	Status       Status
	From         time.Time
	To           time.Time
}

type historyFile struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

// Store is a bounded, concurrency-safe JSON history store.
type Store struct {
	path  string
	limit int
	mu    sync.RWMutex
}

// NewStore creates a history store. Invalid limits fall back to DefaultLimit.
func NewStore(path string, limit int) *Store {
	if limit < 1 || limit > MaxLimit {
		limit = DefaultLimit
	}
	return &Store{path: path, limit: limit}
}

// Add sanitizes and appends an entry, retaining only the newest configured entries.
func (s *Store) Add(entry Entry) error {
	entry.SQL = RedactSQL(strings.TrimSpace(entry.SQL))
	entry.ConnectionName = redactText(entry.ConnectionName)
	entry.Database = redactText(entry.Database)
	entry.Error = RedactSQL(entry.Error)
	if entry.ID == "" {
		entry.ID = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	if entry.ExecutedAt.IsZero() {
		entry.ExecutedAt = time.Now().UTC()
	}
	if entry.Status != StatusSuccess && entry.Status != StatusError && entry.Status != StatusCancelled {
		return errors.New("query history: invalid status")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.load()
	if err != nil {
		return err
	}
	entries = append(entries, entry)
	slices.SortFunc(entries, func(a, b Entry) int { return b.ExecutedAt.Compare(a.ExecutedAt) })
	if len(entries) > s.limit {
		entries = slices.Clone(entries[:s.limit])
	}
	return s.write(entries)
}

// List returns matching entries in reverse chronological order.
func (s *Store) List(filter Filter) ([]Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := s.load()
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(strings.TrimSpace(filter.Search))
	result := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if needle != "" && !strings.Contains(strings.ToLower(entry.SQL), needle) {
			continue
		}
		if filter.ConnectionID != "" && entry.ConnectionID != filter.ConnectionID {
			continue
		}
		if filter.Status != StatusUnknown && entry.Status != filter.Status {
			continue
		}
		if !filter.From.IsZero() && entry.ExecutedAt.Before(filter.From) {
			continue
		}
		if !filter.To.IsZero() && entry.ExecutedAt.After(filter.To) {
			continue
		}
		result = append(result, entry)
	}
	slices.SortFunc(result, func(a, b Entry) int { return b.ExecutedAt.Compare(a.ExecutedAt) })
	return result, nil
}

// Delete removes a single entry by ID.
func (s *Store) Delete(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.load()
	if err != nil {
		return false, err
	}
	filtered := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.ID != id {
			filtered = append(filtered, entry)
		}
	}
	if len(filtered) == len(entries) {
		return false, nil
	}
	return true, s.write(filtered)
}

// Clear removes every history entry while preserving the store file.
func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.write([]Entry{})
}

func (s *Store) load() ([]Entry, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading query history: %w", err)
	}
	var file historyFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("decoding query history: %w", err)
	}
	if file.Version != fileVersion {
		return nil, fmt.Errorf("query history: unsupported file version %d", file.Version)
	}
	if file.Entries == nil {
		file.Entries = []Entry{}
	}
	return file.Entries, nil
}

func (s *Store) write(entries []Entry) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating query history directory: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".history-*.json")
	if err != nil {
		return fmt.Errorf("creating temporary query history: %w", err)
	}
	temporaryPath := temporary.Name()
	isClosed := false
	defer func() {
		if !isClosed {
			_ = temporary.Close()
		}
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("securing temporary query history: %w", err)
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(historyFile{Version: fileVersion, Entries: entries}); err != nil {
		return fmt.Errorf("encoding query history: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("syncing query history: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("closing query history: %w", err)
	}
	isClosed = true
	if err := os.Rename(temporaryPath, s.path); err != nil {
		return fmt.Errorf("replacing query history: %w", err)
	}
	if err := os.Chmod(s.path, 0o600); err != nil {
		return fmt.Errorf("securing query history: %w", err)
	}
	return nil
}

var (
	urlSecretPattern        = regexp.MustCompile(`(?i)(postgres(?:ql)?|sqlserver)://([^/@:]+):([^@]*)@`)
	assignmentSecretPattern = regexp.MustCompile(`(?i)(password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key|client[_-]?secret)(\s*(?:=|:=|=>|\bto\b)\s*)(?:'([^']|'')*'|"([^"]|"")*"|[^,;\s)]+)`)
	passwordClausePattern   = regexp.MustCompile(`(?i)(\bpassword\s+)(?:'([^']|'')*'|"([^"]|"")*"|[^=,;\s)][^,;\s)]*)`)
)

// RedactSQL removes common credential literals while leaving ordinary SQL intact.
func RedactSQL(value string) string {
	value = urlSecretPattern.ReplaceAllString(value, `${1}://${2}:[REDACTED]@`)
	value = assignmentSecretPattern.ReplaceAllString(value, `${1}${2}'[REDACTED]'`)
	return passwordClausePattern.ReplaceAllString(value, `${1}'[REDACTED]'`)
}

func redactText(value string) string {
	value = urlSecretPattern.ReplaceAllString(value, `${1}://${2}:[REDACTED]@`)
	return assignmentSecretPattern.ReplaceAllString(value, `${1}${2}[REDACTED]`)
}
