package db

import (
	"database/sql"
	"testing"
)

func TestMigration023_NormalizesSessionEndTimestamps(t *testing.T) {
	database := openRawMemory(t)
	applyUpToVersion(t, database, 22)

	rows := []struct {
		id      string
		endedAt any
	}{
		{id: "legacy", endedAt: "2026-09-19 12:34:56"},
		{id: "null", endedAt: nil},
		{id: "rfc3339", endedAt: "2026-09-19T12:34:56Z"},
		{id: "rfc3339-nano", endedAt: "2026-09-19T12:34:56.123456789Z"},
	}
	for _, row := range rows {
		if _, err := database.Exec(
			`INSERT INTO sessions (id, project, agent, started_at, ended_at) VALUES (?, 'p', 'codex', '2026-09-19T12:00:00Z', ?)`,
			row.id, row.endedAt,
		); err != nil {
			t.Fatalf("seed session %s: %v", row.id, err)
		}
	}

	if err := applyMigration(database, 23, loadMigration023(t)); err != nil {
		t.Fatalf("apply migration 023: %v", err)
	}

	want := map[string]any{
		"legacy":       "2026-09-19T12:34:56Z",
		"null":         nil,
		"rfc3339":      "2026-09-19T12:34:56Z",
		"rfc3339-nano": "2026-09-19T12:34:56.123456789Z",
	}
	for id, expected := range want {
		var got sql.NullString
		if err := database.QueryRow(`SELECT ended_at FROM sessions WHERE id = ?`, id).Scan(&got); err != nil {
			t.Fatalf("read session %s: %v", id, err)
		}
		if expected == nil {
			if got.Valid {
				t.Errorf("session %s ended_at = %q, want NULL", id, got.String)
			}
			continue
		}
		if !got.Valid || got.String != expected {
			t.Errorf("session %s ended_at = %q (valid=%t), want %q", id, got.String, got.Valid, expected)
		}
	}

	var versionRows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM schema_version WHERE version = 23`).Scan(&versionRows); err != nil {
		t.Fatalf("read schema version 23: %v", err)
	}
	if versionRows != 1 {
		t.Errorf("schema version 23 rows = %d, want 1", versionRows)
	}
}

func loadMigration023(t *testing.T) string {
	t.Helper()
	b, err := migrationsFS.ReadFile("migrations/023_normalize_session_end_timestamps.sql")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
