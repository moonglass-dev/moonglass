// Command moonglass is the single Moonglass binary.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"moonglass.dev/moonglass/internal/agent"
	"moonglass.dev/moonglass/internal/server"
)

// Set at build time with -ldflags "-X main.version=...".
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

const usage = `Usage: moonglass <command>

Commands:
  version                                                  Print the version, commit and build date
  server [--data-dir DIR] [--listen ADDR] [--print-enroll-token]
                                                           Run the control plane
  agent enroll [--data-dir DIR] --url URL TOKEN            Enrol this machine and print its fingerprint
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	switch {
	case len(args) == 1 && args[0] == "version":
		return runVersion(stdout, stderr)
	case len(args) >= 1 && args[0] == "server":
		return runServer(args[1:], stdout, stderr)
	case len(args) >= 2 && args[0] == "agent" && args[1] == "enroll":
		return runAgentEnroll(args[2:], stdout, stderr)
	}
	_, _ = io.WriteString(stderr, usage)
	return 2
}

func runVersion(stdout, stderr io.Writer) int {
	if _, err := fmt.Fprintf(stdout, "moonglass %s\ncommit %s\nbuilt %s\n", version, commit, date); err != nil {
		_, _ = fmt.Fprintf(stderr, "moonglass: print version: %v\n", err)
		return 1
	}
	return 0
}

func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func runServer(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("server")
	dataDir := fs.String("data-dir", "/var/lib/moonglass", "server data directory")
	listen := fs.String("listen", "127.0.0.1:8080", "listen address")
	printToken := fs.Bool("print-enroll-token", false, "issue one enrolment token and print it")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		_, _ = io.WriteString(stderr, usage)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Run(ctx, *dataDir, *listen, *printToken, stdout); err != nil {
		_, _ = fmt.Fprintf(stderr, "moonglass: server: %v\n", err)
		return 1
	}
	return 0
}

func runAgentEnroll(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("agent enroll")
	dataDir := fs.String("data-dir", "/var/lib/moonglass/agent", "agent data directory")
	url := fs.String("url", "", "control-plane URL")
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 || *url == "" {
		_, _ = io.WriteString(stderr, usage)
		return 2
	}
	fingerprint, err := agent.Enroll(context.Background(), http.DefaultClient, *dataDir, *url, fs.Arg(0))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "moonglass: %v\n", err)
		return 1
	}
	if _, err := fmt.Fprintln(stdout, fingerprint); err != nil {
		_, _ = fmt.Fprintf(stderr, "moonglass: print fingerprint: %v\n", err)
		return 1
	}
	return 0
}
