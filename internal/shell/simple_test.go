package shell

import (
	"errors"
	"reflect"
	"testing"
)

func TestSimpleCommands(t *testing.T) {
	t.Parallel()

	adv := []string{"mneme", "spec", "advance"}
	tests := []struct {
		name string
		in   string
		want []SimpleCommand
	}{
		{"empty", "", nil},
		{"blank", "  \n ", nil},
		{"plain argv", "mneme spec advance", []SimpleCommand{{Args: adv}}},
		{"quotes removed", `"mneme" 'spec' advance`, []SimpleCommand{{Args: adv}}},
		{"semicolon", "true; mneme spec advance", []SimpleCommand{{Args: []string{"true"}}, {Args: adv}}},
		{"and", "a && b", []SimpleCommand{{Args: []string{"a"}}, {Args: []string{"b"}}}},
		{"or", "a || b", []SimpleCommand{{Args: []string{"a"}}, {Args: []string{"b"}}}},
		{"pipe", "echo x | mneme spec advance", []SimpleCommand{{Args: []string{"echo", "x"}}, {Args: adv}}},
		{"subshell", "(cd /tmp && mneme spec advance)", []SimpleCommand{{Args: []string{"cd", "/tmp"}}, {Args: adv}}},
		{"block", "{ mneme spec advance; }", []SimpleCommand{{Args: adv}}},
		{"if", "if true; then mneme spec advance; fi", []SimpleCommand{{Args: []string{"true"}}, {Args: adv}}},
		{"while", "while true; do mneme spec advance; done", []SimpleCommand{{Args: []string{"true"}}, {Args: adv}}},
		{"for", "for i in 1; do mneme spec advance; done", []SimpleCommand{{Args: adv}}},
		{"case", "case x in x) mneme spec advance;; esac", []SimpleCommand{{Args: adv}}},
		{"function", "f() { mneme spec advance; }", []SimpleCommand{{Args: adv}}},
		{"cmd subst", "echo $(mneme spec advance)", []SimpleCommand{{Args: []string{"echo", "$(mneme spec advance)"}}, {Args: adv}}},
		{"backticks", "echo `mneme spec advance`", []SimpleCommand{{Args: []string{"echo", "$(mneme spec advance)"}}, {Args: adv}}},
		{
			"prefix assignment", "HOME=/tmp/x mneme spec advance",
			[]SimpleCommand{{Assigns: []Assignment{{"HOME", "/tmp/x"}}, Args: adv}},
		},
		{
			"two assignments", "A=1 B=2 cmd",
			[]SimpleCommand{{Assigns: []Assignment{{"A", "1"}, {"B", "2"}}, Args: []string{"cmd"}}},
		},
		{"bare assignment", "HOME=x", nil},
		{
			"param expansion kept as text", `HOME="$HOME" mneme x`,
			[]SimpleCommand{{Assigns: []Assignment{{"HOME", "$HOME"}}, Args: []string{"mneme", "x"}}},
		},
		{"quoted arg expansion", `echo "$HOME"`, []SimpleCommand{{Args: []string{"echo", "$HOME"}}}},
		{
			"quoted heredoc body is data", "cat <<'EOF'\nmneme spec advance S\nEOF",
			[]SimpleCommand{{Args: []string{"cat"}}},
		},
		{
			"unquoted heredoc substitution runs", "cat <<EOF\n$(mneme spec advance)\nEOF",
			[]SimpleCommand{{Args: []string{"cat"}}, {Args: adv}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := SimpleCommands(tt.in)
			if err != nil {
				t.Fatalf("SimpleCommands(%q): %v", tt.in, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SimpleCommands(%q)\n got %#v\nwant %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestSimpleCommands_ParseErrorIsWrapped(t *testing.T) {
	t.Parallel()
	_, err := SimpleCommands(`mneme spec advance "sin cerrar`)
	if err == nil {
		t.Fatal("expected a parse error")
	}
	if errors.Unwrap(err) == nil {
		t.Errorf("error %v does not wrap the parser error with %%w", err)
	}
}
