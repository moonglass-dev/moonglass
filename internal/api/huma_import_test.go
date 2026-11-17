package api_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestHumaConfinedToAPI fails if any Go file outside internal/api imports Huma
// (ADR-0013). It walks the whole module so a package added later is covered.
func TestHumaConfinedToAPI(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("module root: %v", err)
	}
	apiDir := filepath.Join(root, "internal", "api")
	const huma = "github.com/danielgtaylor/huma/v2"
	fset := token.NewFileSet()
	var bad []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "testdata", "bin":
				return filepath.SkipDir
			}
			if path == apiDir {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			if p == huma || strings.HasPrefix(p, huma+"/") {
				rel, _ := filepath.Rel(root, path)
				bad = append(bad, rel+" imports "+p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if len(bad) > 0 {
		t.Errorf("Huma imported outside internal/api: %v, want none", bad)
	}
	if _, err := os.Stat(apiDir); err != nil {
		t.Fatalf("internal/api missing: %v", err)
	}
}
