package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"

	"moonglass.dev/moonglass/internal/keys"
	"moonglass.dev/moonglass/internal/protocol"
	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

// startAgents serves the agent endpoint on a loopback port and returns its address.
func startAgents(t *testing.T, f *fixture) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: unexpected error %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- f.srv.ServeAgents(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Errorf("ServeAgents: expected it to return after ctx cancel, still running after 10s")
		}
	})
	return ln.Addr().String()
}

func (f *fixture) controlPlanePub(t *testing.T) ed25519.PublicKey {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.dir, controlPlaneKeyFile))
	if err != nil {
		t.Fatalf("read control-plane key: unexpected error %v", err)
	}
	return ed25519.PrivateKey(raw).Public().(ed25519.PublicKey)
}

// enrolled adds a client key to the store and returns its private key.
func (f *fixture) enrolled(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	pub, priv := mustKey(t)
	if _, err := f.st.AddServer(context.Background(), f.org, hex.EncodeToString(pub), "age1"+hex.EncodeToString(pub)); err != nil {
		t.Fatalf("AddServer: unexpected error %v", err)
	}
	return priv
}

func mustKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: unexpected error %v", err)
	}
	return pub, priv
}

func dialAgent(ctx context.Context, f *fixture, t *testing.T, addr string, priv ed25519.PrivateKey) (*websocket.Conn, error) {
	t.Helper()
	cfg, err := keys.ClientTLSConfig(priv, f.controlPlanePub(t))
	if err != nil {
		t.Fatalf("ClientTLSConfig: unexpected error %v", err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}}
	t.Cleanup(client.CloseIdleConnections)
	c, _, err := websocket.Dial(ctx, "wss://"+addr+agentPath, &websocket.DialOptions{HTTPClient: client})
	return c, err
}

func heartbeatFrame(t *testing.T, id string) []byte {
	t.Helper()
	env, err := protocol.Wrap(agentv1.MessageType_HEARTBEAT, id, &agentv1.Heartbeat{AgentVersion: "test"})
	if err != nil {
		t.Fatalf("protocol.Wrap: unexpected error %v", err)
	}
	b, err := protocol.Marshal(env)
	if err != nil {
		t.Fatalf("protocol.Marshal: unexpected error %v", err)
	}
	return b
}

func ackFrame(t *testing.T, id string) []byte {
	t.Helper()
	env, err := protocol.Wrap(agentv1.MessageType_HEARTBEAT_ACK, id, &agentv1.HeartbeatAck{})
	if err != nil {
		t.Fatalf("protocol.Wrap: unexpected error %v", err)
	}
	b, err := protocol.Marshal(env)
	if err != nil {
		t.Fatalf("protocol.Marshal: unexpected error %v", err)
	}
	return b
}

// exchange sends one Heartbeat and returns the error from the exchange, or
// nil when a HeartbeatAck with the same request id came back.
func exchange(ctx context.Context, t *testing.T, c *websocket.Conn, id string) error {
	t.Helper()
	if err := c.Write(ctx, websocket.MessageBinary, heartbeatFrame(t, id)); err != nil {
		return err
	}
	typ, data, err := c.Read(ctx)
	if err != nil {
		return err
	}
	if typ != websocket.MessageBinary {
		t.Errorf("reply message type: expected binary, got %v", typ)
	}
	env, err := protocol.Unmarshal(data)
	if err != nil {
		t.Fatalf("protocol.Unmarshal reply: unexpected error %v", err)
	}
	if env.GetType() != agentv1.MessageType_HEARTBEAT_ACK || env.GetRequestId() != id {
		t.Errorf("reply: expected HEARTBEAT_ACK with request id %q, got %v with %q", id, env.GetType(), env.GetRequestId())
	}
	return nil
}

func TestRejectsUnknownAgent(t *testing.T) {
	f := newFixture(t)
	addr := startAgents(t, f)
	known := f.enrolled(t)
	_, unknown := mustKey(t)

	tests := []struct {
		name    string
		priv    ed25519.PrivateKey
		wantAck bool
	}{
		{"enrolled key", known, true},
		{"key never added to the store", unknown, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			c, err := dialAgent(ctx, f, t, addr, tt.priv)
			if err == nil {
				defer func() { _ = c.CloseNow() }()
				err = exchange(ctx, t, c, "r1")
			}
			if tt.wantAck && err != nil {
				t.Errorf("exchange: expected a HeartbeatAck, got error %v", err)
			}
			if !tt.wantAck && err == nil {
				t.Errorf("exchange: expected the dial or first read to fail, got a HeartbeatAck")
			}
		})
	}
}

func TestRejectsTLSBelow13(t *testing.T) {
	f := newFixture(t)
	addr := startAgents(t, f)

	cfg, err := f.srv.AgentTLSConfig()
	if err != nil {
		t.Fatalf("AgentTLSConfig: unexpected error %v", err)
	}
	if cfg.MinVersion != tls.VersionTLS13 {
		t.Errorf("MinVersion: expected %#x, got %#x", tls.VersionTLS13, cfg.MinVersion)
	}
	if cfg.ClientAuth != tls.RequireAnyClientCert {
		t.Errorf("ClientAuth: expected %v, got %v", tls.RequireAnyClientCert, cfg.ClientAuth)
	}

	priv := f.enrolled(t)
	cert, err := keys.SelfSignedCert(priv)
	if err != nil {
		t.Fatalf("SelfSignedCert: unexpected error %v", err)
	}
	tests := []struct {
		name    string
		max     uint16
		wantErr bool
	}{
		{"TLS 1.2 client", tls.VersionTLS12, true},
		{"TLS 1.1 client", tls.VersionTLS11, true},
		{"TLS 1.3 client", tls.VersionTLS13, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := tls.Dial("tcp", addr, &tls.Config{
				Certificates:       []tls.Certificate{cert},
				MaxVersion:         tt.max,
				InsecureSkipVerify: true, //nolint:gosec // test client; the server certificate is a key carrier
			})
			if err == nil {
				_ = conn.Close()
			}
			if (err != nil) != tt.wantErr {
				t.Errorf("tls.Dial with max version %#x: expected error=%v, got %v", tt.max, tt.wantErr, err)
			}
		})
	}
}

func TestServerClosesOnBadAgentFrame(t *testing.T) {
	f := newFixture(t)
	addr := startAgents(t, f)
	priv := f.enrolled(t)

	tests := []struct {
		name string
		typ  websocket.MessageType
		data []byte
	}{
		{"text frame", websocket.MessageText, []byte("hello")},
		{"oversize frame", websocket.MessageBinary, bytes.Repeat([]byte{1}, protocol.MaxEnvelopeSize+1)},
		{"garbage binary frame", websocket.MessageBinary, []byte("not an envelope")},
		{"non-heartbeat envelope", websocket.MessageBinary, ackFrame(t, "r1")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			c, err := dialAgent(ctx, f, t, addr, priv)
			if err != nil {
				t.Fatalf("dial: unexpected error %v", err)
			}
			defer func() { _ = c.CloseNow() }()
			c.SetReadLimit(protocol.MaxEnvelopeSize * 2)
			_ = c.Write(ctx, tt.typ, tt.data)
			typ, data, err := c.Read(ctx)
			if err == nil {
				t.Errorf("read after bad frame: expected the connection to close, got message type %v of %d bytes", typ, len(data))
			}
		})
	}
}
