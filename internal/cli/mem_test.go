package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"testing"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/model"
)

// seedMemCmdEnv points the package flags at an isolated data directory and
// slug, chdirs into a non-git temp directory (SPEC-085 point 3) and returns
// the ids of two freshly saved memories.
func seedMemCmdEnv(t *testing.T) (idA, idB string) {
	t.Helper()

	oldProject, oldDataDir := flagProject, flagDataDir
	flagProject, flagDataDir = "test/mem-cmd", t.TempDir()
	t.Cleanup(func() { flagProject, flagDataDir = oldProject, oldDataDir })

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	svc, cleanup, err := initService()
	if err != nil {
		t.Fatalf("initService: %v", err)
	}
	defer cleanup()

	a, err := svc.Save(context.Background(), model.SaveRequest{Title: "First memory", Content: "alpha content"})
	if err != nil {
		t.Fatalf("save A: %v", err)
	}
	b, err := svc.Save(context.Background(), model.SaveRequest{Title: "Second memory", Content: "beta content"})
	if err != nil {
		t.Fatalf("save B: %v", err)
	}
	return a.ID, b.ID
}

// runCmd executes cmd with args and returns its captured stdout.
func runCmd(t *testing.T, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// TestMemTimelineCmd is SPEC-164 AC7 (timeline half).
func TestMemTimelineCmd(t *testing.T) {
	idA, _ := seedMemCmdEnv(t)

	out, err := runCmd(t, newTimelineCmd(), idA, "--json")
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	var resp model.SearchResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, out)
	}
	found := false
	for _, r := range resp.Results {
		if r.ID == idA {
			found = true
		}
	}
	if !found {
		t.Errorf("seeded memory %s not in timeline: %s", idA, out)
	}

	table, err := runCmd(t, newTimelineCmd(), idA)
	if err != nil {
		t.Fatalf("timeline table: %v", err)
	}
	if !bytes.Contains([]byte(table), []byte("CREATED")) || !bytes.Contains([]byte(table), []byte("First memory")) {
		t.Errorf("unexpected table output: %s", table)
	}
}

// TestMemRelateCmd is SPEC-164 AC7 (relate half).
func TestMemRelateCmd(t *testing.T) {
	idA, idB := seedMemCmdEnv(t)

	out, err := runCmd(t, newRelateCmd(), idA, idB, "depends_on", "--json")
	if err != nil {
		t.Fatalf("relate: %v", err)
	}
	var resp model.RelateResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, out)
	}
	if !resp.Created || resp.RelationID == "" {
		t.Errorf("expected a created relation, got %+v", resp)
	}

	human, err := runCmd(t, newRelateCmd(), idA, idB, "depends_on")
	if err != nil {
		t.Fatalf("relate again: %v", err)
	}
	if !bytes.Contains([]byte(human), []byte("Updated relation")) {
		t.Errorf("second call should report an existing relation: %s", human)
	}

	_, err = runCmd(t, newRelateCmd(), idA, idB, "bogus_relation")
	if !errors.Is(err, model.ErrInvalidRelationType) {
		t.Errorf("expected ErrInvalidRelationType, got %v", err)
	}
}

// TestMemCmd_SubcommandsAndAliases is SPEC-164 AC8.
func TestMemCmd_SubcommandsAndAliases(t *testing.T) {
	root := NewRootCmd()

	mem, _, err := root.Find([]string{"mem"})
	if err != nil || mem.Name() != "mem" {
		t.Fatalf("mem command not registered: %v", err)
	}
	var got []string
	for _, c := range mem.Commands() {
		got = append(got, c.Name())
	}
	sort.Strings(got)
	want := []string{"explore", "forget", "gaps", "promote", "relate", "stats", "timeline"}
	if len(got) != len(want) {
		t.Fatalf("mem subcommands = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("mem subcommands = %v, want %v", got, want)
			break
		}
	}

	for _, alias := range []string{"stats", "forget", "promote", "gaps", "explore"} {
		c, _, err := root.Find([]string{alias})
		if err != nil || c.Name() != alias {
			t.Errorf("top-level %q alias missing: %v", alias, err)
		}
	}
}
