//go:build !darwin && !windows

package keyring

import (
	"errors"
	"fmt"

	gokeyring "github.com/zalando/go-keyring"
	ss "github.com/zalando/go-keyring/secret_service"
)

// rSchema is the libsecret schema R's keyring package stores and searches
// with. libsecret matches a schema through the item's xdg:schema attribute, so
// R finds only items that carry it. go-keyring and Python's keyring search by
// service and username alone, so they find these items too.
const rSchema = "com.rstudio.keyring.password"

type system struct{}

func (system) Get(service, account string) (string, error) {
	secret, err := gokeyring.Get(service, account)
	if errors.Is(err, gokeyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return secret, err
}

// Set stores the secret through the Secret Service as go-keyring does, with
// R's schema attribute added.
func (system) Set(service, account, secret string) error {
	if err := checkSecret(secret); err != nil {
		return err
	}
	svc, err := ss.NewSecretService()
	if err != nil {
		return err
	}
	session, err := svc.OpenSession()
	if err != nil {
		return err
	}
	defer func() { _ = svc.Close(session) }()

	collection := svc.GetLoginCollection()
	if err := svc.Unlock(collection.Path()); err != nil {
		return err
	}
	// Remove earlier items for this entry, which may lack the schema
	// attribute, so every tool reads the new value.
	old, err := svc.SearchItems(collection, map[string]string{"service": service, "username": account})
	if err != nil {
		return err
	}
	for _, item := range old {
		if err := svc.Delete(item); err != nil {
			return err
		}
	}
	attributes := map[string]string{"service": service, "username": account, "xdg:schema": rSchema}
	return svc.CreateItem(collection, fmt.Sprintf("Password for '%s' on '%s'", account, service), attributes, ss.NewSecret(session.Path(), secret))
}

func (system) Delete(service, account string) error {
	err := gokeyring.Delete(service, account)
	if errors.Is(err, gokeyring.ErrNotFound) {
		return ErrNotFound
	}
	return err
}
