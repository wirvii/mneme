package shell

import (
	"testing"
)

// tokenCase is a single table-driven test entry for Tokenize.
type tokenCase struct {
	name    string
	input   string
	want    []Token
	wantErr bool
}

// golden10 is the mandatory suite from spec SPEC-033, section "Suite golden del
// tokenizer (casos obligatorios)".
var golden10 = []tokenCase{
	{
		name:  "1_plain_rm",
		input: `rm /etc/passwd`,
		want: []Token{
			{Value: "rm", Type: TypeWord},
			{Value: "/etc/passwd", Type: TypeWord},
		},
	},
	{
		name:  "2_echo_dbl_quoted_rm",
		input: `echo "rm /etc/passwd"`,
		want: []Token{
			{Value: "echo", Type: TypeWord},
			{Value: "rm /etc/passwd", Type: TypeWord, Quoted: true},
		},
	},
	{
		name:  "3_echo_sgl_quoted_rm",
		input: `echo 'rm /tmp'`,
		want: []Token{
			{Value: "echo", Type: TypeWord},
			{Value: "rm /tmp", Type: TypeWord, Quoted: true},
		},
	},
	{
		name:  "4_gh_pr_create_title",
		input: `gh pr create --title "feat(install): X"`,
		want: []Token{
			{Value: "gh", Type: TypeWord},
			{Value: "pr", Type: TypeWord},
			{Value: "create", Type: TypeWord},
			{Value: "--title", Type: TypeWord},
			{Value: "feat(install): X", Type: TypeWord, Quoted: true},
		},
	},
	{
		name:  "5_heredoc_body_dbl_delimiter",
		input: "cat <<EOF\nrm /tmp\nEOF",
		want: []Token{
			{Value: "cat", Type: TypeWord},
			{Value: "<<", Type: TypeRedirect},
			{Value: "rm /tmp\n", Type: TypeHeredocBody},
		},
	},
	{
		name:  "6_heredoc_body_sgl_quoted_delimiter",
		input: "cat <<'EOF'\nrm /tmp\nEOF",
		want: []Token{
			{Value: "cat", Type: TypeWord},
			{Value: "<<", Type: TypeRedirect},
			{Value: "rm /tmp\n", Type: TypeHeredocBody},
		},
	},
	{
		name:  "7_redirect_to_go_file",
		input: `echo foo > internal/x.go`,
		want: []Token{
			{Value: "echo", Type: TypeWord},
			{Value: "foo", Type: TypeWord},
			{Value: ">", Type: TypeRedirect},
			{Value: "internal/x.go", Type: TypeRedirectTarget},
		},
	},
	{
		name:  "8_rmdir_with_stderr_redirect",
		input: `rmdir .claude/tmp 2>/dev/null`,
		want: []Token{
			{Value: "rmdir", Type: TypeWord},
			{Value: ".claude/tmp", Type: TypeWord},
			{Value: "2>", Type: TypeRedirect},
			{Value: "/dev/null", Type: TypeRedirectTarget},
		},
	},
	{
		name:  "9_cmd_subst_in_arg",
		input: `rm $(date +%s).txt`,
		want: []Token{
			{Value: "rm", Type: TypeWord},
			{Value: "date +%s", Type: TypeCommandSubstitution},
			{Value: ".txt", Type: TypeWord},
		},
	},
	{
		name:  "10_bash_dash_c",
		input: `bash -c "rm /tmp/x"`,
		want: []Token{
			{Value: "bash", Type: TypeWord},
			{Value: "-c", Type: TypeWord},
			{Value: "rm /tmp/x", Type: TypeWord, Quoted: true},
		},
	},
}

// edgeCases tests additional scenarios not covered by the golden suite.
var edgeCases = []tokenCase{
	{
		name:  "empty_input",
		input: "",
		want:  nil,
	},
	{
		name:  "whitespace_only",
		input: "   \t  ",
		want:  nil,
	},
	{
		name:  "append_redirect",
		input: `echo hello >> docs/notes.md`,
		want: []Token{
			{Value: "echo", Type: TypeWord},
			{Value: "hello", Type: TypeWord},
			{Value: ">>", Type: TypeRedirect},
			{Value: "docs/notes.md", Type: TypeRedirectTarget},
		},
	},
	{
		name:  "rdr_all_redirect",
		input: `make build &> /dev/null`,
		want: []Token{
			{Value: "make", Type: TypeWord},
			{Value: "build", Type: TypeWord},
			{Value: "&>", Type: TypeRedirect},
			{Value: "/dev/null", Type: TypeRedirectTarget},
		},
	},
	{
		name:  "false_positive_install_in_title",
		input: `gh pr create --title "feat(install): embed and install hook"`,
		want: []Token{
			{Value: "gh", Type: TypeWord},
			{Value: "pr", Type: TypeWord},
			{Value: "create", Type: TypeWord},
			{Value: "--title", Type: TypeWord},
			{Value: "feat(install): embed and install hook", Type: TypeWord, Quoted: true},
		},
	},
	{
		name:  "echo_rm_quoted_no_cmd",
		input: `echo "rm /tmp"`,
		want: []Token{
			{Value: "echo", Type: TypeWord},
			{Value: "rm /tmp", Type: TypeWord, Quoted: true},
		},
	},
	{
		name:  "echo_redirect_inside_json_string",
		// The redirect > internal/x.go is a real redirect here; the hook
		// should detect it. This is separate from a redirect inside a
		// quoted string.
		input: `echo "content" > internal/x.go`,
		want: []Token{
			{Value: "echo", Type: TypeWord},
			{Value: "content", Type: TypeWord, Quoted: true},
			{Value: ">", Type: TypeRedirect},
			{Value: "internal/x.go", Type: TypeRedirectTarget},
		},
	},
	{
		name:  "multiple_args_with_flags",
		input: `cp -r src/ dist/`,
		want: []Token{
			{Value: "cp", Type: TypeWord},
			{Value: "-r", Type: TypeWord},
			{Value: "src/", Type: TypeWord},
			{Value: "dist/", Type: TypeWord},
		},
	},
	{
		name:  "semicolon_separated_commands",
		input: `echo hello; rm /tmp/x`,
		want: []Token{
			{Value: "echo", Type: TypeWord},
			{Value: "hello", Type: TypeWord},
			{Value: ";", Type: TypeSeparator},
			{Value: "rm", Type: TypeWord},
			{Value: "/tmp/x", Type: TypeWord},
		},
	},
	{
		name:    "parse_error",
		input:   `echo "unclosed`,
		wantErr: true,
	},
	// --- coverage: input redirect ---
	{
		name:  "input_redirect",
		input: `cat < /tmp/in.txt`,
		want: []Token{
			{Value: "cat", Type: TypeWord},
			{Value: "<", Type: TypeRedirect},
			{Value: "/tmp/in.txt", Type: TypeRedirectTarget},
		},
	},
}

// TestTokenize_Golden runs the 10 mandatory golden cases from SPEC-033.
func TestTokenize_Golden(t *testing.T) {
	runCases(t, golden10)
}

// TestTokenize_EdgeCases runs additional edge cases beyond the golden suite.
func TestTokenize_EdgeCases(t *testing.T) {
	runCases(t, edgeCases)
}

// runCases is the shared table-driven runner.
func runCases(t *testing.T, cases []tokenCase) {
	t.Helper()
	for _, tc := range cases {
		tc := tc // capture range variable
		t.Run(tc.name, func(t *testing.T) {
			got, err := Tokenize(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error for input %q, got nil", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for input %q: %v", tc.input, err)
			}
			if !tokensEqual(got, tc.want) {
				t.Errorf("Tokenize(%q)\n  got:  %v\n  want: %v", tc.input, formatTokens(got), formatTokens(tc.want))
			}
		})
	}
}

// tokensEqual returns true when got and want contain the same tokens in the
// same order.
func tokensEqual(got, want []Token) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// formatTokens renders a token slice as a human-readable string for test output.
func formatTokens(tokens []Token) string {
	if tokens == nil {
		return "[]"
	}
	parts := make([]string, len(tokens))
	for i, t := range tokens {
		q := ""
		if t.Quoted {
			q = ",quoted"
		}
		parts[i] = string(t.Type) + ":" + t.Value + q
	}
	return "[" + joinStrings(parts) + "]"
}

// joinStrings joins a slice with ", " separator without importing strings.
func joinStrings(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	result := ss[0]
	for _, s := range ss[1:] {
		result += ", " + s
	}
	return result
}

// TestTokenize_CmdSubstInsideDblQuote exercises the CmdSubst branch inside
// extractDblQuoted. The input has $(...) inside double quotes.
func TestTokenize_CmdSubstInsideDblQuote(t *testing.T) {
	tokens, err := Tokenize(`echo "$(date +%Y)"`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect: [word:echo, word(quoted): something containing "date"]
	if len(tokens) < 2 {
		t.Fatalf("expected at least 2 tokens, got %d: %v", len(tokens), formatTokens(tokens))
	}
	if tokens[0].Value != "echo" || tokens[0].Type != TypeWord {
		t.Errorf("token[0]: got %v, want word:echo", tokens[0])
	}
	if tokens[1].Type != TypeWord || !tokens[1].Quoted {
		t.Errorf("token[1]: got %v, want quoted word", tokens[1])
	}
	if tokens[1].Value == "" {
		t.Errorf("token[1]: expected non-empty value for $(date +%%Y) inside double quotes")
	}
}

// TestTokenize_ParamExpansionInRedirect exercises extractWordLiteral's
// multi-part fallback path via a parameter expansion in a redirect target.
func TestTokenize_ParamExpansionInRedirect(t *testing.T) {
	tokens, err := Tokenize(`echo hello > ${HOME}/out.txt`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect at least: [word:echo, word:hello, redirect:>, redirect_target:...]
	if len(tokens) < 4 {
		t.Fatalf("expected at least 4 tokens, got %d: %v", len(tokens), formatTokens(tokens))
	}
	// Find the redirect target.
	var target string
	for _, tok := range tokens {
		if tok.Type == TypeRedirectTarget {
			target = tok.Value
			break
		}
	}
	if target == "" {
		t.Errorf("expected a redirect_target token, got: %v", formatTokens(tokens))
	}
}

// TestTokenize_MultiPartWord exercises tokensFromWord's multi-part path where
// a word has more than one part without being purely a single type.
func TestTokenize_MultiPartWord(t *testing.T) {
	// "prefix$(date)suffix" — three parts: Lit, CmdSubst, Lit
	tokens, err := Tokenize(`rm prefix$(date)suffix`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) < 2 {
		t.Fatalf("expected at least 2 tokens, got %d: %v", len(tokens), formatTokens(tokens))
	}
	// First token must be rm
	if tokens[0].Value != "rm" {
		t.Errorf("token[0]: got %q, want %q", tokens[0].Value, "rm")
	}
	// Second argument has multiple parts — at least one should be command_substitution
	foundCmdSubst := false
	for _, tok := range tokens[1:] {
		if tok.Type == TypeCommandSubstitution {
			foundCmdSubst = true
			break
		}
	}
	if !foundCmdSubst {
		t.Errorf("expected a command_substitution token among %v", formatTokens(tokens))
	}
}

// TestTokenize_EmptyCmdSubst exercises the extractCmdSubst empty-stmts path.
func TestTokenize_EmptyCmdSubst(t *testing.T) {
	// $() has zero statements.
	tokens, err := Tokenize(`echo $()`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should produce [word:echo, command_substitution:""]
	if len(tokens) < 2 {
		t.Fatalf("expected at least 2 tokens, got %d: %v", len(tokens), formatTokens(tokens))
	}
	if tokens[0].Value != "echo" {
		t.Errorf("token[0]: got %q, want echo", tokens[0].Value)
	}
	if tokens[1].Type != TypeCommandSubstitution {
		t.Errorf("token[1]: got type %q, want command_substitution", tokens[1].Type)
	}
}

// TestTokenize_NonCallExprStmt exercises the stmt path for non-CallExpr
// commands that still carry redirects (e.g. a subshell).
func TestTokenize_NonCallExprStmt(t *testing.T) {
	// A compound command with a redirect — Stmt.Cmd is not a CallExpr.
	tokens, err := Tokenize(`(echo hi) > /tmp/out.txt`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Must see at least the redirect operator and target.
	found := false
	for _, tok := range tokens {
		if tok.Type == TypeRedirectTarget && tok.Value == "/tmp/out.txt" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected redirect_target:/tmp/out.txt in %v", formatTokens(tokens))
	}
}

// TestTokenize_ParamExpInMultipartWord exercises the default branch in
// tokensFromWord when a multi-part word contains a ParamExp (e.g. $HOME).
func TestTokenize_ParamExpInMultipartWord(t *testing.T) {
	// $HOME/foo is a word with ParamExp + Lit parts.
	tokens, err := Tokenize(`ls $HOME/foo`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) < 2 {
		t.Fatalf("expected at least 2 tokens, got %d: %v", len(tokens), formatTokens(tokens))
	}
	if tokens[0].Value != "ls" {
		t.Errorf("token[0]: got %q, want ls", tokens[0].Value)
	}
	// The second token should contain "foo" (the Lit part) and possibly additional
	// tokens for the ParamExp; either way at least one non-empty value must exist.
	found := false
	for _, tok := range tokens[1:] {
		if tok.Value != "" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected at least one non-empty token after ls, got: %v", formatTokens(tokens))
	}
}

// TestTokenize_ParamExpInDblQuoted exercises the default branch in
// extractDblQuoted when double quotes contain only a parameter expansion.
func TestTokenize_ParamExpInDblQuoted(t *testing.T) {
	// echo "${HOME}" — DblQuoted with a ParamExp part only.
	tokens, err := Tokenize(`echo "${HOME}"`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) < 2 {
		t.Fatalf("expected at least 2 tokens, got %d: %v", len(tokens), formatTokens(tokens))
	}
	if tokens[0].Value != "echo" {
		t.Errorf("token[0]: got %q, want echo", tokens[0].Value)
	}
	if tokens[1].Type != TypeWord || !tokens[1].Quoted {
		t.Errorf("token[1]: expected quoted word, got %v", tokens[1])
	}
}

// TestTokenize_SglQuotedRedirectTarget exercises extractWordLiteral's SglQuoted
// branch when a redirect target is single-quoted.
func TestTokenize_SglQuotedRedirectTarget(t *testing.T) {
	tokens, err := Tokenize(`echo hi > '/tmp/out.txt'`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var target string
	for _, tok := range tokens {
		if tok.Type == TypeRedirectTarget {
			target = tok.Value
			break
		}
	}
	if target != "/tmp/out.txt" {
		t.Errorf("expected redirect_target:/tmp/out.txt, got %q", target)
	}
}

// TestTokenize_DblQuotedRedirectTarget exercises extractWordLiteral's DblQuoted
// branch when a redirect target is double-quoted.
func TestTokenize_DblQuotedRedirectTarget(t *testing.T) {
	tokens, err := Tokenize(`echo hi > "/tmp/out.txt"`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var target string
	for _, tok := range tokens {
		if tok.Type == TypeRedirectTarget {
			target = tok.Value
			break
		}
	}
	if target != "/tmp/out.txt" {
		t.Errorf("expected redirect_target:/tmp/out.txt, got %q", target)
	}
}

// TestTokenize_MultiPartSglQuotedInWord exercises tokensFromWord's SglQuoted
// branch in the multi-part path.
func TestTokenize_MultiPartSglQuotedInWord(t *testing.T) {
	// prefix'suffix' — two parts: Lit + SglQuoted
	tokens, err := Tokenize(`rm prefix'quoted'`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) < 2 {
		t.Fatalf("expected at least 2 tokens, got %d", len(tokens))
	}
	// At least one token should be quoted.
	found := false
	for _, tok := range tokens {
		if tok.Quoted {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected at least one quoted token in %v", formatTokens(tokens))
	}
}

// TestTokenize_MultiPartDblQuotedInWord exercises tokensFromWord's DblQuoted
// branch in the multi-part path.
func TestTokenize_MultiPartDblQuotedInWord(t *testing.T) {
	// prefix"suffix" — two parts: Lit + DblQuoted
	tokens, err := Tokenize(`rm prefix"quoted"`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) < 2 {
		t.Fatalf("expected at least 2 tokens, got %d", len(tokens))
	}
	found := false
	for _, tok := range tokens {
		if tok.Quoted {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected at least one quoted token in %v", formatTokens(tokens))
	}
}

// TestTokenize_Pipeline exercises the BinaryCmd path in tokensFromStmt.
// A pipeline like `echo foo | tee bar` must emit tokens from both sides
// separated by a separator token carrying the "|" operator.
func TestTokenize_Pipeline(t *testing.T) {
	tokens, err := Tokenize(`echo foo | tee internal/x.go`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect: [word:echo, word:foo, separator:|, word:tee, word:internal/x.go]
	if len(tokens) < 5 {
		t.Fatalf("expected at least 5 tokens from pipeline, got %d: %v", len(tokens), formatTokens(tokens))
	}
	// Verify separator is present with correct operator.
	foundSep := false
	for _, tok := range tokens {
		if tok.Type == TypeSeparator && tok.Value == "|" {
			foundSep = true
		}
	}
	if !foundSep {
		t.Errorf("expected separator:| in pipeline output %v", formatTokens(tokens))
	}
	values := make(map[string]bool)
	for _, tok := range tokens {
		values[tok.Value] = true
	}
	for _, want := range []string{"echo", "foo", "tee", "internal/x.go"} {
		if !values[want] {
			t.Errorf("expected token %q in pipeline output %v", want, formatTokens(tokens))
		}
	}
}

// TestTokenize_LogicalAnd exercises BinaryCmd for && chains.
// A separator token with value "&&" must appear between the two sides.
func TestTokenize_LogicalAnd(t *testing.T) {
	tokens, err := Tokenize(`mkdir tmp && mv src.go tmp/`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect: [word:mkdir, word:tmp, separator:&&, word:mv, word:src.go, word:tmp/]
	if len(tokens) < 5 {
		t.Fatalf("expected at least 5 tokens, got %d: %v", len(tokens), formatTokens(tokens))
	}
	foundSep := false
	for _, tok := range tokens {
		if tok.Type == TypeSeparator && tok.Value == "&&" {
			foundSep = true
		}
	}
	if !foundSep {
		t.Errorf("expected separator:&& in output %v", formatTokens(tokens))
	}
	values := make(map[string]bool)
	for _, tok := range tokens {
		values[tok.Value] = true
	}
	for _, want := range []string{"mkdir", "tmp", "mv", "src.go"} {
		if !values[want] {
			t.Errorf("expected token %q in && chain output %v", want, formatTokens(tokens))
		}
	}
}

// TestTokenize_LogicalOr exercises BinaryCmd for || chains.
// A separator token with value "||" must appear between the two sides.
func TestTokenize_LogicalOr(t *testing.T) {
	tokens, err := Tokenize(`rm internal/x.go || echo "fallback"`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect: [word:rm, word:internal/x.go, separator:||, word:echo, word(quoted):fallback]
	if len(tokens) < 4 {
		t.Fatalf("expected at least 4 tokens from || chain, got %d: %v", len(tokens), formatTokens(tokens))
	}
	foundSep := false
	for _, tok := range tokens {
		if tok.Type == TypeSeparator && tok.Value == "||" {
			foundSep = true
		}
	}
	if !foundSep {
		t.Errorf("expected separator:|| in output %v", formatTokens(tokens))
	}
	values := make(map[string]bool)
	for _, tok := range tokens {
		values[tok.Value] = true
	}
	for _, want := range []string{"rm", "internal/x.go", "echo"} {
		if !values[want] {
			t.Errorf("expected token %q in || chain output %v", want, formatTokens(tokens))
		}
	}
}

// TestTokenize_PipelineAndChain exercises a combined pipeline followed by a &&
// logical chain: A | B && C. Two separator tokens must appear.
func TestTokenize_PipelineAndChain(t *testing.T) {
	tokens, err := Tokenize(`echo foo | tee internal/x.go && rm internal/x.go`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect separators: | between A and B, && between (A|B) and C.
	// Total: word:echo, word:foo, sep:|, word:tee, word:internal/x.go, sep:&&, word:rm, word:internal/x.go
	if len(tokens) < 7 {
		t.Fatalf("expected at least 7 tokens from A|B&&C, got %d: %v", len(tokens), formatTokens(tokens))
	}
	var seps []string
	for _, tok := range tokens {
		if tok.Type == TypeSeparator {
			seps = append(seps, tok.Value)
		}
	}
	if len(seps) < 2 {
		t.Errorf("expected at least 2 separator tokens in A|B&&C, got %v: %v", seps, formatTokens(tokens))
	}
	values := make(map[string]bool)
	for _, tok := range tokens {
		values[tok.Value] = true
	}
	for _, want := range []string{"echo", "foo", "tee", "internal/x.go", "rm"} {
		if !values[want] {
			t.Errorf("expected token %q in A|B&&C output %v", want, formatTokens(tokens))
		}
	}
}

// TestTokenize_CdAndRm exercises a common attack pattern: cd /tmp && rm internal/x.go.
// Both sides of the && must be tokenised and a separator token must appear between them.
func TestTokenize_CdAndRm(t *testing.T) {
	tokens, err := Tokenize(`cd /tmp && rm internal/x.go`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect: [word:cd, word:/tmp, separator:&&, word:rm, word:internal/x.go]
	if len(tokens) < 5 {
		t.Fatalf("expected at least 5 tokens, got %d: %v", len(tokens), formatTokens(tokens))
	}
	foundSep := false
	for _, tok := range tokens {
		if tok.Type == TypeSeparator && tok.Value == "&&" {
			foundSep = true
		}
	}
	if !foundSep {
		t.Errorf("expected separator:&& in cd&&rm output %v", formatTokens(tokens))
	}
	values := make(map[string]bool)
	for _, tok := range tokens {
		values[tok.Value] = true
	}
	for _, want := range []string{"cd", "/tmp", "rm", "internal/x.go"} {
		if !values[want] {
			t.Errorf("expected token %q in cd&&rm output %v", want, formatTokens(tokens))
		}
	}
}

// TestTokenize_SeparatorOperators is a table-driven test that verifies separator
// tokens are emitted with the correct operator value for |, &&, and || chains.
func TestTokenize_SeparatorOperators(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantSep string
	}{
		{name: "pipe", input: "A | B", wantSep: "|"},
		{name: "and", input: "A && B", wantSep: "&&"},
		{name: "or", input: "A || B", wantSep: "||"},
		{name: "pipe_and", input: "A | B && C", wantSep: "&&"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tokens, err := Tokenize(tc.input)
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.input, err)
			}
			found := false
			for _, tok := range tokens {
				if tok.Type == TypeSeparator && tok.Value == tc.wantSep {
					found = true
				}
			}
			if !found {
				t.Errorf("Tokenize(%q): expected separator:%q in %v", tc.input, tc.wantSep, formatTokens(tokens))
			}
		})
	}
}

// TestTokenize_BoundaryBleedFix is the golden test for the boundary-bleed exploit
// fixed in SPEC-033 round 3. The token list for "rm internal/x.go && echo CLAUDE.md"
// must contain a separator token between the rm segment and the echo segment,
// preventing _find_last_word_target from crossing the boundary.
func TestTokenize_BoundaryBleedFix(t *testing.T) {
	tokens, err := Tokenize(`rm internal/x.go && echo CLAUDE.md`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Golden: [word:rm, word:internal/x.go, separator:&&, word:echo, word:CLAUDE.md]
	want := []Token{
		{Value: "rm", Type: TypeWord},
		{Value: "internal/x.go", Type: TypeWord},
		{Value: "&&", Type: TypeSeparator},
		{Value: "echo", Type: TypeWord},
		{Value: "CLAUDE.md", Type: TypeWord},
	}
	if !tokensEqual(tokens, want) {
		t.Errorf("Tokenize boundary bleed golden\n  got:  %v\n  want: %v",
			formatTokens(tokens), formatTokens(want))
	}
}

// TestTokenize_SemicolonSeparator verifies that semicolon-separated top-level
// statements emit a TypeSeparator token with value ";" between them.
// This prevents enforcement hooks from crossing the boundary.
func TestTokenize_SemicolonSeparator(t *testing.T) {
	tokens, err := Tokenize(`rm /tmp/a ; echo b`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Golden: [word:rm, word:/tmp/a, separator:;, word:echo, word:b]
	want := []Token{
		{Value: "rm", Type: TypeWord},
		{Value: "/tmp/a", Type: TypeWord},
		{Value: ";", Type: TypeSeparator},
		{Value: "echo", Type: TypeWord},
		{Value: "b", Type: TypeWord},
	}
	if !tokensEqual(tokens, want) {
		t.Errorf("Tokenize semicolon separator golden\n  got:  %v\n  want: %v",
			formatTokens(tokens), formatTokens(want))
	}
}

// TestTokenize_NewlineSeparator verifies that newline-separated top-level
// statements emit a TypeSeparator token with value ";" between them.
func TestTokenize_NewlineSeparator(t *testing.T) {
	tokens, err := Tokenize("rm /tmp/a\necho b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Golden: [word:rm, word:/tmp/a, separator:;, word:echo, word:b]
	want := []Token{
		{Value: "rm", Type: TypeWord},
		{Value: "/tmp/a", Type: TypeWord},
		{Value: ";", Type: TypeSeparator},
		{Value: "echo", Type: TypeWord},
		{Value: "b", Type: TypeWord},
	}
	if !tokensEqual(tokens, want) {
		t.Errorf("Tokenize newline separator golden\n  got:  %v\n  want: %v",
			formatTokens(tokens), formatTokens(want))
	}
}

// TestTokenize_C4ExploitGolden is the exact golden test for the C4 security
// bypass reported in QA round 3. "rm internal/x.go ; echo CLAUDE.md" must
// produce a separator token between the two statements so that
// _find_last_word_target stops at the boundary and returns internal/x.go
// (blocked) rather than CLAUDE.md (whitelisted).
func TestTokenize_C4ExploitGolden(t *testing.T) {
	tokens, err := Tokenize(`rm internal/x.go ; echo CLAUDE.md`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Golden: [word:rm, word:internal/x.go, separator:;, word:echo, word:CLAUDE.md]
	want := []Token{
		{Value: "rm", Type: TypeWord},
		{Value: "internal/x.go", Type: TypeWord},
		{Value: ";", Type: TypeSeparator},
		{Value: "echo", Type: TypeWord},
		{Value: "CLAUDE.md", Type: TypeWord},
	}
	if !tokensEqual(tokens, want) {
		t.Errorf("Tokenize C4 exploit golden\n  got:  %v\n  want: %v",
			formatTokens(tokens), formatTokens(want))
	}
}
