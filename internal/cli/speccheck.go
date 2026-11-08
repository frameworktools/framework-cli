package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/frameworktools/framework-cli/internal/spec"
)

// checkSpecForCloud checks .framework/spec.json against spec.v1 before fw
// sends it to Framework Cloud, which refuses a spec that doesn't match. The
// keys an older upgrade() wrote (spec.StaleKeys) are left out of what is
// sent, with a warning on w. Anything else stops the command with the
// problems listed and a pointer to fw project upgrade --apply. next is the
// command to run again afterwards. It returns the spec to send.
func checkSpecForCloud(w io.Writer, data []byte, next string) ([]byte, error) {
	checked, err := spec.CheckSpec(data)
	if err != nil {
		return nil, fmt.Errorf(".framework/spec.json is %w", err)
	}
	if len(checked.Dropped) > 0 {
		fmt.Fprintf(w, "Warning: .framework/spec.json has %s, which an older upgrade() wrote and spec.v1 doesn't allow. "+
			"fw leaves them out of what it sends. To drop them from the file too, run fw project upgrade --apply (in R projects, with a framework package newer than 2.0.1).\n",
			strings.Join(checked.Dropped, " and "))
	}
	if len(checked.Problems) == 0 {
		return checked.Data, nil
	}
	lines := make([]string, len(checked.Problems))
	for i, p := range checked.Problems {
		lines[i] = p.String()
	}
	msg := fmt.Sprintf(".framework/spec.json doesn't match spec.v1, so Framework Cloud would refuse it. Nothing was sent.\n  %s\n"+
		"Run fw project upgrade --apply to rewrite the spec (in R projects, with a framework package newer than 2.0.1), then run %s again",
		strings.Join(lines, "\n  "), next)
	return nil, &exitError{code: exitFailure, err: errors.New(msg), reason: "spec_invalid", hint: "fw project upgrade --apply", problems: lines}
}
