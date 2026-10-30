package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnvValue(t *testing.T) {
	dir := t.TempDir()
	body := "# comment\nFW_CLOUD_URL= 'https://a.example.edu' \nFW_PROJECT_TOKEN=\"fw_abc\"\nFW_CLOUD_URL=https://second.example.edu\nEMPTY=\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"FW_CLOUD_URL":     "https://a.example.edu",
		"FW_PROJECT_TOKEN": "fw_abc",
		"EMPTY":            "",
		"MISSING":          "",
	}
	for key, want := range cases {
		if got := EnvValue(dir, key); got != want {
			t.Errorf("EnvValue(%q) = %q; want %q", key, got, want)
		}
	}
	if got := EnvValue(t.TempDir(), "FW_CLOUD_URL"); got != "" {
		t.Errorf("no .env: got %q", got)
	}
}
