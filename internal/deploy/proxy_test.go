package deploy

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"moonglass.dev/moonglass/internal/agent/state"
	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

const (
	fakeAdminJSON   = `{"listen":"unix//x|0600"}`
	fakeStorageJSON = `{"module":"file_system","root":"/x"}`
)

// shortTempDir makes a short temp directory, because a unix socket path is
// limited to about 100 bytes.
func shortTempDir(t *testing.T, prefix string) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", prefix)
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// fakeAdmin is a stand-in for the proxy's admin socket. It serves the two
// config reads, answers GET /config/ from the last pushed document unless told
// otherwise, and records every pushed document.
type fakeAdmin struct {
	t    *testing.T
	path string

	mu           sync.Mutex
	srv          *httptest.Server
	loads        [][]byte
	requests     int
	adminStatus  int
	adminBody    []byte
	storageBody  []byte
	loadStatus   int
	loadBody     []byte
	loadDelay    time.Duration
	onLoad       func()
	inflight     int
	maxInflight  int
	configAnswer []byte
}

func newFakeAdmin(t *testing.T) *fakeAdmin {
	t.Helper()
	f := &fakeAdmin{
		t:           t,
		path:        filepath.Join(shortTempDir(t, "mgpush"), "admin.sock"),
		adminBody:   []byte(fakeAdminJSON),
		storageBody: []byte(fakeStorageJSON),
	}
	f.start()
	t.Cleanup(f.close)
	return f
}

func (f *fakeAdmin) start() {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(f.handle))
	listener, err := net.Listen("unix", f.path)
	if err != nil {
		f.t.Fatalf("listen on %s: %v", f.path, err)
	}
	srv.Listener = listener
	srv.Start()
	f.mu.Lock()
	f.srv = srv
	f.mu.Unlock()
}

func (f *fakeAdmin) close() {
	f.mu.Lock()
	srv := f.srv
	f.srv = nil
	f.mu.Unlock()
	if srv != nil {
		srv.Close()
	}
}

func (f *fakeAdmin) reopen() {
	_ = os.Remove(f.path)
	f.start()
}

func writeFake(w http.ResponseWriter, status int, body []byte) {
	if status == 0 {
		status = http.StatusOK
	}
	if status == http.StatusOK {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func (f *fakeAdmin) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests++
	f.mu.Unlock()

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/config/admin":
		f.mu.Lock()
		status, body := f.adminStatus, f.adminBody
		f.mu.Unlock()
		writeFake(w, status, body)
	case r.Method == http.MethodGet && r.URL.Path == "/config/storage":
		f.mu.Lock()
		status, body := f.adminStatus, f.storageBody
		f.mu.Unlock()
		writeFake(w, status, body)
	case r.Method == http.MethodGet && r.URL.Path == "/config/":
		f.mu.Lock()
		answer := f.configAnswer
		if answer == nil && len(f.loads) > 0 {
			answer = f.loads[len(f.loads)-1]
		}
		if answer == nil {
			answer = []byte("{}")
		}
		f.mu.Unlock()
		writeFake(w, http.StatusOK, answer)
	case r.Method == http.MethodPost && r.URL.Path == "/load":
		body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
		if err != nil {
			http.Error(w, "read pushed config: "+err.Error(), http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.inflight++
		if f.inflight > f.maxInflight {
			f.maxInflight = f.inflight
		}
		delay, status, response := f.loadDelay, f.loadStatus, f.loadBody
		f.mu.Unlock()

		if delay > 0 {
			time.Sleep(delay)
		}

		f.mu.Lock()
		f.loads = append(f.loads, body)
		f.inflight--
		onLoad := f.onLoad
		f.mu.Unlock()
		if onLoad != nil {
			onLoad()
		}
		writeFake(w, status, response)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeAdmin) loadCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.loads)
}

func (f *fakeAdmin) loadAt(i int) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	body := make([]byte, len(f.loads[i]))
	copy(body, f.loads[i])
	return body
}

func (f *fakeAdmin) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

func (f *fakeAdmin) maxInFlight() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxInflight
}

func (f *fakeAdmin) setAdminStatus(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.adminStatus = status
}

func (f *fakeAdmin) setLoadResponse(status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loadStatus = status
	f.loadBody = []byte(body)
}

func (f *fakeAdmin) setLoadDelay(delay time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loadDelay = delay
}

func (f *fakeAdmin) setConfigAnswer(body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.configAnswer = body
}

func newProxyDB(t *testing.T) *state.DB {
	t.Helper()
	db, err := state.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("state.Open error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func decodeAny(t *testing.T, body []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode JSON %q: %v", body, err)
	}
	return v
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode JSON: %v", err)
	}
	return string(raw)
}

// normalizedJSON renders v as the generic value json.Unmarshal returns, so a
// hand-built expected document compares equal to a decoded body.
func normalizedJSON(t *testing.T, v any) any {
	t.Helper()
	return decodeAny(t, []byte(mustJSON(t, v)))
}

func requireJSONEqual(t *testing.T, field string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %s, want %s", field, mustJSON(t, got), mustJSON(t, want))
	}
}

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", timeout, what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// containerIPOn returns the release container's address on the project's
// network.
func containerIPOn(t *testing.T, e *Engine, p *project, app, rel string) string {
	t.Helper()
	c := requireContainer(t, e, testContainerName(app, rel))
	if c.NetworkSettings == nil {
		t.Fatalf("container %s NetworkSettings = nil, want its networks", testContainerName(app, rel))
	}
	endpoint, ok := c.NetworkSettings.Networks["mg-"+p.name]
	if !ok || endpoint == nil || !endpoint.IPAddress.IsValid() {
		t.Fatalf("container %s has no address on network mg-%s: %+v",
			testContainerName(app, rel), p.name, c.NetworkSettings.Networks)
	}
	return endpoint.IPAddress.String()
}

func expectedRoute(domains []string, releaseID, dial string) map[string]any {
	hosts := make([]any, len(domains))
	for i, domain := range domains {
		hosts[i] = domain
	}
	return map[string]any{
		"match": []any{map[string]any{"host": hosts}},
		"handle": []any{
			map[string]any{"handler": "moonglass_counter", "release": releaseID},
			map[string]any{"handler": "reverse_proxy", "upstreams": []any{map[string]any{"dial": dial}}},
		},
		"terminal": true,
	}
}

func TestProxyPushBuildsCompleteConfig(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)
	p := testProject(t, e)
	x, dir := newExecutor(t, e)
	fake := newFakeAdmin(t)

	appA := testAppID(p, "a")
	appB := testAppID(p, "b")
	appC := testAppID(p, "c")
	appD := testAppID(p, "d")

	requireDeployOutcome(t,
		deployDomains(t, e, x, p, appA, "r1", []string{"a1.example.com", "a2.example.com"}),
		agentv1.CommandOutcome_SUCCEEDED, "")
	requireDeployOutcome(t,
		deployDomainsPort(t, e, x, p, appB, "r1", 8081, []string{"b.example.com"}),
		agentv1.CommandOutcome_SUCCEEDED, "")
	requireDeployOutcome(t,
		deployDomains(t, e, x, p, appC, "r1", nil),
		agentv1.CommandOutcome_SUCCEEDED, "")
	requireDeployOutcome(t,
		deployDomains(t, e, x, p, appD, "r1", []string{"d.example.com"}),
		agentv1.CommandOutcome_SUCCEEDED, "")
	makeCurrent(t, dir, appA)
	makeCurrent(t, dir, appB)
	makeCurrent(t, dir, appC)

	pusher := NewProxy(e, x.db, ProxyConfig{Socket: fake.path, Issuer: "internal"})
	if err := pusher.Push(ctx); err != nil {
		t.Fatalf("Push() error = %v, want nil", err)
	}
	requireEqual(t, "POST /load count", fake.loadCount(), 1)

	want := map[string]any{
		"admin":   map[string]any{"listen": "unix//x|0600"},
		"storage": map[string]any{"module": "file_system", "root": "/x"},
		"logging": map[string]any{"logs": map[string]any{"default": map[string]any{"level": "WARN"}}},
		"apps": map[string]any{
			"http": map[string]any{
				"grace_period": "30s",
				"servers": map[string]any{"moonglass": map[string]any{
					"listen": []any{":443"},
					"routes": []any{
						expectedRoute([]string{"a1.example.com", "a2.example.com"}, "r1",
							net.JoinHostPort(containerIPOn(t, e, p, appA, "r1"), "8080")),
						expectedRoute([]string{"b.example.com"}, "r1",
							net.JoinHostPort(containerIPOn(t, e, p, appB, "r1"), "8081")),
					},
				}},
			},
			"tls": map[string]any{"automation": map[string]any{"policies": []any{
				map[string]any{"issuers": []any{map[string]any{"module": "internal"}}},
			}}},
			"pki": map[string]any{"certificate_authorities": map[string]any{
				"local": map[string]any{"install_trust": false},
			}},
		},
	}

	requireJSONEqual(t, "pushed config", decodeAny(t, fake.loadAt(0)), normalizedJSON(t, want))

	t.Run("no routes", func(t *testing.T) {
		fresh := newFakeAdmin(t)
		pusher := NewProxy(nil, newProxyDB(t), ProxyConfig{Socket: fresh.path, Issuer: "internal"})
		if err := pusher.Push(context.Background()); err != nil {
			t.Fatalf("Push() with no routed app error = %v, want nil", err)
		}
		requireEqual(t, "POST /load count", fresh.loadCount(), 1)

		doc, ok := decodeAny(t, fresh.loadAt(0)).(map[string]any)
		if !ok {
			t.Fatalf("pushed config is not a JSON object")
		}
		apps, ok := doc["apps"].(map[string]any)
		if !ok {
			t.Fatalf("pushed config apps = %#v, want an object", doc["apps"])
		}
		requireJSONEqual(t, "apps.http", apps["http"], normalizedJSON(t, map[string]any{"grace_period": "30s"}))
	})
}

func TestProxyPushIssuers(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		issuer  string
		wantTLS map[string]any
		wantPKI map[string]any
	}{
		{
			name:   "acme",
			issuer: "acme",
			wantTLS: map[string]any{"automation": map[string]any{"policies": []any{
				map[string]any{"issuers": []any{map[string]any{"module": "acme"}}},
			}}},
		},
		{
			name:   "acme staging",
			issuer: "acme-staging",
			wantTLS: map[string]any{"automation": map[string]any{"policies": []any{
				map[string]any{"issuers": []any{map[string]any{
					"module": "acme",
					"ca":     "https://acme-staging-v02.api.letsencrypt.org/directory",
				}}},
			}}},
		},
		{
			name:   "internal",
			issuer: "internal",
			wantTLS: map[string]any{"automation": map[string]any{"policies": []any{
				map[string]any{"issuers": []any{map[string]any{"module": "internal"}}},
			}}},
			wantPKI: map[string]any{"certificate_authorities": map[string]any{
				"local": map[string]any{"install_trust": false},
			}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeAdmin(t)
			pusher := NewProxy(nil, newProxyDB(t), ProxyConfig{Socket: fake.path, Issuer: tc.issuer})
			if err := pusher.Push(ctx); err != nil {
				t.Fatalf("Push() with issuer %q error = %v, want nil", tc.issuer, err)
			}
			requireEqual(t, "POST /load count", fake.loadCount(), 1)

			doc, ok := decodeAny(t, fake.loadAt(0)).(map[string]any)
			if !ok {
				t.Fatalf("pushed config is not a JSON object")
			}
			apps, ok := doc["apps"].(map[string]any)
			if !ok {
				t.Fatalf("pushed config apps = %#v, want an object", doc["apps"])
			}
			requireJSONEqual(t, "apps.tls", apps["tls"], normalizedJSON(t, tc.wantTLS))

			gotPKI, hasPKI := apps["pki"]
			if tc.wantPKI == nil {
				if hasPKI {
					t.Errorf("apps.pki = %#v, want no pki app for issuer %q", gotPKI, tc.issuer)
				}
				return
			}
			if !hasPKI {
				t.Fatalf("apps.pki is missing for issuer %q, want %s", tc.issuer, mustJSON(t, tc.wantPKI))
			}
			requireJSONEqual(t, "apps.pki", gotPKI, normalizedJSON(t, tc.wantPKI))
		})
	}

	t.Run("unknown issuer", func(t *testing.T) {
		fake := newFakeAdmin(t)
		pusher := NewProxy(nil, newProxyDB(t), ProxyConfig{Socket: fake.path, Issuer: "letsencrypt"})
		err := pusher.Push(ctx)
		if err == nil {
			t.Fatalf("Push() with issuer letsencrypt error = nil, want an error")
		}
		for _, want := range []string{"letsencrypt", "acme", "acme-staging", "internal"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("Push() error = %q, want it to contain %q", err, want)
			}
		}
		requireEqual(t, "requests on the admin socket", fake.requestCount(), 0)
	})
}

func TestProxyPushFailuresPushNothing(t *testing.T) {
	ctx := context.Background()

	t.Run("no socket", func(t *testing.T) {
		sock := filepath.Join(shortTempDir(t, "mgpush"), "admin.sock")
		pusher := NewProxy(nil, newProxyDB(t), ProxyConfig{Socket: sock, Issuer: "internal"})
		err := pusher.Push(ctx)
		if err == nil {
			t.Fatalf("Push() with no socket error = nil, want an error")
		}
		if !strings.Contains(err.Error(), sock) {
			t.Errorf("Push() error = %q, want it to name the socket %s", err, sock)
		}
	})

	t.Run("config admin answers 500", func(t *testing.T) {
		fake := newFakeAdmin(t)
		fake.setAdminStatus(http.StatusInternalServerError)
		pusher := NewProxy(nil, newProxyDB(t), ProxyConfig{Socket: fake.path, Issuer: "internal"})
		err := pusher.Push(ctx)
		if err == nil {
			t.Fatalf("Push() with GET /config/admin answering 500 error = nil, want an error")
		}
		if !strings.Contains(err.Error(), "500") {
			t.Errorf("Push() error = %q, want it to contain the status 500", err)
		}
		requireEqual(t, "POST /load count", fake.loadCount(), 0)
	})

	t.Run("load answers 400", func(t *testing.T) {
		fake := newFakeAdmin(t)
		fake.setLoadResponse(http.StatusBadRequest, "bad config here")
		pusher := NewProxy(nil, newProxyDB(t), ProxyConfig{Socket: fake.path, Issuer: "internal"})
		err := pusher.Push(ctx)
		if err == nil {
			t.Fatalf("Push() with POST /load answering 400 error = nil, want an error")
		}
		for _, want := range []string{"400", "bad config here"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("Push() error = %q, want it to contain %q", err, want)
			}
		}
		requireEqual(t, "POST /load count", fake.loadCount(), 1)
	})

	t.Run("routed container is gone", func(t *testing.T) {
		e := testEngine(t)
		p := testProject(t, e)
		x, dir := newExecutor(t, e)
		fake := newFakeAdmin(t)
		app := testAppID(p, "gone")

		requireDeployOutcome(t,
			deployDomains(t, e, x, p, app, "r1", []string{"gone.example.com"}),
			agentv1.CommandOutcome_SUCCEEDED, "")
		makeCurrent(t, dir, app)

		container := singleContainerID(t, e, testContainerName(app, "r1"))
		if _, err := e.cli.ContainerRemove(ctx, container, client.ContainerRemoveOptions{Force: true}); err != nil {
			t.Fatalf("remove container %s: %v", container, err)
		}

		pusher := NewProxy(e, x.db, ProxyConfig{Socket: fake.path, Issuer: "internal"})
		err := pusher.Push(ctx)
		if err == nil {
			t.Fatalf("Push() with a removed routed container error = nil, want an error")
		}
		for _, want := range []string{app, "r1", container} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("Push() error = %q, want it to name %q", err, want)
			}
		}
		requireEqual(t, "POST /load count", fake.loadCount(), 0)
	})

	t.Run("a failed push is not recorded as last", func(t *testing.T) {
		fake := newFakeAdmin(t)
		fake.setLoadResponse(http.StatusBadRequest, "bad config here")
		// configAnswer stays unset, so GET /config/ echoes the last /load body.
		// A second /load therefore proves the failed push was not recorded as
		// last: had it been, the probe would see no difference and stay quiet.

		var errs errorLog
		pusher := NewProxy(nil, newProxyDB(t), ProxyConfig{
			Socket: fake.path, Issuer: "internal", OnError: errs.add,
		})
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		var runErr error
		go func() {
			defer close(done)
			runErr = pusher.Run(runCtx)
		}()
		t.Cleanup(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Errorf("Run() did not stop")
			}
		})

		waitFor(t, "a second /load after the first failed push", 3*time.Second,
			func() bool { return fake.loadCount() >= 2 })

		cancel()
		select {
		case <-done:
			if runErr != nil {
				t.Errorf("Run() after cancel = %v, want nil", runErr)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("Run() did not return within 2 s of cancel")
		}
		if !errs.contains("400") {
			t.Errorf("OnError received %q, want an error containing 400", errs.snapshot())
		}
	})
}

func TestProxyPushesOneAtATime(t *testing.T) {
	fake := newFakeAdmin(t)
	fake.setLoadDelay(50 * time.Millisecond)
	pusher := NewProxy(nil, newProxyDB(t), ProxyConfig{Socket: fake.path, Issuer: "internal"})

	const pushes = 10
	errs := make([]error, pushes)
	var wg sync.WaitGroup
	for i := range pushes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = pusher.Push(context.Background())
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("Push() #%d error = %v, want nil", i, err)
		}
	}
	requireEqual(t, "POST /load count", fake.loadCount(), pushes)
	requireEqual(t, "most /load handlers in flight at once", fake.maxInFlight(), 1)
}

type errorLog struct {
	mu   sync.Mutex
	errs []error
}

func (l *errorLog) add(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errs = append(l.errs, err)
}

func (l *errorLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.errs)
}

func (l *errorLog) contains(sub string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, err := range l.errs {
		if strings.Contains(err.Error(), sub) {
			return true
		}
	}
	return false
}

func (l *errorLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.errs))
	for i, err := range l.errs {
		out[i] = err.Error()
	}
	return out
}

func TestProxyRunPushesAgainWhenRunningConfigDiffers(t *testing.T) {
	fake := newFakeAdmin(t)
	var errs errorLog
	pusher := NewProxy(nil, newProxyDB(t), ProxyConfig{
		Socket: fake.path, Issuer: "internal", OnError: errs.add,
	})

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var runErr error
	go func() {
		defer close(done)
		runErr = pusher.Run(runCtx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Errorf("Run() did not stop")
		}
	})

	waitFor(t, "the first push", 2*time.Second, func() bool { return fake.loadCount() >= 1 })

	// While /config/ answers the last pushed document there is nothing to push.
	time.Sleep(3 * time.Second)
	requireEqual(t, "loads with the running config unchanged after 3 s", fake.loadCount(), 1)

	startup := []byte(`{"admin":{"listen":"unix//x|0600"},"storage":{"module":"file_system","root":"/x"},"logging":{"logs":{"default":{"level":"WARN"}}}}`)
	fake.setConfigAnswer(startup)
	waitFor(t, "a push after the running config changed", 3*time.Second,
		func() bool { return fake.loadCount() >= 2 })

	before := fake.loadCount()
	// Let one probe run with the running config equal to the last pushed one,
	// so no push is in flight when the socket closes.
	fake.setConfigAnswer(fake.loadAt(before - 1))
	requests := fake.requestCount()
	waitFor(t, "a probe with the running config equal", 2*time.Second,
		func() bool { return fake.requestCount() > requests })
	fake.close()
	time.Sleep(2500 * time.Millisecond)
	if got := errs.count(); got != 0 {
		t.Errorf("OnError called %d times while the socket was closed, want 0: %v", got, errs.snapshot())
	}

	fake.reopen()
	fake.setConfigAnswer(startup)
	waitFor(t, "a push after the socket reopened", 3*time.Second,
		func() bool { return fake.loadCount() > before })

	fake.setLoadResponse(http.StatusBadRequest, "bad config here")
	fake.setConfigAnswer(startup)
	waitFor(t, "OnError with the /load 400", 3*time.Second, func() bool { return errs.contains("400") })

	probeStart := fake.requestCount()
	waitFor(t, "the probe to keep running after a failed push", 2*time.Second,
		func() bool { return fake.requestCount() > probeStart })

	cancel()
	select {
	case <-done:
		if runErr != nil {
			t.Errorf("Run() after cancel = %v, want nil", runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Run() did not return within 2 s of cancel")
	}
}
