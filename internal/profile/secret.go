package profile

import (
	"errors"
	"fmt"
	"sync"

	"github.com/zalando/go-keyring"
)

const keyringService = "tui-db"

var ErrSecretNotFound = errors.New("profile: secret not found")

// SecretStore provides password persistence without exposing storage details.
type SecretStore interface {
	Get(profileID string) (string, error)
	Set(profileID, secret string) error
	Delete(profileID string) error
}

// KeyringStore stores passwords in the operating system credential service.
type KeyringStore struct{}

// NewKeyringStore returns an OS keyring-backed secret store.
func NewKeyringStore() *KeyringStore {
	return &KeyringStore{}
}

// Get retrieves a profile password.
func (s *KeyringStore) Get(profileID string) (string, error) {
	secret, err := keyring.Get(keyringService, profileID)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrSecretNotFound
	}
	if err != nil {
		return "", fmt.Errorf("reading password from keyring: %w", err)
	}
	return secret, nil
}

// Set stores a profile password.
func (s *KeyringStore) Set(profileID, secret string) error {
	if err := keyring.Set(keyringService, profileID, secret); err != nil {
		return fmt.Errorf("writing password to keyring: %w", err)
	}
	return nil
}

// Delete removes a profile password.
func (s *KeyringStore) Delete(profileID string) error {
	err := keyring.Delete(keyringService, profileID)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("deleting password from keyring: %w", err)
	}
	return nil
}

// MemoryStore retains passwords for only the lifetime of the process.
type MemoryStore struct {
	mu      sync.RWMutex
	secrets map[string]string
}

// NewMemoryStore returns an initialized in-memory secret store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{secrets: map[string]string{}}
}

// Get retrieves a session password.
func (s *MemoryStore) Get(profileID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	secret, ok := s.secrets[profileID]
	if !ok {
		return "", ErrSecretNotFound
	}
	return secret, nil
}

// Set retains a password for this process.
func (s *MemoryStore) Set(profileID, secret string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.secrets[profileID] = secret
	return nil
}

// Delete forgets a session password.
func (s *MemoryStore) Delete(profileID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.secrets, profileID)
	return nil
}
