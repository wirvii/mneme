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

// ---- C5: compound command tests (SPEC-033 QA round 4 regressions) ----

// TestTokenize_Subshell verifies that a subshell (echo content > internal/x.go)
// emits the redirect target so the enforcement hook can block it.
func TestTokenize_Subshell(t *testing.T) {
	tokens, err := Tokenize(`(echo content > internal/x.go)`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContainsTokens(t, tokens,
		Token{Value: "echo", Type: TypeWord},
		Token{Value: ">", Type: TypeRedirect},
		Token{Value: "internal/x.go", Type: TypeRedirectTarget},
	)
}

// TestTokenize_Block verifies that a brace block { rm internal/x.go; }
// emits the rm command and its argument.
func TestTokenize_Block(t *testing.T) {
	tokens, err := Tokenize(`{ rm internal/x.go; }`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContainsTokens(t, tokens,
		Token{Value: "rm", Type: TypeWord},
		Token{Value: "internal/x.go", Type: TypeWord},
	)
}

// TestTokenize_IfClause verifies that the then-body of an if statement is
// tokenized: "if true; then rm internal/x.go; fi".
func TestTokenize_IfClause(t *testing.T) {
	tokens, err := Tokenize(`if true; then rm internal/x.go; fi`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContainsTokens(t, tokens,
		Token{Value: "rm", Type: TypeWord},
		Token{Value: "internal/x.go", Type: TypeWord},
	)
}

// TestTokenize_IfElseClause verifies that both then and else branches are
// tokenized in an if/else construct.
func TestTokenize_IfElseClause(t *testing.T) {
	tokens, err := Tokenize(`if false; then echo ok; else rm internal/x.go; fi`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContainsTokens(t, tokens,
		Token{Value: "rm", Type: TypeWord},
		Token{Value: "internal/x.go", Type: TypeWord},
	)
}

// TestTokenize_ForClause verifies that the in-list words and the do-body of
// a for loop are tokenized: "for f in internal/x.go; do rm $f; done".
func TestTokenize_ForClause(t *testing.T) {
	tokens, err := Tokenize(`for f in internal/x.go; do rm $f; done`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The in-list item "internal/x.go" must be visible.
	assertContainsToken(t, tokens, func(tok Token) bool {
		return tok.Type == TypeWord && tok.Value == "internal/x.go"
	}, "expected word:internal/x.go in for-clause in-list")
	// The rm command in the body must be visible.
	assertContainsToken(t, tokens, func(tok Token) bool {
		return tok.Type == TypeWord && tok.Value == "rm"
	}, "expected word:rm in for-clause body")
}

// TestTokenize_WhileClause verifies that both condition and body of a while
// loop are tokenized.
func TestTokenize_WhileClause(t *testing.T) {
	tokens, err := Tokenize(`while true; do rm internal/x.go; break; done`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContainsTokens(t, tokens,
		Token{Value: "rm", Type: TypeWord},
		Token{Value: "internal/x.go", Type: TypeWord},
	)
}

// TestTokenize_UntilClause verifies that the until variant of WhileClause is
// also tokenized (WhileClause.Until == true).
func TestTokenize_UntilClause(t *testing.T) {
	tokens, err := Tokenize(`until false; do rm internal/x.go; done`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContainsTokens(t, tokens,
		Token{Value: "rm", Type: TypeWord},
		Token{Value: "internal/x.go", Type: TypeWord},
	)
}

// TestTokenize_CaseClause verifies that the body statements of each case item
// are tokenized: "case x in x) rm internal/x.go;; esac".
func TestTokenize_CaseClause(t *testing.T) {
	tokens, err := Tokenize(`case x in x) rm internal/x.go;; esac`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContainsTokens(t, tokens,
		Token{Value: "rm", Type: TypeWord},
		Token{Value: "internal/x.go", Type: TypeWord},
	)
}

// TestTokenize_FuncDecl verifies that the function body is tokenized:
// "f(){ rm internal/x.go; }; f".
func TestTokenize_FuncDecl(t *testing.T) {
	tokens, err := Tokenize(`f(){ rm internal/x.go; }; f`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContainsTokens(t, tokens,
		Token{Value: "rm", Type: TypeWord},
		Token{Value: "internal/x.go", Type: TypeWord},
	)
}

// TestTokenize_C5Regressions is a table-driven golden test for all 7 compound
// command regressions reported in SPEC-033 QA round 4. Each case must produce
// at least 2 tokens that include "rm" and "internal/x.go" (or the redirect
// equivalent), matching the legacy awk parser's behaviour.
func TestTokenize_C5Regressions(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantWords []string // values that must appear as word or redirect_target tokens
	}{
		{
			name:      "block",
			input:     `{ rm internal/x.go; }`,
			wantWords: []string{"rm", "internal/x.go"},
		},
		{
			name:      "if_then",
			input:     `if true; then rm internal/x.go; fi`,
			wantWords: []string{"rm", "internal/x.go"},
		},
		{
			name:      "for_loop",
			input:     `for f in internal/x.go; do rm $f; done`,
			wantWords: []string{"rm", "internal/x.go"},
		},
		{
			name:      "while_loop",
			input:     `while true; do rm internal/x.go; break; done`,
			wantWords: []string{"rm", "internal/x.go"},
		},
		{
			name:      "case",
			input:     `case x in x) rm internal/x.go;; esac`,
			wantWords: []string{"rm", "internal/x.go"},
		},
		{
			name:      "func_decl",
			input:     `f(){ rm internal/x.go; }; f`,
			wantWords: []string{"rm", "internal/x.go"},
		},
		{
			name:      "subshell_redirect",
			input:     `(echo content > internal/x.go)`,
			wantWords: []string{"echo", "internal/x.go"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tokens, err := Tokenize(tc.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			values := make(map[string]bool, len(tokens))
			for _, tok := range tokens {
				values[tok.Value] = true
			}
			for _, want := range tc.wantWords {
				if !values[want] {
					t.Errorf("Tokenize(%q): expected value %q in tokens %v",
						tc.input, want, formatTokens(tokens))
				}
			}
		})
	}
}

// TestTokenize_FdDup_NoRedirectTarget is the table-driven suite for fd-dup
// operators (DplOut / DplIn). It verifies that numeric fd words (e.g. "1" in
// 2>&1) do NOT produce a TypeRedirectTarget token, while non-numeric path words
// (e.g. "out.txt" in >&out.txt) DO produce one (SPEC-040 P1).
func TestTokenize_FdDup_NoRedirectTarget(t *testing.T) {
	cases := []tokenCase{
		{
			// 2>&1: fd-dup with numeric word — must NOT emit redirect_target.
			// Fix for the false positive observed with `golangci-lint run 2>&1`.
			name:  "fd_dup_2_to_1",
			input: `git log 2>&1`,
			want: []Token{
				{Value: "git", Type: TypeWord},
				{Value: "log", Type: TypeWord},
				{Value: "2>&", Type: TypeRedirect},
				// No TypeRedirectTarget — "1" is a file descriptor, not a path.
			},
		},
		{
			// 1>&2: reverse fd-dup — must NOT emit redirect_target.
			name:  "fd_dup_1_to_2",
			input: `cmd 1>&2`,
			want: []Token{
				{Value: "cmd", Type: TypeWord},
				{Value: "1>&", Type: TypeRedirect},
				// No TypeRedirectTarget — "2" is a file descriptor, not a path.
			},
		},
		{
			// >&out.txt: non-numeric word — MUST emit redirect_target (no bypass).
			name:  "fd_dup_to_path",
			input: `echo hi >&out.txt`,
			want: []Token{
				{Value: "echo", Type: TypeWord},
				{Value: "hi", Type: TypeWord},
				{Value: ">&", Type: TypeRedirect},
				{Value: "out.txt", Type: TypeRedirectTarget},
			},
		},
		{
			// &>/dev/null (RdrAll): regression — must still emit redirect_target.
			name:  "rdr_all_dev_null_regression",
			input: `make build &>/dev/null`,
			want: []Token{
				{Value: "make", Type: TypeWord},
				{Value: "build", Type: TypeWord},
				{Value: "&>", Type: TypeRedirect},
				{Value: "/dev/null", Type: TypeRedirectTarget},
			},
		},
		{
			// 2>/dev/null (RdrOut with N=2): regression — must still emit redirect_target.
			name:  "stderr_to_dev_null_regression",
			input: `rmdir x 2>/dev/null`,
			want: []Token{
				{Value: "rmdir", Type: TypeWord},
				{Value: "x", Type: TypeWord},
				{Value: "2>", Type: TypeRedirect},
				{Value: "/dev/null", Type: TypeRedirectTarget},
			},
		},
		{
			// echo foo > internal/x.go (plain redirect): regression — must still emit redirect_target.
			name:  "plain_redirect_regression",
			input: `echo foo > internal/x.go`,
			want: []Token{
				{Value: "echo", Type: TypeWord},
				{Value: "foo", Type: TypeWord},
				{Value: ">", Type: TypeRedirect},
				{Value: "internal/x.go", Type: TypeRedirectTarget},
			},
		},
	}
	runCases(t, cases)
}

// TestTokenize_FdDup_Pipeline_NoSpuriousTarget verifies that a pipeline
// containing a fd-dup redirect (2>&1) does NOT produce any TypeRedirectTarget
// token. This is the exact scenario observed in production with golangci-lint.
func TestTokenize_FdDup_Pipeline_NoSpuriousTarget(t *testing.T) {
	input := `golangci-lint run 2>&1 | tee log`
	tokens, err := Tokenize(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, tok := range tokens {
		if tok.Type == TypeRedirectTarget {
			t.Errorf("Tokenize(%q) produced unexpected redirect_target token: %v", input, tok)
		}
	}
}

// TestTokenize_FdDup_Consumer verifies the consumer-level contract: Tokenize of
// "golangci-lint run 2>&1" produces zero TypeRedirectTarget tokens.
func TestTokenize_FdDup_Consumer(t *testing.T) {
	input := `golangci-lint run 2>&1`
	tokens, err := Tokenize(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, tok := range tokens {
		if tok.Type == TypeRedirectTarget {
			t.Errorf("Tokenize(%q): unexpected redirect_target %q; 2>&1 fd-dup must not produce path tokens",
				input, tok.Value)
		}
	}
}

// TestTokenize_TimeClause verifies that a time-prefixed statement is tokenized.
func TestTokenize_TimeClause(t *testing.T) {
	tokens, err := Tokenize(`time rm internal/x.go`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContainsTokens(t, tokens,
		Token{Value: "rm", Type: TypeWord},
		Token{Value: "internal/x.go", Type: TypeWord},
	)
}

// assertContainsTokens checks that each of the expected tokens appears in got.
func assertContainsTokens(t *testing.T, got []Token, expected ...Token) {
	t.Helper()
	for _, want := range expected {
		found := false
		for _, tok := range got {
			if tok == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected token %v in %v", want, formatTokens(got))
		}
	}
}

// assertContainsToken checks that at least one token in got satisfies predicate.
func assertContainsToken(t *testing.T, got []Token, pred func(Token) bool, msg string) {
	t.Helper()
	for _, tok := range got {
		if pred(tok) {
			return
		}
	}
	t.Errorf("%s: tokens = %v", msg, formatTokens(got))
}
