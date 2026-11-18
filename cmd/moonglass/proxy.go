package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"moonglass.dev/moonglass/internal/proxy"
)

// runProxy runs `moonglass proxy --data-dir <dir> [--admin-socket <path>]` until
// SIGINT or SIGTERM.
func runProxy(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("moonglass proxy", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dataDir := fs.String("data-dir", "", "directory for Caddy's certificate store, its autosave file and its inner admin socket")
	adminSocket := fs.String("admin-socket", proxy.DefaultAdminSocket, "Unix socket for the guarded admin API the agent uses")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dataDir == "" || *adminSocket == "" || fs.NArg() != 0 {
		_, _ = io.WriteString(stderr, usage)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := proxy.Run(ctx, *dataDir, *adminSocket); err != nil {
		_, _ = fmt.Fprintf(stderr, "moonglass: proxy: %v\n", err)
		return 1
	}
	return 0
}
