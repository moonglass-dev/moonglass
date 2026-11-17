package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

// latencyBoundsMillis are the inclusive upper bounds of the latency buckets.
// A request slower than the last bound lands in one extra overflow bucket.
var latencyBoundsMillis = [...]int64{5, 10, 25, 50, 100, 250, 500, 1000}

// statusClasses name the status class counters, indexed by status/100 - 1.
var statusClasses = [...]string{"1xx", "2xx", "3xx", "4xx", "5xx"}

func init() {
	caddy.RegisterModule(Counter{})
	caddy.RegisterModule(Counters{})
}

// releaseCounts are the live counters of one release. They live in the
// process, not in a module instance, so a config reload keeps the counts.
type releaseCounts struct {
	requests     atomic.Uint64
	latencyNanos atomic.Uint64
	status       [len(statusClasses)]atomic.Uint64
	buckets      [len(latencyBoundsMillis) + 1]atomic.Uint64
}

var registry = struct {
	releases map[string]*releaseCounts
	mu       sync.Mutex
}{releases: map[string]*releaseCounts{}}

func countsFor(release string) *releaseCounts {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	c, ok := registry.releases[release]
	if !ok {
		c = new(releaseCounts)
		registry.releases[release] = c
	}
	return c
}

// Counter is the Caddy handler `moonglass_counter`. Put it first in a route's
// handle list with the id of the release that route serves:
//
//	{"handler": "moonglass_counter", "release": "rel_123"}
//
// It counts requests, status classes and latency for that release, and the
// counts are read from the admin socket (see Counters).
type Counter struct {
	counts *releaseCounts

	// Release is the release id the route serves.
	Release string `json:"release"`
}

// CaddyModule returns the Caddy module information.
func (Counter) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.moonglass_counter",
		New: func() caddy.Module { return new(Counter) },
	}
}

// Provision binds the handler to its release's counters.
func (c *Counter) Provision(caddy.Context) error {
	c.counts = countsFor(c.Release)
	return nil
}

// Validate rejects a handler without a release id.
func (c *Counter) Validate() error {
	if c.Release == "" {
		return errors.New("moonglass_counter: release is empty; set \"release\" to the release id the route serves")
	}
	return nil
}

// ServeHTTP counts the request after the rest of the route has handled it.
func (c *Counter) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	start := time.Now()
	sw := &statusWriter{ResponseWriter: w}
	err := next.ServeHTTP(sw, r)
	status := sw.status
	if status == 0 {
		status = http.StatusOK
		if err != nil {
			status = http.StatusInternalServerError
			var herr caddyhttp.HandlerError
			if errors.As(err, &herr) && herr.StatusCode != 0 {
				status = herr.StatusCode
			}
		}
	}
	c.counts.record(status, time.Since(start))
	return err
}

func (c *releaseCounts) record(status int, elapsed time.Duration) {
	c.requests.Add(1)
	c.latencyNanos.Add(uint64(max(elapsed, 0)))
	if class := status/100 - 1; class >= 0 && class < len(c.status) {
		c.status[class].Add(1)
	}
	ms := elapsed.Milliseconds()
	bucket := len(latencyBoundsMillis)
	for i, bound := range latencyBoundsMillis {
		if ms <= bound {
			bucket = i
			break
		}
	}
	c.buckets[bucket].Add(1)
}

// statusWriter remembers the status code of the response.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 && (code >= http.StatusOK || code == http.StatusSwitchingProtocols) {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Counts are the numbers for one release since the proxy process started.
type Counts struct {
	// Status counts responses by class: "1xx" to "5xx".
	Status map[string]uint64 `json:"status"`
	// LatencyBuckets counts requests per latency bucket (not cumulative), in
	// the order of LatencyBoundsMillis plus a last overflow bucket.
	LatencyBuckets    []uint64 `json:"latency_buckets"`
	Requests          uint64   `json:"requests"`
	LatencyNanosTotal uint64   `json:"latency_nanos_total"`
}

// Snapshot is the body of GET /moonglass/counters on the admin socket.
type Snapshot struct {
	// Releases maps a release id to its counts.
	Releases map[string]Counts `json:"releases"`
	// LatencyBoundsMillis are the inclusive upper bounds of the latency buckets.
	LatencyBoundsMillis []int64 `json:"latency_bounds_ms"`
}

func snapshot() Snapshot {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	snap := Snapshot{
		Releases:            make(map[string]Counts, len(registry.releases)),
		LatencyBoundsMillis: latencyBoundsMillis[:],
	}
	for release, c := range registry.releases {
		counts := Counts{
			Requests:          c.requests.Load(),
			LatencyNanosTotal: c.latencyNanos.Load(),
			Status:            make(map[string]uint64, len(statusClasses)),
			LatencyBuckets:    make([]uint64, len(c.buckets)),
		}
		for i, name := range statusClasses {
			counts.Status[name] = c.status[i].Load()
		}
		for i := range c.buckets {
			counts.LatencyBuckets[i] = c.buckets[i].Load()
		}
		snap.Releases[release] = counts
	}
	return snap
}

// Counters is the admin API module that serves the counts at
// GET /moonglass/counters, on the same socket as the rest of Caddy's admin API.
type Counters struct{}

// CaddyModule returns the Caddy module information.
func (Counters) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "admin.api.moonglass_counters",
		New: func() caddy.Module { return new(Counters) },
	}
}

// Routes registers the counters endpoint.
func (Counters) Routes() []caddy.AdminRoute {
	return []caddy.AdminRoute{{
		Pattern: "/moonglass/counters",
		Handler: caddy.AdminHandlerFunc(serveCounters),
	}}
}

func serveCounters(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodGet {
		return caddy.APIError{
			HTTPStatus: http.StatusMethodNotAllowed,
			Err:        fmt.Errorf("method %s not allowed on /moonglass/counters; use GET", r.Method),
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(snapshot()); err != nil {
		return fmt.Errorf("write counters: %w", err)
	}
	return nil
}
