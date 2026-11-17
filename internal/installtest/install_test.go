// Package installtest holds the acceptance tests for scripts/install and the
// systemd units under deploy/systemd. It has no runtime code.
//
// The tests drive the script with temporary directories, fixture artifacts and
// throwaway software ed25519 keys. They need sh and OpenSSH 8.2 or later
// (ssh-keygen -Y). They never need a FIDO2 token, Docker or a network.
//
// Interface the script must satisfy (docs/specs/install.md):
//
//	sh scripts/install --from DIR --allowed-signers FILE --prefix DIR --unit-dir DIR
//
// DIR holds the artifacts moonglass, SHA256SUMS, moonglass.sbom.json,
// moonglass-agent.service and moonglass-proxy.service, each with a
// <artifact>.sig. The binary is installed to <prefix>/bin/moonglass (0755),
// the units to <unit-dir>.
package installtest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const namespace = "moonglass-release"

var (
	binaryName = "moonglass"
	unitNames  = []string{"moonglass-agent.service", "moonglass-proxy.service"}
	artifacts  = append([]string{binaryName, "SHA256SUMS", "moonglass.sbom.json"}, unitNames...)
)

type installEnv struct {
	root    string // repository root
	src     string // directory the script fetches from
	signers string // allowed_signers file
	prefix  string // install prefix
	unitDir string // where units are installed
	tmp     string // TMPDIR for the script
	shimDir string // optional PATH shim directory
}

func (e installEnv) dest() string { return filepath.Join(e.prefix, "bin", binaryName) }

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod found above the test directory")
		}
		dir = parent
	}
}

func requireSSHKeygen(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Fatalf("ssh-keygen not found on PATH: install OpenSSH 8.2 or later (ssh-keygen -Y is required): %v", err)
	}
	out, err := exec.Command("ssh", "-V").CombinedOutput()
	if err != nil {
		t.Fatalf("ssh -V failed: %v: %s", err, out)
	}
	m := regexp.MustCompile(`OpenSSH_(\d+)\.(\d+)`).FindStringSubmatch(string(out))
	if m == nil {
		t.Fatalf("cannot parse OpenSSH version from %q", out)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	if major < 8 || (major == 8 && minor < 2) {
		t.Fatalf("OpenSSH version = %d.%d, want 8.2 or later", major, minor)
	}
	return path
}

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v: %s", name, strings.Join(args, " "), err, out)
	}
}

// newKey creates a throwaway software ed25519 key and returns the private and
// public key paths.
func newKey(t *testing.T, dir, name string) (priv, pub string) {
	t.Helper()
	priv = filepath.Join(dir, name)
	run(t, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", name, "-f", priv)
	return priv, priv + ".pub"
}

// signArtifact writes <artifact>.sig with the given key and namespace, hash sha512.
func signArtifact(t *testing.T, priv, artifact, ns string) {
	t.Helper()
	_ = os.Remove(artifact + ".sig") // ssh-keygen -Y sign will not overwrite an existing .sig
	run(t, "ssh-keygen", "-Y", "sign", "-q", "-f", priv, "-n", ns, "-O", "hashalg=sha512", artifact)
}

func writeSigners(t *testing.T, path string, pubs ...string) {
	t.Helper()
	var b strings.Builder
	for _, pub := range pubs {
		data, err := os.ReadFile(pub)
		if err != nil {
			t.Fatalf("read public key: %v", err)
		}
		fmt.Fprintf(&b, "%s namespaces=%q %s", namespace, namespace, data)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write allowed_signers: %v", err)
	}
}

// newEnv builds fixture artifacts (unsigned) and an allowed_signers file that
// lists the given public keys. It returns the environment and the binary bytes.
func newEnv(t *testing.T, pubs ...string) (installEnv, []byte) {
	t.Helper()
	requireSSHKeygen(t)
	base := t.TempDir()
	e := installEnv{
		root:    repoRoot(t),
		src:     filepath.Join(base, "src"),
		signers: filepath.Join(base, "allowed_signers"),
		prefix:  filepath.Join(base, "prefix"),
		unitDir: filepath.Join(base, "units"),
		tmp:     filepath.Join(base, "tmp"),
	}
	for _, d := range []string{e.src, e.prefix, e.unitDir, e.tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	writeSigners(t, e.signers, pubs...)

	binary := bytes.Repeat([]byte("\x7fELF moonglass fixture binary\n"), 64)
	files := map[string][]byte{
		binaryName:                binary,
		"moonglass.sbom.json":     []byte(`{"bomFormat":"CycloneDX"}` + "\n"),
		"moonglass-agent.service": []byte("[Unit]\nDescription=fixture agent\n"),
		"moonglass-proxy.service": []byte("[Unit]\nDescription=fixture proxy\n"),
		"SHA256SUMS":              []byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(binary), binaryName)),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(e.src, name), data, 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
	return e, binary
}

func signAll(t *testing.T, e installEnv, priv, ns string) {
	t.Helper()
	for _, name := range artifacts {
		signArtifact(t, priv, filepath.Join(e.src, name), ns)
	}
}

// runInstall runs scripts/install against e and returns stderr and the exit code.
func runInstall(t *testing.T, e installEnv, args ...string) (stderr string, exit int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	argv := append([]string{
		filepath.Join(e.root, "scripts", "install"),
		"--from", e.src,
		"--allowed-signers", e.signers,
		"--prefix", e.prefix,
		"--unit-dir", e.unitDir,
	}, args...)
	cmd := exec.CommandContext(ctx, "sh", argv...)
	path := os.Getenv("PATH")
	if e.shimDir != "" {
		path = e.shimDir + string(os.PathListSeparator) + path
	}
	cmd.Env = append(os.Environ(), "PATH="+path, "TMPDIR="+e.tmp)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return errBuf.String(), 0
	case errors.As(err, &ee):
		return errBuf.String(), ee.ExitCode()
	default:
		t.Fatalf("run install: %v", err)
		return "", -1
	}
}

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	var got []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != dir {
			rel, _ := filepath.Rel(dir, p)
			got = append(got, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return got
}

func assertNoStagingLeft(t *testing.T, e installEnv) {
	t.Helper()
	if got := dirEntries(t, e.tmp); len(got) != 0 {
		t.Errorf("TMPDIR leftovers = %v, want none (staging must be cleaned up)", got)
	}
}

func TestInstallVerifiesSignatureBeforeWriting(t *testing.T) {
	realKeygen := requireSSHKeygen(t)
	keys := t.TempDir()
	priv, pub := newKey(t, keys, "release1")

	t.Run("valid signature installs", func(t *testing.T) {
		e, binary := newEnv(t, pub)
		signAll(t, e, priv, namespace)

		// A shim records whether the destination existed each time the script
		// called ssh-keygen. Every verify must run before the first write.
		e.shimDir = t.TempDir()
		log := filepath.Join(e.shimDir, "calls.log")
		shim := fmt.Sprintf("#!/bin/sh\nif [ -e '%s' ]; then echo dest-existed >> '%s'; else echo dest-absent >> '%s'; fi\nexec '%s' \"$@\"\n",
			e.dest(), log, log, realKeygen)
		if err := os.WriteFile(filepath.Join(e.shimDir, "ssh-keygen"), []byte(shim), 0o755); err != nil {
			t.Fatalf("write shim: %v", err)
		}

		stderr, exit := runInstall(t, e)
		if exit != 0 {
			t.Fatalf("exit = %d, want 0\nstderr: %s", exit, stderr)
		}
		got, err := os.ReadFile(e.dest())
		if err != nil {
			t.Fatalf("read destination: %v", err)
		}
		if !bytes.Equal(got, binary) {
			t.Errorf("destination bytes differ from fixture: got %d bytes, want %d bytes", len(got), len(binary))
		}
		info, err := os.Stat(e.dest())
		if err != nil {
			t.Fatalf("stat destination: %v", err)
		}
		if info.Mode().Perm() != 0o755 {
			t.Errorf("destination mode = %o, want 755", info.Mode().Perm())
		}
		for _, name := range unitNames {
			want, _ := os.ReadFile(filepath.Join(e.src, name))
			got, err := os.ReadFile(filepath.Join(e.unitDir, name))
			if err != nil || !bytes.Equal(got, want) {
				t.Errorf("unit %s in %s = %q (err %v), want %q", name, e.unitDir, got, err, want)
			}
		}
		calls, err := os.ReadFile(log)
		if err != nil {
			t.Fatalf("ssh-keygen was never called: %v", err)
		}
		lines := strings.Fields(string(calls))
		if len(lines) < len(artifacts) {
			t.Errorf("ssh-keygen calls = %d, want at least %d (one verify per artifact)", len(lines), len(artifacts))
		}
		for i, l := range lines {
			if l != "dest-absent" {
				t.Errorf("ssh-keygen call %d saw %q, want dest-absent (verify must run before any write)", i, l)
			}
		}
		assertNoStagingLeft(t, e)
	})

	t.Run("failed verification never creates destination", func(t *testing.T) {
		e, _ := newEnv(t, pub)
		signAll(t, e, priv, namespace)
		e.shimDir = t.TempDir()
		shim := "#!/bin/sh\necho 'shim: forced verification failure' >&2\nexit 1\n"
		if err := os.WriteFile(filepath.Join(e.shimDir, "ssh-keygen"), []byte(shim), 0o755); err != nil {
			t.Fatalf("write shim: %v", err)
		}

		stderr, exit := runInstall(t, e)
		if exit == 0 {
			t.Errorf("exit = 0, want non-zero when verification fails\nstderr: %s", stderr)
		}
		if _, err := os.Stat(e.dest()); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("destination stat error = %v, want not-exist (nothing may be written)", err)
		}
		if got := dirEntries(t, e.unitDir); len(got) != 0 {
			t.Errorf("unit dir = %v, want empty", got)
		}
		assertNoStagingLeft(t, e)
	})
}

func TestInstallRejectsBadSignatureWritesNothing(t *testing.T) {
	requireSSHKeygen(t)
	keys := t.TempDir()
	priv, pub := newKey(t, keys, "release1")
	other, _ := newKey(t, keys, "unknown")

	tests := []struct {
		name       string
		breakIt    func(t *testing.T, e installEnv)
		wantReason string // lower-case substring expected after the artifact name
	}{
		{"missing sig", func(t *testing.T, e installEnv) {
			if err := os.Remove(filepath.Join(e.src, binaryName+".sig")); err != nil {
				t.Fatal(err)
			}
		}, "missing"},
		{"unknown key", func(t *testing.T, e installEnv) {
			signArtifact(t, other, filepath.Join(e.src, binaryName), namespace)
		}, "signature"},
		{"wrong namespace", func(t *testing.T, e installEnv) {
			signArtifact(t, priv, filepath.Join(e.src, binaryName), "other-namespace")
		}, "signature"},
		{"truncated artifact", func(t *testing.T, e installEnv) {
			p := filepath.Join(e.src, binaryName)
			data, _ := os.ReadFile(p)
			if err := os.WriteFile(p, data[:len(data)/2], 0o644); err != nil {
				t.Fatal(err)
			}
		}, "signature"},
		{"swapped signature", func(t *testing.T, e installEnv) {
			data, err := os.ReadFile(filepath.Join(e.src, "moonglass.sbom.json.sig"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(e.src, binaryName+".sig"), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}, "signature"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, _ := newEnv(t, pub)
			signAll(t, e, priv, namespace)

			old := []byte("previously installed moonglass\n")
			past := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
			if err := os.MkdirAll(filepath.Dir(e.dest()), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(e.dest(), old, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(e.dest(), past, past); err != nil {
				t.Fatal(err)
			}
			tt.breakIt(t, e)

			stderr, exit := runInstall(t, e)
			if exit == 0 {
				t.Errorf("exit = 0, want non-zero\nstderr: %s", stderr)
			}
			re := regexp.MustCompile(`(?m)^moonglass-install: ` + regexp.QuoteMeta(binaryName) + `: \S.*$`)
			line := re.FindString(stderr)
			if line == "" {
				t.Errorf("stderr = %q, want a line like %q naming the artifact and a reason", stderr, "moonglass-install: moonglass: <reason>")
			} else if !strings.Contains(strings.ToLower(line), tt.wantReason) {
				t.Errorf("stderr line = %q, want reason containing %q", line, tt.wantReason)
			}

			got, err := os.ReadFile(e.dest())
			if err != nil || !bytes.Equal(got, old) {
				t.Errorf("destination = %q (err %v), want unchanged %q", got, err, old)
			}
			info, err := os.Stat(e.dest())
			if err == nil && !info.ModTime().Equal(past) {
				t.Errorf("destination mtime = %v, want unchanged %v", info.ModTime(), past)
			}
			wantTree := []string{"bin", filepath.Join("bin", binaryName)}
			if tree := dirEntries(t, e.prefix); strings.Join(tree, "\n") != strings.Join(wantTree, "\n") {
				t.Errorf("prefix contents = %v, want only %v", tree, wantTree)
			}
			if units := dirEntries(t, e.unitDir); len(units) != 0 {
				t.Errorf("unit dir contents = %v, want empty", units)
			}
			assertNoStagingLeft(t, e)
		})
	}
}

func TestInstallAcceptsEitherPinnedKey(t *testing.T) {
	requireSSHKeygen(t)
	keys := t.TempDir()
	priv1, pub1 := newKey(t, keys, "release1")
	priv2, pub2 := newKey(t, keys, "release2")
	priv3, _ := newKey(t, keys, "unlisted")

	tests := []struct {
		name     string
		priv     string
		wantExit int
	}{
		{"first key", priv1, 0},
		{"second key", priv2, 0},
		{"unlisted key", priv3, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, binary := newEnv(t, pub1, pub2)
			signAll(t, e, tt.priv, namespace)

			stderr, exit := runInstall(t, e)
			if (exit == 0) != (tt.wantExit == 0) {
				t.Fatalf("exit = %d, want zero=%v\nstderr: %s", exit, tt.wantExit == 0, stderr)
			}
			got, err := os.ReadFile(e.dest())
			if tt.wantExit == 0 {
				if err != nil || !bytes.Equal(got, binary) {
					t.Errorf("destination = %d bytes (err %v), want the %d-byte fixture", len(got), err, len(binary))
				}
			} else if !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("destination read error = %v, want not-exist", err)
			}
		})
	}
}

// parseUnit is a minimal INI reader. Keys are "Section.Key"; each assignment
// appends a value, so repeated keys keep all values. An empty assignment resets
// the key, as systemd does.
func parseUnit(t *testing.T, path string) map[string][]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read unit: %v", err)
	}
	out := map[string][]string{}
	section := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = line[1 : len(line)-1]
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key := section + "." + strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if v == "" {
			delete(out, key)
			continue
		}
		out[key] = append(out[key], v)
	}
	return out
}

func unitPaths(t *testing.T) (agent, proxy string) {
	t.Helper()
	root := repoRoot(t)
	return filepath.Join(root, "deploy", "systemd", "moonglass-agent.service"),
		filepath.Join(root, "deploy", "systemd", "moonglass-proxy.service")
}

func fields(values []string) []string { return strings.Fields(strings.Join(values, " ")) }

func TestUnitsAreHardened(t *testing.T) {
	agentPath, proxyPath := unitPaths(t)
	tests := []struct {
		name        string
		path        string
		wantUser    string
		wantAmbient []string
	}{
		{"agent", agentPath, "moonglass", nil},
		{"proxy", proxyPath, "moonglass-proxy", []string{"CAP_NET_BIND_SERVICE"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := parseUnit(t, tt.path)
			if got := u["Service.User"]; len(got) != 1 || got[0] != tt.wantUser {
				t.Errorf("User = %v, want [%s]", got, tt.wantUser)
			}
			if got := u["Service.NoNewPrivileges"]; len(got) != 1 || got[0] != "true" {
				t.Errorf("NoNewPrivileges = %v, want [true]", got)
			}
			if got := u["Service.ProtectSystem"]; len(got) != 1 || (got[0] != "strict" && got[0] != "full") {
				t.Errorf("ProtectSystem = %v, want [strict] or [full]", got)
			}
			if got := u["Service.MemoryMax"]; len(got) != 1 {
				t.Errorf("MemoryMax = %v, want one value", got)
			}
			if got := fields(u["Service.AmbientCapabilities"]); strings.Join(got, " ") != strings.Join(tt.wantAmbient, " ") {
				t.Errorf("AmbientCapabilities = %v, want %v", got, tt.wantAmbient)
			}
		})
	}

	t.Run("proxy runs as a different user than the agent", func(t *testing.T) {
		agentUser := parseUnit(t, agentPath)["Service.User"]
		proxyUser := parseUnit(t, proxyPath)["Service.User"]
		if len(agentUser) != 1 || len(proxyUser) != 1 {
			t.Fatalf("User: agent = %v, proxy = %v, want one value each", agentUser, proxyUser)
		}
		if agentUser[0] == proxyUser[0] {
			t.Errorf("proxy User = %q, agent User = %q, want different users so the proxy cannot read the agent's keys", proxyUser[0], agentUser[0])
		}
	})

	t.Run("proxy does not need docker", func(t *testing.T) {
		u := parseUnit(t, proxyPath)
		for key, values := range u {
			for _, v := range values {
				if strings.Contains(v, "docker.sock") {
					t.Errorf("%s = %q, want no reference to docker.sock", key, v)
				}
			}
		}
		for _, key := range []string{"Unit.Requires", "Unit.Wants", "Unit.BindsTo", "Unit.After", "Unit.Requisite"} {
			for _, v := range fields(u[key]) {
				if strings.Contains(v, "docker") {
					t.Errorf("%s contains %q, want no docker dependency", key, v)
				}
			}
		}
	})

	t.Run("port sysctl is not lowered", func(t *testing.T) {
		root := repoRoot(t)
		for _, dir := range []string{filepath.Join(root, "scripts", "install"), filepath.Join(root, "deploy")} {
			err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				data, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				if bytes.Contains(data, []byte("ip_unprivileged_port_start")) {
					t.Errorf("%s mentions ip_unprivileged_port_start, want the sysctl left alone", p)
				}
				return nil
			})
			if err != nil {
				t.Errorf("walk %s: %v (the install script and deploy/systemd must exist)", dir, err)
			}
		}
	})
}

func TestOnlyAgentHasDockerGroup(t *testing.T) {
	agentPath, proxyPath := unitPaths(t)

	agent := parseUnit(t, agentPath)
	if got := fields(agent["Service.SupplementaryGroups"]); !containsString(got, "docker") {
		t.Errorf("agent SupplementaryGroups = %v, want it to contain docker", got)
	}

	proxy := parseUnit(t, proxyPath)
	if len(proxy) == 0 {
		t.Fatalf("proxy unit %s is missing or empty", proxyPath)
	}
	for _, key := range []string{"Service.SupplementaryGroups", "Service.Group", "Service.User"} {
		for _, v := range fields(proxy[key]) {
			if strings.Contains(v, "docker") {
				t.Errorf("proxy %s contains %q, want no docker group", key, v)
			}
		}
	}

	script, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "install"))
	if err != nil {
		t.Fatalf("read install script: %v", err)
	}
	re := regexp.MustCompile(`(?m)^[^#\n]*\b(usermod|adduser|useradd|gpasswd)\b[^\n]*\bdocker\b`)
	if m := re.FindString(string(script)); m != "" {
		t.Errorf("install script line %q adds a user to the docker group, want none (only the agent unit gets it)", m)
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
