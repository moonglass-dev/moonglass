package deploy_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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
	firstErr   error
	firstErrAt time.Time
	started    time.Time
	responses  []response
}

// String gives the count, the total, the first error and when it happened.
func (r loadResult) String() string {
	if r.firstErr == nil {
		return fmt.Sprintf("%d of %d requests failed, no error", r.failed, r.total)
	}
	return fmt.Sprintf("%d of %d requests failed, first error %v at %s (%.3f s after start)",
		r.failed, r.total, r.firstErr, r.firstErrAt.Format(time.RFC3339Nano),
		r.firstErrAt.Sub(r.started).Seconds())
}

// load is one run of the load generator. It opens a new connection for every
// request, so each request is routed by the config live when it was sent.
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
	firstErr   error
	firstErrAt time.Time
	responses  []response
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

// startLoad starts sending requests to cfg at its rate until stop is called.
func startLoad(cfg loadConfig) *load {
	rate := cfg.rate
	if rate == 0 {
		rate = 50
	}
	l := &load{
		cfg:      cfg,
		client:   newLoadClient(cfg),
		started:  time.Now(),
		ticker:   time.NewTicker(time.Second / time.Duration(rate)),
		stopped:  make(chan struct{}),
		finished: make(chan struct{}),
	}
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

// get sends one request and records its answer or its failure.
func (l *load) get() {
	req, err := http.NewRequest(http.MethodGet, "https://"+l.cfg.host+"/", nil)
	if err != nil {
		l.fail(fmt.Errorf("build GET %s: %w", l.cfg.host, err))
		return
	}
	res, err := l.client.Do(req)
	if err != nil {
		l.fail(fmt.Errorf("GET %s: %w", l.cfg.host, err))
		return
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 256))
	_ = res.Body.Close()
	if err != nil {
		l.fail(fmt.Errorf("GET %s: read its answer: %w", l.cfg.host, err))
		return
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		l.fail(fmt.Errorf("GET %s: status %d", l.cfg.host, res.StatusCode))
		return
	}

	arrived := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.total++
	l.responses = append(l.responses, response{
		arrived: arrived, release: strings.TrimSpace(string(body)),
	})
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

	l.mu.Lock()
	defer l.mu.Unlock()
	return loadResult{
		total: l.total, failed: l.failed,
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
}
