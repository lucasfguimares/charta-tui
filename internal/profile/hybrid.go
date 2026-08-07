package profile

import "errors"

// HybridStore uses the OS keyring when available and transparently falls back
// to process memory when a desktop keyring service is unavailable.
type HybridStore struct {
	persistent SecretStore
	session    *MemoryStore
}

// NewHybridStore combines a persistent secret store with session fallback.
func NewHybridStore(persistent SecretStore) *HybridStore {
	return &HybridStore{persistent: persistent, session: NewMemoryStore()}
}

// Get checks the keyring first and then the current session.
func (s *HybridStore) Get(profileID string) (string, error) {
	secret, err := s.persistent.Get(profileID)
	if err == nil {
		return secret, nil
	}
	secret, sessionErr := s.session.Get(profileID)
	if sessionErr == nil {
		return secret, nil
	}
	// A missing or unavailable desktop keyring has the same user-facing
	// recovery: ask for the password and retain it in the session store.
	return "", ErrSecretNotFound
}

// Set stores in the keyring, falling back to session memory on failure.
func (s *HybridStore) Set(profileID, secret string) error {
	if err := s.persistent.Set(profileID, secret); err == nil {
		return s.session.Set(profileID, secret)
	}
	return s.session.Set(profileID, secret)
}

// Delete removes both persistent and session copies.
func (s *HybridStore) Delete(profileID string) error {
	persistentErr := s.persistent.Delete(profileID)
	sessionErr := s.session.Delete(profileID)
	return errors.Join(persistentErr, sessionErr)
}
