package keyring

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

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

// Set writes the plain token through `security -i`, reading the command from
// stdin so the token never appears in the process list.
func (system) Set(service, account, secret string) error {
	if err := checkSecret(secret); err != nil {
		return err
	}
	if strings.ContainsAny(service+account+secret, `'\`) {
		return errors.New("token contains a quote or backslash")
	}
	cmd := exec.Command("/usr/bin/security", "-i")
	cmd.Stdin = strings.NewReader(fmt.Sprintf("add-generic-password -U -s '%s' -a '%s' -w '%s'\n", service, account, secret))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("macOS keychain: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (system) Delete(service, account string) error {
	err := gokeyring.Delete(service, account)
	if errors.Is(err, gokeyring.ErrNotFound) {
		return ErrNotFound
	}
	return err
}
