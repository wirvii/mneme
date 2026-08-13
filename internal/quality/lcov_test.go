package quality

import (
	"errors"
	"testing"
)

// TestLcovParser_ValidDocument covers the DA:-count half of AC7: a
// well-formed document with several DA: records is parsed with the right
// line/hit set.
func TestLcovParser_ValidDocument(t *testing.T) {
	doc := `TN:
SF:a.go
FN:1,main
FNDA:1,main
DA:1,1
DA:2,0
DA:3,5
BRDA:2,0,0,1
LF:3
LH:2
end_of_record
`
	p, err := ParseProfile("lcov", []byte(doc))
	if err != nil {
		t.Fatalf("ParseProfile: %v", err)
	}
	fc, ok := p.Files["a.go"]
	if !ok {
		t.Fatal("expected a.go in profile")
	}
	if !fc.Covered(1) {
		t.Error("line 1: want covered")
	}
	if fc.Covered(2) {
		t.Error("line 2 (DA:2,0): want NOT covered")
	}
	if !fc.Covered(3) {
		t.Error("line 3: want covered")
	}
}

// TestLcovParser_MalformedDA covers the "DA:12,abc" case of AC7.
func TestLcovParser_MalformedDA(t *testing.T) {
	doc := "SF:a.go\nDA:12,abc\nend_of_record\n"
	_, err := ParseProfile("lcov", []byte(doc))
	if !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("ParseProfile(malformed DA) error = %v, want ErrInvalidProfile", err)
	}
}

// TestLcovParser_EmptySF covers "SF: sin valor -> ErrInvalidProfile" of AC7.
func TestLcovParser_EmptySF(t *testing.T) {
	doc := "SF:\nDA:1,1\nend_of_record\n"
	_, err := ParseProfile("lcov", []byte(doc))
	if !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("ParseProfile(empty SF) error = %v, want ErrInvalidProfile", err)
	}
}

// TestLcovParser_UnknownDirectivesIgnored is the paired "fila hermana" of
// AC7: a document WITHOUT the unknown/tool-specific directives must produce
// the identical result to one WITH them scattered throughout.
func TestLcovParser_UnknownDirectivesIgnored(t *testing.T) {
	withExtras := `TN:suite
SF:a.go
FN:1,main
FNDA:3,main
DA:1,1
DA:2,0
BRDA:2,0,0,1
LF:2
LH:1
BRF:1
end_of_record
`
	withoutExtras := `SF:a.go
DA:1,1
DA:2,0
end_of_record
`
	p1, err := ParseProfile("lcov", []byte(withExtras))
	if err != nil {
		t.Fatalf("ParseProfile(withExtras): %v", err)
	}
	p2, err := ParseProfile("lcov", []byte(withoutExtras))
	if err != nil {
		t.Fatalf("ParseProfile(withoutExtras): %v", err)
	}
	if p1.Files["a.go"].Covered(1) != p2.Files["a.go"].Covered(1) ||
		p1.Files["a.go"].Covered(2) != p2.Files["a.go"].Covered(2) {
		t.Fatal("unknown directives changed the parsed result — they must be ignored without effect")
	}
}

// TestLcovParser_LyingSummariesIgnored is the paired case for LF:/LH:: a
// document whose LF:/LH: summary claims 100% while its DA: records say 50%
// must still report 50% — the summary is NEVER read (D9).
func TestLcovParser_LyingSummariesIgnored(t *testing.T) {
	lying := `SF:a.go
DA:1,1
DA:2,0
LF:2
LH:2
end_of_record
`
	honest := `SF:a.go
DA:1,1
DA:2,0
LF:2
LH:1
end_of_record
`
	pLying, err := ParseProfile("lcov", []byte(lying))
	if err != nil {
		t.Fatalf("ParseProfile(lying): %v", err)
	}
	pHonest, err := ParseProfile("lcov", []byte(honest))
	if err != nil {
		t.Fatalf("ParseProfile(honest): %v", err)
	}
	if pLying.Files["a.go"].Covered(2) {
		t.Fatal("line 2 has DA:2,0 — a lying LF:/LH: summary must not make it covered")
	}
	if pLying.Files["a.go"].Covered(2) != pHonest.Files["a.go"].Covered(2) {
		t.Fatal("LF:/LH: must never influence the per-line result")
	}
}

// TestLcovParser_MergedRecords_SameLineDifferentHits covers the "misma
// linea con DA:7,0 y DA:7,3 -> cubierta" row of AC7, expressed as two
// SF:/end_of_record blocks for the same file — the merged-profile case D9
// names explicitly.
func TestLcovParser_MergedRecords_SameLineDifferentHits(t *testing.T) {
	merged := `SF:a.go
DA:7,0
end_of_record
SF:a.go
DA:7,3
end_of_record
`
	p, err := ParseProfile("lcov", []byte(merged))
	if err != nil {
		t.Fatalf("ParseProfile: %v", err)
	}
	if !p.Files["a.go"].Covered(7) {
		t.Fatal("merged records DA:7,0 + DA:7,3: want covered")
	}

	bothZero := `SF:a.go
DA:7,0
end_of_record
SF:a.go
DA:7,0
end_of_record
`
	p2, err := ParseProfile("lcov", []byte(bothZero))
	if err != nil {
		t.Fatalf("ParseProfile: %v", err)
	}
	if p2.Files["a.go"].Covered(7) {
		t.Fatal("merged records DA:7,0 + DA:7,0: want NOT covered")
	}
}

// TestLcovParser_UnknownFormat is the last row of AC7.
func TestLcovParser_UnknownFormat(t *testing.T) {
	_, err := ParseProfile("not-a-format", []byte("SF:a.go\nDA:1,1\nend_of_record\n"))
	if !errors.Is(err, ErrUnknownFormat) {
		t.Fatalf("ParseProfile(unknown format) error = %v, want ErrUnknownFormat", err)
	}
}
