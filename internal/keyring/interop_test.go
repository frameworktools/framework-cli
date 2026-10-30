//go:build interop

package keyring

// Checks that fw, R's keyring package, and Python's keyring library read each
// other's tokens from the system keyring. CI runs it on Linux with a real
// Secret Service:
//
//	go test -tags interop ./internal/keyring -run Interop -v
//
// It needs Rscript with the keyring package and python3 with keyring.

import (
	"os/exec"
	"strings"
	"testing"
)

const interopService = "framework-cloud:interop.invalid"

func rGet(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("Rscript", "-e", `cat(keyring::key_get("`+interopService+`", "token"))`).CombinedOutput()
	if err != nil {
		t.Fatalf("R key_get: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func rSet(t *testing.T, v string) {
	t.Helper()
	if out, err := exec.Command("Rscript", "-e", `keyring::key_set_with_value("`+interopService+`", "token", "`+v+`")`).CombinedOutput(); err != nil { // #nosec G204 -- fixed test values
		t.Fatalf("R key_set: %v\n%s", err, out)
	}
}

func pyGet(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("python3", "-c", `import keyring; print(keyring.get_password("`+interopService+`", "token") or "")`).CombinedOutput()
	if err != nil {
		t.Fatalf("Python get: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func pySet(t *testing.T, v string) {
	t.Helper()
	if out, err := exec.Command("python3", "-c", `import keyring; keyring.set_password("`+interopService+`", "token", "`+v+`")`).CombinedOutput(); err != nil { // #nosec G204 -- fixed test values
		t.Fatalf("Python set: %v\n%s", err, out)
	}
}

func TestInterop(t *testing.T) {
	defer func() { _ = System.Delete(interopService, "token") }()

	if err := System.Set(interopService, "token", "1|fwWrote"); err != nil {
		t.Fatal(err)
	}
	if got := rGet(t); got != "1|fwWrote" {
		t.Errorf("R read %q after fw wrote", got)
	}
	if got := pyGet(t); got != "1|fwWrote" {
		t.Errorf("Python read %q after fw wrote", got)
	}

	rSet(t, "2|rWrote")
	if got, err := System.Get(interopService, "token"); err != nil || got != "2|rWrote" {
		t.Errorf("fw read %q, %v after R wrote", got, err)
	}

	pySet(t, "3|pyWrote")
	if got, err := System.Get(interopService, "token"); err != nil || got != "3|pyWrote" {
		t.Errorf("fw read %q, %v after Python wrote", got, err)
	}
	if got := rGet(t); got != "3|pyWrote" {
		t.Errorf("R read %q after Python wrote", got)
	}
}
