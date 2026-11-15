package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

const (
	ensureComment = "SPEC-184: on by default, decided by the orchestrator on the owner's behalf; set false to turn it off"
	ensureLines   = "# " + ensureComment + "\norchestrator_trivial_inline = true\n"
)

func ensure(t *testing.T, path string) (bool, error) {
	t.Helper()
	return EnsureDeclared(path, "delegation", "orchestrator_trivial_inline", "true", ensureComment)
}

func mustLoad(t *testing.T, path string) *Config {
	t.Helper()
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load after write: %v", err)
	}
	return cfg
}

// TestEnsureDeclared covers SPEC-184 C14 (helper part).
func TestEnsureDeclared(t *testing.T) {
	t.Setenv("MNEME_DELEGATION_TRIVIAL_INLINE", "")

	t.Run("missing_file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nested", "config.toml")
		written, err := ensure(t, path)
		if err != nil || !written {
			t.Fatalf("written=%v err=%v", written, err)
		}
		got, _ := os.ReadFile(path)
		if string(got) != "[delegation]\n"+ensureLines {
			t.Errorf("file = %q", got)
		}
		mustLoad(t, path)
	})

	t.Run("header_with_comment", func(t *testing.T) {
		prefix := "# my config\n[search]\ndefault_limit = 9\n\n[delegation]   # user note\n"
		suffix := "# keep this\nsubagent_containment = \"warn\"\n"
		path := writeConfig(t, prefix+suffix)
		written, err := ensure(t, path)
		if err != nil || !written {
			t.Fatalf("written=%v err=%v", written, err)
		}
		got, _ := os.ReadFile(path)
		want := prefix + ensureLines + suffix
		if !bytes.Equal(got, []byte(want)) {
			t.Errorf("file =\n%s\nwant\n%s", got, want)
		}
		if !bytes.HasPrefix(got, []byte(prefix)) || !bytes.HasSuffix(got, []byte(suffix)) {
			t.Error("bytes outside the inserted lines changed")
		}
		mustLoad(t, path)
	})

	for _, val := range []string{"true", "false"} {
		t.Run("declared_"+val, func(t *testing.T) {
			body := "[delegation]\norchestrator_trivial_inline = " + val + "\n"
			path := writeConfig(t, body)
			written, err := ensure(t, path)
			if err != nil || written {
				t.Fatalf("written=%v err=%v, want false/nil", written, err)
			}
			got, _ := os.ReadFile(path)
			if string(got) != body {
				t.Errorf("file changed: %q", got)
			}
		})
	}

	for name, body := range map[string]string{
		"only_project_subtable":                "[delegation.projects.\"wirvii/x\"]\nsubagent_containment = \"block\"\n",
		"only_project_subtable_with_own_value": "[delegation.projects.\"wirvii/x\"]\norchestrator_trivial_inline = false\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := writeConfig(t, body)
			written, err := ensure(t, path)
			if err != nil || !written {
				t.Fatalf("written=%v err=%v", written, err)
			}
			got, _ := os.ReadFile(path)
			if !strings.HasPrefix(string(got), body) || !strings.HasSuffix(string(got), "\n[delegation]\n"+ensureLines) {
				t.Errorf("file = %q, want the original plus a [delegation] block at the end", got)
			}
			cfg := mustLoad(t, path)
			if !cfg.Delegation.OrchestratorTrivialInline {
				t.Error("global value not true")
			}
			if strings.Contains(body, "orchestrator_trivial_inline = false") && cfg.OrchestratorTrivialInlineEnabled("wirvii/x") {
				t.Error("project's own false was not kept")
			}
		})
	}

	t.Run("root_dotted_keys", func(t *testing.T) {
		body := "delegation.subagent_containment = \"warn\"\n"
		path := writeConfig(t, body)
		if _, err := ensure(t, path); err == nil {
			t.Fatal("want an error for root-level dotted keys")
		}
		got, _ := os.ReadFile(path)
		if string(got) != body {
			t.Errorf("file changed: %q", got)
		}
	})

	t.Run("spaced_header", func(t *testing.T) {
		path := writeConfig(t, "[ delegation ]\nenabled = true\n")
		written, err := ensure(t, path)
		if err != nil || !written {
			t.Fatalf("written=%v err=%v", written, err)
		}
		got, _ := os.ReadFile(path)
		if string(got) != "[ delegation ]\n"+ensureLines+"enabled = true\n" {
			t.Errorf("file = %q", got)
		}
		mustLoad(t, path)
	})

	t.Run("crlf_file", func(t *testing.T) {
		path := writeConfig(t, "[delegation]\r\nenabled = true\r\n")
		if _, err := ensure(t, path); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(path)
		want := "[delegation]\r\n# " + ensureComment + "\r\norchestrator_trivial_inline = true\r\nenabled = true\r\n"
		if string(got) != want {
			t.Errorf("file = %q, want %q", got, want)
		}
		mustLoad(t, path)
	})

	t.Run("result_that_does_not_load_leaves_original", func(t *testing.T) {
		// A dotted key inside [delegation] that defines projects."a" means
		// a new [delegation.projects."a"] header would redefine it: the
		// post-write Load fails and nothing changes.
		body := "[delegation]\nprojects.a.subagent_containment = \"warn\"\n"
		path := writeConfig(t, body)
		if err := SetSubagentContainmentMode(path, "a", "block"); err == nil {
			t.Fatal("want an error when the result would not load")
		}
		got, _ := os.ReadFile(path)
		if string(got) != body {
			t.Errorf("file changed: %q", got)
		}
		if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
			t.Errorf(".tmp left behind (stat err=%v)", err)
		}
	})

	t.Run("invalid_toml_is_an_error", func(t *testing.T) {
		path := writeConfig(t, "[delegation\n")
		if _, err := ensure(t, path); err == nil {
			t.Error("want a parse error")
		}
	})
}

// TestSplitHeaderKey pins the single header comparison every editor uses.
func TestSplitHeaderKey(t *testing.T) {
	tests := []struct {
		in   string
		want []string
		bad  bool
	}{
		{in: "delegation", want: []string{"delegation"}},
		{in: " delegation . projects ", want: []string{"delegation", "projects"}},
		{in: "delegation.projects.'wirvii/mneme'", want: []string{"delegation", "projects", "wirvii/mneme"}},
		{in: `delegation.projects."wirvii/mneme"`, want: []string{"delegation", "projects", "wirvii/mneme"}},
		{in: `a."b.c"`, want: []string{"a", "b.c"}},
		{in: `a."q\"x"`, want: []string{"a", `q"x`}},
		{in: `a."b`, bad: true},
		{in: `a.'b`, bad: true},
		{in: "a..b", bad: true},
		{in: "", bad: true},
		{in: "a.", bad: true},
		{in: `"a" b`, bad: true},
	}
	for _, tt := range tests {
		got, err := splitHeaderKey(tt.in)
		if tt.bad {
			if err == nil {
				t.Errorf("splitHeaderKey(%q) = %v, want an error", tt.in, got)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("splitHeaderKey(%q) = %v, %v; want %v", tt.in, got, err, tt.want)
		}
	}
}

// TestSetSubagentContainmentMode_MarshalWrittenFile covers files written by
// the pre-SPEC-184 toml.Marshal, whose headers use single quotes.
func TestSetSubagentContainmentMode_MarshalWrittenFile(t *testing.T) {
	t.Setenv("MNEME_DELEGATION_TRIVIAL_INLINE", "")

	marshalled := func(t *testing.T) string {
		t.Helper()
		cfg := Default()
		cfg.Delegation.Projects = map[string]DelegationProjectConfig{"wirvii/mneme": {SubagentContainment: "warn"}}
		out, err := toml.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out), "[delegation.projects.'wirvii/mneme']") {
			t.Fatalf("precondition: Marshal no longer writes single quotes:\n%s", out)
		}
		return writeConfig(t, string(out))
	}

	t.Run("marshal_written_single_quotes", func(t *testing.T) {
		path := marshalled(t)
		if err := SetSubagentContainmentMode(path, "wirvii/mneme", "block"); err != nil {
			t.Fatal(err)
		}
		cfg := mustLoad(t, path)
		if got := cfg.SubagentContainmentMode("wirvii/mneme"); got != "block" {
			t.Errorf("mode = %q, want block", got)
		}
		got, _ := os.ReadFile(path)
		if n := strings.Count(string(got), "[delegation.projects."); n != 1 {
			t.Errorf("%d headers for the project table, want 1:\n%s", n, got)
		}

		written, err := ensure(t, path)
		if err != nil || written {
			t.Errorf("EnsureDeclared on a marshalled file: written=%v err=%v, want false (key already declared)", written, err)
		}
	})

	t.Run("new_project_header_quoted", func(t *testing.T) {
		path := writeConfig(t, "[search]\ndefault_limit = 7\n")
		if err := SetSubagentContainmentMode(path, "wirvii/mneme", "block"); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(path)
		if !strings.Contains(string(got), "\n[delegation.projects.\"wirvii/mneme\"]\nsubagent_containment = \"block\"\n") {
			t.Errorf("file = %q", got)
		}
	})

	t.Run("existing_key_replaced_in_place", func(t *testing.T) {
		path := writeConfig(t, "[delegation.projects.\"wirvii/mneme\"]\nsubagent_containment = \"warn\"\norchestrator_trivial_inline = false\n")
		if err := SetSubagentContainmentMode(path, "wirvii/mneme", "off"); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(path)
		if string(got) != "[delegation.projects.\"wirvii/mneme\"]\nsubagent_containment = \"off\"\norchestrator_trivial_inline = false\n" {
			t.Errorf("file = %q", got)
		}
	})
}

// TestReplaceTable_Shapes covers the table replacement the model writer
// relies on: a body replaced before another header, and an appended table.
func TestReplaceTable_Shapes(t *testing.T) {
	got, err := replaceTable("[models.overrides]\nold = \"x\"\n# gone\n[search]\ndefault_limit = 7\n", "models.overrides", []string{`a = "b"`})
	if err != nil {
		t.Fatal(err)
	}
	if got != "[models.overrides]\na = \"b\"\n\n[search]\ndefault_limit = 7\n" {
		t.Errorf("replaced = %q", got)
	}
	got, err = replaceTable("x = 1", "models.overrides", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "x = 1\n\n[models.overrides]\n" {
		t.Errorf("appended = %q", got)
	}
	if _, err := replaceTable("", "a..b", nil); err == nil {
		t.Error("malformed header accepted")
	}
	if _, err := upsertKey("", "a..b", "k", "1", ""); err == nil {
		t.Error("malformed header accepted by upsertKey")
	}
	if _, err := replaceTable("models.overrides.a = \"b\"\n", "models", nil); err == nil {
		t.Error("root dotted section accepted by replaceTable")
	}
}
