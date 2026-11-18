package main

import (
	"fmt"
	"io"

	"moonglass.dev/moonglass/internal/keys"
)

// recoveryLossWarning is the ADR-0003 consequence keys init prints word for word.
const recoveryLossWarning = "If every server in an environment is gone and no recovery key exists, nobody can decrypt its secrets or backups, including us."

// runKeysInit writes a fresh recovery identity to the file named by --out and
// prints its recipient plus the loss warning. The identity stays in the file.
func runKeysInit(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("keys init")
	out := fs.String("out", "", "new recovery key file to create")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *out == "" {
		_, _ = io.WriteString(stderr, usage)
		return 2
	}
	recipient, err := keys.CreateRecoveryKeyFile(*out)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "moonglass: %v\n", err)
		return 1
	}
	if _, err := fmt.Fprintf(stdout, "%s\nRecovery key written to %s (mode 0600). Keep it offline: anyone who can read it can decrypt what is wrapped to it.\n%s\n",
		recipient.String(), *out, recoveryLossWarning); err != nil {
		_, _ = fmt.Fprintf(stderr, "moonglass: keys init: print recipient: %v\n", err)
		return 1
	}
	return 0
}
