// Package cloud talks to Framework Cloud (framework.pub or a self-hosted
// server). Server and token rules match the R package's R/cloud.R.
package cloud

import (
	"fmt"
	"regexp"
	"strings"
)

// DefaultURL is the server used when nothing else names one.
const DefaultURL = "https://framework.pub"

var schemePrefix = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://`)

// Normalize validates and tidies a server URL. A bare host gets https://.
// Plain http is allowed only for localhost, 127.0.0.1, and [::1].
func Normalize(raw string) (string, error) {
	url := strings.TrimSpace(raw)
	if !schemePrefix.MatchString(url) {
		url = "https://" + url
	}
	sep := strings.Index(url, "://")
	scheme := strings.ToLower(url[:sep])
	url = url[:sep+3] + strings.TrimRight(url[sep+3:], "/")
	host := Hostname(url)
	if host == "" {
		return "", fmt.Errorf("not a server URL: %s", raw)
	}
	switch scheme {
	case "https":
	case "http":
		if host != "localhost" && host != "127.0.0.1" && host != "[::1]" {
			return "", fmt.Errorf("refusing plain http for %s: use https, or http only for localhost and 127.0.0.1 (single-user mode)", host)
		}
	default:
		return "", fmt.Errorf("server URLs must start with https:// (got %s://)", scheme)
	}
	return url, nil
}

// authority returns the URL's user info, host, and port, lowercased, without
// the user info.
func authority(url string) string {
	a := schemePrefix.ReplaceAllString(url, "")
	if i := strings.IndexAny(a, "/?#"); i >= 0 {
		a = a[:i]
	}
	if i := strings.LastIndex(a, "@"); i >= 0 {
		a = a[i+1:]
	}
	return strings.ToLower(a)
}

// Hostname returns the host without scheme, port, path, or user info.
func Hostname(url string) string {
	a := authority(url)
	if strings.HasPrefix(a, "[") {
		if i := strings.Index(a, "]"); i >= 0 {
			return a[:i+1]
		}
		return a
	}
	if i := strings.Index(a, ":"); i >= 0 {
		return a[:i]
	}
	return a
}

// Host returns host[:port], the name that keys a server's stored token.
func Host(url string) string {
	return authority(url)
}

// IsDefault reports whether url is framework.pub.
func IsDefault(url string) bool {
	return Host(url) == Host(DefaultURL)
}
