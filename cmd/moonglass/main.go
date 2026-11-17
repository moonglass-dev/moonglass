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
	"moonglass.dev/moonglass/internal/cli"
	"moonglass.dev/moonglass/internal/server"
)

// Set at build time with -ldflags "-X main.version=...".
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// usage lists the generated and the local subcommands.
var usage = cli.Usage()

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches through cli.Run: operations are generated from the registry,
// and these are the local-only commands named in registry.LocalCommands.
func run(args []string, stdout, stderr io.Writer) int {
	local := map[string]func([]string) int{
		"version": func(rest []string) int {
			if len(rest) != 0 {
				_, _ = io.WriteString(stderr, usage)
				return 2
			}
			return runVersion(stdout, stderr)
		},
		"server":        func(rest []string) int { return runServer(rest, stdout, stderr) },
		"agent enroll":  func(rest []string) int { return runAgentEnroll(rest, stdout, stderr) },
		"agent connect": func(rest []string) int { return runAgentConnect(rest, stderr) },
		"proxy":         func(rest []string) int { return runProxy(rest, stderr) },
	}
	return cli.Run(args, os.Getenv, stdout, stderr, local)
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
	listen := fs.String("listen", "127.0.0.1:8080", "enrolment listen address (plain HTTP)")
	agentListen := fs.String("agent-listen", "127.0.0.1:8443", "agent listen address (TLS 1.3 WSS)")
	printToken := fs.Bool("print-enroll-token", false, "issue one enrolment token and print it")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		_, _ = io.WriteString(stderr, usage)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Run(ctx, *dataDir, *listen, *agentListen, *printToken, stdout); err != nil {
		_, _ = fmt.Fprintf(stderr, "moonglass: server: %v\n", err)
		return 1
	}
	return 0
}

func runAgentConnect(args []string, stderr io.Writer) int {
	fs := newFlags("agent connect")
	dataDir := fs.String("data-dir", "/var/lib/moonglass/agent", "agent data directory")
	url := fs.String("url", "", "control-plane URL")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *url == "" {
		_, _ = io.WriteString(stderr, usage)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	opts := agent.ConnectOptions{
		AgentVersion: version,
		OnError:      func(err error) { _, _ = fmt.Fprintf(stderr, "moonglass: %v\n", err) },
	}
	if err := agent.Connect(ctx, *dataDir, *url, opts); err != nil {
		_, _ = fmt.Fprintf(stderr, "moonglass: %v\n", err)
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
