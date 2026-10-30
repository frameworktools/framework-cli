// Package keyring stores tokens in the OS credential store in the format the
// R keyring package and the Python package use, so one login works for all
// three tools.
//
//   - macOS: a generic password with the service and account, holding the
//     plain token. (go-keyring's own Set adds an encoding prefix that R would
//     read as part of the token, so fw writes the entry itself.)
//   - Windows: a generic credential with target ":<service>:<account>", the
//     name R's wincred backend uses.
//   - Linux and the BSDs: Secret Service items with "service" and "username"
//     attributes.
package keyring

import (
	"errors"
	"fmt"
)

// ErrNotFound means the keyring has no entry for the service and account.
var ErrNotFound = errors.New("not found in the keyring")

// Store reads and writes secrets by service and account.
type Store interface {
	Get(service, account string) (string, error)
	Set(service, account, secret string) error
	Delete(service, account string) error
}

// System is the OS credential store.
var System Store = system{}

// checkSecret rejects values that would not survive the trip through every
// backend unchanged. Framework tokens are printable ASCII.
func checkSecret(secret string) error {
	if secret == "" {
		return errors.New("empty token")
	}
	for _, r := range secret {
		if r < 0x21 || r > 0x7e {
			return fmt.Errorf("token contains an unexpected character (code %d)", r)
		}
	}
	return nil
}

// Memory is an in-memory Store for tests.
type Memory map[string]string

func (m Memory) key(service, account string) string { return service + "\x00" + account }

// Get returns the stored secret or ErrNotFound.
func (m Memory) Get(service, account string) (string, error) {
	if v, ok := m[m.key(service, account)]; ok {
		return v, nil
	}
	return "", ErrNotFound
}

// Set stores a secret.
func (m Memory) Set(service, account, secret string) error {
	if err := checkSecret(secret); err != nil {
		return err
	}
	m[m.key(service, account)] = secret
	return nil
}

// Delete removes a secret or returns ErrNotFound.
func (m Memory) Delete(service, account string) error {
	if _, ok := m[m.key(service, account)]; !ok {
		return ErrNotFound
	}
	delete(m, m.key(service, account))
	return nil
}
