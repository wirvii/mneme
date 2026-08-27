package speech

import (
	"errors"
	"strings"
	"testing"
)

func TestClean(t *testing.T) {
	got := Clean("# Resultado\n\n**Listo**: [archivo](https://example.test).\n```go\nfmt.Println(1)\n```")
	if got != "Resultado Listo: archivo." {
		t.Fatalf("Clean() = %q", got)
	}
}

func TestValidateEmit(t *testing.T) {
	tests := []struct {
		name        string
		disposition Disposition
		mode        Mode
		text        string
		wantErr     error
	}{
		{"brief", DispositionEmit, ModeBrief, "Terminé.", nil},
		{"skip", DispositionSkip, ModeBrief, "", nil},
		{"skip with text", DispositionSkip, ModeBrief, "do not read", ErrTextForbidden},
		{"empty", DispositionEmit, ModeBrief, "```go\nx\n```", ErrTextRequired},
		{"bad mode", DispositionEmit, Mode("long"), "x", ErrInvalidMode},
		{"too long", DispositionEmit, ModeBrief, strings.Repeat("á", BriefLimit+1), ErrTextTooLong},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateEmit(tc.disposition, tc.mode, tc.text)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestOriginPrefix(t *testing.T) {
	tests := []struct {
		name     string
		language string
		label    string
		want     string
	}{
		{"empty label", "es", "", ""},
		{"spanish", "es", "mneme", "En mneme: "},
		{"spanish region", "es-MX", "mneme", "En mneme: "},
		{"english", "en", "mneme", "From mneme: "},
		{"empty language", "", "mneme", "From mneme: "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := OriginPrefix(tc.language, tc.label); got != tc.want {
				t.Fatalf("OriginPrefix(%q, %q) = %q, want %q", tc.language, tc.label, got, tc.want)
			}
		})
	}
}

func TestSpokenLabel(t *testing.T) {
	tests := []struct {
		name string
		slug string
		want string
	}{
		{"empty", "", ""},
		{"with slash", "wirvii/mneme", "mneme"},
		{"without slash", "mneme", "mneme"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SpokenLabel(tc.slug); got != tc.want {
				t.Fatalf("SpokenLabel(%q) = %q, want %q", tc.slug, got, tc.want)
			}
		})
	}
}
