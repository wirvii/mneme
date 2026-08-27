package db

import (
	"database/sql"
	"fmt"

	"github.com/gofrs/uuid/v5"
)

// ensureSDDUUIDs is the self-healing half of SPEC-128 D7 mitad A: it makes
// "every backlog_items/specs row carries a non-empty uuid" a PERMANENT
// invariant, re-checked on every Open, rather than trusting that a one-shot
// migration ran to completion. A power cut between migration 019 (which adds
// the column with DEFAULT "") and this fill self-corrects on the very next
// Open — see migrations/019_sdd_anchors.sql.
//
// Two guards keep the common case (nothing to do) at microsecond cost, which
// matters because this runs on every mneme invocation that goes through
// Open/OpenMemory:
//
//  1. Schema-version guard: below migration 19 the uuid columns don't exist
//     yet — exit without touching anything. This also makes ensureSDDUUIDs
//     safe against a database mid-way through a legacy migration sequence.
//  2. Existence guard, per table: a cheap SELECT EXISTS(... LIMIT 1) against
//     the same predicate the partial unique index already indexes. Only
//     when at least one row is missing its anchor do we pay for a full
//     scan-and-fill.
//
// SQLite cannot generate a UUIDv7 in pure SQL, so this cannot be folded into
// the migration itself — fabricating a UUIDv4 via randomblob() would leave
// the column holding two different kinds of value with no way to tell them
// apart later.
//
// OpenReadOnly does not call migrate (and therefore never calls this), so
// the latency-sensitive pre-tool-use hook path pays nothing here.
func ensureSDDUUIDs(db *sql.DB) error {
	var schemaVersion int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&schemaVersion); err != nil {
		return fmt.Errorf("db: ensure sdd uuids: read schema_version: %w", err)
	}
	if schemaVersion < 19 {
		return nil
	}

	if err := fillMissingBacklogUUIDs(db); err != nil {
		return err
	}
	if err := fillMissingSpecUUIDs(db); err != nil {
		return err
	}
	return nil
}

// fillMissingBacklogUUIDs assigns a fresh UUIDv7 to every backlog_items row
// whose uuid column is still empty. backlog_items' primary key is the bare
// id column (unlike specs, see fillMissingSpecUUIDs), so id alone is enough
// to address a row precisely.
func fillMissingBacklogUUIDs(db *sql.DB) error {
	var hasMissing bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM backlog_items WHERE uuid = '' LIMIT 1)`).Scan(&hasMissing); err != nil {
		return fmt.Errorf("db: ensure sdd uuids: check backlog_items: %w", err)
	}
	if !hasMissing {
		return nil
	}

	rows, err := db.Query(`SELECT id FROM backlog_items WHERE uuid = ''`)
	if err != nil {
		return fmt.Errorf("db: ensure sdd uuids: select backlog_items: %w", err)
	}

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return fmt.Errorf("db: ensure sdd uuids: scan backlog_items: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("db: ensure sdd uuids: iterate backlog_items: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("db: ensure sdd uuids: close backlog_items rows: %w", err)
	}

	for _, id := range ids {
		anchor, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("db: ensure sdd uuids: generate uuid for backlog_items %s: %w", id, err)
		}
		if _, err := db.Exec(`UPDATE backlog_items SET uuid = ? WHERE id = ? AND uuid = ''`, anchor.String(), id); err != nil {
			return fmt.Errorf("db: ensure sdd uuids: update backlog_items %s: %w", id, err)
		}
	}
	return nil
}

// specKey identifies a specs row by its actual primary key.
type specKey struct {
	project string
	id      string
}

// fillMissingSpecUUIDs assigns a fresh UUIDv7 to every specs row whose uuid
// column is still empty. specs' primary key is the COMPOSITE (project, id)
// since migration 005 — id alone is not unique across projects, so an
// UPDATE keyed only on id could silently anchor every project's "SPEC-001"
// to the same row. Both columns are read and used in the WHERE clause for
// exactly that reason.
func fillMissingSpecUUIDs(db *sql.DB) error {
	var hasMissing bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM specs WHERE uuid = '' LIMIT 1)`).Scan(&hasMissing); err != nil {
		return fmt.Errorf("db: ensure sdd uuids: check specs: %w", err)
	}
	if !hasMissing {
		return nil
	}

	rows, err := db.Query(`SELECT project, id FROM specs WHERE uuid = ''`)
	if err != nil {
		return fmt.Errorf("db: ensure sdd uuids: select specs: %w", err)
	}

	var keys []specKey
	for rows.Next() {
		var k specKey
		if err := rows.Scan(&k.project, &k.id); err != nil {
			_ = rows.Close()
			return fmt.Errorf("db: ensure sdd uuids: scan specs: %w", err)
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("db: ensure sdd uuids: iterate specs: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("db: ensure sdd uuids: close specs rows: %w", err)
	}

	for _, k := range keys {
		anchor, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("db: ensure sdd uuids: generate uuid for specs %s/%s: %w", k.project, k.id, err)
		}
		if _, err := db.Exec(
			`UPDATE specs SET uuid = ? WHERE project = ? AND id = ? AND uuid = ''`,
			anchor.String(), k.project, k.id,
		); err != nil {
			return fmt.Errorf("db: ensure sdd uuids: update specs %s/%s: %w", k.project, k.id, err)
		}
	}
	return nil
}
