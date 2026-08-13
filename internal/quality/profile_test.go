package quality

import (
	"errors"
	"reflect"
	"testing"
)

// TestFormats_IsSortedRegistrySnapshot covers half of AC6: Formats() is the
// single source of truth Parse (constitution.go) will consult — here it is
// pinned to the exact registered set, sorted, so a future format addition
// is a one-line diff to this list rather than a silent behavior change.
func TestFormats_IsSortedRegistrySnapshot(t *testing.T) {
	got := Formats()
	want := []string{"go-cover", "lcov"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Formats() = %v, want %v", got, want)
	}
}

// TestParseProfile_UnknownFormat covers ErrUnknownFormat: a format not in
// the registry is rejected, never guessed from the bytes (D18).
func TestParseProfile_UnknownFormat(t *testing.T) {
	_, err := ParseProfile("cobertura", []byte("whatever"))
	if !errors.Is(err, ErrUnknownFormat) {
		t.Fatalf("ParseProfile(unknown format) error = %v, want ErrUnknownFormat", err)
	}
}

// TestParseProfile_DispatchesToRegisteredParser is the hermana pasa of the
// unknown-format rejection above, on the SAME entry point: a registered
// format dispatches to its parser and returns a usable Profile.
func TestParseProfile_DispatchesToRegisteredParser(t *testing.T) {
	p, err := ParseProfile("lcov", []byte("SF:a.go\nDA:1,1\nend_of_record\n"))
	if err != nil {
		t.Fatalf("ParseProfile(lcov): %v", err)
	}
	if !p.Files["a.go"].Covered(1) {
		t.Fatal("expected a.go line 1 covered")
	}
}

// TestFileCoverage_MergeLine_TakesMax covers the shared merge rule (D9): the
// SAME line observed with hits=0 and then hits>0 (in either order) ends up
// covered — never overwritten by "the last record wins".
func TestFileCoverage_MergeLine_TakesMax(t *testing.T) {
	var fc FileCoverage
	fc.mergeLine(7, 0)
	fc.mergeLine(7, 3)
	if !fc.Covered(7) {
		t.Error("line 7 with hits 0 then 3: want covered")
	}

	var fcZero FileCoverage
	fcZero.mergeLine(7, 0)
	fcZero.mergeLine(7, 0)
	if fcZero.Covered(7) {
		t.Error("line 7 with hits 0 then 0: want NOT covered")
	}

	var fcReverse FileCoverage
	fcReverse.mergeLine(7, 3)
	fcReverse.mergeLine(7, 0)
	if !fcReverse.Covered(7) {
		t.Error("line 7 with hits 3 then 0: want covered (order must not matter)")
	}
}

// TestFileCoverage_Instrumented_vs_Covered pins the distinction G7's
// mutation (whole "cubierta si algun registro > 0" rule) protects: a line
// present with hits=0 is instrumented but not covered; an absent line is
// neither.
func TestFileCoverage_Instrumented_vs_Covered(t *testing.T) {
	var fc FileCoverage
	fc.mergeLine(5, 0)

	if !fc.Instrumented(5) {
		t.Error("line 5 has a record: want Instrumented true")
	}
	if fc.Covered(5) {
		t.Error("line 5 hits=0: want Covered false")
	}
	if fc.Instrumented(99) {
		t.Error("line 99 has no record: want Instrumented false")
	}
	if fc.Covered(99) {
		t.Error("line 99 has no record: want Covered false")
	}
}
