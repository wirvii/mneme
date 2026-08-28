package sddfile

import (
	"os"
	"path/filepath"
)

// MaxBacklogID returns the highest backlog correlative number found by
// NAME alone under <repoRoot>/.mneme/sdd/backlog/ (SPEC-131 D55/D21):
// nBackID never opens a single file — it only reads directory entry names —
// so it is immune to a broken record (D22) and cheap enough to run on
// every BacklogAdd when the mechanism is on.
//
// Returns 0 when the directory does not exist or contains no validly-named
// file — the caller combines this with the database's own NextBacklogID to
// take MAX(base, files), so 0 is a safe "nothing here" value: a real
// database is always at least BL-001 once anything exists.
//
// A name that does not match BL-<digits>.md is ignored, never counted and
// never causing an error — the filename is what reserves the number (D4),
// so a file mneme did not name is not this function's concern.
func MaxBacklogID(repoRoot string) (int, error) {
	dir := filepath.Join(RootDir(repoRoot), "backlog")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	max := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		id, ok := backlogIDFromFilename(e.Name())
		if !ok {
			continue
		}
		if n, numOK := correlativeNumber(id, "BL-"); numOK && n > max {
			max = n
		}
	}
	return max, nil
}

// MaxSpecID is MaxBacklogID's sibling for specs: the highest number found
// among DIRECTORY names under <repoRoot>/.mneme/sdd/specs/, regardless of
// whether that directory holds a record.md yet. A directory reserves the
// number the moment it exists (D4) — its content, or lack of it, is not
// this function's concern, and it never opens a single file.
func MaxSpecID(repoRoot string) (int, error) {
	dir := filepath.Join(RootDir(repoRoot), "specs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	max := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if !isValidCorrelative(name, "SPEC-") {
			continue
		}
		if n, ok := correlativeNumber(name, "SPEC-"); ok && n > max {
			max = n
		}
	}
	return max, nil
}

// correlativeNumber parses the digits following prefix in id into an int.
// Both callers have already validated the shape via isValidCorrelative /
// backlogIDFromFilename, so a parse failure here can only mean an
// unreasonably large number of digits — treated as "not a match" rather
// than propagated, since this package never fails a numbering scan over a
// single odd filename (D22's spirit applied to names, not content).
func correlativeNumber(id, prefix string) (int, bool) {
	rest := id[len(prefix):]
	n := 0
	for _, r := range rest {
		d := int(r - '0')
		// Guard against overflow on a pathological number of digits rather
		// than wrapping silently.
		if n > (1<<62)/10 {
			return 0, false
		}
		n = n*10 + d
	}
	return n, true
}
