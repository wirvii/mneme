package codegraph

import (
	"database/sql"
	"fmt"
	"os"

	// Register the sqlite driver. modernc.org/sqlite is a pure-Go transpilation
	// of SQLite with FTS5 compiled in by default — no CGO, no C compiler, no
	// build tags required.
	_ "modernc.org/sqlite"
)

// ProbeGraph reports whether a codegraph DB at path has at least one node and
// the timestamp of the most recent node update (for staleness). It opens the DB
// read-only and is safe to call from the pre-tool-use hook. Returns
// hasNodes=false and a nil error when the file is absent or empty.
//
// The function is intentionally minimal: it executes only two lightweight
// queries ("SELECT 1 FROM nodes LIMIT 1" and "SELECT MAX(updated_at) FROM
// nodes") and immediately closes the connection. It never writes to the DB.
//
// lastUpdatedUnixMs is the value of MAX(nodes.updated_at), which the schema
// stores as a Unix epoch in milliseconds. Returns 0 when there are no nodes.
func ProbeGraph(path string) (hasNodes bool, lastUpdatedUnixMs int64, err error) {
	// Fast path: if the file does not exist, return without error (fail-open).
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		return false, 0, nil
	}

	dsn := fmt.Sprintf(
		"file:%s?mode=ro&_pragma=foreign_keys(ON)&_pragma=busy_timeout(1000)",
		path,
	)

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return false, 0, fmt.Errorf("codegraph: probe: open: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()

	if err = sqlDB.Ping(); err != nil {
		return false, 0, fmt.Errorf("codegraph: probe: ping: %w", err)
	}

	// Check whether any node exists.
	var dummy int
	err = sqlDB.QueryRow(`SELECT 1 FROM nodes LIMIT 1`).Scan(&dummy)
	if err == sql.ErrNoRows {
		// Table exists but is empty.
		return false, 0, nil
	}
	if err != nil {
		return false, 0, fmt.Errorf("codegraph: probe: check nodes: %w", err)
	}

	// At least one node exists; fetch the staleness timestamp.
	var maxUpdated sql.NullInt64
	if err = sqlDB.QueryRow(`SELECT MAX(updated_at) FROM nodes`).Scan(&maxUpdated); err != nil {
		return false, 0, fmt.Errorf("codegraph: probe: max updated_at: %w", err)
	}

	if maxUpdated.Valid {
		lastUpdatedUnixMs = maxUpdated.Int64
	}

	return true, lastUpdatedUnixMs, nil
}
