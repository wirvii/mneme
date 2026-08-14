package quality

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestVisualFormats_IsSortedRegistrySnapshot pins VisualFormats() to the
// exact registered set — a future format addition is a one-line diff to
// this list, mirroring TestMutantFormats_IsSortedRegistrySnapshot.
//
// UNLIKE mutants.go's registry (two formats: mutants-v1 + gremlins), this
// one has exactly ONE entry (D2): no native browser-harness format is
// registered, because the reason S2/S5 registered a native format —
// otherwise THIS repository could never exercise the chain at all — does
// not apply here. The gap is the absence of a graphical interface, not a
// format (D15), and a native format would not close it.
func TestVisualFormats_IsSortedRegistrySnapshot(t *testing.T) {
	got := VisualFormats()
	want := []string{"visual-v1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("VisualFormats() = %v, want %v", got, want)
	}
}

// TestParseVisualReport_UnknownFormat covers ErrUnknownVisualFormat: a
// format not in the registry is rejected, never guessed from the bytes.
func TestParseVisualReport_UnknownFormat(t *testing.T) {
	_, err := ParseVisualReport("not-a-format", []byte("whatever"))
	if !errors.Is(err, ErrUnknownVisualFormat) {
		t.Fatalf("ParseVisualReport(unknown format) error = %v, want ErrUnknownVisualFormat", err)
	}
}

// TestParseVisualReport_DispatchesToRegisteredParser is the hermana pasa: a
// registered format dispatches to its parser, over a fixture that exercises
// EVERY field this model has: two targets, one clean-and-rendered with an
// a11y block, one non-rendered with page_errors and NO a11y block at all
// (P1 point 5's Reported==false case).
func TestParseVisualReport_DispatchesToRegisteredParser(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "visual", "visual-v1-full.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	report, err := ParseVisualReport("visual-v1", raw)
	if err != nil {
		t.Fatalf("ParseVisualReport(visual-v1): %v", err)
	}
	if len(report.Targets) != 2 {
		t.Fatalf("len(report.Targets) = %d, want 2", len(report.Targets))
	}

	light := report.Targets[0]
	if !light.Rendered || light.Error != "" {
		t.Errorf("home-light-360: rendered=%v error=%q, want rendered=true error=\"\"", light.Rendered, light.Error)
	}
	if !light.A11y.Reported || len(light.A11y.Violations) != 1 {
		t.Errorf("home-light-360: a11y.Reported=%v violations=%d, want Reported=true, 1 violation", light.A11y.Reported, len(light.A11y.Violations))
	}

	dark := report.Targets[1]
	if dark.Rendered || dark.Error == "" {
		t.Errorf("home-dark-360: rendered=%v error=%q, want rendered=false, non-empty error", dark.Rendered, dark.Error)
	}
	if dark.A11y.Reported {
		t.Errorf("home-dark-360: a11y.Reported=true, want false (no a11y block declared)")
	}
	if len(dark.PageErrors) != 1 {
		t.Errorf("home-dark-360: len(page_errors)=%d, want 1", len(dark.PageErrors))
	}
}

// TestVisualV1Parser_Strict covers AC8: visual-v1 is as strict as any other
// parser in this leaf. Table-driven, with the two mandatory hermanas at the
// bottom (zero targets OK — P1 point 2 — and a fully-populated document with
// all four a11y impacts OK).
func TestVisualV1Parser_Strict(t *testing.T) {
	tests := []struct {
		name    string
		doc     string
		wantErr bool
	}{
		{
			name:    "unknown key",
			doc:     `{"schema":"visual-v1","targets":[],"bogus":true}`,
			wantErr: true,
		},
		{
			name:    "wrong schema",
			doc:     `{"schema":"visual-v0","targets":[]}`,
			wantErr: true,
		},
		{
			name:    "target missing id",
			doc:     `{"schema":"visual-v1","targets":[{"rendered":true,"error":""}]}`,
			wantErr: true,
		},
		{
			name:    "duplicate target id",
			doc:     `{"schema":"visual-v1","targets":[{"id":"a","rendered":true,"error":""},{"id":"a","rendered":true,"error":""}]}`,
			wantErr: true,
		},
		{
			// G7a: rendered=false requires a non-empty error.
			name:    "rendered false with empty error",
			doc:     `{"schema":"visual-v1","targets":[{"id":"a","rendered":false,"error":""}]}`,
			wantErr: true,
		},
		{
			// G7b: rendered=true prohibits a non-empty error — the opposite
			// hermana of the row above, over the SAME fixture shape.
			name:    "rendered true with non-empty error",
			doc:     `{"schema":"visual-v1","targets":[{"id":"a","rendered":true,"error":"boom"}]}`,
			wantErr: true,
		},
		{
			name:    "rendered false with non-empty error is valid",
			doc:     `{"schema":"visual-v1","targets":[{"id":"a","rendered":false,"error":"boom"}]}`,
			wantErr: false,
		},
		{
			name:    "rendered true with empty error is valid",
			doc:     `{"schema":"visual-v1","targets":[{"id":"a","rendered":true,"error":""}]}`,
			wantErr: false,
		},
		{
			// G6a: an impact outside the closed vocabulary is rejected,
			// enumerating the four accepted values.
			name:    "impact outside vocabulary",
			doc:     `{"schema":"visual-v1","targets":[{"id":"a","rendered":true,"error":"","a11y":{"engine":"axe","engine_version":"1","violations":[{"rule":"x","impact":"catastrophic","nodes":1}]}}]}`,
			wantErr: true,
		},
		{
			name:    "a11y missing engine",
			doc:     `{"schema":"visual-v1","targets":[{"id":"a","rendered":true,"error":"","a11y":{"engine_version":"1","violations":[]}}]}`,
			wantErr: true,
		},
		{
			name:    "negative console count",
			doc:     `{"schema":"visual-v1","targets":[{"id":"a","rendered":true,"error":"","console":{"error":-1,"warning":0,"info":0}}]}`,
			wantErr: true,
		},
		{
			name:    "zero targets parses OK — not this parser's error to raise",
			doc:     `{"schema":"visual-v1","targets":[]}`,
			wantErr: false,
		},
		{
			name: "all four a11y impacts parse OK",
			doc: `{"schema":"visual-v1","targets":[{"id":"a","rendered":true,"error":"",` +
				`"a11y":{"engine":"axe","engine_version":"1","violations":[` +
				`{"rule":"r1","impact":"critical","nodes":1},` +
				`{"rule":"r2","impact":"serious","nodes":1},` +
				`{"rule":"r3","impact":"moderate","nodes":1},` +
				`{"rule":"r4","impact":"minor","nodes":1}` +
				`]}}]}`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseVisualReport("visual-v1", []byte(tt.doc))
			if tt.wantErr && !errors.Is(err, ErrInvalidVisualReport) {
				t.Fatalf("ParseVisualReport() error = %v, want ErrInvalidVisualReport", err)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("ParseVisualReport() unexpected error: %v", err)
			}
		})
	}
}
