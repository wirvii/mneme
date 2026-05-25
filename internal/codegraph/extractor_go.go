package codegraph

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strings"
	"time"
)

// GoExtractor parses Go source files using the standard go/ast, go/parser, and
// go/token packages. It is stateless and safe for concurrent use.
type GoExtractor struct{}

// NewGoExtractor returns a new GoExtractor. Callers may reuse the same instance
// across multiple Extract calls.
func NewGoExtractor() *GoExtractor { return &GoExtractor{} }

// Language returns "go", the language identifier for this extractor.
func (e *GoExtractor) Language() string { return "go" }

// Extract parses the Go source in content (attributed to filePath for IDs and
// error messages) and returns all nodes, edges, and unresolved references it
// finds. It never returns nil — a partial result is returned alongside any error.
func (e *GoExtractor) Extract(filePath string, content []byte) (*ExtractionResult, error) {
	start := time.Now()
	result := &ExtractionResult{}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, content, parser.ParseComments)
	if err != nil {
		result.Errors = append(result.Errors, ExtractionError{
			Message:  err.Error(),
			FilePath: filePath,
			Severity: "error",
			Code:     "parse_error",
		})
		result.DurationMs = time.Since(start).Milliseconds()
		return result, err
	}

	ex := &goExtraction{
		fset:     fset,
		file:     f,
		filePath: filePath,
		result:   result,
		// topLevel maps unqualified name → NodeID for same-file call resolution.
		topLevel: make(map[string]string),
	}
	ex.run()
	result.DurationMs = time.Since(start).Milliseconds()
	return result, nil
}

// ---------------------------------------------------------------------------
// Internal extraction state
// ---------------------------------------------------------------------------

// goExtraction holds mutable state for a single file extraction pass so that
// the stateless GoExtractor struct does not carry per-file fields.
type goExtraction struct {
	fset     *token.FileSet
	file     *ast.File
	filePath string
	result   *ExtractionResult
	// topLevel maps short function/method names to their NodeIDs for same-file
	// call resolution. Methods are keyed as "ReceiverType.MethodName".
	topLevel map[string]string
	// fileNodeID is the NodeID of the synthetic file node.
	fileNodeID string
}

// run orchestrates the two-pass extraction: first collect top-level declarations
// (building topLevel map), then walk call expressions inside bodies.
func (ex *goExtraction) run() {
	ex.addFileNode()
	ex.extractDeclarations()
	ex.extractCalls()
}

// addFileNode creates the synthetic file-level node that contains all top-level
// declarations and adds it to the result.
func (ex *goExtraction) addFileNode() {
	pos := ex.fset.Position(ex.file.Pos())
	end := ex.fset.Position(ex.file.End())
	id := NodeID(ex.filePath, ex.filePath)
	ex.fileNodeID = id

	ex.result.Nodes = append(ex.result.Nodes, Node{
		ID:            id,
		Kind:          NodeKindFile,
		Name:          ex.filePath,
		QualifiedName: ex.filePath,
		FilePath:      ex.filePath,
		Language:      "go",
		StartLine:     pos.Line,
		EndLine:       end.Line,
		StartColumn:   pos.Column - 1,
		EndColumn:     end.Column - 1,
	})
}

// extractDeclarations walks all top-level declarations in the file and produces
// nodes plus file→declaration contains edges.
func (ex *goExtraction) extractDeclarations() {
	for _, decl := range ex.file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			ex.extractFuncDecl(d)
		case *ast.GenDecl:
			ex.extractGenDecl(d)
		}
	}
}

// extractFuncDecl handles *ast.FuncDecl — both top-level functions and methods.
func (ex *goExtraction) extractFuncDecl(d *ast.FuncDecl) {
	if d.Name == nil {
		return
	}
	name := d.Name.Name
	qualifiedName := name
	kind := NodeKindFunction

	if d.Recv != nil && len(d.Recv.List) > 0 {
		kind = NodeKindMethod
		receiverType := receiverTypeName(d.Recv.List[0].Type)
		qualifiedName = receiverType + "." + name
	}

	id := NodeID(ex.filePath, qualifiedName)
	ex.topLevel[qualifiedName] = id
	// Also index by short name for simple same-file call resolution.
	ex.topLevel[name] = id

	pos := ex.fset.Position(d.Pos())
	end := ex.fset.Position(d.End())

	node := Node{
		ID:            id,
		Kind:          kind,
		Name:          name,
		QualifiedName: qualifiedName,
		FilePath:      ex.filePath,
		Language:      "go",
		StartLine:     pos.Line,
		EndLine:       end.Line,
		StartColumn:   pos.Column - 1,
		EndColumn:     end.Column - 1,
		IsExported:    ast.IsExported(name),
		Docstring:     extractDocstring(d.Doc),
		Signature:     buildFuncSignature(ex.fset, d.Type),
	}
	ex.result.Nodes = append(ex.result.Nodes, node)

	// file → declaration contains edge.
	ex.addContainsEdge(ex.fileNodeID, id, pos.Line, pos.Column-1)
}

// extractGenDecl handles *ast.GenDecl — type declarations, var blocks, const
// blocks, and import groups.
func (ex *goExtraction) extractGenDecl(d *ast.GenDecl) {
	switch d.Tok {
	case token.TYPE:
		for _, spec := range d.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			ex.extractTypeSpec(ts, d.Doc)
		}
	case token.VAR:
		for _, spec := range d.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			ex.extractValueSpec(vs, NodeKindVariable, d.Doc)
		}
	case token.CONST:
		for _, spec := range d.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			ex.extractValueSpec(vs, NodeKindConstant, d.Doc)
		}
	case token.IMPORT:
		for _, spec := range d.Specs {
			is, ok := spec.(*ast.ImportSpec)
			if !ok {
				continue
			}
			ex.extractImportSpec(is)
		}
	}
}

// extractTypeSpec handles a single *ast.TypeSpec within a TYPE GenDecl.
func (ex *goExtraction) extractTypeSpec(ts *ast.TypeSpec, groupDoc *ast.CommentGroup) {
	if ts.Name == nil {
		return
	}
	name := ts.Name.Name
	id := NodeID(ex.filePath, name)
	ex.topLevel[name] = id

	var kind NodeKind
	switch ts.Type.(type) {
	case *ast.StructType:
		kind = NodeKindStruct
	case *ast.InterfaceType:
		kind = NodeKindInterface
	default:
		kind = NodeKindTypeAlias
	}

	// Per-spec doc: prefer TypeSpec.Doc, fall back to enclosing GenDecl doc.
	doc := ts.Doc
	if doc == nil {
		doc = groupDoc
	}

	pos := ex.fset.Position(ts.Pos())
	end := ex.fset.Position(ts.End())

	node := Node{
		ID:            id,
		Kind:          kind,
		Name:          name,
		QualifiedName: name,
		FilePath:      ex.filePath,
		Language:      "go",
		StartLine:     pos.Line,
		EndLine:       end.Line,
		StartColumn:   pos.Column - 1,
		EndColumn:     end.Column - 1,
		IsExported:    ast.IsExported(name),
		Docstring:     extractDocstring(doc),
	}
	ex.result.Nodes = append(ex.result.Nodes, node)
	ex.addContainsEdge(ex.fileNodeID, id, pos.Line, pos.Column-1)
}

// extractValueSpec handles a single *ast.ValueSpec (var or const).
func (ex *goExtraction) extractValueSpec(vs *ast.ValueSpec, kind NodeKind, groupDoc *ast.CommentGroup) {
	doc := vs.Doc
	if doc == nil {
		doc = groupDoc
	}
	for _, nameIdent := range vs.Names {
		if nameIdent == nil {
			continue
		}
		name := nameIdent.Name
		id := NodeID(ex.filePath, name)
		ex.topLevel[name] = id

		pos := ex.fset.Position(nameIdent.Pos())

		node := Node{
			ID:            id,
			Kind:          kind,
			Name:          name,
			QualifiedName: name,
			FilePath:      ex.filePath,
			Language:      "go",
			StartLine:     pos.Line,
			EndLine:       pos.Line,
			StartColumn:   pos.Column - 1,
			EndColumn:     pos.Column - 1 + len(name),
			IsExported:    ast.IsExported(name),
			Docstring:     extractDocstring(doc),
		}
		ex.result.Nodes = append(ex.result.Nodes, node)
		ex.addContainsEdge(ex.fileNodeID, id, pos.Line, pos.Column-1)
	}
}

// extractImportSpec handles a single *ast.ImportSpec (one import path).
func (ex *goExtraction) extractImportSpec(is *ast.ImportSpec) {
	if is.Path == nil {
		return
	}
	// Strip surrounding quotes from the import path string literal.
	importPath := strings.Trim(is.Path.Value, `"`)
	id := NodeID(ex.filePath, importPath)

	pos := ex.fset.Position(is.Pos())
	end := ex.fset.Position(is.End())

	node := Node{
		ID:            id,
		Kind:          NodeKindImport,
		Name:          importPath,
		QualifiedName: importPath,
		FilePath:      ex.filePath,
		Language:      "go",
		StartLine:     pos.Line,
		EndLine:       end.Line,
		StartColumn:   pos.Column - 1,
		EndColumn:     end.Column - 1,
	}
	ex.result.Nodes = append(ex.result.Nodes, node)

	// file → import node (imports edge).
	ex.result.Edges = append(ex.result.Edges, Edge{
		Source:     ex.fileNodeID,
		Target:     id,
		Kind:       EdgeKindImports,
		Line:       pos.Line,
		Col:        pos.Column - 1,
		Provenance: "ast",
	})
}

// extractCalls walks function/method bodies and records call expressions as
// calls edges (if the callee is in the same file) or as UnresolvedRefs.
func (ex *goExtraction) extractCalls() {
	for _, decl := range ex.file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		callerName := fd.Name.Name
		callerQN := callerName
		if fd.Recv != nil && len(fd.Recv.List) > 0 {
			callerQN = receiverTypeName(fd.Recv.List[0].Type) + "." + callerName
		}
		callerID, ok := ex.topLevel[callerQN]
		if !ok {
			callerID, ok = ex.topLevel[callerName]
			if !ok {
				continue
			}
		}
		ex.walkCallsInBody(fd.Body, callerID)
	}
}

// walkCallsInBody inspects all call expressions within a function body and adds
// the appropriate calls edge or unresolved reference.
func (ex *goExtraction) walkCallsInBody(body *ast.BlockStmt, callerID string) {
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		pos := ex.fset.Position(call.Pos())
		line := pos.Line
		col := pos.Column - 1

		switch fn := call.Fun.(type) {
		case *ast.SelectorExpr:
			// pkg.Func() or recv.Method() pattern.
			calleeName := fn.Sel.Name
			var qualifiedCallee string
			if ident, ok2 := fn.X.(*ast.Ident); ok2 {
				qualifiedCallee = ident.Name + "." + calleeName
			} else {
				qualifiedCallee = calleeName
			}
			// Check if the callee is in the same file (e.g. a local method call).
			if targetID, found := ex.topLevel[qualifiedCallee]; found {
				ex.result.Edges = append(ex.result.Edges, Edge{
					Source:     callerID,
					Target:     targetID,
					Kind:       EdgeKindCalls,
					Line:       line,
					Col:        col,
					Provenance: "ast",
				})
			} else {
				ex.result.UnresolvedRefs = append(ex.result.UnresolvedRefs, UnresolvedRef{
					FromNodeID:    callerID,
					ReferenceName: qualifiedCallee,
					ReferenceKind: EdgeKindCalls,
					Line:          line,
					Col:           col,
					FilePath:      ex.filePath,
					Language:      "go",
				})
			}

		case *ast.Ident:
			// Simple Func() call — check same-file first.
			calleeName := fn.Name
			if targetID, found := ex.topLevel[calleeName]; found {
				ex.result.Edges = append(ex.result.Edges, Edge{
					Source:     callerID,
					Target:     targetID,
					Kind:       EdgeKindCalls,
					Line:       line,
					Col:        col,
					Provenance: "ast",
				})
			} else {
				ex.result.UnresolvedRefs = append(ex.result.UnresolvedRefs, UnresolvedRef{
					FromNodeID:    callerID,
					ReferenceName: calleeName,
					ReferenceKind: EdgeKindCalls,
					Line:          line,
					Col:           col,
					FilePath:      ex.filePath,
					Language:      "go",
				})
			}
		}
		return true
	})
}

// addContainsEdge appends a contains edge from parent to child.
func (ex *goExtraction) addContainsEdge(parentID, childID string, line, col int) {
	ex.result.Edges = append(ex.result.Edges, Edge{
		Source:     parentID,
		Target:     childID,
		Kind:       EdgeKindContains,
		Line:       line,
		Col:        col,
		Provenance: "ast",
	})
}

// ---------------------------------------------------------------------------
// Pure helper functions
// ---------------------------------------------------------------------------

// receiverTypeName returns the base type name from a receiver type expression.
// It strips pointer indirection (* or &) so that "*MemoryService" yields
// "MemoryService".
func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return receiverTypeName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		// Generic receiver e.g. *Foo[T].
		return receiverTypeName(t.X)
	default:
		return ""
	}
}

// extractDocstring returns the trimmed text from a CommentGroup, or "" if nil.
func extractDocstring(doc *ast.CommentGroup) string {
	if doc == nil {
		return ""
	}
	return strings.TrimSpace(doc.Text())
}

// buildFuncSignature constructs a human-readable signature string from a
// *ast.FuncType using go/printer for accurate formatting. The signature is
// formatted as "(params) (results)", e.g. "(ctx context.Context) error".
func buildFuncSignature(fset *token.FileSet, ft *ast.FuncType) string {
	if ft == nil {
		return ""
	}
	var buf bytes.Buffer

	// Parameters.
	buf.WriteByte('(')
	if ft.Params != nil {
		writeFieldList(&buf, fset, ft.Params)
	}
	buf.WriteByte(')')

	// Results.
	if ft.Results != nil && len(ft.Results.List) > 0 {
		buf.WriteByte(' ')
		if len(ft.Results.List) == 1 && len(ft.Results.List[0].Names) == 0 {
			// Single unnamed return value — no parentheses needed.
			writeExpr(&buf, fset, ft.Results.List[0].Type)
		} else {
			buf.WriteByte('(')
			writeFieldList(&buf, fset, ft.Results)
			buf.WriteByte(')')
		}
	}
	return buf.String()
}

// writeFieldList formats a *ast.FieldList into buf using go/printer for type
// expressions. Fields are comma-separated; named fields include the name.
func writeFieldList(buf *bytes.Buffer, fset *token.FileSet, fl *ast.FieldList) {
	for i, field := range fl.List {
		if i > 0 {
			buf.WriteString(", ")
		}
		// Write names (may be absent for unnamed parameters / return values).
		for j, name := range field.Names {
			if j > 0 {
				buf.WriteString(", ")
			}
			buf.WriteString(name.Name)
		}
		if len(field.Names) > 0 {
			buf.WriteByte(' ')
		}
		writeExpr(buf, fset, field.Type)
	}
}

// writeExpr formats a single ast.Expr into buf using go/printer.
func writeExpr(buf *bytes.Buffer, fset *token.FileSet, expr ast.Expr) {
	if err := printer.Fprint(buf, fset, expr); err != nil {
		// Fallback: use %T so we never silently drop the type.
		buf.WriteString("?")
	}
}
