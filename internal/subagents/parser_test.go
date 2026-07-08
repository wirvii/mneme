package subagents

import "testing"

func TestParseGenerated(t *testing.T) {
	raw := "# Backend Agent\n\nSome intro prose.\n\n## Reglas\n\nRegla uno.\nRegla dos.\n\n## Workflow\n\n1. Paso uno\n2. Paso dos\n"

	got, err := ParseGenerated(raw)
	if err != nil {
		t.Fatalf("ParseGenerated: %v", err)
	}
	if got.Title != "Backend Agent" {
		t.Errorf("Title = %q, want %q", got.Title, "Backend Agent")
	}
	if len(got.Sections) != 2 {
		t.Fatalf("expected 2 sections, got %d: %+v", len(got.Sections), got.Sections)
	}
	if got.Sections[0].Heading != "Reglas" || got.Sections[0].Content != "Regla uno.\nRegla dos." {
		t.Errorf("unexpected section 0: %+v", got.Sections[0])
	}
	if got.Sections[1].Heading != "Workflow" {
		t.Errorf("unexpected section 1 heading: %q", got.Sections[1].Heading)
	}
}

func TestParseGenerated_StripsWrappingCodeFence(t *testing.T) {
	raw := "```markdown\n# Title\n\n## Section\n\nBody.\n```"

	got, err := ParseGenerated(raw)
	if err != nil {
		t.Fatalf("ParseGenerated: %v", err)
	}
	if got.Title != "Title" {
		t.Errorf("Title = %q, want %q", got.Title, "Title")
	}
	if len(got.Sections) != 1 || got.Sections[0].Content != "Body." {
		t.Errorf("unexpected sections: %+v", got.Sections)
	}
}

func TestParseGenerated_MidDocumentFenceIsPreserved(t *testing.T) {
	raw := "# Title\n\n## Example\n\nHere is code:\n\n```go\nfunc main() {}\n```\n"

	got, err := ParseGenerated(raw)
	if err != nil {
		t.Fatalf("ParseGenerated: %v", err)
	}
	want := "Here is code:\n\n```go\nfunc main() {}\n```"
	if got.Sections[0].Content != want {
		t.Errorf("Content = %q, want %q", got.Sections[0].Content, want)
	}
}

func TestParseGenerated_EmptyAfterStripping(t *testing.T) {
	_, err := ParseGenerated("```markdown\n```")
	if err == nil {
		t.Fatal("expected error for empty content after stripping fences")
	}
}

func TestParseGenerated_MissingTitle(t *testing.T) {
	_, err := ParseGenerated("no title here, just prose\n\n## Section\n\nbody\n")
	if err == nil {
		t.Fatal("expected error for missing H1 title")
	}
}

func TestParseGenerated_NoSectionsIsNotAnError(t *testing.T) {
	got, err := ParseGenerated("# Title\n\nJust some prose, no H2 sections at all.\n")
	if err != nil {
		t.Fatalf("ParseGenerated: %v", err)
	}
	if len(got.Sections) != 0 {
		t.Errorf("expected no sections, got %+v", got.Sections)
	}
}
