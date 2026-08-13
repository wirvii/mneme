package quality

import (
	"errors"
	"testing"
)

// TestGoCoverParser_MissingModeHeader covers AC8's anchor row: no `mode:`
// line at all is ErrInvalidProfile — this is exactly what makes declaring
// `format = "go-cover"` over an LCOV document ruidoso rather than silent
// (D18).
func TestGoCoverParser_MissingModeHeader(t *testing.T) {
	_, err := ParseProfile("go-cover", []byte("f.go:10.2,14.3 3 1\n"))
	if !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("ParseProfile(no mode header) error = %v, want ErrInvalidProfile", err)
	}
}

// TestGoCoverParser_LCOVDeclaredAsGoCover is AC8's hermana for the header
// case, expressed with a real LCOV document mistakenly declared go-cover.
func TestGoCoverParser_LCOVDeclaredAsGoCover(t *testing.T) {
	lcovDoc := "SF:a.go\nDA:1,1\nend_of_record\n"
	_, err := ParseProfile("go-cover", []byte(lcovDoc))
	if !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("ParseProfile(LCOV as go-cover) error = %v, want ErrInvalidProfile", err)
	}
}

// TestGoCoverParser_NonNumericColumns covers "un registro con columnas no
// numericas -> ErrInvalidProfile".
func TestGoCoverParser_NonNumericColumns(t *testing.T) {
	doc := "mode: count\nf.go:ten.2,14.3 3 1\n"
	_, err := ParseProfile("go-cover", []byte(doc))
	if !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("ParseProfile(non-numeric column) error = %v, want ErrInvalidProfile", err)
	}
}

// TestGoCoverParser_BlockExpansion covers the block-marking row of AC8: a
// block `f.go:10.2,14.3 3 1` marks lines 10..14 instrumented AND covered;
// the sibling row with count=0 marks them instrumented but NOT covered.
func TestGoCoverParser_BlockExpansion(t *testing.T) {
	covered := "mode: count\nf.go:10.2,14.3 3 1\n"
	p, err := ParseProfile("go-cover", []byte(covered))
	if err != nil {
		t.Fatalf("ParseProfile: %v", err)
	}
	fc := p.Files["f.go"]
	for l := 10; l <= 14; l++ {
		if !fc.Instrumented(l) {
			t.Errorf("line %d: want instrumented", l)
		}
		if !fc.Covered(l) {
			t.Errorf("line %d: want covered", l)
		}
	}

	uncovered := "mode: count\nf.go:10.2,14.3 3 0\n"
	p2, err := ParseProfile("go-cover", []byte(uncovered))
	if err != nil {
		t.Fatalf("ParseProfile: %v", err)
	}
	fc2 := p2.Files["f.go"]
	for l := 10; l <= 14; l++ {
		if !fc2.Instrumented(l) {
			t.Errorf("line %d (count=0): want instrumented", l)
		}
		if fc2.Covered(l) {
			t.Errorf("line %d (count=0): want NOT covered", l)
		}
	}
}

// TestGoCoverParser_OverlappingBlocks covers "dos bloques solapados sobre
// la misma linea, uno con 0 y otro con >0 -> cubierta" (and its hermana:
// both at 0 -> not covered).
func TestGoCoverParser_OverlappingBlocks(t *testing.T) {
	mixed := "mode: count\nf.go:1.1,5.1 2 0\nf.go:3.1,3.1 1 4\n"
	p, err := ParseProfile("go-cover", []byte(mixed))
	if err != nil {
		t.Fatalf("ParseProfile: %v", err)
	}
	if !p.Files["f.go"].Covered(3) {
		t.Fatal("line 3 covered by overlapping block with count>0: want covered")
	}

	bothZero := "mode: count\nf.go:1.1,5.1 2 0\nf.go:3.1,3.1 1 0\n"
	p2, err := ParseProfile("go-cover", []byte(bothZero))
	if err != nil {
		t.Fatalf("ParseProfile: %v", err)
	}
	if p2.Files["f.go"].Covered(3) {
		t.Fatal("line 3 covered by two zero-count overlapping blocks: want NOT covered")
	}
}

// TestGoCoverParser_PathKeepsImportPrefix covers "las rutas conservan su
// prefijo de import y no se tocan en el parser" — NormalizeSourcePath
// (coverage.go) is what reconciles this, not the parser.
func TestGoCoverParser_PathKeepsImportPrefix(t *testing.T) {
	doc := "mode: count\ngithub.com/wirvii/mneme/internal/quality/git.go:10.1,10.1 1 1\n"
	p, err := ParseProfile("go-cover", []byte(doc))
	if err != nil {
		t.Fatalf("ParseProfile: %v", err)
	}
	if _, ok := p.Files["github.com/wirvii/mneme/internal/quality/git.go"]; !ok {
		t.Fatalf("expected path to keep its import prefix verbatim, got files: %v", p.Files)
	}
}
