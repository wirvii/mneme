// Package sddfile owns the on-disk record format for the SDD git-native
// mechanism (SPEC-130 §2a): serialization, deserialization, escape,
// round-trip verification, paths, schema versioning, the enable/disable
// marker, and atomic read/write of a single record. It is a sibling of
// internal/vault, not an extension of it — see BacklogPath's godoc for why.
//
// This package imports only the standard library plus internal/model (the
// same perimeter internal/vault has, V1 of SPEC-130's design). It is
// deliberately PURE with respect to the environment: it never resolves the
// working directory, never reads HOME, never execs git, and never imports
// internal/gitident. Every path it touches is a parameter its caller
// supplies (D38) — see leaf_test.go for the guardian that enforces this.
package sddfile

import (
	"errors"
	"fmt"
)

const (
	// MinFileSchema is the oldest record schema this mneme can read.
	MinFileSchema = 1

	// CurrentFileSchema is the schema this mneme WRITES. A record with no
	// schema field at all is treated as schema 1 (D28) — nobody types a
	// version number by hand.
	//
	// The comparison against a record's schema is a RANGE
	// (< MinFileSchema || > CurrentFileSchema) from day one, never
	// equality. SPEC-116 had to correct exactly this mistake in
	// internal/quality after shipping an equality check that would have
	// bricked every existing constitution on the next schema bump — this
	// package starts from the corrected shape instead of repeating that
	// history.
	CurrentFileSchema = 1
)

// ErrSchemaOutOfRange is the sentinel a caller can match with errors.Is when
// a record's schema falls outside [MinFileSchema, CurrentFileSchema]. The
// wrapped message names which side of the range was violated and what to do
// about it.
var ErrSchemaOutOfRange = errors.New("sddfile: schema out of range")

// checkSchema validates a record's declared schema against the supported
// range. A schema NEWER than CurrentFileSchema means the record was written
// by a newer mneme — this one refuses to guess at unknown sections rather
// than silently dropping them on the next rewrite (D28's data-loss
// argument: a lossy round trip through an old reader is worse than a
// refusal). A schema OLDER than MinFileSchema cannot happen today
// (Min == Current == 1) but the check is written as a genuine range so a
// future MinFileSchema bump does not require touching this function.
func checkSchema(v int) error {
	if v > CurrentFileSchema {
		return fmt.Errorf(
			"sddfile: record schema %d is newer than this mneme understands (max %d) — "+
				"written by a newer mneme; upgrade before reading this file: %w",
			v, CurrentFileSchema, ErrSchemaOutOfRange)
	}
	if v < MinFileSchema {
		return fmt.Errorf(
			"sddfile: record schema %d is older than supported (min %d): %w",
			v, MinFileSchema, ErrSchemaOutOfRange)
	}
	return nil
}
