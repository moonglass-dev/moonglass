// Command pipeline holds every deterministic step of the ticket pipeline
// described in docs/pipeline/README.md: ticket state, step selection, test
// locking, the verify gates, opening pull requests and merging.
//
// It is run through the wrappers scripts/ticket-status, scripts/verify and
// scripts/ship. Every command prints one JSON document on stdout. Exit codes:
// 0 success, 1 a gate or result failed, 2 usage or environment error, 3 the
// stop file .pipeline/STOP exists.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

const usage = `usage:
  scripts/ticket-status [--json] [--no-fetch]   ticket table, or the full state as JSON
  scripts/ticket-status start ID                worker: check out p/ID and count a worker run
  scripts/ticket-status next ID [--peek]        worker: claim the next step
  scripts/ticket-status set ID STATUS [--reason TEXT]
  scripts/verify record ID                      worker: check, commit and push a child's result
  scripts/verify lock ID                        hash the approved tests
  scripts/verify check ID                       run every gate, write check.json
  scripts/verify skip-review ID                 record a review skip when check.json allows it
  scripts/ship pr ID                            push and open or reuse the pull request
  scripts/ship merge ID [--wait]                advance the merge; --wait repeats while CI runs`

// errStop means .pipeline/STOP exists. Commands that change state refuse to run.
var errStop = errors.New("stop file .pipeline/STOP exists: remove it to resume")

// gateError is a failed gate or result: exit 1, not a usage error.
type gateError struct{ msg string }

func (e *gateError) Error() string { return e.msg }

func failf(format string, args ...any) error {
	return &gateError{msg: fmt.Sprintf(format, args...)}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		say(stderr, "%s\n", usage)
		return 2
	}
	root, err := repoRoot(".")
	if err != nil {
		say(stderr, "pipeline: %v\n", err)
		return 2
	}
	env := &env{repo: root, out: stdout}
	err = dispatch(env, args)
	var gate *gateError
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errStop):
		writeJSON(stdout, map[string]any{"stop": true, "error": err.Error()})
		return 3
	case errors.As(err, &gate):
		say(stderr, "pipeline: %v\n", err)
		return 1
	default:
		say(stderr, "pipeline: %v\n", err)
		return 2
	}
}

func dispatch(e *env, args []string) error {
	group, rest := args[0], args[1:]
	switch group {
	case "ticket-status":
		return ticketStatus(e, rest)
	case "verify":
		return verify(e, rest)
	case "ship":
		return ship(e, rest)
	default:
		return fmt.Errorf("unknown command group %q\n%s", group, usage)
	}
}

func ticketStatus(e *env, args []string) error {
	if len(args) == 0 || args[0] == "--json" || args[0] == "--no-fetch" {
		return cmdStatus(e, args)
	}
	switch args[0] {
	case "start":
		id, err := oneID(args[1:])
		if err != nil {
			return err
		}
		return cmdStart(e, id)
	case "next":
		if len(args) < 2 {
			return fmt.Errorf("next needs a ticket id\n%s", usage)
		}
		peek := len(args) == 3 && args[2] == "--peek"
		return cmdNext(e, args[1], peek)
	case "set":
		if len(args) != 3 && (len(args) != 5 || args[3] != "--reason") {
			return fmt.Errorf("set needs ID STATUS [--reason TEXT]\n%s", usage)
		}
		reason := ""
		if len(args) == 5 {
			reason = args[4]
		}
		return cmdSet(e, args[1], args[2], reason)
	default:
		return fmt.Errorf("unknown ticket-status command %q\n%s", args[0], usage)
	}
}

func verify(e *env, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("verify needs a command and a ticket id\n%s", usage)
	}
	id := args[1]
	switch args[0] {
	case "record":
		return cmdRecord(e, id)
	case "lock":
		return cmdLock(e, id)
	case "check":
		return cmdCheck(e, id)
	case "skip-review":
		return cmdSkipReview(e, id)
	default:
		return fmt.Errorf("unknown verify command %q\n%s", args[0], usage)
	}
}

func ship(e *env, args []string) error {
	wait := len(args) == 3 && args[0] == "merge" && args[2] == "--wait"
	if len(args) != 2 && !wait {
		return fmt.Errorf("ship needs a command and a ticket id\n%s", usage)
	}
	switch args[0] {
	case "pr":
		return cmdPR(e, args[1])
	case "merge":
		return cmdMerge(e, args[1], wait)
	default:
		return fmt.Errorf("unknown ship command %q\n%s", args[0], usage)
	}
}

func oneID(args []string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("expected exactly one ticket id\n%s", usage)
	}
	return args[0], nil
}

func writeJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		say(os.Stderr, "pipeline: write output: %v\n", err)
	}
}

// say writes to a terminal stream. A failed write to stdout or stderr has
// nowhere to be reported, so it is dropped on purpose.
func say(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}
