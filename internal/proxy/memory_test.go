package proxy_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/agent"
	"moonglass.dev/moonglass/internal/server"
	"moonglass.dev/moonglass/internal/store/sqlite"
)

// memoryCeilingKiB is the G1 hard ceiling: agent and proxy together at most
// 100 MiB resident (docs/specs/memory.md).
const memoryCeilingKiB = 102400

// idleSettle is how long both processes sit idle before they are sampled.
const idleSettle = 3 * time.Second

var rssLine = regexp.MustCompile(`^rss_kib (\d+) pid \d+\n$`)

// measureRSS runs scripts/measure-memory --pid and returns the RSS in KiB.
func measureRSS(t *testing.T, name string, pid int) int {
	t.Helper()
	script, err := filepath.Abs("../../scripts/measure-memory")
	if err != nil {
		t.Fatalf("locate scripts/measure-memory: %v", err)
	}
	out, err := exec.Command(script, "--pid", strconv.Itoa(pid)).Output()
	if err != nil {
		t.Fatalf("measure %s (pid %d) with scripts/measure-memory: %v", name, pid, err)
	}
	m := rssLine.FindSubmatch(out)
	if m == nil {
		t.Fatalf("measure %s: expected %q, got %q", name, "rss_kib <KiB> pid <pid>", out)
	}
	kib, err := strconv.Atoi(string(m[1]))
	if err != nil {
		t.Fatalf("measure %s: parse %q: %v", name, m[1], err)
	}
	return kib
}

// startProcess runs the binary and stops it (SIGTERM, then kill) at cleanup.
func startProcess(t *testing.T, name, bin string, args ...string) *exec.Cmd {
	t.Helper()
	var stderr bytes.Buffer
	cmd := exec.Command(bin, args...)
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start moonglass %s: %v", name, err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	t.Cleanup(func() {
		if t.Failed() && stderr.Len() > 0 {
			t.Logf("moonglass %s stderr:\n%s", name, stderr.String())
		}
	})
	return cmd
}

func TestProxyAndAgentIdleUnderBudget(t *testing.T) {
	testEngine(t)
	ctx := context.Background()
	tmp, err := os.MkdirTemp("/tmp", "mgmem")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })

	bin := filepath.Join(tmp, "moonglass")
	build := exec.Command("go", "build", "-trimpath", "-o", bin, "moonglass.dev/moonglass/cmd/moonglass")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the moonglass binary with CGO_ENABLED=0: %v\n%s", err, out)
	}

	// A real control plane on loopback with random ports; the agent enrols
	// with the real binary-independent client, then connects as a process.
	cpDir := filepath.Join(tmp, "cp")
	if err := os.Mkdir(cpDir, 0o700); err != nil {
		t.Fatalf("create control-plane dir: %v", err)
	}
	st, err := sqlite.Open(ctx, filepath.Join(cpDir, "server.db"))
	if err != nil {
		t.Fatalf("open control-plane store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	org, err := st.CreateOrg(ctx)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	s, err := server.New(st, cpDir, time.Now)
	if err != nil {
		t.Fatalf("create control plane: %v", err)
	}
	enrolSrv := httptest.NewServer(s.Handler())
	t.Cleanup(enrolSrv.Close)
	token, err := s.IssueEnrolToken(ctx, org)
	if err != nil {
		t.Fatalf("issue enrolment token: %v", err)
	}
	agentDir := filepath.Join(tmp, "agent")
	if _, err := agent.Enroll(ctx, http.DefaultClient, agentDir, enrolSrv.URL, token); err != nil {
		t.Fatalf("enrol the agent against the local control plane: %v", err)
	}

	cfg, err := s.AgentTLSConfig()
	if err != nil {
		t.Fatalf("agent tls config: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on a loopback port: %v", err)
	}
	var upgrades atomic.Int64
	agentHandler := s.AgentHandler()
	srv := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			upgrades.Add(1)
			agentHandler.ServeHTTP(w, r)
		}),
	}
	go func() { _ = srv.Serve(tls.NewListener(ln, cfg)) }()
	t.Cleanup(func() { _ = srv.Close() })

	adminSock := filepath.Join(tmp, "admin.sock")
	proxyCmd := startProcess(t, "proxy", bin, "proxy", "--data-dir", filepath.Join(tmp, "proxy"), "--admin-socket", adminSock)
	agentCmd := startProcess(t, "agent", bin, "agent", "connect", "--data-dir", agentDir, "--proxy-socket", adminSock, "--tls-issuer", "internal", "--url", "https://"+ln.Addr().String())

	deadline := time.Now().Add(startTimeout)
	for upgrades.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("agent did not connect to the local control plane within %s", startTimeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
	for {
		if _, err := os.Stat(adminSock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("proxy admin socket %s did not appear within %s", adminSock, startTimeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
	admin := adminClient(adminSock)
	var grace any
	var graceErr error
	for {
		resp, err := admin.Get("http://localhost/config/apps/http/grace_period")
		if err != nil {
			graceErr = err
		} else {
			grace = nil
			graceErr = json.NewDecoder(resp.Body).Decode(&grace)
			_ = resp.Body.Close()
		}
		if graceErr == nil && grace == "30s" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET /config/apps/http/grace_period on %s: expected %q (the agent's first push) within %s, got %#v (error %v)", adminSock, "30s", startTimeout, grace, graceErr)
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(idleSettle)

	proxyKiB := measureRSS(t, "proxy", proxyCmd.Process.Pid)
	agentKiB := measureRSS(t, "agent", agentCmd.Process.Pid)
	total := proxyKiB + agentKiB
	t.Logf("idle RSS: proxy %d KiB, agent %d KiB, total %d KiB (ceiling %d KiB)", proxyKiB, agentKiB, total, memoryCeilingKiB)
	if total > memoryCeilingKiB {
		t.Errorf("proxy + agent idle RSS: expected at most %d KiB, got %d KiB (proxy %d, agent %d); find what grew, do not raise the ceiling", memoryCeilingKiB, total, proxyKiB, agentKiB)
	}
}
