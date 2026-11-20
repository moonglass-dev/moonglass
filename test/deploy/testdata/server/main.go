// Command server is the static test image the deploy-correctness suite imports
// (docs/specs/deploy-suite.md). It takes its release id as its one argument,
// answers GET /healthz with 200 and every other path with 200 and a body of
// exactly that id, and exits on SIGTERM after finishing the requests in flight.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// shutdownGrace is how long a SIGTERM waits for the requests in flight.
const shutdownGrace = 25 * time.Second

func main() {
	// As PID 1 the process would otherwise ignore SIGTERM, so the handler is
	// installed before the server starts.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: server <release id>")
		os.Exit(2)
	}
	release := os.Args[1]

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, release)
	})

	srv := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	served := make(chan error, 1)
	go func() { served <- srv.ListenAndServe() }()

	select {
	case err := <-served:
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		os.Exit(1)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			fmt.Fprintf(os.Stderr, "shut down: %v\n", err)
			os.Exit(1)
		}
	}
}
