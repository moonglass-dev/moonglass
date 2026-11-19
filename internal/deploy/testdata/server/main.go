// Command server is the static test image the deploy tests import
// (docs/specs/deploy-suite.md). With no arguments it answers 200 on every path.
// Otherwise its argv selects what the container does, so a pre-deploy step can
// exit with a chosen code, print a marker, or sleep.
package main

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"
)

func main() {
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
	fmt.Fprintln(os.Stderr, "usage: app [exit <n> | print <marker> <n> | sleep <seconds>]")
	os.Exit(2)
}

func serve() {
	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	if err := http.ListenAndServe(":8080", nil); err != nil {
		panic(err)
	}
}
