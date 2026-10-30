package keyring

import (
	"errors"

	"github.com/danieljoos/wincred"
	"golang.org/x/sys/windows"
)

type system struct{}

// target is the Credential Manager name R's keyring package uses.
func target(service, account string) string {
	return ":" + service + ":" + account
}

func (system) Get(service, account string) (string, error) {
	cred, err := wincred.GetGenericCredential(target(service, account))
	if errors.Is(err, windows.ERROR_NOT_FOUND) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return decodeBlob(cred.CredentialBlob), nil
}

func (system) Set(service, account, secret string) error {
	if err := checkSecret(secret); err != nil {
		return err
	}
	cred := wincred.NewGenericCredential(target(service, account))
	cred.UserName = account
	cred.CredentialBlob = []byte(secret)
	cred.Persist = wincred.PersistLocalMachine
	return cred.Write()
}

func (system) Delete(service, account string) error {
	cred, err := wincred.GetGenericCredential(target(service, account))
	if errors.Is(err, windows.ERROR_NOT_FOUND) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return cred.Delete()
}
