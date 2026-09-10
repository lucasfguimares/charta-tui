package profile

import (
	"errors"
	"fmt"
	"sync"

	"github.com/zalando/go-keyring"
)

const (
	keyringService       = "charta"
	legacyKeyringService = "tui-db"
)

var ErrSecretNotFound = errors.New("profile: secret not found")

// SecretStore provides password persistence without exposing storage details.
type SecretStore interface {
	Get(profileID string) (string, error)
	Set(profileID, secret string) error
	Delete(profileID string) error
}

type keyringBackend interface {
	Get(service, user string) (string, error)
	Set(service, user, password string) error
	Delete(service, user string) error
}

type systemKeyring struct{}

func (systemKeyring) Get(service, user string) (string, error) {
	return keyring.Get(service, user)
}
func (systemKeyring) Set(service, user, password string) error {
	return keyring.Set(service, user, password)
}
func (systemKeyring) Delete(service, user string) error {
	return keyring.Delete(service, user)
}

// KeyringStore stores passwords in the operating system credential service.
// Reads transparently migrate credentials written by pre-Charta builds.
type KeyringStore struct{ backend keyringBackend }

// NewKeyringStore returns an OS keyring-backed secret store.
func NewKeyringStore() *KeyringStore {
	return &KeyringStore{backend: systemKeyring{}}
}

// Get retrieves a profile password.
func (s *KeyringStore) Get(profileID string) (string, error) {
	secret, err := s.backend.Get(keyringService, profileID)
	if err == nil {
		return secret, nil
	}
	if !errors.Is(err, keyring.ErrNotFound) {
		return "", fmt.Errorf("reading password from keyring: %w", err)
	}
	secret, err = s.backend.Get(legacyKeyringService, profileID)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrSecretNotFound
	}
	if err != nil {
		return "", fmt.Errorf("reading password from keyring: %w", err)
	}
	// Credential migration is best effort: a readable legacy secret should
	// still be usable when the keyring is temporarily read-only.
	_ = s.backend.Set(keyringService, profileID, secret)
	return secret, nil
}

// Set stores a profile password.
func (s *KeyringStore) Set(profileID, secret string) error {
	if err := s.backend.Set(keyringService, profileID, secret); err != nil {
		return fmt.Errorf("writing password to keyring: %w", err)
	}
	return nil
}

// Delete removes a profile password.
func (s *KeyringStore) Delete(profileID string) error {
	currentErr := ignoreMissingKeyringSecret(s.backend.Delete(keyringService, profileID))
	legacyErr := ignoreMissingKeyringSecret(s.backend.Delete(legacyKeyringService, profileID))
	if err := errors.Join(currentErr, legacyErr); err != nil {
		return fmt.Errorf("deleting password from keyring: %w", err)
	}
	return nil
}

func ignoreMissingKeyringSecret(err error) error {
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
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
