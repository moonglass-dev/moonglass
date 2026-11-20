// Command server is the static test image the deploy tests import
// (docs/specs/deploy-suite.md). With no arguments it answers 200 on every path.
// Otherwise its argv selects what the container does, so a pre-deploy step can
// exit with a chosen code, print a marker or sleep, and a health-gate candidate
// can answer a chosen status or exit after a delay.
package main

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

// healthBody is the body every health answer carries. It must never reach a
// result, a stored error or a log line (docs/threat-model.md §9.3).
const healthBody = "health-body-must-not-leak"

func main() {
	// As PID 1 the server would otherwise ignore SIGTERM, so a stop of it would
	// wait the Engine's full timeout instead of ending at once.
	go exitOnTerm()
	if len(os.Args) == 1 {
		serve()
		return
	}
	switch os.Args[1] {
	case "exit":
		os.Exit(number(os.Args[2:]))
	case "print":
		if len(os.Args) < 4 {
			usage()
		}
		marker := os.Args[2]
		code := number(os.Args[3:])
		fmt.Fprintln(os.Stdout, marker)
		fmt.Fprintln(os.Stderr, marker)
		os.Exit(code)
	case "sleep":
		time.Sleep(time.Duration(number(os.Args[2:])) * time.Second)
	case "health":
		if len(os.Args) < 4 {
			usage()
		}
		health(os.Args[2], number(os.Args[3:]))
	case "die":
		if len(os.Args) < 4 {
			usage()
		}
		die(time.Duration(number(os.Args[2:]))*time.Second, number(os.Args[3:]))
	default:
		usage()
	}
}

func number(args []string) int {
	if len(args) < 1 {
		usage()
	}
	n, err := strconv.Atoi(args[0])
	if err != nil {
		usage()
	}
	return n
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: app [exit <n> | print <marker> <n> | sleep <seconds> | health <path> <code> | die <seconds> <code>]")
	os.Exit(2)
}

// exitOnTerm ends the process on SIGTERM, as a stop sends it before SIGKILL.
func exitOnTerm() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	<-signals
	os.Exit(0)
}

func serve() {
	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	if err := http.ListenAndServe(":8080", nil); err != nil {
		panic(err)
	}
}

// health answers path with code on :8080, so a health-gate test can choose
// whether the candidate passes. A 3xx points at /followed, which answers 500, so
// a gate that followed the redirect would fail. Every answer carries the marker
// body, so a test can show no gate error ever repeats it.
func health(path string, code int) {
	mux := http.NewServeMux()
	answer := func(w http.ResponseWriter, status int) {
		if status >= 300 && status < 400 {
			w.Header().Set("Location", "/followed")
		}
		w.WriteHeader(status)
		fmt.Fprint(w, healthBody)
	}
	mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) { answer(w, code) })
	mux.HandleFunc("/followed", func(w http.ResponseWriter, _ *http.Request) { answer(w, http.StatusInternalServerError) })
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { answer(w, http.StatusNotFound) })
	if err := http.ListenAndServe(":8080", mux); err != nil {
		panic(err)
	}
}

// die answers 200 on every path, then exits with code after the given delay, so
// a health-gate test can show the gate fails when the candidate exits or, under
// a restart policy, restarts.
func die(after time.Duration, code int) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	go func() {
		if err := http.ListenAndServe(":8080", mux); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}()
	time.Sleep(after)
	os.Exit(code)
}
