// Package querylibrary persists favorite queries and user SQL snippets.
package querylibrary

import (
	"crypto/rand"
	"encoding/hex"
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

const fileVersion = 1

// Favorite is a named reusable query, independent from query history.
type Favorite struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description,omitempty"`
	SQL          string    `json:"sql"`
	ConnectionID string    `json:"connection_id,omitempty"`
	Tags         []string  `json:"tags,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Snippet maps a short trigger to reusable SQL with optional placeholders.
type Snippet struct {
	ID          string    `json:"id"`
	Trigger     string    `json:"trigger"`
	Description string    `json:"description,omitempty"`
	Body        string    `json:"body"`
	IsBuiltIn   bool      `json:"-"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Placeholder identifies a navigable placeholder after snippet expansion.
type Placeholder struct {
	Name  string
	Start int
	End   int
}

type libraryFile struct {
	Version   int        `json:"version"`
	Favorites []Favorite `json:"favorites"`
	Snippets  []Snippet  `json:"snippets"`
}

// Store persists user-owned favorites and snippets in one atomic JSON document.
type Store struct {
	path string
	mu   sync.RWMutex
}

// NewStore creates a SQL library store at path.
func NewStore(path string) *Store { return &Store{path: path} }

// Favorites returns favorites ordered by name and optionally filtered by text.
func (s *Store) Favorites(search string) ([]Favorite, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	file, err := s.load()
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(strings.TrimSpace(search))
	result := make([]Favorite, 0, len(file.Favorites))
	for _, favorite := range file.Favorites {
		haystack := favorite.Name + "\n" + favorite.Description + "\n" + favorite.SQL + "\n" + strings.Join(favorite.Tags, " ")
		if needle == "" || strings.Contains(strings.ToLower(haystack), needle) {
			result = append(result, favorite)
		}
	}
	slices.SortFunc(result, func(a, b Favorite) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return result, nil
}

// SaveFavorite creates or updates a favorite.
func (s *Store) SaveFavorite(favorite Favorite) (Favorite, error) {
	favorite.Name = strings.TrimSpace(favorite.Name)
	favorite.Description = strings.TrimSpace(favorite.Description)
	favorite.SQL = strings.TrimSpace(favorite.SQL)
	favorite.Tags = normalizeTags(favorite.Tags)
	if favorite.Name == "" || favorite.SQL == "" {
		return Favorite{}, errors.New("sql library: favorite name and sql are required")
	}
	now := time.Now().UTC()
	if favorite.ID == "" {
		id, err := newID()
		if err != nil {
			return Favorite{}, fmt.Errorf("generating favorite id: %w", err)
		}
		favorite.ID, favorite.CreatedAt = id, now
	}
	if favorite.CreatedAt.IsZero() {
		favorite.CreatedAt = now
	}
	favorite.UpdatedAt = now

	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.load()
	if err != nil {
		return Favorite{}, err
	}
	replaced := false
	for i := range file.Favorites {
		if file.Favorites[i].ID == favorite.ID {
			file.Favorites[i] = favorite
			replaced = true
			break
		}
	}
	if !replaced {
		file.Favorites = append(file.Favorites, favorite)
	}
	if err := s.write(file); err != nil {
		return Favorite{}, err
	}
	return favorite, nil
}

// DeleteFavorite removes a favorite by ID.
func (s *Store) DeleteFavorite(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.load()
	if err != nil {
		return false, err
	}
	before := len(file.Favorites)
	file.Favorites = slices.DeleteFunc(file.Favorites, func(value Favorite) bool { return value.ID == id })
	if len(file.Favorites) == before {
		return false, nil
	}
	return true, s.write(file)
}

// Snippets returns built-in and custom snippets ordered by trigger.
func (s *Store) Snippets(search string) ([]Snippet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	file, err := s.load()
	if err != nil {
		return nil, err
	}
	result := append(BuiltInSnippets(), file.Snippets...)
	needle := strings.ToLower(strings.TrimSpace(search))
	result = slices.DeleteFunc(result, func(snippet Snippet) bool {
		return needle != "" && !strings.Contains(strings.ToLower(snippet.Trigger+"\n"+snippet.Description+"\n"+snippet.Body), needle)
	})
	slices.SortFunc(result, func(a, b Snippet) int { return strings.Compare(strings.ToLower(a.Trigger), strings.ToLower(b.Trigger)) })
	return result, nil
}

// SaveSnippet creates or updates a custom snippet.
func (s *Store) SaveSnippet(snippet Snippet) (Snippet, error) {
	snippet.Trigger = strings.ToLower(strings.TrimSpace(snippet.Trigger))
	snippet.Description = strings.TrimSpace(snippet.Description)
	snippet.Body = strings.TrimSpace(snippet.Body)
	if !validTrigger.MatchString(snippet.Trigger) || snippet.Body == "" {
		return Snippet{}, errors.New("sql library: snippet trigger must contain only letters, digits, dash or underscore and body is required")
	}
	for _, builtIn := range BuiltInSnippets() {
		if builtIn.Trigger == snippet.Trigger {
			return Snippet{}, fmt.Errorf("sql library: trigger %q is reserved by a built-in snippet", snippet.Trigger)
		}
	}
	now := time.Now().UTC()
	if snippet.ID == "" {
		id, err := newID()
		if err != nil {
			return Snippet{}, fmt.Errorf("generating snippet id: %w", err)
		}
		snippet.ID, snippet.CreatedAt = id, now
	}
	if snippet.CreatedAt.IsZero() {
		snippet.CreatedAt = now
	}
	snippet.UpdatedAt, snippet.IsBuiltIn = now, false

	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.load()
	if err != nil {
		return Snippet{}, err
	}
	for _, current := range file.Snippets {
		if current.Trigger == snippet.Trigger && current.ID != snippet.ID {
			return Snippet{}, fmt.Errorf("sql library: snippet trigger %q already exists", snippet.Trigger)
		}
	}
	replaced := false
	for i := range file.Snippets {
		if file.Snippets[i].ID == snippet.ID {
			file.Snippets[i] = snippet
			replaced = true
			break
		}
	}
	if !replaced {
		file.Snippets = append(file.Snippets, snippet)
	}
	if err := s.write(file); err != nil {
		return Snippet{}, err
	}
	return snippet, nil
}

// DeleteSnippet removes a custom snippet. Built-ins are not persisted or removable.
func (s *Store) DeleteSnippet(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.load()
	if err != nil {
		return false, err
	}
	before := len(file.Snippets)
	file.Snippets = slices.DeleteFunc(file.Snippets, func(value Snippet) bool { return value.ID == id })
	if len(file.Snippets) == before {
		return false, nil
	}
	return true, s.write(file)
}

// Expand replaces the trigger immediately before cursor and returns placeholder ranges.
func Expand(value string, cursor int, snippets []Snippet) (string, []Placeholder, bool) {
	if cursor < 0 || cursor > len(value) {
		return value, nil, false
	}
	start := cursor
	for start > 0 && isTriggerByte(value[start-1]) {
		start--
	}
	trigger := strings.ToLower(value[start:cursor])
	if trigger == "" {
		return value, nil, false
	}
	for _, snippet := range snippets {
		if strings.EqualFold(snippet.Trigger, trigger) {
			body, placeholders := parsePlaceholders(snippet.Body, start)
			return value[:start] + body + value[cursor:], placeholders, true
		}
	}
	return value, nil, false
}

// Insert inserts one snippet body at cursor without requiring its trigger to be typed.
func Insert(value string, cursor int, snippet Snippet) (string, []Placeholder) {
	if cursor < 0 || cursor > len(value) {
		return value, nil
	}
	body, placeholders := parsePlaceholders(snippet.Body, cursor)
	return value[:cursor] + body + value[cursor:], placeholders
}

// BuiltInSnippets returns a fresh copy of the default SQL snippet catalog.
func BuiltInSnippets() []Snippet {
	return []Snippet{
		{ID: "builtin-sel", Trigger: "sel", Description: "SELECT query", Body: "SELECT\n    ${columns}\nFROM ${table};", IsBuiltIn: true},
		{ID: "builtin-cte", Trigger: "cte", Description: "common table expression", Body: "WITH dados AS (\n    SELECT\n        ${columns}\n    FROM ${table}\n)\nSELECT\n    *\nFROM dados;", IsBuiltIn: true},
		{ID: "builtin-join", Trigger: "join", Description: "JOIN clause", Body: "JOIN ${table} AS ${alias}\n    ON ${condition}", IsBuiltIn: true},
		{ID: "builtin-insert", Trigger: "insert", Description: "INSERT statement", Body: "INSERT INTO ${table} (${columns})\nVALUES (${values});", IsBuiltIn: true},
		{ID: "builtin-update", Trigger: "update", Description: "UPDATE statement", Body: "UPDATE ${table}\nSET ${column} = ${value}\nWHERE ${condition};", IsBuiltIn: true},
		{ID: "builtin-delete", Trigger: "delete", Description: "DELETE statement", Body: "DELETE FROM ${table}\nWHERE ${condition};", IsBuiltIn: true},
		{ID: "builtin-case", Trigger: "case", Description: "CASE expression", Body: "CASE\n    WHEN ${condition} THEN ${value}\n    ELSE ${fallback}\nEND", IsBuiltIn: true},
		{ID: "builtin-exists", Trigger: "exists", Description: "EXISTS predicate", Body: "EXISTS (\n    SELECT 1\n    FROM ${table}\n    WHERE ${condition}\n)", IsBuiltIn: true},
		{ID: "builtin-group", Trigger: "group", Description: "GROUP BY clause", Body: "GROUP BY ${columns}\nHAVING ${condition}", IsBuiltIn: true},
	}
}

func (s *Store) load() (libraryFile, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return libraryFile{Version: fileVersion, Favorites: []Favorite{}, Snippets: []Snippet{}}, nil
	}
	if err != nil {
		return libraryFile{}, fmt.Errorf("reading sql library: %w", err)
	}
	var file libraryFile
	if err := json.Unmarshal(data, &file); err != nil {
		return libraryFile{}, fmt.Errorf("decoding sql library: %w", err)
	}
	if file.Version != fileVersion {
		return libraryFile{}, fmt.Errorf("sql library: unsupported file version %d", file.Version)
	}
	if file.Favorites == nil {
		file.Favorites = []Favorite{}
	}
	if file.Snippets == nil {
		file.Snippets = []Snippet{}
	}
	return file, nil
}

func (s *Store) write(file libraryFile) error {
	file.Version = fileVersion
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating sql library directory: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".library-*.json")
	if err != nil {
		return fmt.Errorf("creating temporary sql library: %w", err)
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
		return fmt.Errorf("securing temporary sql library: %w", err)
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(file); err != nil {
		return fmt.Errorf("encoding sql library: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("syncing sql library: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("closing sql library: %w", err)
	}
	isClosed = true
	if err := os.Rename(temporaryPath, s.path); err != nil {
		return fmt.Errorf("replacing sql library: %w", err)
	}
	if err := os.Chmod(s.path, 0o600); err != nil {
		return fmt.Errorf("securing sql library: %w", err)
	}
	return nil
}

var (
	validTrigger       = regexp.MustCompile(`^[a-z0-9_-]+$`)
	placeholderPattern = regexp.MustCompile(`\$\{([A-Za-z][A-Za-z0-9_-]*)\}`)
)

func parsePlaceholders(body string, offset int) (string, []Placeholder) {
	matches := placeholderPattern.FindAllStringSubmatchIndex(body, -1)
	placeholders := make([]Placeholder, 0, len(matches))
	for _, match := range matches {
		placeholders = append(placeholders, Placeholder{Name: body[match[2]:match[3]], Start: offset + match[0], End: offset + match[1]})
	}
	return body, placeholders
}

func isTriggerByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_' || value == '-'
}

func normalizeTags(tags []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag != "" && !seen[tag] {
			seen[tag] = true
			result = append(result, tag)
		}
	}
	slices.Sort(result)
	return result
}

func newID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
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
