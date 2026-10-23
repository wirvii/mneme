package shell

import (
	"reflect"
	"strings"
	"testing"
)

func TestRedirects(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want []Redirect
	}{
		{"empty", "", nil},
		{"plain out", "echo x > a", []Redirect{{">", "a"}}},
		{"append", "echo x >> a", []Redirect{{">>", "a"}}},
		{"clobber", "echo x >| a", []Redirect{{">|", "a"}}},
		{"both", "cmd &> a", []Redirect{{"&>", "a"}}},
		{"both append", "cmd &>> a", []Redirect{{"&>>", "a"}}},
		{"fd out", "cmd 2> a", []Redirect{{"2>", "a"}}},
		{"fd append", "cmd 2>> a", []Redirect{{"2>>", "a"}}},
		{"read write", "cmd <> a", []Redirect{{"<>", "a"}}},
		{"dup to file", "cmd >&file", []Redirect{{">&", "file"}}},
		{"quoted target", `echo x > ".mneme/sdd/a b"`, []Redirect{{">", ".mneme/sdd/a b"}}},
		{"input is reported", "cat < f", []Redirect{{"<", "f"}}},
		{"compound block", "{ echo a; } > f", []Redirect{{">", "f"}}},
		{"compound loop", "for i in 1; do echo $i; done >> f", []Redirect{{">>", "f"}}},
		{"command substitution", "echo $(cat x > y)", []Redirect{{">", "y"}}},
		{"heredoc skipped", "cat <<EOF\nx\nEOF", nil},
		{"dash heredoc skipped", "cat <<-EOF\nx\nEOF", nil},
		{"here string skipped", "cat <<< hola", nil},
		{"fd dup skipped", "cmd 2>&1", nil},
		{"fd dup out skipped", "cmd 1>&2", nil},
		{"mixed", "echo x > /tmp/y 2>&1", []Redirect{{">", "/tmp/y"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Redirects(tt.cmd)
			if err != nil {
				t.Fatalf("Redirects(%q) error: %v", tt.cmd, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Redirects(%q) = %#v, want %#v", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestRedirects_ParseError(t *testing.T) {
	_, err := Redirects("echo 'sin cerrar")
	if err == nil {
		t.Fatal("expected a parse error")
	}
	if !strings.HasPrefix(err.Error(), "shell: parse:") {
		t.Errorf("error %q lacks the shell: parse: prefix", err)
	}
}
