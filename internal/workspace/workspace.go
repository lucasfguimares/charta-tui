// Package workspace persists a snapshot of open editor tabs between sessions.
package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	fileVersion = 1
	// MaxTabs is the maximum number of tabs persisted; extra tabs are dropped.
	MaxTabs = 50
	// MaxSQLBytes is the maximum editor text persisted per tab; longer text is truncated.
	MaxSQLBytes = 1 << 20
)

// ErrNotOwner reports that another process holds the session lock.
var ErrNotOwner = errors.New("workspace: session is owned by another instance")

// Position is a 1-based editor line and column.
type Position struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

// Tab is the persisted state of one editor tab. It never holds results,
// credentials, or trust decisions.
type Tab struct {
	ConnectionID string    `json:"connection_id,omitempty"`
	SQL          string    `json:"sql"`
	Cursor       Position  `json:"cursor"`
	Selection    *Position `json:"selection,omitempty"`
}

// Snapshot is the ordered tab list and the active tab index.
type Snapshot struct {
	Tabs      []Tab `json:"tabs"`
	ActiveTab int   `json:"active_tab"`
}

// LoadResult carries the loaded snapshot and, when the previous file was
// unreadable, the path it was preserved under.
type LoadResult struct {
	Snapshot      Snapshot
	RecoveredPath string
}

// SaveReport describes limits applied while saving.
type SaveReport struct {
	TruncatedTabs int
	DroppedTabs   int
}

type workspaceFile struct {
	Version   int   `json:"version"`
	Tabs      []Tab `json:"tabs"`
	ActiveTab int   `json:"active_tab"`
}

// Store persists one session snapshot guarded by an advisory process lock.
type Store struct {
	path       string
	lock       *os.File
	isOwner    bool
	isDisabled bool
	lastSeq    uint64
	mu         sync.RWMutex
}

// Open creates a store at path and tries to acquire the session lock. Failing
// to acquire the lock is not an error: the store then loads but never saves.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("creating workspace directory: %w", err)
	}
	lock, err := os.OpenFile(path+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening workspace lock: %w", err)
	}
	store := &Store{path: path}
	if err := lockFile(lock); err != nil {
		_ = lock.Close()
		return store, nil
	}
	store.lock, store.isOwner = lock, true
	return store, nil
}

// IsOwner reports whether this process holds the session lock.
func (s *Store) IsOwner() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isOwner && !s.isDisabled
}

// Load reads the snapshot. An unreadable file or unknown version is renamed to
// workspace.json.corrupt-<timestamp> by the owner and an empty snapshot is returned.
func (s *Store) Load() (LoadResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return LoadResult{Snapshot: Snapshot{Tabs: []Tab{}}}, nil
	}
	if err != nil {
		return LoadResult{}, fmt.Errorf("reading workspace: %w", err)
	}
	var file workspaceFile
	if err := json.Unmarshal(data, &file); err == nil && file.Version == fileVersion {
		if file.Tabs == nil {
			file.Tabs = []Tab{}
		}
		return LoadResult{Snapshot: Snapshot{Tabs: file.Tabs, ActiveTab: file.ActiveTab}}, nil
	}
	if !s.isOwner {
		return LoadResult{Snapshot: Snapshot{Tabs: []Tab{}}}, nil
	}
	recovered := fmt.Sprintf("%s.corrupt-%s", s.path, time.Now().UTC().Format("20060102T150405.000000000Z"))
	if err := os.Rename(s.path, recovered); err != nil {
		s.isDisabled = true
		return LoadResult{}, fmt.Errorf("preserving unreadable workspace: %w", err)
	}
	return LoadResult{Snapshot: Snapshot{Tabs: []Tab{}}, RecoveredPath: recovered}, nil
}

// Save atomically replaces the snapshot. Saves with a sequence number not
// greater than the last saved one are ignored so stale writes never win.
func (s *Store) Save(seq uint64, snapshot Snapshot) (SaveReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.isOwner || s.isDisabled {
		return SaveReport{}, ErrNotOwner
	}
	if seq <= s.lastSeq {
		return SaveReport{}, nil
	}
	file, report := limit(snapshot)
	if err := s.write(file); err != nil {
		return report, err
	}
	s.lastSeq = seq
	return report, nil
}

// Close releases the session lock.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return nil
	}
	err := errors.Join(unlockFile(s.lock), s.lock.Close())
	s.lock, s.isOwner = nil, false
	if err != nil {
		return fmt.Errorf("releasing workspace lock: %w", err)
	}
	return nil
}

func limit(snapshot Snapshot) (workspaceFile, SaveReport) {
	report := SaveReport{}
	tabs := snapshot.Tabs
	if len(tabs) > MaxTabs {
		report.DroppedTabs = len(tabs) - MaxTabs
		tabs = tabs[:MaxTabs]
	}
	limited := make([]Tab, len(tabs))
	for i, tab := range tabs {
		if len(tab.SQL) > MaxSQLBytes {
			tab.SQL = truncateUTF8(tab.SQL, MaxSQLBytes)
			report.TruncatedTabs++
		}
		limited[i] = tab
	}
	active := snapshot.ActiveTab
	if active >= len(limited) {
		active = len(limited) - 1
	}
	if active < 0 {
		active = 0
	}
	return workspaceFile{Version: fileVersion, Tabs: limited, ActiveTab: active}, report
}

func truncateUTF8(value string, size int) string {
	value = value[:size]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func (s *Store) write(file workspaceFile) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating workspace directory: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".workspace-*.json")
	if err != nil {
		return fmt.Errorf("creating temporary workspace: %w", err)
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
		return fmt.Errorf("securing temporary workspace: %w", err)
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(file); err != nil {
		return fmt.Errorf("encoding workspace: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("syncing workspace: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("closing workspace: %w", err)
	}
	isClosed = true
	if err := os.Rename(temporaryPath, s.path); err != nil {
		return fmt.Errorf("replacing workspace: %w", err)
	}
	if err := os.Chmod(s.path, 0o600); err != nil {
		return fmt.Errorf("securing workspace: %w", err)
	}
	return nil
}
