package agent

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"moonglass.dev/moonglass/internal/agent/state"
	"moonglass.dev/moonglass/internal/deploy"
	"moonglass.dev/moonglass/internal/policy"
	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

// engineSocket is the Engine socket the test reaches directly: the DOCKER_HOST
// path, or the default socket (ADR-0014 decision 4).
func engineSocket(t *testing.T) string {
	t.Helper()
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	path, ok := strings.CutPrefix(host, "unix://")
	if !ok {
		t.Fatalf("DOCKER_HOST = %q, want a unix:// socket (ADR-0014)", host)
	}
	return path
}

// engineCall makes one Engine API call over a Unix socket with the standard
// library only (ADR-0014 decision 2).
func engineCall(c *http.Client, method, path string, body []byte, contentType string) (int, []byte, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, "http://docker"+path, r)
	if err != nil {
		return 0, nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, data, nil
}

func engineRequest(t *testing.T, c *http.Client, method, path string) (int, []byte) {
	t.Helper()
	code, body, err := engineCall(c, method, path, nil, "")
	if err != nil {
		t.Fatalf("%s %s: unexpected error %v", method, path, err)
	}
	return code, body
}

func engineDelete(t *testing.T, c *http.Client, path string) {
	t.Helper()
	code, body := engineRequest(t, c, http.MethodDelete, path)
	if code != http.StatusOK && code != http.StatusNoContent && code != http.StatusNotFound {
		t.Errorf("DELETE %s: status %d, want 200, 204 or 404 (body %s)", path, code, body)
	}
}

// engineClient talks to the Engine over its Unix socket.
func engineClient(sock string) *http.Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}
	return &http.Client{Transport: tr, Timeout: 2 * time.Minute}
}

// engineProxy is a Unix-socket HTTP proxy in front of the Engine that counts
// container creates, so the deploy path is observed without importing the
// Docker client.
type engineProxy struct {
	srv     *http.Server
	sock    string
	creates atomic.Int64
}

func newEngineProxy(t *testing.T, realSock string) *engineProxy {
	t.Helper()
	dir, err := os.MkdirTemp("", "mg")
	if err != nil {
		t.Fatalf("MkdirTemp: unexpected error %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "engine.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen on %q: unexpected error %v", sock, err)
	}

	p := &engineProxy{sock: sock}
	reverse := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: "docker"})
	reverse.Transport = engineClient(realSock).Transport
	p.srv = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v") && strings.HasSuffix(r.URL.Path, "/containers/create") {
			p.creates.Add(1)
		}
		reverse.ServeHTTP(w, r)
	})}
	go func() { _ = p.srv.Serve(ln) }()
	t.Cleanup(func() { _ = p.srv.Close() })
	return p
}

// buildServerTar builds the deploy suite's small static server for the Engine's
// platform and returns it as a one-file root filesystem tar
// (docs/specs/deploy-suite.md, "Test images are imported, not pulled").
func buildServerTar(t *testing.T) []byte {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "server")
	build := exec.Command("go", "build", "-o", binary, "../deploy/testdata/server")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ../deploy/testdata/server: unexpected error %v\n%s", err, out)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatalf("read %s: unexpected error %v", binary, err)
	}

	var rootfs bytes.Buffer
	tw := tar.NewWriter(&rootfs)
	header := &tar.Header{Name: "app", Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(header); err != nil {
		t.Fatalf("write tar header: unexpected error %v", err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatalf("write tar body: unexpected error %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: unexpected error %v", err)
	}
	return rootfs.Bytes()
}

// importEngineImage imports a root filesystem tar as repo:tag with the server
// as its entrypoint, and fails on an error reported in the stream.
func importEngineImage(t *testing.T, c *http.Client, tarBytes []byte, repo, tag string) {
	t.Helper()
	q := url.Values{}
	q.Set("fromSrc", "-")
	q.Set("repo", repo)
	q.Set("tag", tag)
	q.Add("changes", `ENTRYPOINT ["/app"]`)
	code, body, err := engineCall(c, http.MethodPost, "/v1.44/images/create?"+q.Encode(), tarBytes, "application/x-tar")
	if err != nil {
		t.Fatalf("import %s:%s: unexpected error %v", repo, tag, err)
	}
	if code != http.StatusOK {
		t.Fatalf("import %s:%s: status %d, want 200 (body %s)", repo, tag, code, body)
	}
	if bytes.Contains(body, []byte(`"errorDetail"`)) {
		t.Fatalf("import %s:%s: the Engine reported an error: %s", repo, tag, body)
	}
}

// engineImageID returns the ID of the image named by ref.
func engineImageID(t *testing.T, c *http.Client, ref string) string {
	t.Helper()
	code, body := engineRequest(t, c, http.MethodGet, "/v1.44/images/"+ref+"/json")
	if code != http.StatusOK {
		t.Fatalf("inspect image %s: status %d, want 200 (body %s)", ref, code, body)
	}
	var row struct {
		ID string `json:"Id"`
	}
	if err := json.Unmarshal(body, &row); err != nil {
		t.Fatalf("decode the image %s: unexpected error %v", ref, err)
	}
	return row.ID
}

// removeProject removes everything one run created: the containers and networks
// carrying its project label, its project network and its image.
func removeProject(t *testing.T, sock, project, imageID string) {
	t.Helper()
	c := engineClient(sock)
	filter := url.QueryEscape(`{"label":["dev.moonglass.project=` + project + `"]}`)

	code, body := engineRequest(t, c, http.MethodGet, "/v1.44/containers/json?all=1&filters="+filter)
	if code != http.StatusOK {
		t.Errorf("list the containers of project %q: status %d, want 200 (body %s)", project, code, body)
	}
	var containers []struct {
		ID string `json:"Id"`
	}
	if err := json.Unmarshal(body, &containers); err != nil {
		t.Errorf("decode the containers of project %q: unexpected error %v", project, err)
	}
	for _, row := range containers {
		engineDelete(t, c, "/v1.44/containers/"+row.ID+"?force=1&v=1")
	}

	code, body = engineRequest(t, c, http.MethodGet, "/v1.44/networks?filters="+filter)
	if code != http.StatusOK {
		t.Errorf("list the networks of project %q: status %d, want 200 (body %s)", project, code, body)
	}
	var networks []struct {
		ID string `json:"Id"`
	}
	if err := json.Unmarshal(body, &networks); err != nil {
		t.Errorf("decode the networks of project %q: unexpected error %v", project, err)
	}
	for _, row := range networks {
		engineDelete(t, c, "/v1.44/networks/"+row.ID)
	}
	engineDelete(t, c, "/v1.44/networks/mg-"+project)

	if imageID != "" {
		engineDelete(t, c, "/v1.44/images/"+imageID+"?force=1")
	}
}

func randProject(t *testing.T) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand.Read: unexpected error %v", err)
	}
	return "t" + hex.EncodeToString(b)
}

// TestResentCommandReturnsStoredResult proves rules 2 and 3 of "Each request id
// runs once" against a real Engine: a command re-sent after a reconnect is
// answered from the stored record and never runs twice.
func TestResentCommandReturnsStoredResult(t *testing.T) {
	realSock := engineSocket(t)
	real := engineClient(realSock)
	code, body, err := engineCall(real, http.MethodGet, "/_ping", nil, "")
	if err != nil || code != http.StatusOK {
		if os.Getenv("CI") != "" {
			t.Fatalf("no Docker Engine at %s: status %d, error %v, body %s: CI must provide Docker", realSock, code, err, body)
		}
		t.Skipf("no Docker Engine at %s: status %d, error %v", realSock, code, err)
	}

	project := randProject(t)
	imageRef := "moonglass.local/" + project + "/web:r1"
	imageID := ""
	t.Cleanup(func() { removeProject(t, realSock, project, imageID) })

	importEngineImage(t, real, buildServerTar(t), "moonglass.local/"+project+"/web", "r1")
	imageID = engineImageID(t, real, imageRef)

	counting := newEngineProxy(t, realSock)
	t.Setenv("DOCKER_HOST", "unix://"+counting.sock)

	ctx := context.Background()
	engine, err := deploy.Connect(ctx)
	if err != nil {
		t.Fatalf("deploy.Connect through the counting proxy: unexpected error %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	dataDir := t.TempDir()
	db, err := state.Open(ctx, dataDir)
	if err != nil {
		t.Fatalf("state.Open(%q): unexpected error %v", dataDir, err)
	}
	t.Cleanup(func() { _ = db.Close() })

	env, err := deploy.PolicyEnv(ctx, engine)
	if err != nil {
		t.Fatalf("deploy.PolicyEnv: unexpected error %v", err)
	}
	pol, err := policy.Parse(nil, env)
	if err != nil {
		t.Fatalf("policy.Parse(empty): unexpected error %v", err)
	}
	pusher := deploy.NewProxy(engine, db, deploy.ProxyConfig{Socket: filepath.Join(t.TempDir(), "admin.sock"), Issuer: "internal"})
	exec := deploy.New(engine, pol, db, pusher)

	cp, agentDir := newCmdCP(t)
	_, stop := runConnect(t, agentDir, "https://"+cp.cp.addr, ConnectOptions{
		Executor: exec, AgentVersion: testVersion, HeartbeatInterval: 20 * time.Millisecond, Sleep: fastSleep,
	})
	cp.waitConn(t, 1)

	reqID := "c" + hex.EncodeToString(bytes.Repeat([]byte{7}, 16))
	frame := mustMarshal(t, agentv1.MessageType_DEPLOY, reqID, &agentv1.Deploy{
		AppId:       "app-web",
		ReleaseId:   "r1",
		Image:       imageRef,
		Project:     project,
		Environment: "production",
		Service:     "web",
	})
	if err := cp.send(frame); err != nil {
		t.Fatalf("send the DEPLOY: unexpected error %v", err)
	}
	first := cp.nextResult(t)
	if first.ID != reqID {
		t.Fatalf("first result request id = %q, want %q", first.ID, reqID)
	}
	if got, want := first.Res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
		t.Fatalf("DEPLOY outcome = %v with detail %q, want %v", got, first.Res.GetDetail(), want)
	}

	cp.closeLive()
	cp.waitConn(t, 2)
	if err := cp.send(frame); err != nil {
		t.Fatalf("re-send the identical DEPLOY on connection 2: unexpected error %v", err)
	}
	second := cp.nextResult(t)
	if second.ID != reqID {
		t.Errorf("second result request id = %q, want %q", second.ID, reqID)
	}
	if second.Conn != 2 {
		t.Errorf("second result arrived on connection %d, want 2", second.Conn)
	}
	if !proto.Equal(first.Res, second.Res) {
		t.Errorf("second result = %v, want the first result %v unchanged", second.Res, first.Res)
	}
	if got := counting.creates.Load(); got != 1 {
		t.Errorf("POST /containers/create count = %d, want 1: the re-sent command must not run twice", got)
	}
	_ = stop()
}
