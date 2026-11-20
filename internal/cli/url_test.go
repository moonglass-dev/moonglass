package cli_test

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/cli"
)

// runGeneratedCLI runs the generated `servers list` subcommand with a fixed
// environment and returns its exit code and output.
func runGeneratedCLI(t *testing.T, base, token string) (code int, stdout, stderr string) {
	t.Helper()
	env := func(k string) string {
		switch k {
		case "MOONGLASS_URL":
			return base
		case "MOONGLASS_TOKEN":
			return token
		}
		return ""
	}
	var out, errb bytes.Buffer
	code = cli.Run([]string{"servers", "list"}, env, &out, &errb, localHandlers())
	return code, out.String(), errb.String()
}

// countingServersHandler answers like GET /v1/servers, reports the bearer it saw
// and counts requests.
func countingServersHandler() (http.Handler, <-chan string, *atomic.Int64) {
	auth := make(chan string, 16)
	var requests atomic.Int64
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		auth <- r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"servers":[]}`))
	})
	return h, auth, &requests
}

// serveLoopback starts h on a 127.0.0.1 port and returns that port.
func serveLoopback(t *testing.T, h http.Handler) int {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts.Listener.Addr().(*net.TCPAddr).Port
}

// serveIPv6Loopback starts h on a ::1 port and returns that port. A host with no
// IPv6 loopback cannot run this case.
func serveIPv6Loopback(t *testing.T, h http.Handler) int {
	t.Helper()
	ln, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skipf("bind [::1]:0: %v", err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().(*net.TCPAddr).Port
}

func TestGeneratedRunnerRefusesPlainHTTPExceptLocalhost(t *testing.T) {
	token := "mg_" + strings.Repeat("c", 52)

	loopback := []struct {
		name  string
		serve func(t *testing.T, h http.Handler) int
		url   func(port int) string
	}{
		{"localhost", serveLoopback, func(port int) string { return fmt.Sprintf("http://localhost:%d", port) }},
		{"loopback IP", serveLoopback, func(port int) string { return fmt.Sprintf("http://127.0.0.1:%d", port) }},
		{"IPv6 loopback", serveIPv6Loopback, func(port int) string { return fmt.Sprintf("http://[::1]:%d", port) }},
	}
	for _, tt := range loopback {
		t.Run("http reaches "+tt.name, func(t *testing.T) {
			h, auth, requests := countingServersHandler()
			base := tt.url(tt.serve(t, h))

			code, out, errb := runGeneratedCLI(t, base, token)

			if code != 0 {
				t.Fatalf("%s exit status: expected 0, got %d (stderr %q)", base, code, errb)
			}
			if !strings.Contains(out, `"servers"`) {
				t.Errorf("%s stdout: expected the response body, got %q", base, out)
			}
			if got := requests.Load(); got != 1 {
				t.Errorf("%s requests: expected 1, got %d", base, got)
			}
			select {
			case got := <-auth:
				if want := "Bearer " + token; got != want {
					t.Errorf("%s Authorization header: expected %q, got %q", base, want, got)
				}
			default:
				t.Errorf("%s: expected the request to carry a bearer token, got none", base)
			}
			if strings.Contains(errb, token) {
				t.Errorf("%s stderr leaks the token: %q", base, errb)
			}
		})
	}

	var requests atomic.Int64
	unused := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	}))
	t.Cleanup(unused.Close)
	unusedPort := unused.Listener.Addr().(*net.TCPAddr).Port

	refused := []struct {
		name string
		base string
	}{
		{"non-loopback host", "http://example.invalid"},
		{"private address", fmt.Sprintf("http://10.0.0.1:%d", unusedPort)},
	}
	for _, tt := range refused {
		t.Run("http to a "+tt.name+" exits 2", func(t *testing.T) {
			code, out, errb := runGeneratedCLI(t, tt.base, token)

			if code != 2 {
				t.Fatalf("%s exit status: expected 2, got %d (stderr %q)", tt.base, code, errb)
			}
			if !strings.Contains(errb, "MOONGLASS_URL") {
				t.Errorf("%s stderr: expected it to name MOONGLASS_URL, got %q", tt.base, errb)
			}
			if strings.Contains(errb, token) {
				t.Errorf("%s stderr leaks the token: %q", tt.base, errb)
			}
			if out != "" {
				t.Errorf("%s stdout: expected it empty, got %q", tt.base, out)
			}
			if got := requests.Load(); got != 0 {
				t.Errorf("%s requests: expected 0, got %d", tt.base, got)
			}
		})
	}

	t.Run("https with an untrusted certificate exits 1", func(t *testing.T) {
		h, auth, requests := countingServersHandler()
		ts := httptest.NewTLSServer(h)
		t.Cleanup(ts.Close)

		code, out, errb := runGeneratedCLI(t, ts.URL, token)

		if code != 1 {
			t.Fatalf("%s exit status: expected 1 (the certificate is not trusted by the system roots), got %d (stderr %q)", ts.URL, code, errb)
		}
		if got := requests.Load(); got != 0 {
			t.Errorf("%s requests: expected 0, got %d", ts.URL, got)
		}
		if len(auth) != 0 {
			t.Errorf("%s: expected no request with the bearer token, got %d", ts.URL, len(auth))
		}
		if out != "" {
			t.Errorf("%s stdout: expected it empty, got %q", ts.URL, out)
		}
		if strings.Contains(errb, token) {
			t.Errorf("%s stderr leaks the token: %q", ts.URL, errb)
		}
	})
}
