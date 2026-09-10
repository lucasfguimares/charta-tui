package profile

import (
	"errors"
	"testing"

	"github.com/zalando/go-keyring"
)

type fakeKeyring struct {
	values map[string]string
}

func (f *fakeKeyring) Get(service, user string) (string, error) {
	value, ok := f.values[service+"\x00"+user]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return value, nil
}

func (f *fakeKeyring) Set(service, user, password string) error {
	f.values[service+"\x00"+user] = password
	return nil
}

func (f *fakeKeyring) Delete(service, user string) error {
	key := service + "\x00" + user
	if _, ok := f.values[key]; !ok {
		return keyring.ErrNotFound
	}
	delete(f.values, key)
	return nil
}

func TestKeyringStoreMigratesLegacyCredential(t *testing.T) {
	t.Parallel()

	backend := &fakeKeyring{values: map[string]string{legacyKeyringService + "\x00profile": "secret"}}
	store := &KeyringStore{backend: backend}
	secret, err := store.Get("profile")
	if err != nil || secret != "secret" {
		t.Fatalf("Get() = %q, %v", secret, err)
	}
	if got := backend.values[keyringService+"\x00profile"]; got != "secret" {
		t.Fatalf("migrated credential = %q", got)
	}
	if err := store.Delete("profile"); err != nil {
		t.Fatal(err)
	}
	if len(backend.values) != 0 {
		t.Fatalf("credentials remain after Delete: %#v", backend.values)
	}
}

func TestKeyringStoreReportsMissingCredential(t *testing.T) {
	t.Parallel()
	store := &KeyringStore{backend: &fakeKeyring{values: map[string]string{}}}
	if _, err := store.Get("missing"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("Get() error = %v, want ErrSecretNotFound", err)
	}
}
