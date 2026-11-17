package registry_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// Only the registry's audit method may append to the chain, so a handler
// cannot forge or reorder an event.
func TestAppendAuditIsCalledOnlyByTheRegistry(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	found := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(path)
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "AppendAudit" {
				return true
			}
			found++
			if !strings.HasSuffix(slash, "internal/registry/registry.go") && !strings.HasSuffix(slash, "internal/registry/audit.go") {
				t.Errorf("%s calls AppendAudit, want only internal/registry/registry.go or audit.go", fset.Position(call.Pos()))
				return true
			}
			// Inside the registry, only the audit method may call it.
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if ok && fn.Pos() <= call.Pos() && call.Pos() <= fn.End() && fn.Name.Name != "audit" {
					t.Errorf("%s calls AppendAudit in %s, want only the audit method", fset.Position(call.Pos()), fn.Name.Name)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk module: %v", err)
	}
	if found == 0 {
		t.Errorf("found no AppendAudit call, want the registry's audit method to call it")
	}
}
