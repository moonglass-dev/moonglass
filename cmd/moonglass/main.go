// Command moonglass is the single Moonglass binary.
package main

import (
	"fmt"
	"io"
	"os"
)

// Set at build time with -ldflags "-X main.version=...".
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

const usage = `Usage: moonglass <command>

Commands:
  version   Print the version, commit and build date
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || args[0] != "version" {
		_, _ = io.WriteString(stderr, usage)
		return 2
	}
	if _, err := fmt.Fprintf(stdout, "moonglass %s\ncommit %s\nbuilt %s\n", version, commit, date); err != nil {
		_, _ = fmt.Fprintf(stderr, "moonglass: print version: %v\n", err)
		return 1
	}
	return 0
}
