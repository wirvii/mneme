package enforcement

import (
	"reflect"
	"testing"
)

var linuxPC = PathContext{Home: "/home/u", TempDir: "/tmp", GOOS: "linux"}

func TestEvaluateLifecycleCommand_Blocks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		cmd  string
		want string
	}{
		{`mneme spec advance SPEC-1`, "spec advance"},
		{`mneme spec quick SPEC-1 "r"`, "spec quick"},
		{`mneme sdd import`, "sdd import"},
		{`mneme backlog archive BL-1 --reason x`, "backlog archive"},
		{`mneme lane override SPEC-1 --reason x --by y`, "lane override"},
		{`./mneme spec advance S`, "spec advance"},
		{`bin/mneme spec advance S`, "spec advance"},
		{`/usr/local/bin/mneme spec advance S`, "spec advance"},
		{`"mneme" "spec" advance S`, "spec advance"},
		{`go run ./cmd/mneme spec advance S`, "spec advance"},
		{`go run -tags fts5 ./cmd/mneme/ spec advance S`, "spec advance"},
		{`go run cmd/mneme/main.go spec advance S`, "spec advance"},
		{`go run github.com/wirvii/mneme/cmd/mneme@latest sdd import`, "sdd import"},
		{`mneme --data-dir= spec advance S`, "spec advance"},
		{`mneme -p wirvii/mneme spec advance S`, "spec advance"},
		{`mneme spec --project x advance S`, "spec advance"},
		{`make build && ./mneme spec advance S`, "spec advance"},
		{`true; mneme sdd import`, "sdd import"},
		{`false || mneme spec quick S r`, "spec quick"},
		{`echo x | mneme backlog archive BL-1`, "backlog archive"},
		{`echo $(mneme spec advance S)`, "spec advance"},
		{`(cd /tmp && mneme spec advance S)`, "spec advance"},
		{`if true; then mneme spec advance S; fi`, "spec advance"},
		{`bash -c "mneme spec advance S"`, "spec advance"},
		{`sh -c 'cd x && mneme sdd import'`, "sdd import"},
		{`env mneme spec advance S`, "spec advance"},
		{`command mneme spec advance S`, "spec advance"},
		{`exec mneme spec advance S`, "spec advance"},
		{`nohup mneme spec advance S`, "spec advance"},
		{`timeout 30 mneme spec advance S`, "spec advance"},
		{`nohup env FOO=1 mneme spec advance S`, "spec advance"},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			t.Parallel()
			got := EvaluateLifecycleCommand(tt.cmd, linuxPC)
			if !got.Block || got.Command != tt.want {
				t.Errorf("EvaluateLifecycleCommand(%q) = %+v, want Block with %q", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestEvaluateLifecycleCommand_Allows(t *testing.T) {
	t.Parallel()
	tests := []string{
		`mneme spec status S`,
		`mneme spec list`,
		`mneme spec reject S --reason x`,
		`mneme spec pushback S`,
		`mneme backlog add t`,
		`mneme backlog promote BL-1`,
		`mneme lane audit S`,
		`mneme lane status S`,
		`mneme lane reclassify S`,
		`mneme sdd status`,
		`mneme mem save x`,
		`mneme help spec advance`,
		`mneme spec advance --help`,
		`mneme sdd import -h`,
		`mnemex spec advance S`,
		`my-mneme spec advance S`,
		`mneme-dev spec advance S`,
		`go build ./cmd/mneme`,
		`go test ./cmd/mneme`,
		`go run ./cmd/other spec advance`,
		`grep -rn "mneme spec advance" docs/`,
		`echo mneme spec advance`,
		`git commit -m "mneme spec advance"`,
		"cat <<'EOF'\nmneme spec advance S\nEOF",
		`mneme`,
		`mneme spec`,
		`mneme Spec Advance S`,
		`bash -c`,
		``,
	}
	for _, cmd := range tests {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			if got := EvaluateLifecycleCommand(cmd, linuxPC); got.Block || got.Command != "" {
				t.Errorf("EvaluateLifecycleCommand(%q) = %+v, want allow", cmd, got)
			}
		})
	}
}

func TestEvaluateLifecycleCommand_Isolation(t *testing.T) {
	t.Parallel()
	tests := []string{
		`mneme --data-dir /tmp/iso spec advance S`,
		`mneme spec --data-dir /tmp/iso advance S`,
		`mneme spec advance S --data-dir=/tmp/iso`,
		`HOME=/tmp/iso mneme spec advance S`,
		`USERPROFILE=/tmp/iso mneme sdd import`,
		`MNEME_DATA_DIR=/tmp/iso mneme lane override S`,
		`env HOME=/tmp/iso mneme spec advance S`,
		`HOME=/tmp/iso bash -c "mneme spec advance S"`,
		`HOME=/tmp/iso go run ./cmd/mneme spec advance S`,
	}
	for _, cmd := range tests {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			if got := EvaluateLifecycleCommand(cmd, linuxPC); got.Block {
				t.Errorf("EvaluateLifecycleCommand(%q) = %+v, want allow (isolated)", cmd, got)
			}
		})
	}
}

func TestEvaluateLifecycleCommand_SameCommandOnly(t *testing.T) {
	t.Parallel()
	tests := []string{
		`export HOME=/tmp/iso; mneme spec advance S`,
		`HOME=/tmp/iso true && mneme spec advance S`,
		`mneme --data-dir /tmp/iso spec advance S && mneme spec advance S`,
	}
	for _, cmd := range tests {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			if got := EvaluateLifecycleCommand(cmd, linuxPC); !got.Block {
				t.Errorf("EvaluateLifecycleCommand(%q) allowed, want Block", cmd)
			}
		})
	}
}

func TestEvaluateLifecycleCommand_RealDirNotIsolation(t *testing.T) {
	t.Parallel()
	tests := []string{
		`HOME= mneme spec advance S`,
		`HOME=$HOME mneme spec advance S`,
		`HOME=${HOME} mneme spec advance S`,
		`HOME=~ mneme spec advance S`,
		`HOME=/home/u mneme spec advance S`,
		`HOME=/home/u/ mneme spec advance S`,
		`mneme --data-dir ~/.mneme spec advance S`,
		`mneme --data-dir $HOME/.mneme spec advance S`,
		`mneme --data-dir /home/u/.mneme spec advance S`,
		`MNEME_DATA_DIR=/home/u/.mneme mneme spec advance S`,
		`MNEME_DATA_DIR= mneme spec advance S`,
	}
	for _, cmd := range tests {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			if got := EvaluateLifecycleCommand(cmd, linuxPC); !got.Block {
				t.Errorf("EvaluateLifecycleCommand(%q) allowed, want Block", cmd)
			}
		})
	}

	t.Run("empty Home keeps literals", func(t *testing.T) {
		t.Parallel()
		got := EvaluateLifecycleCommand(`HOME=$HOME mneme spec advance S`, PathContext{GOOS: "linux"})
		if !got.Block {
			t.Error("HOME=$HOME allowed with empty Home, want Block")
		}
	})
}

func TestEvaluateLifecycleCommand_Windows(t *testing.T) {
	t.Parallel()
	pc := PathContext{GOOS: "windows", Home: `C:\Users\x`}
	blocks := []string{
		`"C:\Users\x\bin\mneme.exe" spec advance S`,
		`"MNEME.EXE" sdd import`,
		`go.exe run ./cmd/mneme spec advance S`,
		`mneme --data-dir "C:\Users\X\.mneme" spec advance S`,
		`USERPROFILE="C:\Users\x" mneme spec advance S`,
	}
	for _, cmd := range blocks {
		t.Run("block "+cmd, func(t *testing.T) {
			t.Parallel()
			if got := EvaluateLifecycleCommand(cmd, pc); !got.Block {
				t.Errorf("EvaluateLifecycleCommand(%q) allowed, want Block", cmd)
			}
		})
	}
	t.Run("isolated", func(t *testing.T) {
		t.Parallel()
		if got := EvaluateLifecycleCommand(`mneme --data-dir "C:\Temp\iso" spec advance S`, pc); got.Block {
			t.Errorf("isolated data dir blocked: %+v", got)
		}
	})
}

func TestEvaluateLifecycleCommand_ParseErrorFailsOpen(t *testing.T) {
	t.Parallel()
	if got := EvaluateLifecycleCommand(`mneme spec advance "sin cerrar`, linuxPC); got.Block {
		t.Errorf("unparseable command blocked: %+v", got)
	}
}

func TestLifecycleCommands_Set(t *testing.T) {
	t.Parallel()
	want := []string{"backlog archive", "lane override", "sdd import", "spec advance", "spec quick"}
	if got := LifecycleCommands(); !reflect.DeepEqual(got, want) {
		t.Errorf("LifecycleCommands() = %v, want %v", got, want)
	}
}
