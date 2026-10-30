package cloud

import "testing"

func TestNormalize(t *testing.T) {
	ok := map[string]string{
		"https://framework.pub":           "https://framework.pub",
		"https://framework.pub/":          "https://framework.pub",
		"  framework.example.edu  ":       "https://framework.example.edu",
		"http://localhost:8080":           "http://localhost:8080",
		"http://127.0.0.1":                "http://127.0.0.1",
		"http://[::1]:8000":               "http://[::1]:8000",
		"HTTPS://Framework.Example.edu//": "HTTPS://Framework.Example.edu",
	}
	for in, want := range ok {
		got, err := Normalize(in)
		if err != nil || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got, err, want)
		}
	}

	bad := []string{
		"http://framework.example.edu",
		"ftp://framework.pub",
		"https://",
		"",
	}
	for _, in := range bad {
		if got, err := Normalize(in); err == nil {
			t.Errorf("Normalize(%q) = %q; want an error", in, got)
		}
	}
}

func TestHostAndHostname(t *testing.T) {
	cases := []struct{ url, host, hostname string }{
		{"https://framework.pub", "framework.pub", "framework.pub"},
		{"http://localhost:8080/api", "localhost:8080", "localhost"},
		{"https://user@Example.EDU:8443?x", "example.edu:8443", "example.edu"},
		{"http://[::1]:8000", "[::1]:8000", "[::1]"},
	}
	for _, c := range cases {
		if got := Host(c.url); got != c.host {
			t.Errorf("Host(%q) = %q; want %q", c.url, got, c.host)
		}
		if got := Hostname(c.url); got != c.hostname {
			t.Errorf("Hostname(%q) = %q; want %q", c.url, got, c.hostname)
		}
	}
}

func TestTokenNames(t *testing.T) {
	cases := []struct{ url, file, service string }{
		{"https://framework.pub", "cloud-token", "framework-cloud:framework.pub"},
		{"http://localhost:8080", "cloud-token-localhost_8080", "framework-cloud:localhost:8080"},
		{"https://Framework.Example.edu", "cloud-token-framework.example.edu", "framework-cloud:framework.example.edu"},
		{"http://[::1]:8000", "cloud-token-___1__8000", "framework-cloud:[::1]:8000"},
	}
	for _, c := range cases {
		if got := TokenFileName(c.url); got != c.file {
			t.Errorf("TokenFileName(%q) = %q; want %q", c.url, got, c.file)
		}
		if got := KeyringService(c.url); got != c.service {
			t.Errorf("KeyringService(%q) = %q; want %q", c.url, got, c.service)
		}
	}
}
