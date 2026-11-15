package model

// SynthesisSweepRequest drives `mneme mem synthesis-sweep` (SPEC-187). The
// data directory is not part of it: the service reads it from the loaded
// configuration, to which the CLI has already applied --data-dir, so there is
// a single source for where the databases live.
type SynthesisSweepRequest struct {
	// Project limits the sweep to one project database. It accepts both slug
	// forms ("org/repo" as stored in memories.project, "org-repo" as used in
	// the file name). Empty means every project database plus global.db.
	Project string `json:"project,omitempty"`

	// Apply retires the syntheses found (soft delete). False is a dry run
	// that changes nothing, the default because retiring is not reversible
	// other than by regenerating.
	Apply bool `json:"apply"`
}

// SynthesisSweepRow is the measure of one (database, project) pair. Counts
// cover synthesis rows that are not soft-deleted, including those a dedup
// marked as superseded: they still take space and still count for nothing
// a person wrote.
type SynthesisSweepRow struct {
	// Project is the memories.project value; empty for rows without one.
	Project string `json:"project"`

	// Database is the file path relative to the data directory, with forward
	// slashes so the output is the same on every OS.
	Database string `json:"database"`

	// Synthesis is the number of active synthesis rows.
	Synthesis int `json:"synthesis"`

	// NeverAccessed is how many of them were never read (access_count = 0),
	// the measure of how much nobody used them.
	NeverAccessed int `json:"never_accessed"`

	// Bytes is the size of their title plus content, counted in bytes and
	// not in characters, because bytes are what the context window pays.
	Bytes int64 `json:"bytes"`

	// Retired is the number of rows the apply step soft-deleted; always 0 in
	// a dry run.
	Retired int `json:"retired"`
}

// SynthesisSweepError records a database the sweep could not read or
// update. The sweep keeps going with the other databases, so a locked or
// corrupt file does not hide the measure of the rest.
type SynthesisSweepError struct {
	// Database is the file path relative to the data directory.
	Database string `json:"database"`

	// Error is the failure message.
	Error string `json:"error"`
}

// SynthesisSweepReport is the full result of one sweep: what was measured,
// what was retired and what failed.
type SynthesisSweepReport struct {
	// Apply mirrors the request: true when rows were retired.
	Apply bool `json:"apply"`

	// Rows holds one entry per (database, project), in database order and
	// then by project.
	Rows []SynthesisSweepRow `json:"rows"`

	// Errors lists the databases that failed; omitted when none did.
	Errors []SynthesisSweepError `json:"errors,omitempty"`
}

// Totals sums every row into one, so the CLI can print the TOTAL line and a
// caller can tell at a glance how much the sweep found. Project and
// Database are left empty.
func (r SynthesisSweepReport) Totals() SynthesisSweepRow {
	var t SynthesisSweepRow
	for _, row := range r.Rows {
		t.Synthesis += row.Synthesis
		t.NeverAccessed += row.NeverAccessed
		t.Bytes += row.Bytes
		t.Retired += row.Retired
	}
	return t
}
