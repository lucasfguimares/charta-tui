package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

const fileVersion = 1

type profileFile struct {
	Version     int          `json:"version"`
	Connections []Connection `json:"connections"`
}

// Store persists connection metadata as an atomically replaced JSON document.
type Store struct {
	path string
	mu   sync.RWMutex
}

// NewStore creates a profile store at path.
func NewStore(path string) *Store {
	return &Store{path: path}
}

// Path returns the backing JSON file path.
func (s *Store) Path() string {
	return s.path
}

// List loads and validates all saved profiles.
func (s *Store) List() ([]Connection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	connections, err := s.load()
	if err != nil {
		return nil, err
	}
	return slices.Clone(connections), nil
}

// Save creates or replaces a profile by stable ID.
func (s *Store) Save(connection Connection) error {
	if err := connection.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	connections, err := s.load()
	if err != nil {
		return err
	}
	for _, existing := range connections {
		isSameName := strings.EqualFold(strings.TrimSpace(existing.Name), strings.TrimSpace(connection.Name))
		if isSameName && existing.ID != connection.ID {
			return fmt.Errorf("profile: name %q already exists", connection.Name)
		}
	}

	isReplaced := false
	for i := range connections {
		if connections[i].ID == connection.ID {
			connections[i] = connection
			isReplaced = true
			break
		}
	}
	if !isReplaced {
		connections = append(connections, connection)
	}

	slices.SortFunc(connections, func(a, b Connection) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return s.write(connections)
}

// Delete removes a profile by ID. It reports whether a profile existed.
func (s *Store) Delete(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	connections, err := s.load()
	if err != nil {
		return false, err
	}
	filtered := make([]Connection, 0, len(connections))
	for _, connection := range connections {
		if connection.ID != id {
			filtered = append(filtered, connection)
		}
	}
	if len(filtered) == len(connections) {
		return false, nil
	}
	return true, s.write(filtered)
}

func (s *Store) load() ([]Connection, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return []Connection{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading profiles: %w", err)
	}

	var file profileFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("decoding profiles: %w", err)
	}
	if file.Version != fileVersion {
		return nil, fmt.Errorf("profile: unsupported file version %d", file.Version)
	}
	if file.Connections == nil {
		file.Connections = []Connection{}
	}
	for index := range file.Connections {
		if file.Connections[index].MaxColumnWidth == 0 {
			file.Connections[index].MaxColumnWidth = DefaultMaxColumnWidth
		}
		if err := file.Connections[index].Validate(); err != nil {
			return nil, fmt.Errorf("validating profile %q: %w", file.Connections[index].Name, err)
		}
	}
	return file.Connections, nil
}

func (s *Store) write(connections []Connection) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating profile directory: %w", err)
	}

	temporary, err := os.CreateTemp(dir, ".connections-*.json")
	if err != nil {
		return fmt.Errorf("creating temporary profile file: %w", err)
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
		return fmt.Errorf("securing temporary profile file: %w", err)
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(profileFile{Version: fileVersion, Connections: connections}); err != nil {
		return fmt.Errorf("encoding profiles: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("syncing profiles: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("closing profiles: %w", err)
	}
	isClosed = true
	if err := os.Rename(temporaryPath, s.path); err != nil {
		return fmt.Errorf("replacing profiles: %w", err)
	}
	if err := os.Chmod(s.path, 0o600); err != nil {
		return fmt.Errorf("securing profiles: %w", err)
	}
	return nil
}
