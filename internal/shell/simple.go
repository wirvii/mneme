package shell

import (
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Assignment is one NAME=VALUE prefix of a simple command, as in
// `HOME=/tmp/x mneme ...`. Value has its shell quoting removed.
type Assignment struct {
	Name  string
	Value string
}

// SimpleCommand is one executed simple command: the assignments written in
// front of it and its argv with shell quoting removed.
type SimpleCommand struct {
	Assigns []Assignment
	Args    []string
}

// SimpleCommands returns every simple command that command would execute, in
// source order, wherever it sits: after `;`, `&&`, `||` or `|`, inside a
// subshell, a block, an if/while/for/case, a function body, a `$(...)`, a
// backtick or a process substitution.
//
// It exists next to Tokenize because Tokenize flattens the tree: it drops the
// NAME=VALUE prefixes of a command and does not mark where each command starts
// inside a compound statement. A caller that must reason about one command at
// a time, prefix assignments included, cannot be built on it.
//
// A call made only of assignments (a bare `HOME=x`) executes nothing and is not
// returned. Here-document bodies are data, not commands; only a `$(...)` inside
// an unquoted body, which the shell does run, is found. A parse error is
// returned wrapped; an empty command returns nil, nil.
func SimpleCommands(command string) ([]SimpleCommand, error) {
	if strings.TrimSpace(command) == "" {
		return nil, nil
	}

	f, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return nil, fmt.Errorf("shell: parse: %w", err)
	}

	var out []SimpleCommand
	syntax.Walk(f, func(n syntax.Node) bool {
		call, ok := n.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sc := SimpleCommand{}
		for _, a := range call.Assigns {
			sc.Assigns = append(sc.Assigns, Assignment{Name: a.Name.Value, Value: extractWordLiteral(a.Value)})
		}
		for _, w := range call.Args {
			sc.Args = append(sc.Args, extractWordLiteral(w))
		}
		out = append(out, sc)
		return true
	})
	return out, nil
}
