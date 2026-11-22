// Command moonglass is the single Moonglass binary.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"moonglass.dev/moonglass/internal/agent"
	"moonglass.dev/moonglass/internal/agent/state"
	"moonglass.dev/moonglass/internal/cli"
	"moonglass.dev/moonglass/internal/deploy"
	"moonglass.dev/moonglass/internal/policy"
	"moonglass.dev/moonglass/internal/proxy"
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
		"keys init":     func(rest []string) int { return runKeysInit(rest, stdout, stderr) },
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
	listen := fs.String("listen", "127.0.0.1:8080", "API and enrolment listen address (TLS 1.3)")
	agentListen := fs.String("agent-listen", "127.0.0.1:8443", "agent listen address (TLS 1.3 WSS)")
	printToken := fs.Bool("print-enroll-token", false, "issue one enrolment token and print it")
	ownerToken := fs.String("print-owner-token", "", "mint one owner token named NAME and print it")
	if err := fs.Parse(args); err != nil {
		_, _ = io.WriteString(stderr, usage)
		return 2
	}
	ownerGiven := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "print-owner-token" {
			ownerGiven = true
		}
	})
	if fs.NArg() != 0 || ownerGiven && *ownerToken == "" {
		_, _ = io.WriteString(stderr, usage)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Run(ctx, *dataDir, *listen, *agentListen, *printToken, *ownerToken, stdout); err != nil {
		_, _ = fmt.Fprintf(stderr, "moonglass: server: %v\n", err)
		return 1
	}
	return 0
}

// agentConnectArgs is the parsed form of `moonglass agent connect`. Parsing
// sets only the proxy socket and issuer; the run function sets OnError.
type agentConnectArgs struct {
	dataDir    string
	url        string
	policyFile string
	proxy      deploy.ProxyConfig
}

// parseAgentConnect parses the flags of `moonglass agent connect` and checks
// every value before the command opens or dials anything.
func parseAgentConnect(args []string) (agentConnectArgs, bool) {
	fs := newFlags("agent connect")
	dataDir := fs.String("data-dir", "/var/lib/moonglass/agent", "agent data directory")
	url := fs.String("url", "", "control-plane URL")
	policyFile := fs.String("policy", "/etc/moonglass/agent.toml", "local policy file (agent.toml)")
	socket := fs.String("proxy-socket", proxy.DefaultAdminSocket, "the proxy's agent-facing admin socket")
	issuer := fs.String("tls-issuer", "acme", "TLS issuer: acme, acme-staging or internal")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *url == "" || *policyFile == "" || *socket == "" {
		return agentConnectArgs{}, false
	}
	if err := deploy.CheckIssuer(*issuer); err != nil {
		return agentConnectArgs{}, false
	}
	return agentConnectArgs{
		dataDir:    *dataDir,
		url:        *url,
		policyFile: *policyFile,
		proxy:      deploy.ProxyConfig{Socket: *socket, Issuer: *issuer},
	}, true
}

// lockedWriter serialises the connect loop's and the push goroutine's writes.
type lockedWriter struct {
	w  io.Writer
	mu sync.Mutex
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func runAgentConnect(args []string, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return agentConnect(ctx, args, stderr)
}

// agentConnect loads agent.toml against the local Engine, opens the Engine and
// the agent state, then runs the proxy push and the executor beside the
// connection for as long as the command runs (docs/specs/connection.md,
// "Commands").
func agentConnect(ctx context.Context, args []string, stderr io.Writer) int {
	a, ok := parseAgentConnect(args)
	if !ok {
		_, _ = io.WriteString(stderr, usage)
		return 2
	}
	w := &lockedWriter{w: stderr}
	report := func(err error) { _, _ = fmt.Fprintf(w, "moonglass: %v\n", err) }

	engine, err := deploy.Connect(ctx)
	if err != nil {
		report(err)
		return 1
	}
	env, err := deploy.PolicyEnv(ctx, engine)
	if err != nil {
		_ = engine.Close()
		report(err)
		return 1
	}
	pol, err := policy.LoadFile(a.policyFile, env, 0)
	if err != nil {
		_ = engine.Close()
		report(err)
		return 1
	}
	db, err := state.Open(ctx, a.dataDir)
	if err != nil {
		_ = engine.Close()
		report(err)
		return 1
	}

	runCtx, cancel := context.WithCancel(ctx)
	a.proxy.OnError = report
	p := deploy.NewProxy(engine, db, a.proxy)
	x := deploy.New(engine, pol, db, p)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = p.Run(runCtx)
	}()
	go func() {
		defer wg.Done()
		x.Run(runCtx, report)
	}()

	opts := agent.ConnectOptions{AgentVersion: version, OnError: report, Executor: x}
	connectErr := agent.Connect(runCtx, a.dataDir, a.url, opts)
	cancel()
	wg.Wait()
	code := 0
	if connectErr != nil {
		report(connectErr)
		code = 1
	}
	if err := db.Close(); err != nil {
		report(err)
		code = 1
	}
	if err := engine.Close(); err != nil {
		report(err)
		code = 1
	}
	return code
}

func runAgentEnroll(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("agent enroll")
	dataDir := fs.String("data-dir", "/var/lib/moonglass/agent", "agent data directory")
	url := fs.String("url", "", "control-plane URL")
	serverFP := fs.String("server-fingerprint", "", "control-plane key fingerprint printed by moonglass server")
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 || *url == "" || *serverFP == "" {
		_, _ = io.WriteString(stderr, usage)
		return 2
	}
	fingerprint, err := agent.Enroll(context.Background(), *dataDir, *url, *serverFP, fs.Arg(0))
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
