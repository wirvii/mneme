// Package shell provides shell command tokenization using a real bash/POSIX AST
// parser. It exposes Tokenize, a pure function that converts a shell command
// string into a slice of structured Tokens suitable for enforcement hooks.
//
// Design constraints (D2, SPEC-033):
//   - Leaf package: zero I/O dependencies, zero deps on store/service/db.
//   - Import direction: cli/ -> shell/ -> mvdan.cc/sh/v3/syntax.
//   - No globals, no init(). All state is local to Tokenize.
//
// Compound command types (if, for, while, case, function bodies, etc.) are
// walked recursively so that protected paths inside control-flow constructs
// are visible to enforcement hooks. bash -c "..." and $() nodes are emitted
// as-is; the calling bash hook handles those via 1-level recursion (D5).
package shell

import (
	"bytes"
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// TokenType describes the semantic role of a token in a shell command.
type TokenType string

const (
	// TypeWord is a normal word: a command name, argument, or flag.
	TypeWord TokenType = "word"
	// TypeRedirect is a redirect operator (>, >>, 2>, &>, <, <<, etc.).
	TypeRedirect TokenType = "redirect"
	// TypeRedirectTarget is the file path or word that is the destination of a redirect.
	TypeRedirectTarget TokenType = "redirect_target"
	// TypeHeredocBody is the literal body text of a here-document.
	TypeHeredocBody TokenType = "heredoc_body"
	// TypeCommandSubstitution is the command text inside $(...) or backticks.
	TypeCommandSubstitution TokenType = "command_substitution"
	// TypeSeparator marks the boundary between two sub-commands in a BinaryCmd
	// (pipeline |, logical-and &&, or logical-or ||). It prevents enforcement
	// hooks from crossing statement boundaries when scanning for the target of a
	// destructive command. Value holds the operator string ("|", "&&", "||").
	TypeSeparator TokenType = "separator"
)

// Token is a single element extracted from a shell command string.
//
// Value holds the extracted text:
//   - TypeWord: the word as the shell would see it (without surrounding quotes).
//   - TypeRedirect: the operator string (e.g. ">", ">>", "2>", "&>").
//   - TypeRedirectTarget: the path or word after the operator.
//   - TypeHeredocBody: the raw here-document body, including the trailing newline.
//   - TypeCommandSubstitution: the command string inside $(...) or backticks.
//
// Quoted is true when Value came from a single-quoted or double-quoted string,
// meaning it should NOT be interpreted as a shell command by the caller.
type Token struct {
	Value  string    `json:"value"`
	Type   TokenType `json:"type"`
	Quoted bool      `json:"quoted,omitempty"`
}

// Tokenize parses command using mvdan.cc/sh/v3/syntax and returns a flat list
// of structured tokens. It processes all statements in the input (semicolon-
// or newline-separated commands are walked in order).
//
// Error handling: a parse error returns a non-nil error. An empty command
// returns an empty slice with no error. The caller is expected to treat any
// error as a signal to fall back to a safer parser.
func Tokenize(command string) ([]Token, error) {
	if strings.TrimSpace(command) == "" {
		return nil, nil
	}

	r := strings.NewReader(command)
	f, err := syntax.NewParser().Parse(r, "")
	if err != nil {
		return nil, fmt.Errorf("shell: parse: %w", err)
	}

	return tokensFromStmtList(f.Stmts), nil
}

// tokensFromStmtList converts a slice of Stmts into a flat token list.
// A TypeSeparator with value ";" is emitted between consecutive statements
// so that enforcement hooks respect statement boundaries.
func tokensFromStmtList(stmts []*syntax.Stmt) []Token {
	var out []Token
	for i, s := range stmts {
		if i > 0 {
			out = append(out, Token{Value: ";", Type: TypeSeparator})
		}
		out = append(out, tokensFromStmt(s)...)
	}
	return out
}

// tokensFromStmt extracts tokens from a single statement. It handles CallExpr
// (simple commands), BinaryCmd (pipelines, &&, ||), and all compound command
// types (Subshell, Block, IfClause, ForClause, WhileClause, CaseClause,
// FuncDecl, TimeClause, CoprocClause). Any attached redirects are always
// appended at the end regardless of command type.
func tokensFromStmt(stmt *syntax.Stmt) []Token {
	var tokens []Token

	switch cmd := stmt.Cmd.(type) {
	case *syntax.CallExpr:
		// Simple command: extract all argument words.
		for _, arg := range cmd.Args {
			tokens = append(tokens, tokensFromWord(arg)...)
		}

	case *syntax.BinaryCmd:
		// Pipeline or && / || compound: walk both sides recursively and emit
		// a separator token between them. The separator carries the operator
		// string ("|", "&&", "||") so that enforcement hooks can stop at the
		// boundary and never cross into the next sub-command's arguments.
		tokens = append(tokens, tokensFromStmt(cmd.X)...)
		tokens = append(tokens, Token{Value: cmd.Op.String(), Type: TypeSeparator})
		tokens = append(tokens, tokensFromStmt(cmd.Y)...)

	case *syntax.Subshell:
		// ( stmts ) — walk inner statements.
		tokens = append(tokens, tokensFromStmtList(cmd.Stmts)...)

	case *syntax.Block:
		// { stmts; } — walk inner statements.
		tokens = append(tokens, tokensFromStmtList(cmd.Stmts)...)

	case *syntax.IfClause:
		// if cond; then body; [elif/else ...]; fi
		// Walk condition, then-body, and the chain of elif/else clauses.
		tokens = append(tokens, tokensFromStmtList(cmd.Cond)...)
		tokens = append(tokens, tokensFromStmtList(cmd.Then)...)
		for chain := cmd.Else; chain != nil; chain = chain.Else {
			tokens = append(tokens, Token{Value: ";", Type: TypeSeparator})
			tokens = append(tokens, tokensFromStmtList(chain.Cond)...)
			tokens = append(tokens, tokensFromStmtList(chain.Then)...)
		}

	case *syntax.WhileClause:
		// while/until cond; do body; done
		// WhileClause.Until==true means "until". Either way we walk the same fields.
		tokens = append(tokens, tokensFromStmtList(cmd.Cond)...)
		tokens = append(tokens, tokensFromStmtList(cmd.Do)...)

	case *syntax.ForClause:
		// for var in items; do body; done  (also handles select)
		// Walk the item-list words (which may contain protected paths) and the body.
		if wi, ok := cmd.Loop.(*syntax.WordIter); ok {
			for _, item := range wi.Items {
				tokens = append(tokens, tokensFromWord(item)...)
			}
		}
		tokens = append(tokens, tokensFromStmtList(cmd.Do)...)

	case *syntax.CaseClause:
		// case word in pattern) stmts;; esac
		// Walk the discriminant word and each item's statement list.
		tokens = append(tokens, tokensFromWord(cmd.Word)...)
		for _, item := range cmd.Items {
			tokens = append(tokens, tokensFromStmtList(item.Stmts)...)
		}

	case *syntax.FuncDecl:
		// f() { body; }  — walk the function body statement.
		if cmd.Body != nil {
			tokens = append(tokens, tokensFromStmt(cmd.Body)...)
		}

	case *syntax.TimeClause:
		// time stmt — walk the timed statement if present.
		if cmd.Stmt != nil {
			tokens = append(tokens, tokensFromStmt(cmd.Stmt)...)
		}

	case *syntax.CoprocClause:
		// coproc [name] stmt — walk the coprocess statement.
		if cmd.Stmt != nil {
			tokens = append(tokens, tokensFromStmt(cmd.Stmt)...)
		}

		// *syntax.TestClause, *syntax.LetClause, *syntax.DeclClause:
		// These contain arithmetic/test expressions or variable assignments,
		// not command lists that could reference protected paths. Skip.
	}

	// Redirects are attached to the Stmt regardless of command type.
	for _, redir := range stmt.Redirs {
		tokens = append(tokens, tokensFromRedirect(redir)...)
	}

	return tokens
}

// tokensFromWord converts a Word into one or more tokens. A word with a single
// unquoted literal part produces one TypeWord token. A word whose top-level
// parts contain quotes produces a TypeWord with Quoted=true. A word that
// contains only a command substitution part produces a TypeCommandSubstitution
// token. Compound words (e.g. $(date +%s).txt) are split into their parts.
func tokensFromWord(w *syntax.Word) []Token {
	if len(w.Parts) == 0 {
		return nil
	}

	// Fast path: single Lit — plain unquoted word.
	if len(w.Parts) == 1 {
		switch p := w.Parts[0].(type) {
		case *syntax.Lit:
			return []Token{{Value: p.Value, Type: TypeWord}}
		case *syntax.SglQuoted:
			return []Token{{Value: p.Value, Type: TypeWord, Quoted: true}}
		case *syntax.DblQuoted:
			return []Token{{Value: extractDblQuoted(p), Type: TypeWord, Quoted: true}}
		case *syntax.CmdSubst:
			return []Token{{Value: extractCmdSubst(p), Type: TypeCommandSubstitution}}
		}
	}

	// Multi-part word: walk parts and collect tokens.
	// If the word contains any quoted part, the whole word is considered quoted
	// from the enforcement perspective. We emit individual tokens per part type
	// so the caller can still inspect command substitutions separately.
	var result []Token
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			if p.Value != "" {
				result = append(result, Token{Value: p.Value, Type: TypeWord})
			}
		case *syntax.SglQuoted:
			result = append(result, Token{Value: p.Value, Type: TypeWord, Quoted: true})
		case *syntax.DblQuoted:
			result = append(result, Token{Value: extractDblQuoted(p), Type: TypeWord, Quoted: true})
		case *syntax.CmdSubst:
			result = append(result, Token{Value: extractCmdSubst(p), Type: TypeCommandSubstitution})
		default:
			// ParamExp, ArithmExp, etc.: reconstruct via printer.
			val := reconstructNode(part)
			if val != "" {
				result = append(result, Token{Value: val, Type: TypeWord})
			}
		}
	}
	return result
}

// tokensFromRedirect converts a Redirect node into tokens. It always emits a
// TypeRedirect token for the operator, followed by either a TypeRedirectTarget
// token for the destination word or a TypeHeredocBody token for here-docs.
func tokensFromRedirect(r *syntax.Redirect) []Token {
	var tokens []Token

	// Build the operator string: N (fd number) + Op string (e.g. "2" + ">" = "2>").
	opStr := r.Op.String()
	if r.N != nil {
		opStr = r.N.Value + opStr
	}
	tokens = append(tokens, Token{Value: opStr, Type: TypeRedirect})

	switch r.Op {
	case syntax.Hdoc, syntax.DashHdoc:
		// Here-doc: the redirect target word is the delimiter (skip it);
		// the body is in r.Hdoc.
		if r.Hdoc != nil {
			body := extractWordLiteral(r.Hdoc)
			tokens = append(tokens, Token{Value: body, Type: TypeHeredocBody})
		}
	default:
		// Normal redirect: the target is r.Word.
		if r.Word != nil {
			target := extractWordLiteral(r.Word)
			tokens = append(tokens, Token{Value: target, Type: TypeRedirectTarget})
		}
	}

	return tokens
}

// extractDblQuoted reconstructs the string value of a double-quoted word by
// concatenating its Lit parts. Non-Lit parts (e.g. parameter expansions) are
// reconstructed via the printer so the value is as complete as possible.
func extractDblQuoted(q *syntax.DblQuoted) string {
	var sb strings.Builder
	for _, part := range q.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			sb.WriteString(p.Value)
		case *syntax.CmdSubst:
			// Include the $(...) representation so the caller can detect it.
			sb.WriteString(reconstructNode(p))
		default:
			sb.WriteString(reconstructNode(part))
		}
	}
	return sb.String()
}

// extractCmdSubst reconstructs the command text inside a CmdSubst node using
// the syntax printer. The returned string does not include the surrounding
// $( ) or backtick delimiters.
func extractCmdSubst(c *syntax.CmdSubst) string {
	if len(c.Stmts) == 0 {
		return ""
	}
	// Create a synthetic File containing only these statements so the printer
	// can format them as a command string.
	f := &syntax.File{Stmts: c.Stmts}
	return strings.TrimSpace(printNode(f))
}

// extractWordLiteral returns the plain string value of a Word. For simple
// literal words it avoids the printer. For complex words it falls back to the
// printer.
func extractWordLiteral(w *syntax.Word) string {
	if w == nil {
		return ""
	}
	// Try fast path: all Lit parts.
	if lit := w.Lit(); lit != "" {
		return lit
	}
	// Multi-part or non-literal: concatenate all parts.
	var sb strings.Builder
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			sb.WriteString(p.Value)
		case *syntax.SglQuoted:
			sb.WriteString(p.Value)
		case *syntax.DblQuoted:
			sb.WriteString(extractDblQuoted(p))
		default:
			sb.WriteString(reconstructNode(part))
		}
	}
	return sb.String()
}

// reconstructNode uses the syntax printer to render any Node back to a string.
// This is used as a fallback for node types not handled by the fast paths.
func reconstructNode(node syntax.Node) string {
	return strings.TrimRight(printNode(node), "\n")
}

// printNode renders node to a string via syntax.NewPrinter.
func printNode(node syntax.Node) string {
	var buf bytes.Buffer
	printer := syntax.NewPrinter(syntax.Minify(true))
	//nolint:errcheck // printer errors are non-actionable for in-memory nodes
	_ = printer.Print(&buf, node)
	return buf.String()
}
