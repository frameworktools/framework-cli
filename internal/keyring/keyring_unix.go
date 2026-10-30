//go:build !darwin && !windows

package keyring

import (
	"errors"

	gokeyring "github.com/zalando/go-keyring"
)

type system struct{}

func (system) Get(service, account string) (string, error) {
	secret, err := gokeyring.Get(service, account)
	if errors.Is(err, gokeyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return secret, err
}

func (system) Set(service, account, secret string) error {
	if err := checkSecret(secret); err != nil {
		return err
	}
	return gokeyring.Set(service, account, secret)
}

func (system) Delete(service, account string) error {
	err := gokeyring.Delete(service, account)
	if errors.Is(err, gokeyring.ErrNotFound) {
		return ErrNotFound
	}
	return err
}
