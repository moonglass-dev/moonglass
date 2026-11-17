// Package cli is the moonglass command line: subcommands generated from the
// operation registry plus the local-only commands listed beside the registry.
package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"moonglass.dev/moonglass/internal/registry"
)

const requestTimeout = 30 * time.Second

// command is a generated subcommand: one registry operation.
type command struct {
	Name    string
	Method  string
	Path    string
	Summary string
}

// Subcommands lists every subcommand: generated and local.
func Subcommands() []string {
	var names []string
	for _, c := range generated {
		names = append(names, c.Name)
	}
	for _, c := range registry.LocalCommands {
		names = append(names, c.Name)
	}
	return names
}

// Usage is the help text listing every subcommand.
func Usage() string {
	var b strings.Builder
	b.WriteString("Usage: moonglass <command>\n\nCommands:\n")
	for _, c := range registry.LocalCommands {
		fmt.Fprintf(&b, "  %s\n      %s\n", c.Synopsis, c.Summary)
	}
	for _, c := range generated {
		fmt.Fprintf(&b, "  %s\n      %s (needs MOONGLASS_URL and MOONGLASS_TOKEN)\n", c.Name, c.Summary)
	}
	return b.String()
}

// Run dispatches args. local maps each registry.LocalCommands name to its
// handler, which receives the arguments after the command name; its keys must
// equal that list. It returns the process exit code.
func Run(args []string, env func(string) string, stdout, stderr io.Writer, local map[string]func(args []string) int) int {
	for _, c := range registry.LocalCommands {
		if local[c.Name] == nil {
			_, _ = fmt.Fprintf(stderr, "moonglass: no handler for local command %q: pass one for every registry.LocalCommands entry\n", c.Name)
			return 2
		}
	}
	if len(local) != len(registry.LocalCommands) {
		_, _ = fmt.Fprintf(stderr, "moonglass: %d local handlers for %d registry.LocalCommands: remove the handlers that are not on the list\n", len(local), len(registry.LocalCommands))
		return 2
	}
	best, words := "", 0
	for _, name := range Subcommands() {
		n := len(strings.Fields(name))
		if n > words && len(args) >= n && slices.Equal(args[:n], strings.Fields(name)) {
			best, words = name, n
		}
	}
	if h, ok := local[best]; ok {
		return h(args[words:])
	}
	for _, c := range generated {
		if c.Name == best {
			return runGenerated(c, args[words:], env, stdout, stderr)
		}
	}
	_, _ = io.WriteString(stderr, Usage())
	return 2
}

func runGenerated(c command, args []string, env func(string) string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		_, _ = io.WriteString(stderr, Usage())
		return 2
	}
	base, token := strings.TrimRight(env("MOONGLASS_URL"), "/"), env("MOONGLASS_TOKEN")
	if base == "" || token == "" {
		_, _ = fmt.Fprintf(stderr, "moonglass: %s needs MOONGLASS_URL (the control-plane URL) and MOONGLASS_TOKEN (an owner token)\n", c.Name)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, c.Method, base+c.Path, http.NoBody)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "moonglass: %s: build request: %v\n", c.Name, err)
		return 1
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "moonglass: %s: call %s: %v\n", c.Name, base, err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	out := stdout
	code := 0
	if resp.StatusCode >= http.StatusBadRequest {
		out, code = stderr, 1
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		_, _ = fmt.Fprintf(stderr, "moonglass: %s: read response: %v\n", c.Name, err)
		return 1
	}
	return code
}
