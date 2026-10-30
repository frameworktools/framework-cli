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

// frameworkPy reports whether python3 can import framework-py. Plain Python
// keyring reads tokens the other tools write, but its writes add a second
// Secret Service item without R's schema, so only framework-py's own helpers
// are tested for writing.
func frameworkPy() bool {
	return exec.Command("python3", "-c", "import framework.cloud").Run() == nil
}

func pyGet(t *testing.T) string {
	t.Helper()
	script := `import keyring; print(keyring.get_password("` + interopService + `", "token") or "")`
	if frameworkPy() {
		script = `from framework.cloud import _keyring_get; print(_keyring_get("` + interopService + `") or "")`
	}
	out, err := exec.Command("python3", "-c", script).CombinedOutput() // #nosec G204 -- fixed test values
	if err != nil {
		t.Fatalf("Python get: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func pySet(t *testing.T, v string) {
	t.Helper()
	script := `import sys; from framework.cloud import _keyring_set; sys.exit(0 if _keyring_set("` + interopService + `", "` + v + `") else 1)`
	if out, err := exec.Command("python3", "-c", script).CombinedOutput(); err != nil { // #nosec G204 -- fixed test values
		t.Fatalf("Python set: %v\n%s", err, out)
	}
}

func TestInterop(t *testing.T) {
	defer func() { _ = System.Delete(interopService, "token") }()

	t.Run("fw writes", func(t *testing.T) {
		if err := System.Set(interopService, "token", "1|fwWrote"); err != nil {
			t.Fatal(err)
		}
		if got := rGet(t); got != "1|fwWrote" {
			t.Errorf("R read %q", got)
		}
		if got := pyGet(t); got != "1|fwWrote" {
			t.Errorf("Python read %q", got)
		}
	})

	t.Run("R writes", func(t *testing.T) {
		rSet(t, "2|rWrote")
		if got, err := System.Get(interopService, "token"); err != nil || got != "2|rWrote" {
			t.Errorf("fw read %q, %v", got, err)
		}
		if got := pyGet(t); got != "2|rWrote" {
			t.Errorf("Python read %q", got)
		}
	})

	t.Run("Python writes", func(t *testing.T) {
		if !frameworkPy() {
			t.Skip("framework-py is not installed; plain Python keyring writes are known not to interoperate on Linux")
		}
		pySet(t, "3|pyWrote")
		if got, err := System.Get(interopService, "token"); err != nil || got != "3|pyWrote" {
			t.Errorf("fw read %q, %v", got, err)
		}
		if got := rGet(t); got != "3|pyWrote" {
			t.Errorf("R read %q", got)
		}
	})

	t.Run("fw replaces Python", func(t *testing.T) {
		if err := System.Set(interopService, "token", "4|fwAgain"); err != nil {
			t.Fatal(err)
		}
		if got := rGet(t); got != "4|fwAgain" {
			t.Errorf("R read %q", got)
		}
		if got := pyGet(t); got != "4|fwAgain" {
			t.Errorf("Python read %q", got)
		}
	})
}
