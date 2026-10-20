package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/model"
)

// captureStdout runs fn and returns what it wrote to os.Stdout. The search
// command prints straight to os.Stdout, so this is how a test sees the exact
// bytes a person would read.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	defer func() { os.Stdout = orig }()

	fn()
	_ = w.Close()
	os.Stdout = orig
	return <-done
}

// seedStaleMemory saves one discovery in the isolated environment and
// backdates its review clock by 61 days directly in the SQLite file.
func seedStaleMemory(t *testing.T) string {
	t.Helper()
	seedMemCmdEnv(t) // isolates flags + cwd, saves two filler memories

	svc, cleanup, err := initService()
	if err != nil {
		t.Fatalf("initService: %v", err)
	}
	resp, err := svc.Save(context.Background(), model.SaveRequest{
		Title: "Quartz misfire policy", Content: "Quartz scheduler misfire handling notes.",
	})
	cleanup()
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	cfg := config.Default()
	cfg.Storage.DataDir = flagDataDir
	raw, err := db.Open(cfg.ProjectDBPath(flagProject))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer raw.Close()
	old := time.Now().UTC().Add(-61 * 24 * time.Hour).Format(time.RFC3339Nano)
	if _, err := raw.Exec(`UPDATE memories SET reviewed_at = ? WHERE id = ?`, old, resp.ID); err != nil {
		t.Fatalf("age memory: %v", err)
	}
	return resp.ID
}

// TestSearchCmd_HidesReviewNoteFromHumans is SPEC-167 AC8: the review notice
// is for the agent, so no human-readable search output may show it; only
// --json (the agent's channel) carries it.
func TestSearchCmd_HidesReviewNoteFromHumans(t *testing.T) {
	seedStaleMemory(t)

	for _, args := range [][]string{{"quartz scheduler misfire"}, {"quartz scheduler misfire", "--full"}} {
		out := captureStdout(t, func() {
			cmd := newSearchCmd()
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Errorf("search %v: %v", args, err)
			}
		})
		if !strings.Contains(out, "Quartz misfire policy") {
			t.Fatalf("search %v did not list the memory: %q", args, out)
		}
		for _, banned := range []string{"review_due", "review_note", "Revisión vencida"} {
			if strings.Contains(out, banned) {
				t.Errorf("search %v leaked %q to the human output: %q", args, banned, out)
			}
		}
	}

	out := captureStdout(t, func() {
		cmd := newSearchCmd()
		cmd.SetArgs([]string{"quartz scheduler misfire", "--json"})
		if err := cmd.Execute(); err != nil {
			t.Errorf("search --json: %v", err)
		}
	})
	if !strings.Contains(out, `"review_due": true`) {
		t.Errorf("--json must carry review_due for the agent: %q", out)
	}
}

// TestUpdateCmd_Confirm is SPEC-167 AC8: --confirm is accepted on its own.
func TestUpdateCmd_Confirm(t *testing.T) {
	id := seedStaleMemory(t)

	out, err := runCmd(t, newUpdateCmd(), id, "--confirm")
	if err != nil {
		t.Fatalf("update --confirm: %v", err)
	}
	if !strings.HasPrefix(out, "Confirmed: "+id) {
		t.Errorf("output = %q, want a Confirmed line", out)
	}
	for _, banned := range []string{"review_due", "review_note", "Revisión vencida"} {
		if strings.Contains(out, banned) {
			t.Errorf("update output leaked %q", banned)
		}
	}

	after := captureStdout(t, func() {
		cmd := newSearchCmd()
		cmd.SetArgs([]string{"quartz scheduler misfire", "--json"})
		if err := cmd.Execute(); err != nil {
			t.Errorf("search --json: %v", err)
		}
	})
	if strings.Contains(after, `"review_due": true`) {
		t.Error("confirmed memory must no longer be review_due")
	}
}
