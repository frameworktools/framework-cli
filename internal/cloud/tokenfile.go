package cloud

import "regexp"

var unsafeHostChars = regexp.MustCompile(`[^a-z0-9.-]`)

// TokenFileName is the token file for a server on machines without a
// keyring: cloud-token for framework.pub, cloud-token-<host> otherwise.
func TokenFileName(url string) string {
	if IsDefault(url) {
		return "cloud-token"
	}
	return "cloud-token-" + unsafeHostChars.ReplaceAllString(Host(url), "_")
}

// KeyringService is the keyring service that holds a server's user token.
// The account is always "token".
func KeyringService(url string) string {
	return "framework-cloud:" + Host(url)
}
