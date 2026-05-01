package wikilink

import (
	"testing"
)

// TestParse_Simple verifies that [[topic]] extracts a Link with only Topic set.
func TestParse_Simple(t *testing.T) {
	links := Parse("See [[topic]] for details.")
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1", len(links))
	}
	if links[0].Topic != "topic" {
		t.Errorf("Topic: got %q, want %q", links[0].Topic, "topic")
	}
	if links[0].Anchor != "" {
		t.Errorf("Anchor: got %q, want empty", links[0].Anchor)
	}
	if links[0].Alias != "" {
		t.Errorf("Alias: got %q, want empty", links[0].Alias)
	}
}

// TestParse_WithAnchor verifies that [[topic#section]] captures both topic and anchor.
func TestParse_WithAnchor(t *testing.T) {
	links := Parse("See [[auth/jwt#token-format]] for details.")
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1", len(links))
	}
	if links[0].Topic != "auth/jwt" {
		t.Errorf("Topic: got %q, want %q", links[0].Topic, "auth/jwt")
	}
	if links[0].Anchor != "token-format" {
		t.Errorf("Anchor: got %q, want %q", links[0].Anchor, "token-format")
	}
}

// TestParse_WithAlias verifies that [[topic|Display]] captures topic and alias.
func TestParse_WithAlias(t *testing.T) {
	links := Parse("Read [[architecture/auth|Auth System]] for context.")
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1", len(links))
	}
	if links[0].Topic != "architecture/auth" {
		t.Errorf("Topic: got %q, want %q", links[0].Topic, "architecture/auth")
	}
	if links[0].Alias != "Auth System" {
		t.Errorf("Alias: got %q, want %q", links[0].Alias, "Auth System")
	}
	if links[0].Anchor != "" {
		t.Errorf("Anchor: got %q, want empty", links[0].Anchor)
	}
}

// TestParse_Full verifies [[topic#anchor|alias]] captures all three groups.
func TestParse_Full(t *testing.T) {
	links := Parse("See [[arch/auth#jwt|Auth System]] for details.")
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1", len(links))
	}
	l := links[0]
	if l.Topic != "arch/auth" {
		t.Errorf("Topic: got %q, want %q", l.Topic, "arch/auth")
	}
	if l.Anchor != "jwt" {
		t.Errorf("Anchor: got %q, want %q", l.Anchor, "jwt")
	}
	if l.Alias != "Auth System" {
		t.Errorf("Alias: got %q, want %q", l.Alias, "Auth System")
	}
}

// TestParse_NestedTopicKey verifies that a/b/c/d topic_key is preserved verbatim.
func TestParse_NestedTopicKey(t *testing.T) {
	links := Parse("[[a/b/c/d]]")
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1", len(links))
	}
	if links[0].Topic != "a/b/c/d" {
		t.Errorf("Topic: got %q, want %q", links[0].Topic, "a/b/c/d")
	}
}

// TestParse_MultipleLinks verifies that 3 wikilinks on the same line returns 3 Links.
func TestParse_MultipleLinks(t *testing.T) {
	links := Parse("See [[alpha]], [[beta]], and [[gamma]] for more.")
	if len(links) != 3 {
		t.Fatalf("got %d links, want 3", len(links))
	}
	want := []string{"alpha", "beta", "gamma"}
	for i, w := range want {
		if links[i].Topic != w {
			t.Errorf("links[%d].Topic: got %q, want %q", i, links[i].Topic, w)
		}
	}
}

// TestParse_SkipFencedCodeBlock verifies that a wikilink inside a ``` block
// is not extracted.
func TestParse_SkipFencedCodeBlock(t *testing.T) {
	content := "Before.\n```\n[[inside-fence]]\n```\nAfter."
	links := Parse(content)
	if len(links) != 0 {
		t.Errorf("got %d links, want 0 (wikilink inside fence should be skipped)", len(links))
	}
}

// TestParse_SkipInlineCode verifies that [[link]] inside backtick inline code
// is not extracted.
func TestParse_SkipInlineCode(t *testing.T) {
	links := Parse("Use `[[not-a-link]]` in your code.")
	if len(links) != 0 {
		t.Errorf("got %d links, want 0 (wikilink inside inline code should be skipped)", len(links))
	}
}

// TestParse_MixedCodeAndText verifies that links in text are extracted while
// links inside code blocks or inline code are skipped.
func TestParse_MixedCodeAndText(t *testing.T) {
	content := "Real: [[real-link]]\n```go\n[[code-link]]\n```\nAlso real: [[another-real]]"
	links := Parse(content)
	if len(links) != 2 {
		t.Fatalf("got %d links, want 2", len(links))
	}
	if links[0].Topic != "real-link" {
		t.Errorf("links[0].Topic: got %q, want %q", links[0].Topic, "real-link")
	}
	if links[1].Topic != "another-real" {
		t.Errorf("links[1].Topic: got %q, want %q", links[1].Topic, "another-real")
	}
}

// TestParse_EmptyContent verifies that Parse("") returns nil, not a zero-length
// slice, to keep callers consistent.
func TestParse_EmptyContent(t *testing.T) {
	links := Parse("")
	if links != nil {
		t.Errorf("got %v, want nil for empty content", links)
	}
}

// TestParse_NoWikilinks verifies that plain text with no wikilinks returns nil.
func TestParse_NoWikilinks(t *testing.T) {
	links := Parse("This is just some text with no links at all.")
	if len(links) != 0 {
		t.Errorf("got %d links, want 0", len(links))
	}
}

// TestParse_UnclosedBrackets verifies that [[incomplete does not match.
func TestParse_UnclosedBrackets(t *testing.T) {
	links := Parse("This is [[incomplete")
	if len(links) != 0 {
		t.Errorf("got %d links, want 0 (unclosed brackets should not match)", len(links))
	}
}

// TestParse_EmptyBrackets verifies that [[]] does not match because topic requires
// at least one character.
func TestParse_EmptyBrackets(t *testing.T) {
	links := Parse("Empty: [[]]")
	if len(links) != 0 {
		t.Errorf("got %d links, want 0 (empty brackets should not match)", len(links))
	}
}

// TestParse_LineNumbers verifies that Link.Line reflects the correct 0-based
// line index.
func TestParse_LineNumbers(t *testing.T) {
	content := "line 0\nline 1 [[first]]\nline 2\nline 3 [[second]]"
	links := Parse(content)
	if len(links) != 2 {
		t.Fatalf("got %d links, want 2", len(links))
	}
	if links[0].Line != 1 {
		t.Errorf("links[0].Line: got %d, want 1", links[0].Line)
	}
	if links[1].Line != 3 {
		t.Errorf("links[1].Line: got %d, want 3", links[1].Line)
	}
}

// TestParse_Unicode verifies that unicode topic keys are extracted correctly.
func TestParse_Unicode(t *testing.T) {
	links := Parse("Ver [[topico/autenticacion]] para detalles.")
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1", len(links))
	}
	if links[0].Topic != "topico/autenticacion" {
		t.Errorf("Topic: got %q, want %q", links[0].Topic, "topico/autenticacion")
	}
}

// TestParse_TildeFence verifies that wikilinks inside ~~~ fenced blocks are skipped.
func TestParse_TildeFence(t *testing.T) {
	content := "Before.\n~~~\n[[inside-tilde]]\n~~~\nAfter."
	links := Parse(content)
	if len(links) != 0 {
		t.Errorf("got %d links, want 0 (wikilink inside tilde fence should be skipped)", len(links))
	}
}

// TestParse_NestedFences verifies that CommonMark fence toggling is correct:
// a second opening fence inside a backtick fence does NOT start a new block,
// and a backtick fence is closed only by a matching backtick fence.
func TestParse_NestedFences(t *testing.T) {
	// Outer ``` fence opened, inner ~~~ is just content, not a toggle.
	// The block closes on the second ```.
	content := "```\n[[skip1]]\n~~~\n[[skip2]]\n~~~\n```\n[[real]]"
	links := Parse(content)
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1; got: %v", len(links), links)
	}
	if links[0].Topic != "real" {
		t.Errorf("Topic: got %q, want %q", links[0].Topic, "real")
	}
}

// TestParse_SpacesInTopic verifies that spaces are allowed inside topic names.
func TestParse_SpacesInTopic(t *testing.T) {
	links := Parse("[[topic with spaces]]")
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1", len(links))
	}
	if links[0].Topic != "topic with spaces" {
		t.Errorf("Topic: got %q, want %q", links[0].Topic, "topic with spaces")
	}
}

// TestParse_AnchorOnly verifies that [[#section]] (no topic) does not match
// because the topic capture group requires at least one character.
func TestParse_AnchorOnly(t *testing.T) {
	links := Parse("See [[#section]] here.")
	if len(links) != 0 {
		t.Errorf("got %d links, want 0 ([[#section]] has no topic, should not match)", len(links))
	}
}

// TestParse_PipeOnly verifies that [[|alias]] (no topic) does not match.
func TestParse_PipeOnly(t *testing.T) {
	links := Parse("See [[|alias]] here.")
	if len(links) != 0 {
		t.Errorf("got %d links, want 0 ([[|alias]] has no topic, should not match)", len(links))
	}
}
