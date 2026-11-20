package deploy_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// loadConfig is where and how the load generator sends requests.
type loadConfig struct {
	// addr is the proxy's loopback address, dialled for every request.
	addr string
	// host is the TLS server name and the Host header.
	host string
	// roots holds only the proxy's local CA root.
	roots *x509.CertPool
	// rate is requests per second; 0 means 50.
	rate int
}

// response is one 2xx answer: when it arrived and what release id its body
// carried.
type response struct {
	arrived time.Time
	release string
}

// loadResult is what one run of the load generator counted.
type loadResult struct {
	total      int
	failed     int
	retried    int
	firstErr   error
	firstErrAt time.Time
	started    time.Time
	responses  []response
}

// String gives the count, the total, the retries, the first error and when it
// happened.
func (r loadResult) String() string {
	if r.firstErr == nil {
		return fmt.Sprintf("%d of %d requests failed (%d retried), no error", r.failed, r.total, r.retried)
	}
	return fmt.Sprintf("%d of %d requests failed (%d retried), first error %v at %s (%.3f s after start)",
		r.failed, r.total, r.retried, r.firstErr, r.firstErrAt.Format(time.RFC3339Nano),
		r.firstErrAt.Sub(r.started).Seconds())
}

// load is one run of the load generator. It opens a new connection for every
// request, so each request is routed by the config live when it was sent. A
// request whose connection closed before any answer byte, without timing out,
// is sent once more on a new connection: a Caddy reload closes a connection the
// old server has not read a request from (docs/specs/deploy-suite.md).
type load struct {
	cfg    loadConfig
	client *http.Client

	started  time.Time
	ticker   *time.Ticker
	stopped  chan struct{}
	finished chan struct{}
	wg       sync.WaitGroup

	mu         sync.Mutex
	total      int
	failed     int
	retried    int
	firstErr   error
	firstErrAt time.Time
	responses  []response
}

// errCutBeforeAnswer marks an attempt whose connection closed before any byte
// of an answer arrived, without a timeout.
var errCutBeforeAnswer = errors.New("connection closed before any answer byte")

// closedByPeer reports whether err is the peer closing the connection rather
// than refusing it or failing its TLS handshake: the end of a stream, a reset,
// or a close mid-handshake.
func closedByPeer(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET)
}

// newLoadClient builds the one client the load generator uses: a new connection
// per request, a 5-second limit per request, and only the proxy's local CA root,
// never the system pool and never InsecureSkipVerify.
func newLoadClient(cfg loadConfig) *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			Proxy:             nil,
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "tcp", cfg.addr)
			},
			TLSClientConfig: &tls.Config{
				RootCAs:    cfg.roots,
				ServerName: cfg.host,
				MinVersion: tls.VersionTLS12,
			},
		},
	}
}

// newLoad builds a load generator for cfg that sends nothing until get or
// startLoad.
func newLoad(cfg loadConfig) *load {
	return &load{
		cfg:      cfg,
		client:   newLoadClient(cfg),
		started:  time.Now(),
		stopped:  make(chan struct{}),
		finished: make(chan struct{}),
	}
}

// startLoad starts sending requests to cfg at its rate until stop is called.
func startLoad(cfg loadConfig) *load {
	rate := cfg.rate
	if rate == 0 {
		rate = 50
	}
	l := newLoad(cfg)
	l.ticker = time.NewTicker(time.Second / time.Duration(rate))
	go func() {
		defer close(l.finished)
		for {
			select {
			case <-l.stopped:
				return
			case <-l.ticker.C:
				l.wg.Add(1)
				go func() {
					defer l.wg.Done()
					l.get()
				}()
			}
		}
	}()
	return l
}

// get sends one request, retrying it once on a new connection when the first
// attempt was cut before any answer byte, and records its answer or its
// failure.
func (l *load) get() {
	release, err := l.attempt()
	if errors.Is(err, errCutBeforeAnswer) {
		l.mu.Lock()
		l.retried++
		l.mu.Unlock()
		release, err = l.attempt()
	}
	if err != nil {
		l.fail(err)
		return
	}

	arrived := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.total++
	l.responses = append(l.responses, response{arrived: arrived, release: release})
}

// attempt sends the request once on a new connection and returns the release
// id a 2xx answer carried. An error wraps errCutBeforeAnswer when the
// connection closed before any answer byte and the request did not time out.
func (l *load) attempt() (string, error) {
	var answered atomic.Bool
	trace := &httptrace.ClientTrace{GotFirstResponseByte: func() { answered.Store(true) }}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(context.Background(), trace),
		http.MethodGet, "https://"+l.cfg.host+"/", nil)
	if err != nil {
		return "", fmt.Errorf("build GET %s: %w", l.cfg.host, err)
	}
	res, err := l.client.Do(req)
	if err != nil {
		var netErr net.Error
		if !answered.Load() && closedByPeer(err) && (!errors.As(err, &netErr) || !netErr.Timeout()) {
			return "", fmt.Errorf("GET %s: %w: %w", l.cfg.host, errCutBeforeAnswer, err)
		}
		return "", fmt.Errorf("GET %s: %w", l.cfg.host, err)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 256))
	_ = res.Body.Close()
	if err != nil {
		return "", fmt.Errorf("GET %s: read its answer: %w", l.cfg.host, err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return "", fmt.Errorf("GET %s: status %d", l.cfg.host, res.StatusCode)
	}
	return strings.TrimSpace(string(body)), nil
}

// fail counts one failed request and keeps the first failure and its time.
func (l *load) fail(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.total++
	l.failed++
	if l.firstErr == nil {
		l.firstErr = err
		l.firstErrAt = time.Now()
	}
}

// stop stops sending, waits for every in-flight request, and returns the
// result.
func (l *load) stop() loadResult {
	close(l.stopped)
	<-l.finished
	l.ticker.Stop()
	l.wg.Wait()
	return l.result()
}

// result is what the generator has counted so far.
func (l *load) result() loadResult {
	l.mu.Lock()
	defer l.mu.Unlock()
	return loadResult{
		total: l.total, failed: l.failed, retried: l.retried,
		firstErr: l.firstErr, firstErrAt: l.firstErrAt, started: l.started,
		responses: append([]response(nil), l.responses...),
	}
}

// TestLoadGeneratorCountsEveryNon2xx proves the generator counts every non-2xx,
// timeout and connection error exactly, opens a new connection per request, and
// reports the first failure with its time.
func TestLoadGeneratorCountsEveryNon2xx(t *testing.T) {
	var served, connections atomic.Int64
	var mu sync.Mutex
	var answered418 time.Time

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch served.Add(1) {
		case 3:
			mu.Lock()
			answered418 = time.Now()
			mu.Unlock()
			w.WriteHeader(http.StatusTeapot)
		case 6:
			w.WriteHeader(http.StatusInternalServerError)
		case 9:
			w.WriteHeader(http.StatusServiceUnavailable)
		case 12:
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			conn, buf, err := hijacker.Hijack()
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			// The answer starts, then the connection is cut: a connection
			// error after the first answer byte, never retried.
			_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 10\r\n\r\nr")
			_ = buf.Flush()
			_ = conn.Close()
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "r1")
		}
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	l := startLoad(loadConfig{addr: srv.Listener.Addr().String(), host: "example.com", roots: roots})

	deadline := time.Now().Add(10 * time.Second)
	for served.Load() < 20 {
		if time.Now().After(deadline) {
			l.stop()
			t.Fatalf("the server answered %d requests in 10 s, want at least 20", served.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
	res := l.stop()

	if want := 4; res.failed != want {
		t.Fatalf("load generator %s, want %d failed requests", res, want)
	}
	if res.retried != 0 {
		t.Errorf("load generator retried %d requests, want 0: every failure came after an answer byte", res.retried)
	}
	if got, want := res.total, int(served.Load()); got != want {
		t.Errorf("load generator total = %d, want the %d requests the server answered", got, want)
	}
	if got, want := connections.Load(), int64(res.total); got != want {
		t.Errorf("new connections = %d, want %d, one per request", got, want)
	}

	mu.Lock()
	at418 := answered418
	mu.Unlock()
	if res.firstErr == nil || !strings.Contains(res.firstErr.Error(), "418") {
		t.Errorf("first error = %v, want the 418 response", res.firstErr)
	}
	if res.firstErrAt.Before(at418) {
		t.Errorf("first error time = %s, want no earlier than the 418 answer at %s",
			res.firstErrAt.Format(time.RFC3339Nano), at418.Format(time.RFC3339Nano))
	}
	if late := res.firstErrAt.Sub(at418); late > time.Second {
		t.Errorf("first error time = %s, %s after the 418 answer, want within 1 s",
			res.firstErrAt.Format(time.RFC3339Nano), late)
	}

	if got, want := len(res.responses), res.total-res.failed; got != want {
		t.Errorf("recorded %d 2xx responses, want %d", got, want)
	}
	for _, r := range res.responses {
		if r.release != "r1" {
			t.Errorf("a 2xx response carried release %q, want r1", r.release)
		}
	}

	// A refused connection and a TLS verification failure are not a connection
	// closed before an answer byte: each is one failed request, never retried.
	for _, tc := range []struct {
		name string
		cfg  loadConfig
	}{
		{"refused connection", loadConfig{addr: reservePort(t), host: "example.com", roots: roots}},
		{"untrusted certificate", loadConfig{addr: srv.Listener.Addr().String(), host: "example.com", roots: x509.NewCertPool()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newLoad(tc.cfg)
			l.get()
			got := l.result()
			if got.total != 1 || got.failed != 1 || got.retried != 0 {
				t.Fatalf("load generator %s, want 1 of 1 request failed with 0 retried", got)
			}
			if got.firstErr == nil {
				t.Fatal("the failed request recorded no error")
			}
			if len(got.responses) != 0 {
				t.Errorf("recorded %d 2xx responses, want 0", len(got.responses))
			}
		})
	}
}

// TestLoadGeneratorRetriesOnceACutBeforeAnswer proves a request whose
// connection closes before any answer byte is sent once more on a new
// connection and counted as retried, and that a retry cut again is one failed
// request.
func TestLoadGeneratorRetriesOnceACutBeforeAnswer(t *testing.T) {
	var served, connections atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch served.Add(1) {
		case 1, 3, 4:
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			conn, _, err := hijacker.Hijack()
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_ = conn.Close()
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "r1")
		}
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	l := newLoad(loadConfig{addr: srv.Listener.Addr().String(), host: "example.com", roots: roots})

	// Request one: cut (1), retried and answered (2). Request two: cut (3),
	// retried and cut again (4), so it fails. Request three: answered (5).
	l.get()
	l.get()
	l.get()
	res := l.result()

	if res.total != 3 || res.failed != 1 || res.retried != 2 {
		t.Fatalf("load generator %s, want 1 of 3 requests failed with 2 retried", res)
	}
	if !errors.Is(res.firstErr, errCutBeforeAnswer) {
		t.Errorf("first error = %v, want the retry of request two cut before any answer byte", res.firstErr)
	}
	if got, want := served.Load(), int64(5); got != want {
		t.Errorf("the server answered %d attempts, want %d", got, want)
	}
	if got, want := connections.Load(), int64(5); got != want {
		t.Errorf("new connections = %d, want %d, one per attempt", got, want)
	}
	if got := len(res.responses); got != 2 {
		t.Fatalf("recorded %d 2xx responses, want 2", got)
	}
	for _, r := range res.responses {
		if r.release != "r1" {
			t.Errorf("a 2xx response carried release %q, want r1", r.release)
		}
	}
}
