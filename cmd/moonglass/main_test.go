package main

import (
	"bytes"
	"errors"
	"testing"
)

func TestVersionPrintsVersionCommitAndDate(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"version"}, &stdout, &stderr)

	const want = "moonglass dev\ncommit unknown\nbuilt unknown\n"
	if code != 0 {
		t.Errorf("exit status: expected 0, got %d", code)
	}
	if got := stdout.String(); got != want {
		t.Errorf("stdout: expected %q, got %q", want, got)
	}
	if got := stderr.String(); got != "" {
		t.Errorf("stderr: expected it empty, got %q", got)
	}
}

func TestUsageOnMissingOrUnknownCommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"no arguments", nil},
		{"unknown subcommand", []string{"deploy"}},
		{"version with an extra argument", []string{"version", "extra"}},
		{"proxy without --data-dir", []string{"proxy"}},
		{"proxy with an empty --data-dir", []string{"proxy", "--data-dir", ""}},
		{"proxy with an extra argument", []string{"proxy", "--data-dir", "/tmp/x", "extra"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := run(tt.args, &stdout, &stderr)

			if code != 2 {
				t.Errorf("exit status: expected 2, got %d", code)
			}
			if got := stderr.String(); got != usage {
				t.Errorf("stderr: expected %q, got %q", usage, got)
			}
			if got := stdout.String(); got != "" {
				t.Errorf("stdout: expected it empty, got %q", got)
			}
		})
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("pipe closed")
}

func TestVersionReportsFailedWrite(t *testing.T) {
	var stderr bytes.Buffer

	code := run([]string{"version"}, failingWriter{}, &stderr)

	const want = "moonglass: print version: pipe closed\n"
	if code != 1 {
		t.Errorf("exit status: expected 1, got %d", code)
	}
	if got := stderr.String(); got != want {
		t.Errorf("stderr: expected %q, got %q", want, got)
	}
}
