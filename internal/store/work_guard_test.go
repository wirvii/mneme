package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestWorkContractHasNoAlternativeWriters(t *testing.T) {
	source, err := os.ReadFile("work.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "work.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	functionSQL := map[string]string{}
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		var sql strings.Builder
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if ok && literal.Kind == token.STRING {
				sql.WriteString(literal.Value)
				sql.WriteByte('\n')
			}
			return true
		})
		functionSQL[fn.Name.Name] = sql.String()
	}

	amend := functionSQL["AmendWork"]
	set := regexp.MustCompile(`SET goal=\?,scope_json=\?,verification_json=\?,development_method=\?`)
	if !set.MatchString(amend) {
		t.Fatal("AmendWork no longer declares the normative contract update")
	}
	protected := []string{"goal=", "scope_json=", "verification_json=", "development_method=", "declaration=", "criterion_key=", "constraint_key=", "text="}
	for function, body := range functionSQL {
		if function == "AmendWork" || function == "CreateWork" || function == "replaceWorkChildren" {
			continue
		}
		upper := strings.ToUpper(body)
		if !strings.Contains(upper, "UPDATE") && !strings.Contains(upper, "INSERT") && !strings.Contains(upper, "DELETE") {
			continue
		}
		for _, column := range protected {
			if strings.Contains(body, column) {
				t.Errorf("%s writes protected contract column %q", function, strings.TrimSuffix(column, "="))
			}
		}
	}
}
