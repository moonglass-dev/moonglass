package deploy_test

import (
	"archive/tar"
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"moonglass.dev/moonglass/internal/agent/state"
	"moonglass.dev/moonglass/internal/deploy"
	"moonglass.dev/moonglass/internal/policy"
	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

// projectLabel is the label the agent puts on everything it creates for one
// project (docs/specs/deploy-suite.md).
const projectLabel = "dev.moonglass.project"

// connectEngine connects to the Engine in DOCKER_HOST, or the default socket,
// through both the agent's own client and a raw Docker client. It skips when no
// Engine answers, and fails under CI, which must provide Docker.
func connectEngine(t *testing.T) (*deploy.Engine, *client.Client) {
	t.Helper()
	engine, err := deploy.Connect(context.Background())
	if err != nil {
		skipOrFail(t, fmt.Sprintf("no Docker Engine: %v", err))
	}
	t.Cleanup(func() { _ = engine.Close() })

	host := cmp.Or(os.Getenv("DOCKER_HOST"), "unix:///var/run/docker.sock")
	cli, err := client.New(client.WithHost(host))
	if err != nil {
		t.Fatalf("Docker client for %s: %v", host, err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	if _, err := cli.Ping(context.Background(), client.PingOptions{NegotiateAPIVersion: true}); err != nil {
		skipOrFail(t, fmt.Sprintf("no Docker Engine at %s: %v", host, err))
	}
	return engine, cli
}

// skipOrFail skips the test when no CI is running, and fails under CI.
func skipOrFail(t *testing.T, reason string) {
	t.Helper()
	if os.Getenv("CI") != "" {
		t.Fatalf("%s; CI must provide Docker", reason)
	}
	t.Skip(reason)
}

// newProject returns a project name unique to this run, "t" plus 12 lowercase
// hex characters, and registers the cleanup that removes everything the run
// made, pass or fail.
func newProject(t *testing.T, cli *client.Client) string {
	t.Helper()
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("rand.Read for a project name: %v", err)
	}
	project := "t" + hex.EncodeToString(raw)
	t.Cleanup(func() { cleanupProject(t, cli, project) })
	return project
}

// cleanupProject removes every container and network labelled with the project
// and every image under moonglass.local/<project>/, then reports anything the
// Engine still has. Listing first means an image retention already removed is
// simply not listed.
func cleanupProject(t *testing.T, cli *client.Client, project string) {
	t.Helper()
	ctx := context.Background()
	label := projectLabel + "=" + project
	reference := "moonglass.local/" + project + "/*"

	containers, err := cli.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: make(client.Filters).Add("label", label),
	})
	if err != nil {
		t.Errorf("cleanup: list the containers of project %s: %v", project, err)
	}
	for _, c := range containers.Items {
		if _, err := cli.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true}); err != nil {
			t.Errorf("cleanup: remove container %s of project %s: %v", c.ID, project, err)
		}
	}

	networks, err := cli.NetworkList(ctx, client.NetworkListOptions{
		Filters: make(client.Filters).Add("label", label),
	})
	if err != nil {
		t.Errorf("cleanup: list the networks of project %s: %v", project, err)
	}
	for _, n := range networks.Items {
		if _, err := cli.NetworkRemove(ctx, n.ID, client.NetworkRemoveOptions{}); err != nil {
			t.Errorf("cleanup: remove network %s of project %s: %v", n.Name, project, err)
		}
	}

	images, err := cli.ImageList(ctx, client.ImageListOptions{
		Filters: make(client.Filters).Add("reference", reference),
	})
	if err != nil {
		t.Errorf("cleanup: list the images of project %s: %v", project, err)
	}
	for _, img := range images.Items {
		if _, err := cli.ImageRemove(ctx, img.ID, client.ImageRemoveOptions{Force: true}); err != nil {
			t.Errorf("cleanup: remove image %s of project %s: %v", img.ID, project, err)
		}
	}

	if left, err := cli.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: make(client.Filters).Add("label", label),
	}); err != nil {
		t.Errorf("cleanup: list the containers of project %s again: %v", project, err)
	} else if len(left.Items) != 0 {
		t.Errorf("cleanup: %d container(s) of project %s remain, want none", len(left.Items), project)
	}
	if left, err := cli.NetworkList(ctx, client.NetworkListOptions{
		Filters: make(client.Filters).Add("label", label),
	}); err != nil {
		t.Errorf("cleanup: list the networks of project %s again: %v", project, err)
	} else if len(left.Items) != 0 {
		t.Errorf("cleanup: %d network(s) of project %s remain, want none", len(left.Items), project)
	}
	if left, err := cli.ImageList(ctx, client.ImageListOptions{
		Filters: make(client.Filters).Add("reference", reference),
	}); err != nil {
		t.Errorf("cleanup: list the images of project %s again: %v", project, err)
	} else if len(left.Items) != 0 {
		t.Errorf("cleanup: %d image(s) of project %s remain, want none", len(left.Items), project)
	}
}

// importRelease imports serverBin as moonglass.local/<project>/web:<release> with
// ENTRYPOINT ["/app", "<release>"], so the image answers with its release id. It
// returns the image reference.
func importRelease(t *testing.T, cli *client.Client, project, release string) string {
	t.Helper()
	data, err := os.ReadFile(serverBin)
	if err != nil {
		t.Fatalf("read the test server binary %s: %v", serverBin, err)
	}

	var rootfs bytes.Buffer
	tw := tar.NewWriter(&rootfs)
	header := &tar.Header{Name: "app", Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(header); err != nil {
		t.Fatalf("write the test image's tar header: %v", err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatalf("write the test image's tar body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close the test image's tar: %v", err)
	}

	entrypoint, err := json.Marshal([]string{"/app", release})
	if err != nil {
		t.Fatalf("encode ENTRYPOINT for %s: %v", release, err)
	}
	ref := "moonglass.local/" + project + "/web:" + release
	res, err := cli.ImageImport(context.Background(),
		client.ImageImportSource{Source: bytes.NewReader(rootfs.Bytes()), SourceName: "-"},
		ref,
		client.ImageImportOptions{Changes: []string{"ENTRYPOINT " + string(entrypoint)}},
	)
	if err != nil {
		t.Fatalf("ImageImport(%q) error = %v, want nil", ref, err)
	}
	if _, err := io.Copy(io.Discard, res); err != nil {
		_ = res.Close()
		t.Fatalf("ImageImport(%q) read: %v", ref, err)
	}
	if err := res.Close(); err != nil {
		t.Fatalf("ImageImport(%q) close: %v", ref, err)
	}
	return ref
}

// lockedBuffer is a goroutine-safe buffer for the proxy's output.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// adminClient returns a client that dials the proxy's admin Unix socket at sock.
func adminClient(sock string) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		},
	}
}

// startProxy starts `moonglass proxy` with its data dir and admin socket under
// one temp dir, waits until the admin socket answers GET /config/, and stops it
// with SIGTERM at cleanup.
func startProxy(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "mgd")
	if err != nil {
		t.Fatalf("create the proxy's temp dir: %v", err)
	}
	sock := filepath.Join(dir, "admin.sock")
	cmd := exec.Command(moonglassBin, "proxy", "--data-dir", dir, "--admin-socket", sock)
	var output lockedBuffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(dir)
		t.Fatalf("start %s proxy: %v", moonglassBin, err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(45 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Errorf("the proxy did not exit within 45 s of SIGTERM")
		}
		if t.Failed() {
			t.Logf("proxy output:\n%s", output.String())
		}
		_ = os.RemoveAll(dir)
	})

	admin := adminClient(sock)
	deadline := time.Now().Add(30 * time.Second)
	for {
		select {
		case <-done:
			t.Fatalf("the proxy exited before its admin socket answered: %v\n%s", waitErr, output.String())
		default:
		}
		res, err := admin.Get("http://localhost/config/")
		if err == nil {
			_ = res.Body.Close()
			return sock
		}
		if time.Now().After(deadline) {
			t.Fatalf("the proxy's admin socket %s did not answer within 30 s: %v\n%s", sock, err, output.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// reservePort reserves a free loopback port by listening on 127.0.0.1:0 and
// closing the listener, and returns "127.0.0.1:<port>".
func reservePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a loopback port: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("release the reserved port %s: %v", addr, err)
	}
	return addr
}

// localCA returns a pool holding only the proxy's local CA root, read over the
// admin socket.
func localCA(t *testing.T, sock string) *x509.CertPool {
	t.Helper()
	res, err := adminClient(sock).Get("http://localhost/pki/ca/local")
	if err != nil {
		t.Fatalf("fetch the proxy's local CA: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	var ca struct {
		RootCertificate string `json:"root_certificate"`
	}
	if err := json.NewDecoder(res.Body).Decode(&ca); err != nil {
		t.Fatalf("decode the proxy's local CA: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(ca.RootCertificate)) {
		t.Fatalf("the proxy's local CA root is not valid PEM: %q", ca.RootCertificate)
	}
	return pool
}

// errorLog collects the errors Executor.Run reports.
type errorLog struct {
	mu   sync.Mutex
	errs []error
}

func (l *errorLog) add(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errs = append(l.errs, err)
}

func (l *errorLog) snapshot() []error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]error(nil), l.errs...)
}

// suite is one test's own Engine, project, proxy process, port and executor.
type suite struct {
	t                                  *testing.T
	cli                                *client.Client
	project, app, domain, listen, sock string
	x                                  *deploy.Executor
}

// newSuite builds the run: the Engine, a unique project, the proxy process, a
// reserved port, a fresh agent database and the executor over them, scoped to its
// own project and running for the whole test.
func newSuite(t *testing.T) *suite {
	t.Helper()
	ctx := context.Background()
	engine, cli := connectEngine(t)
	project := newProject(t, cli)
	sock := startProxy(t)
	listen := reservePort(t)

	db, err := state.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("state.Open error = %v, want nil", err)
	}
	pol, err := policy.Parse(nil, policy.Env{})
	if err != nil {
		t.Fatalf("policy.Parse(empty) error = %v, want nil", err)
	}

	proxy := deploy.NewProxy(engine, db, deploy.ProxyConfig{Socket: sock, Issuer: "internal", Listen: listen})
	x := deploy.New(engine, pol, db, proxy)
	x.ScopeReconcile(projectLabel + "=" + project)

	runCtx, cancel := context.WithCancel(ctx)
	errs := &errorLog{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		x.Run(runCtx, errs.add)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(60 * time.Second):
			t.Errorf("Executor.Run did not return within 60 s of cancel")
		}
		_ = db.Close()
		for _, err := range errs.snapshot() {
			t.Errorf("Executor.Run reported: %v", err)
		}
	})

	return &suite{
		t: t, cli: cli, project: project,
		app: "app-web-" + project, domain: project + ".moonglass.test",
		listen: listen, sock: sock, x: x,
	}
}

// deploy imports the release's image and deploys it, requiring SUCCEEDED.
func (s *suite) deploy(release string) {
	s.t.Helper()
	image := importRelease(s.t, s.cli, s.project, release)
	res := s.x.Deploy(context.Background(), &agentv1.Deploy{
		AppId:       s.app,
		ReleaseId:   release,
		Image:       image,
		Project:     s.project,
		Environment: "production",
		Service:     "web",
		Port:        8080,
		HealthPath:  "/healthz",
		Domains:     []string{s.domain},
	})
	if got, want := res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
		s.t.Fatalf("Deploy(%s) outcome = %v with detail %q, want %v", release, got, res.GetDetail(), want)
	}
}

// switchTo switches the app to release and requires SUCCEEDED. It returns when
// the call started and when it returned.
func (s *suite) switchTo(release string) (called, returned time.Time) {
	s.t.Helper()
	called = time.Now()
	res := s.x.SwitchRelease(context.Background(), &agentv1.SwitchRelease{AppId: s.app, ReleaseId: release})
	returned = time.Now()
	if got, want := res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
		s.t.Fatalf("SwitchRelease(%s) outcome = %v with detail %q, want %v", release, got, res.GetDetail(), want)
	}
	return called, returned
}

// requireReachable proves the release container's own address answers from the
// host. When it does not, the test skips with that reason, and fails under CI.
func (s *suite) requireReachable(release string) {
	s.t.Helper()
	ctx := context.Background()
	name := "mg-" + s.app + "-" + release
	res, err := s.cli.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: make(client.Filters).Add("name", "^/"+regexp.QuoteMeta(name)+"$"),
	})
	if err != nil {
		s.t.Fatalf("ContainerList(name=%q) error = %v, want nil", name, err)
	}
	if len(res.Items) != 1 {
		s.t.Fatalf("containers named %q = %d, want exactly 1", name, len(res.Items))
	}
	insp, err := s.cli.ContainerInspect(ctx, res.Items[0].ID, client.ContainerInspectOptions{})
	if err != nil {
		s.t.Fatalf("ContainerInspect(%q) error = %v, want nil", name, err)
	}

	ip := ""
	if insp.Container.NetworkSettings != nil {
		if endpoint := insp.Container.NetworkSettings.Networks["mg-"+s.project]; endpoint != nil && endpoint.IPAddress.IsValid() {
			ip = endpoint.IPAddress.String()
		}
	}
	if ip == "" {
		s.t.Fatalf("container %s has no address on network mg-%s", name, s.project)
	}

	probe := &http.Client{Timeout: 2 * time.Second}
	url := "http://" + net.JoinHostPort(ip, "8080") + "/healthz"
	deadline := time.Now().Add(10 * time.Second)
	var last error
	for {
		res, err := probe.Get(url)
		if err == nil {
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return
			}
			last = fmt.Errorf("status %d", res.StatusCode)
		} else {
			last = err
		}
		if time.Now().After(deadline) {
			reason := fmt.Sprintf("container IP %s on mg-%s does not answer from the host (Docker Desktop?): %v", ip, s.project, last)
			if os.Getenv("CI") != "" {
				s.t.Fatalf("%s", reason)
			}
			s.t.Skip(reason)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// firstRelease deploys and switches r00 before load starts, then waits until a
// TLS answer through the proxy carries r00. It returns the load configuration.
func (s *suite) firstRelease() loadConfig {
	s.t.Helper()
	s.deploy("r00")
	s.requireReachable("r00")
	s.switchTo("r00")

	cfg := loadConfig{addr: s.listen, host: s.domain, roots: localCA(s.t, s.sock)}
	warm := newLoadClient(cfg)
	deadline := time.Now().Add(30 * time.Second)
	var last error
	for {
		res, err := warm.Get("https://" + cfg.host + "/")
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(res.Body, 256))
			_ = res.Body.Close()
			switch {
			case readErr != nil:
				last = readErr
			case res.StatusCode != http.StatusOK:
				last = fmt.Errorf("status %d", res.StatusCode)
			case string(body) != "r00":
				last = fmt.Errorf("body %q, want r00", body)
			default:
				return cfg
			}
		} else {
			last = err
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("no r00 answer through %s over TLS within 30 s: %v", s.listen, last)
		}
		time.Sleep(time.Second)
	}
}

// tenDeploys deploys and switches r01 to r10, each requiring SUCCEEDED, and
// returns one mark per switch.
func (s *suite) tenDeploys() []mark {
	s.t.Helper()
	marks := make([]mark, 0, 10)
	for i := 1; i <= 10; i++ {
		release := fmt.Sprintf("r%02d", i)
		s.deploy(release)
		called, returned := s.switchTo(release)

		banned := make([]string, 0, i)
		banned = append(banned, "r00")
		for j := 1; j < i; j++ {
			banned = append(banned, fmt.Sprintf("r%02d", j))
		}
		marks = append(marks, mark{
			release: release, banned: banned,
			called: called, returned: returned, within: 2 * time.Second,
		})
	}
	return marks
}
