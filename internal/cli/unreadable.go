package cli

import (
	"fmt"
	"io"

	"github.com/wirvii/mneme/internal/model"
)

// renderUnreadableRow prints ONE complete sentence naming a single
// model.UnreadableRow — SPEC-133 D9/D13/AC6/AC8's own report shape.
//
// Deliberately NOT sharing wording with the "%d file(s) could not be
// parsed:" sentence renderSDDStatusResult already prints for BROKEN
// FILES (internal/cli/sdd.go): a FILE that fails to parse and a ROW whose
// content could not be fully read are different failures (SPEC-133 spec.md
// §9 Forma 5), and a shared anchor would let a test pass for the wrong
// reason.
func renderUnreadableRow(out io.Writer, u model.UnreadableRow) {
	fmt.Fprintf(out, "Row %s (%s) could not be fully read — column %s: %s.\n", u.ID, u.Kind, u.Column, u.Reason)
}

// renderUnreadableRows prints one renderUnreadableRow line per entry in
// unreadable, in the order given (already deterministic — the store
// produces rows in the order the underlying SELECT returned them).
func renderUnreadableRows(out io.Writer, unreadable []model.UnreadableRow) {
	for _, u := range unreadable {
		renderUnreadableRow(out, u)
	}
}
