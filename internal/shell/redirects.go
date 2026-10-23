package shell

import (
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Redirect is one redirection found anywhere in a command: Op is the operator
// with its file-descriptor prefix (for example "2>") and Target is the
// destination word with its shell quoting removed.
type Redirect struct {
	Op     string
	Target string
}

// Redirects returns every file redirection command would perform, in source
// order, wherever it sits: on a simple command, on a compound statement
// (`{ ...; } > f`, `for ...; done >> f`) or inside a `$(...)`.
//
// It exists next to SimpleCommands because redirections hang from the
// statement, not from the call, so SimpleCommands cannot expose them; a guard
// that must tell a write (`>`) from a read (`<`) needs the operator and the
// target together. Input redirections are reported too: deciding that they
// are harmless is the caller's job, not the lexer's.
//
// Here-documents, here-strings and numeric descriptor duplications (`2>&1`)
// are omitted, with the same criterion as Tokenize: they name no file. A parse
// error is returned wrapped; an empty command returns nil, nil.
func Redirects(command string) ([]Redirect, error) {
	if strings.TrimSpace(command) == "" {
		return nil, nil
	}

	f, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return nil, fmt.Errorf("shell: parse: %w", err)
	}

	var out []Redirect
	syntax.Walk(f, func(n syntax.Node) bool {
		r, ok := n.(*syntax.Redirect)
		if !ok || r.Word == nil {
			return true
		}
		switch r.Op {
		case syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc:
			return true
		}
		target := extractWordLiteral(r.Word)
		if (r.Op == syntax.DplOut || r.Op == syntax.DplIn) && isAllDigits(target) {
			return true
		}
		op := r.Op.String()
		if r.N != nil {
			op = r.N.Value + op
		}
		out = append(out, Redirect{Op: op, Target: target})
		return true
	})
	return out, nil
}
