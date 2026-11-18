package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"
)

// Match .github/workflows/ci.yml: postgres:18 as a service and this URL.
const (
	ciPostgresImage = "postgres:18"
	ciPostgresURL   = "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable" //nolint:gosec // G101: CI service DSN, same as .github/workflows/ci.yml
	ciPostgresName  = "moonglass-ci-postgres"
)

// ensureTestPostgres makes named-tests (and the rest of this process) use
// Postgres the same way CI does: postgres:18 on 127.0.0.1:5432 and
// MOONGLASS_TEST_POSTGRES set to ciPostgresURL. A URL already in the
// environment is left alone.
func ensureTestPostgres() error {
	if os.Getenv("MOONGLASS_TEST_POSTGRES") != "" {
		return nil
	}
	if err := startCIPostgres(); err != nil {
		return err
	}
	if err := os.Setenv("MOONGLASS_TEST_POSTGRES", ciPostgresURL); err != nil {
		return fmt.Errorf("set MOONGLASS_TEST_POSTGRES: %w", err)
	}
	return nil
}

func startCIPostgres() error {
	if postgresListening() {
		return nil
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("named-tests needs %s as CI does: install Docker, or set MOONGLASS_TEST_POSTGRES to a %s URL: %w", ciPostgresImage, ciPostgresImage, err)
	}
	if err := ensureCIPostgresContainer(); err != nil {
		return err
	}
	if err := waitPostgresListening(60 * time.Second); err != nil {
		return fmt.Errorf("named-tests needs %s as CI does (MOONGLASS_TEST_POSTGRES=%s): %w", ciPostgresImage, ciPostgresURL, err)
	}
	return nil
}

func ensureCIPostgresContainer() error {
	inspect, err := dockerOutput("inspect", "-f", "{{.State.Running}}", ciPostgresName)
	if err == nil {
		if inspect == "true" {
			return nil
		}
		if _, startErr := dockerOutput("start", ciPostgresName); startErr != nil {
			return fmt.Errorf("docker start %s: %w", ciPostgresName, startErr)
		}
		return nil
	}
	args := []string{
		"run", "-d",
		"--name", ciPostgresName,
		"--label", "moonglass.role=ci-postgres",
		"-e", "POSTGRES_PASSWORD=postgres",
		"-p", "127.0.0.1:5432:5432",
		ciPostgresImage,
	}
	if _, runErr := dockerOutput(args...); runErr != nil {
		return fmt.Errorf("docker run %s: %w", ciPostgresImage, runErr)
	}
	return nil
}

func waitPostgresListening(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if postgresListening() {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("127.0.0.1:5432 did not accept connections within %s", timeout)
}

func postgresListening() bool {
	c, err := net.DialTimeout("tcp", "127.0.0.1:5432", time.Second)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func dockerOutput(args ...string) (string, error) {
	cmd := exec.Command("docker", args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := bytes.TrimSpace(buf.Bytes())
	if err != nil {
		return string(out), fmt.Errorf("%w\n%s", err, tail(string(out), 20))
	}
	return string(out), nil
}
