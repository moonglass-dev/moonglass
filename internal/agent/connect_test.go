package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	mrand "math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	"moonglass.dev/moonglass/internal/keys"
	"moonglass.dev/moonglass/internal/protocol"
	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
	"moonglass.dev/moonglass/internal/server"
	"moonglass.dev/moonglass/internal/store/sqlite"
)

const testVersion = "v-test"

type beat struct{ id, version string }

// fakeCP is a pinned test control plane speaking the WSS protocol.
type fakeCP struct {
	t      *testing.T
	addr   string
	ln     net.Listener
	srv    *http.Server
	handle func(ctx context.Context, f *fakeCP, c *websocket.Conn)

	mu       sync.Mutex
	beats    []beat
	accepted int // TCP connections accepted
	wsConns  int // completed WebSocket upgrades
	closed   int // WebSocket connections the peer closed
	versions []uint16
	peers    []ed25519.PublicKey
	raw      []net.Conn
}

type cpOpts struct {
	priv       ed25519.PrivateKey // control-plane identity; random when nil
	agent      ed25519.PublicKey  // the only client key accepted; any when nil
	maxVersion uint16             // 0 means TLS 1.3
	addr       string             // "" means 127.0.0.1:0
	redirect   string             // when set, /v1/agent answers 302 to this URL
	handle     func(ctx context.Context, f *fakeCP, c *websocket.Conn)
}

type trackLn struct {
	net.Listener
	f *fakeCP
}

func (l trackLn) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		l.f.mu.Lock()
		l.f.accepted++
		l.f.raw = append(l.f.raw, c)
		l.f.mu.Unlock()
	}
	return c, err
}

func newFakeCP(t *testing.T, o cpOpts) *fakeCP {
	t.Helper()
	if o.priv == nil {
		_, o.priv = genKey(t)
	}
	if o.addr == "" {
		o.addr = "127.0.0.1:0"
	}
	f := &fakeCP{t: t, handle: o.handle}
	if f.handle == nil {
		f.handle = ackLoop
	}
	cfg, err := keys.ServerTLSConfig(o.priv, func(pub ed25519.PublicKey) error {
		f.mu.Lock()
		f.peers = append(f.peers, pub)
		f.mu.Unlock()
		if o.agent != nil && !o.agent.Equal(pub) {
			return errors.New("unknown agent")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("keys.ServerTLSConfig: unexpected error %v", err)
	}
	if o.maxVersion != 0 {
		cfg.MinVersion, cfg.MaxVersion = o.maxVersion, o.maxVersion
	}
	cfg.VerifyConnection = func(cs tls.ConnectionState) error {
		f.mu.Lock()
		f.versions = append(f.versions, cs.Version)
		f.mu.Unlock()
		return nil
	}
	raw, err := net.Listen("tcp", o.addr)
	if err != nil {
		t.Fatalf("net.Listen %s: unexpected error %v", o.addr, err)
	}
	f.ln = tls.NewListener(trackLn{Listener: raw, f: f}, cfg)
	f.addr = raw.Addr().String()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agent", func(w http.ResponseWriter, r *http.Request) {
		if o.redirect != "" {
			http.Redirect(w, r, o.redirect, http.StatusFound)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.CloseNow() }()
		c.SetReadLimit(protocol.MaxEnvelopeSize)
		f.mu.Lock()
		f.wsConns++
		f.mu.Unlock()
		f.handle(r.Context(), f, c)
	})
	f.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = f.srv.Serve(f.ln) }()
	t.Cleanup(f.stop)
	return f
}

func (f *fakeCP) stop() {
	_ = f.srv.Close()
	_ = f.ln.Close()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.raw {
		_ = c.Close()
	}
}

func (f *fakeCP) count(field *int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return *field
}

func (f *fakeCP) beatsSeen() []beat {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]beat(nil), f.beats...)
}

// readBeat reads one Heartbeat frame and records it.
func (f *fakeCP) readBeat(ctx context.Context, c *websocket.Conn) (string, error) {
	typ, data, err := c.Read(ctx)
	if err != nil {
		f.mu.Lock()
		f.closed++
		f.mu.Unlock()
		return "", err
	}
	if typ != websocket.MessageBinary {
		return "", fmt.Errorf("fake control plane: expected a binary frame, got %v", typ)
	}
	env, err := protocol.Unmarshal(data)
	if err != nil {
		return "", err
	}
	var hb agentv1.Heartbeat
	if err := protocol.Open(env, &hb); err != nil {
		return "", err
	}
	f.mu.Lock()
	f.beats = append(f.beats, beat{id: env.GetRequestId(), version: hb.GetAgentVersion()})
	f.mu.Unlock()
	return env.GetRequestId(), nil
}

func ackLoop(ctx context.Context, f *fakeCP, c *websocket.Conn) {
	for {
		id, err := f.readBeat(ctx, c)
		if err != nil {
			return
		}
		if err := c.Write(ctx, websocket.MessageBinary, ackBytes(f.t, id)); err != nil {
			return
		}
	}
}

func dropAfterFirstAck(ctx context.Context, f *fakeCP, c *websocket.Conn) {
	id, err := f.readBeat(ctx, c)
	if err != nil {
		return
	}
	_ = c.Write(ctx, websocket.MessageBinary, ackBytes(f.t, id))
	_ = c.Close(websocket.StatusNormalClosure, "bye")
}

func ackBytes(t *testing.T, id string) []byte {
	t.Helper()
	return mustMarshal(t, agentv1.MessageType_HEARTBEAT_ACK, id, &agentv1.HeartbeatAck{})
}

func mustMarshal(t *testing.T, typ agentv1.MessageType, id string, msg proto.Message) []byte {
	t.Helper()
	env, err := protocol.Wrap(typ, id, msg)
	if err != nil {
		t.Fatalf("protocol.Wrap: unexpected error %v", err)
	}
	b, err := protocol.Marshal(env)
	if err != nil {
		t.Fatalf("protocol.Marshal: unexpected error %v", err)
	}
	return b
}

func genKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: unexpected error %v", err)
	}
	return pub, priv
}

// writeAgent writes an enrolled agent data dir pinned to pin and returns the
// dir and the agent's public key.
func writeAgent(t *testing.T, pin ed25519.PublicKey) (string, ed25519.PublicKey) {
	t.Helper()
	id, err := keys.GenerateAgentIdentity()
	if err != nil {
		t.Fatalf("GenerateAgentIdentity: unexpected error %v", err)
	}
	fp, err := keys.Fingerprint(id.SigningPublic)
	if err != nil {
		t.Fatalf("Fingerprint: unexpected error %v", err)
	}
	dir := t.TempDir()
	files := map[string][]byte{
		signingFile: id.SigningPrivate,
		agreeFile:   id.AgreePrivate,
		pinFile:     pin,
		fingerFile:  []byte(fp + "\n"),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatalf("write %s: unexpected error %v", name, err)
		}
	}
	return dir, id.SigningPublic
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after 10s waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// runConnect starts Connect and returns a stop func that cancels it and
// returns Connect's result.
func runConnect(t *testing.T, dir, url string, opts ConnectOptions) (context.Context, func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Connect(ctx, dir, url, opts) }()
	stop := func() error {
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(10 * time.Second):
			t.Fatalf("Connect: expected it to return after cancel, still running after 10s")
			return nil
		}
	}
	t.Cleanup(func() { cancel() })
	return ctx, stop
}

func fastSleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Millisecond):
		return nil
	}
}

func seeded(seed int64) *mrand.Rand {
	return mrand.New(mrand.NewSource(seed)) //nolint:gosec // G404: deterministic test jitter
}

func TestConnectDialsOutboundWSS(t *testing.T) {
	pin, pinPriv := genKey(t)
	dir, agentPub := writeAgent(t, pin)
	cp := newFakeCP(t, cpOpts{priv: pinPriv, agent: agentPub})

	// https:// and wss:// both name the same endpoint.
	for _, scheme := range []string{"https://", "wss://"} {
		t.Run(scheme, func(t *testing.T) {
			before := len(cp.beatsSeen())
			_, stop := runConnect(t, dir, scheme+cp.addr, ConnectOptions{AgentVersion: testVersion, HeartbeatInterval: 20 * time.Millisecond, Sleep: fastSleep})
			waitFor(t, "two heartbeats on a TLS 1.3 connection", func() bool { return len(cp.beatsSeen()) >= before+2 })
			_ = stop()

			cp.mu.Lock()
			defer cp.mu.Unlock()
			if len(cp.versions) == 0 || cp.versions[len(cp.versions)-1] != tls.VersionTLS13 {
				t.Errorf("negotiated TLS versions: expected last to be %#x, got %#x", tls.VersionTLS13, cp.versions)
			}
			if len(cp.peers) == 0 || !cp.peers[len(cp.peers)-1].Equal(agentPub) {
				t.Errorf("client certificate key: expected enrolled key %x, got %x", []byte(agentPub), cp.peers)
			}
		})
	}

	src, err := os.ReadFile("connect.go")
	if err != nil {
		t.Fatalf("read connect.go: unexpected error %v", err)
	}
	for _, banned := range []string{"net.Listen", "ListenAndServe", "http.Server"} {
		if bytes.Contains(src, []byte(banned)) {
			t.Errorf("connect.go: expected no inbound listener (%q found); the agent only dials out", banned)
		}
	}
}

func TestHeartbeatSentOnInterval(t *testing.T) {
	pin, pinPriv := genKey(t)
	dir, agentPub := writeAgent(t, pin)
	cp := newFakeCP(t, cpOpts{priv: pinPriv, agent: agentPub})

	start := time.Now()
	_, stop := runConnect(t, dir, "https://"+cp.addr, ConnectOptions{AgentVersion: testVersion, HeartbeatInterval: 20 * time.Millisecond, Sleep: fastSleep})
	waitFor(t, "three heartbeats", func() bool { return len(cp.beatsSeen()) >= 3 })
	_ = stop()

	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("elapsed: expected well under the 30s production interval, got %v", elapsed)
	}
	seen := map[string]bool{}
	for i, b := range cp.beatsSeen() {
		if b.version != testVersion {
			t.Errorf("heartbeat %d agent_version: expected %q, got %q", i, testVersion, b.version)
		}
		if b.id == "" || seen[b.id] {
			t.Errorf("heartbeat %d request id: expected a distinct non-empty id, got %q (seen %v)", i, b.id, seen)
		}
		seen[b.id] = true
	}
}

func TestReconnectBackoffWithJitter(t *testing.T) {
	t.Run("delay bounds per attempt", func(t *testing.T) {
		tests := []struct {
			attempt int
			max     time.Duration
		}{
			{0, 1 * time.Second}, {1, 2 * time.Second}, {2, 4 * time.Second},
			{3, 8 * time.Second}, {4, 16 * time.Second}, {5, 30 * time.Second},
			{6, 30 * time.Second}, {8, 30 * time.Second}, {40, 30 * time.Second},
		}
		for _, tt := range tests {
			r := seeded(1)
			for range 50 {
				got := backoffDelay(tt.attempt, r)
				if got < 0 || got > tt.max {
					t.Fatalf("attempt %d: expected delay in [0, %v], got %v", tt.attempt, tt.max, got)
				}
			}
		}
	})

	t.Run("seeded sequence is deterministic and jittered", func(t *testing.T) {
		seq := func() []time.Duration {
			r := seeded(42)
			var out []time.Duration
			for i := range 10 {
				out = append(out, backoffDelay(i, r))
			}
			return out
		}
		a, b := seq(), seq()
		if !reflect.DeepEqual(a, b) {
			t.Errorf("same seed: expected identical delays, got %v then %v", a, b)
		}
		distinct := map[time.Duration]bool{}
		for _, d := range a {
			distinct[d] = true
		}
		if len(distinct) < 2 {
			t.Errorf("jitter: expected varying delays, got %v", a)
		}
	})

	t.Run("dial failures grow the bound", func(t *testing.T) {
		pin, _ := genKey(t)
		dir, _ := writeAgent(t, pin)
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("net.Listen: unexpected error %v", err)
		}
		addr := ln.Addr().String()
		_ = ln.Close() // nothing listens here: dials are refused

		var got []time.Duration
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		sleep := func(ctx context.Context, d time.Duration) error {
			got = append(got, d)
			if len(got) == 6 {
				cancel()
			}
			return ctx.Err()
		}
		_ = Connect(ctx, dir, "https://"+addr, ConnectOptions{AgentVersion: testVersion, Rand: seeded(7), Sleep: sleep})

		if len(got) != 6 {
			t.Fatalf("sleeps: expected 6, got %d (%v)", len(got), got)
		}
		want := seeded(7)
		for i, d := range got {
			if exp := backoffDelay(i, want); d != exp {
				t.Errorf("delay %d: expected %v (attempt %d, seeded), got %v", i, exp, i, d)
			}
		}
	})

	t.Run("attempt resets after a completed heartbeat", func(t *testing.T) {
		pin, pinPriv := genKey(t)
		dir, agentPub := writeAgent(t, pin)
		cp := newFakeCP(t, cpOpts{priv: pinPriv, agent: agentPub, handle: dropAfterFirstAck})

		var got []time.Duration
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		sleep := func(ctx context.Context, d time.Duration) error {
			got = append(got, d)
			if len(got) == 4 {
				cancel()
			}
			return ctx.Err()
		}
		_ = Connect(ctx, dir, "https://"+cp.addr, ConnectOptions{AgentVersion: testVersion, Rand: seeded(9), Sleep: sleep})

		if len(got) != 4 {
			t.Fatalf("sleeps: expected 4, got %d (%v)", len(got), got)
		}
		want := seeded(9)
		for i, d := range got {
			if exp := backoffDelay(0, want); d != exp {
				t.Errorf("delay %d: expected %v (attempt 0 after an acked heartbeat), got %v", i, exp, d)
			}
		}
	})
}

func TestServerOfflineThenRecovers(t *testing.T) {
	ctx := context.Background()
	cpDir := t.TempDir()
	st, err := sqlite.Open(ctx, filepath.Join(cpDir, "server.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: unexpected error %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	org, err := st.CreateOrg(ctx)
	if err != nil {
		t.Fatalf("CreateOrg: unexpected error %v", err)
	}
	s, err := server.New(st, cpDir, time.Now)
	if err != nil {
		t.Fatalf("server.New: unexpected error %v", err)
	}
	enrolSrv := httptest.NewServer(s.Handler())
	t.Cleanup(enrolSrv.Close)
	token, err := s.IssueEnrolToken(ctx, org)
	if err != nil {
		t.Fatalf("IssueEnrolToken: unexpected error %v", err)
	}
	agentDir := t.TempDir()
	if _, err := Enroll(ctx, http.DefaultClient, agentDir, enrolSrv.URL, token); err != nil {
		t.Fatalf("Enroll: unexpected error %v", err)
	}
	before := snapshot(t, agentDir)

	serve := func(addr string) (bound string, writes *atomic.Int64, stop func()) {
		cfg, err := s.AgentTLSConfig()
		if err != nil {
			t.Fatalf("AgentTLSConfig: unexpected error %v", err)
		}
		raw, err := net.Listen("tcp", addr)
		if err != nil {
			t.Fatalf("net.Listen %s: unexpected error %v", addr, err)
		}
		writes = new(atomic.Int64)
		tl := &countLn{Listener: tls.NewListener(raw, cfg), writes: writes}
		srv := &http.Server{Handler: s.AgentHandler(), ReadHeaderTimeout: 5 * time.Second}
		go func() { _ = srv.Serve(tl) }()
		return raw.Addr().String(), writes, func() {
			_ = srv.Close()
			_ = tl.Close()
			tl.closeAll()
		}
	}

	addr, writes1, stop1 := serve("127.0.0.1:0")
	_, stop := runConnect(t, agentDir, "https://"+addr, ConnectOptions{
		AgentVersion: testVersion, HeartbeatInterval: 20 * time.Millisecond, Sleep: fastSleep,
	})
	// One write is the 101 upgrade; each further write is an acked heartbeat.
	waitFor(t, "the first heartbeat ack", func() bool { return writes1.Load() >= 2 })

	stop1()
	_, writes2, stop2 := serve(addr)
	defer stop2()
	waitFor(t, "an ack after the server restarted", func() bool { return writes2.Load() >= 2 })
	_ = stop()

	after := snapshot(t, agentDir)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("enrolment files changed across the outage: before %d files, after %d files, equal=false", len(before), len(after))
	}
	servers, err := st.Servers(ctx, org)
	if err != nil {
		t.Fatalf("Servers: unexpected error %v", err)
	}
	if len(servers) != 1 {
		t.Errorf("registered servers: expected 1 (no re-enrolment), got %d", len(servers))
	}
}

func TestRejectsControlPlanePinMismatch(t *testing.T) {
	pin, _ := genKey(t)
	dir, agentPub := writeAgent(t, pin)
	before := snapshot(t, dir)
	_, otherPriv := genKey(t) // a valid TLS 1.3 server, but not the pinned key
	cp := newFakeCP(t, cpOpts{priv: otherPriv, agent: agentPub})

	runOnce(t, dir, "https://"+cp.addr)

	if n := len(cp.beatsSeen()); n != 0 {
		t.Errorf("heartbeats reaching the impostor: expected 0, got %d", n)
	}
	if n := cp.count(&cp.wsConns); n != 0 {
		t.Errorf("WebSocket upgrades on the impostor: expected 0, got %d", n)
	}
	if n := cp.count(&cp.accepted); n == 0 {
		t.Errorf("TCP connections to the impostor: expected the agent to dial at least once, got 0")
	}
	assertFilesUnchanged(t, dir, before)
}

func TestAgentRejectsTLS12Server(t *testing.T) {
	pin, pinPriv := genKey(t)
	dir, agentPub := writeAgent(t, pin)
	before := snapshot(t, dir)
	cp := newFakeCP(t, cpOpts{priv: pinPriv, agent: agentPub, maxVersion: tls.VersionTLS12})

	runOnce(t, dir, "https://"+cp.addr)

	if n := len(cp.beatsSeen()); n != 0 {
		t.Errorf("heartbeats on a TLS 1.2 server: expected 0, got %d", n)
	}
	if n := cp.count(&cp.wsConns); n != 0 {
		t.Errorf("WebSocket upgrades on a TLS 1.2 server: expected 0, got %d", n)
	}
	if n := cp.count(&cp.accepted); n == 0 {
		t.Errorf("TCP connections to the TLS 1.2 server: expected the agent to dial at least once, got 0")
	}
	assertFilesUnchanged(t, dir, before)
}

// runOnce lets Connect make one attempt: the first backoff sleep cancels it.
func runOnce(t *testing.T, dir, url string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sleep := func(ctx context.Context, d time.Duration) error {
		cancel()
		return ctx.Err()
	}
	done := make(chan error, 1)
	go func() {
		done <- Connect(ctx, dir, url, ConnectOptions{AgentVersion: testVersion, Sleep: sleep})
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("Connect: expected one failed attempt and a return, still running after 10s")
	}
}

func assertFilesUnchanged(t *testing.T, dir string, before map[string]string) {
	t.Helper()
	if after := snapshot(t, dir); !reflect.DeepEqual(before, after) {
		t.Errorf("data dir files: expected byte-identical to before, got %d files vs %d", len(after), len(before))
	}
	for _, name := range []string{signingFile, agreeFile, pinFile, fingerFile} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("stat %s: unexpected error %v", name, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode: expected 0600, got %v", name, info.Mode().Perm())
		}
	}
}

func TestAgentClosesOnBadFrame(t *testing.T) {
	tests := []struct {
		name  string
		reply func(t *testing.T, id string) (websocket.MessageType, []byte)
	}{
		{"text message", func(t *testing.T, id string) (websocket.MessageType, []byte) {
			return websocket.MessageText, []byte("hello")
		}},
		{"oversize message", func(t *testing.T, id string) (websocket.MessageType, []byte) {
			return websocket.MessageBinary, bytes.Repeat([]byte{1}, protocol.MaxEnvelopeSize+1)
		}},
		{"garbage binary message", func(t *testing.T, id string) (websocket.MessageType, []byte) {
			return websocket.MessageBinary, []byte("not an envelope")
		}},
		{"ack for another request id", func(t *testing.T, id string) (websocket.MessageType, []byte) {
			return websocket.MessageBinary, ackBytes(t, id+"-other")
		}},
		{"envelope that is not an ack", func(t *testing.T, id string) (websocket.MessageType, []byte) {
			return websocket.MessageBinary, mustMarshal(t, agentv1.MessageType_HEARTBEAT, id, &agentv1.Heartbeat{AgentVersion: "x"})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pin, pinPriv := genKey(t)
			dir, agentPub := writeAgent(t, pin)
			handle := func(ctx context.Context, f *fakeCP, c *websocket.Conn) {
				c.SetReadLimit(protocol.MaxEnvelopeSize * 2)
				id, err := f.readBeat(ctx, c)
				if err != nil {
					return
				}
				typ, data := tt.reply(f.t, id)
				_ = c.Write(ctx, typ, data)
				// The agent must hang up: this read ends with the connection.
				_, _, _ = c.Read(ctx)
				f.mu.Lock()
				f.closed++
				f.mu.Unlock()
			}
			cp := newFakeCP(t, cpOpts{priv: pinPriv, agent: agentPub, handle: handle})

			_, stop := runConnect(t, dir, "https://"+cp.addr, ConnectOptions{AgentVersion: testVersion, HeartbeatInterval: time.Hour, Sleep: fastSleep})
			waitFor(t, "a second connection after the bad frame", func() bool { return cp.count(&cp.wsConns) >= 2 })
			_ = stop()

			if n := cp.count(&cp.closed); n < 1 {
				t.Errorf("connections closed by the agent after a %s: expected at least 1, got %d", tt.name, n)
			}
		})
	}
}

func TestDialTimesOutOnSilentServer(t *testing.T) {
	if dialTimeout > 15*time.Second {
		t.Errorf("dialTimeout: expected at most 15s, got %v", dialTimeout)
	}
	old := dialTimeout
	dialTimeout = 300 * time.Millisecond
	t.Cleanup(func() { dialTimeout = old })

	pin, _ := genKey(t)
	dir, _ := writeAgent(t, pin)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: unexpected error %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() }) // held open, never read or written
		}
	}()

	runOnce(t, dir, "https://"+ln.Addr().String())
}

func TestAgentRefusesRedirect(t *testing.T) {
	pin, pinPriv := genKey(t)
	dir, agentPub := writeAgent(t, pin)

	var plainHits atomic.Int64
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plainHits.Add(1)
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.CloseNow() }()
		for {
			if _, _, err := c.Read(r.Context()); err != nil {
				return
			}
		}
	}))
	t.Cleanup(plain.Close)
	other := newFakeCP(t, cpOpts{priv: pinPriv, agent: agentPub})

	tests := []struct {
		name   string
		target string
		check  func(t *testing.T)
	}{
		{"plaintext http", plain.URL + "/v1/agent", func(t *testing.T) {
			if n := plainHits.Load(); n != 0 {
				t.Errorf("requests to the plaintext server: expected 0, got %d", n)
			}
		}},
		{"other https host", "https://" + other.addr + "/v1/agent", func(t *testing.T) {
			if n := other.count(&other.accepted); n != 0 {
				t.Errorf("connections to the other host: expected 0, got %d", n)
			}
			if n := len(other.beatsSeen()); n != 0 {
				t.Errorf("heartbeats to the other host: expected 0, got %d", n)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cp := newFakeCP(t, cpOpts{priv: pinPriv, agent: agentPub, redirect: tt.target})
			runOnce(t, dir, "https://"+cp.addr)
			if n := cp.count(&cp.wsConns); n != 0 {
				t.Errorf("WebSocket upgrades: expected 0, got %d", n)
			}
			tt.check(t)
		})
	}
}

func TestAgentNoticesDropBetweenHeartbeats(t *testing.T) {
	pin, pinPriv := genKey(t)
	dir, agentPub := writeAgent(t, pin)
	cp := newFakeCP(t, cpOpts{priv: pinPriv, agent: agentPub, handle: dropAfterFirstAck})

	start := time.Now()
	_, stop := runConnect(t, dir, "https://"+cp.addr, ConnectOptions{AgentVersion: testVersion, HeartbeatInterval: time.Hour, Sleep: fastSleep})
	waitFor(t, "a second connection", func() bool { return cp.count(&cp.wsConns) >= 2 })
	_ = stop()
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("reconnect after a server close: expected under 2s, got %v", elapsed)
	}
}

// countLn counts the writes the server makes on each accepted connection
// (above TLS): one for the 101 upgrade, then one per frame it sends.
type countLn struct {
	net.Listener
	writes *atomic.Int64
	mu     sync.Mutex
	conns  []net.Conn
}

type countConn struct {
	net.Conn
	writes *atomic.Int64
}

func (c countConn) Write(b []byte) (int, error) {
	c.writes.Add(1)
	return c.Conn.Write(b)
}

func (l *countLn) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	l.conns = append(l.conns, c)
	l.mu.Unlock()
	return countConn{Conn: c, writes: l.writes}, nil
}

func (l *countLn) closeAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range l.conns {
		_ = c.Close()
	}
}
