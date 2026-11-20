package deploy

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// releaseStatusText is the release row status text internal/agent/state owns
// (docs/specs/agent-state.md, "releases"). The executor learns from
// state.DueStop.Retired whether a due stop removes its container.
var releaseStatusText = []string{"candidate", "current", "previous", "retired", "failed", "aborted"}

// TestDeployDeclaresNoReleaseStatusText covers docs/specs/agent-state.md,
// "Rules": the release status text has one owner, internal/agent/state, so no
// non-test file of this package declares it again.
func TestDeployDeclaresNoReleaseStatusText(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("filepath.Glob(%q) error = %v, want nil", "*.go", err)
	}

	parsed := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed++

		fset := token.NewFileSet()
		parsedFile, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("parser.ParseFile(%q) error = %v, want nil", file, err)
		}
		ast.Inspect(parsedFile, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Errorf("%s: strconv.Unquote(%q) error = %v, want nil", fset.Position(lit.Pos()), lit.Value, err)
				return true
			}
			for _, status := range releaseStatusText {
				if value == status {
					t.Errorf("%s: string literal %q is release status text owned by internal/agent/state; use state.DueStop.Retired",
						fset.Position(lit.Pos()), value)
				}
			}
			return true
		})
	}
	if parsed == 0 {
		t.Fatalf("parsed no non-test .go file in the package, want at least one")
	}
}
