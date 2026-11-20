package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"moonglass.dev/moonglass/internal/registry"
)

// RunDeclared runs the generated subcommand that args name, built from ops, and
// returns its exit code. It lets tests exercise the same flag derivation that
// Generate writes to generated.go.
func RunDeclared(ops []registry.Declaration, args []string, env func(string) string, stdout, stderr io.Writer) (int, error) {
	cmds, err := commands(ops, nil)
	if err != nil {
		return 0, fmt.Errorf("generate commands: %w", err)
	}
	best, words := -1, 0
	for i, c := range cmds {
		fields := strings.Fields(c.Name)
		if len(fields) > words && len(args) >= len(fields) && slices.Equal(args[:len(fields)], fields) {
			best, words = i, len(fields)
		}
	}
	if best < 0 {
		return 0, fmt.Errorf("no generated command matches %q", args)
	}
	return runGenerated(cmds[best], args[words:], env, stdout, stderr), nil
}
