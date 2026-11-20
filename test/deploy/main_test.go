package deploy_test

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// moonglassBin is the moonglass binary the suite drives as `moonglass proxy`,
// built once by TestMain.
var moonglassBin string

// serverBin is the static test image binary every release imports, built once
// by TestMain for the Engine's platform.
var serverBin string

// TestMain builds the two binaries the suite needs once, then runs the tests.
// Under -test.list it builds nothing, so make test-list and check-test-list stay
// fast.
func TestMain(m *testing.M) {
	flag.Parse()
	if list := flag.Lookup("test.list"); list != nil && list.Value.String() != "" {
		os.Exit(m.Run())
	}

	dir, err := os.MkdirTemp("", "mgdeploy")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create the suite binaries' temp dir: %v\n", err)
		os.Exit(1)
	}
	moonglassBin = filepath.Join(dir, "moonglass")
	serverBin = filepath.Join(dir, "server")
	if err := build(moonglassBin, "moonglass.dev/moonglass/cmd/moonglass", ""); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	if err := build(serverBin, "./testdata/server", "linux"); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// build builds target into out with CGO_ENABLED=0, for goos when it is set.
func build(out, target, goos string) error {
	cmd := exec.Command("go", "build", "-o", out, target)
	env := append(os.Environ(), "CGO_ENABLED=0")
	if goos != "" {
		env = append(env, "GOOS="+goos, "GOARCH="+runtime.GOARCH)
	}
	cmd.Env = env
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go build %s: %w\n%s", target, err, output)
	}
	return nil
}
