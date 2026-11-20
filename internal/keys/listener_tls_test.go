package keys

import (
	"crypto/tls"
	"net"
	"strings"
	"testing"
	"time"
)

// handshakeResult is both sides of one TLS handshake: the state each side saw
// and the error each side returned. An expected failure is data, not fatal.
type handshakeResult struct {
	serverState tls.ConnectionState
	clientState tls.ConnectionState
	serverErr   error
	clientErr   error
}

// handshake runs one handshake between serverCfg and clientCfg over a loopback
// listener and returns both sides. Deadlines keep a stuck handshake from
// hanging the test.
func handshake(t *testing.T, serverCfg, clientCfg *tls.Config) handshakeResult {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: unexpected error %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	server := make(chan handshakeResult, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			server <- handshakeResult{serverErr: err}
			return
		}
		defer func() { _ = c.Close() }()
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		tc := tls.Server(c, serverCfg)
		err = tc.Handshake()
		server <- handshakeResult{serverState: tc.ConnectionState(), serverErr: err}
	}()

	conn, err := tls.Dial("tcp", ln.Addr().String(), clientCfg)
	var clientState tls.ConnectionState
	if err == nil {
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		clientState = conn.ConnectionState()
		_ = conn.Close()
	}
	got := <-server
	got.clientState, got.clientErr = clientState, err
	return got
}

func TestListenerTLSConfigNeedsNoClientCert(t *testing.T) {
	pub, priv := newKey(t)
	server, err := ListenerTLSConfig(priv)
	if err != nil {
		t.Fatalf("ListenerTLSConfig: unexpected error %v", err)
	}
	if server.MinVersion != tls.VersionTLS13 {
		t.Errorf("listener MinVersion: expected %#x, got %#x", tls.VersionTLS13, server.MinVersion)
	}
	if !server.SessionTicketsDisabled {
		t.Errorf("listener SessionTicketsDisabled: expected true, got false: tickets would let a resumed session skip the key check")
	}
	if server.ClientAuth != tls.NoClientCert {
		t.Errorf("listener ClientAuth: expected %v, got %v", tls.NoClientCert, server.ClientAuth)
	}

	fp, err := Fingerprint(pub)
	if err != nil {
		t.Fatalf("Fingerprint: unexpected error %v", err)
	}
	res := handshake(t, server, EnrolClientTLSConfig(fp))
	if res.clientErr != nil {
		t.Fatalf("handshake with no client certificate: unexpected client error %v", res.clientErr)
	}
	if res.serverErr != nil {
		t.Fatalf("handshake with no client certificate: unexpected server error %v", res.serverErr)
	}
	if res.clientState.Version != tls.VersionTLS13 {
		t.Errorf("negotiated version: expected %#x (TLS 1.3), got %#x", tls.VersionTLS13, res.clientState.Version)
	}
	if n := len(res.serverState.PeerCertificates); n != 0 {
		t.Errorf("server peer certificates: expected none (no client certificate), got %d", n)
	}
	if n := len(res.clientState.PeerCertificates); n != 1 {
		t.Fatalf("client peer certificates: expected exactly 1, got %d", n)
	}
	leaf, err := PeerPublicKey([][]byte{res.clientState.PeerCertificates[0].Raw})
	if err != nil {
		t.Fatalf("PeerPublicKey: unexpected error %v", err)
	}
	if !leaf.Equal(pub) {
		t.Errorf("leaf public key: expected %x (%s), got %x", []byte(pub), fp, []byte(leaf))
	}

	// A client limited to TLS 1.2 must not complete the handshake.
	old := EnrolClientTLSConfig(fp)
	old.MinVersion, old.MaxVersion = tls.VersionTLS12, tls.VersionTLS12
	res = handshake(t, server, old)
	if res.clientErr == nil {
		t.Errorf("handshake from a TLS 1.2-only client: expected an error, got version %#x", res.clientState.Version)
	}
}

func TestEnrolClientTLSConfigChecksFingerprint(t *testing.T) {
	config := EnrolClientTLSConfig("")
	if config.MinVersion != tls.VersionTLS13 {
		t.Errorf("client MinVersion: expected %#x, got %#x", tls.VersionTLS13, config.MinVersion)
	}
	if !config.InsecureSkipVerify {
		t.Errorf("client InsecureSkipVerify: expected true (the fingerprint replaces chain verification), got false")
	}
	if config.ClientSessionCache != nil {
		t.Errorf("client ClientSessionCache: expected none (resumption would skip VerifyPeerCertificate), got %v", config.ClientSessionCache)
	}

	pub, priv := newKey(t)
	fp, err := Fingerprint(pub)
	if err != nil {
		t.Fatalf("Fingerprint: unexpected error %v", err)
	}
	// Uppercase must be a different string, so the case really tests the match.
	for strings.ToUpper(fp) == fp {
		pub, priv = newKey(t)
		if fp, err = Fingerprint(pub); err != nil {
			t.Fatalf("Fingerprint: unexpected error %v", err)
		}
	}
	otherPub, _ := newKey(t)
	otherFP, err := Fingerprint(otherPub)
	if err != nil {
		t.Fatalf("Fingerprint(other key): unexpected error %v", err)
	}

	server, err := ListenerTLSConfig(priv)
	if err != nil {
		t.Fatalf("ListenerTLSConfig: unexpected error %v", err)
	}
	server12 := server.Clone()
	server12.MinVersion, server12.MaxVersion = tls.VersionTLS12, tls.VersionTLS12

	tests := []struct {
		name        string
		server      *tls.Config
		fingerprint string
		wantErr     bool
	}{
		{"matching fingerprint", server, fp, false},
		{"fingerprint of another key", server, otherFP, true},
		{"empty fingerprint", server, "", true},
		{"uppercase fingerprint", server, strings.ToUpper(fp), true},
		{"TLS 1.2-only server", server12, fp, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := handshake(t, tt.server, EnrolClientTLSConfig(tt.fingerprint))

			if (res.clientErr != nil) != tt.wantErr {
				t.Errorf("handshake with fingerprint %q: expected error=%v, got %v", tt.fingerprint, tt.wantErr, res.clientErr)
			}
			if res.clientErr == nil && res.clientState.Version != tls.VersionTLS13 {
				t.Errorf("negotiated version: expected %#x (TLS 1.3), got %#x", tls.VersionTLS13, res.clientState.Version)
			}
		})
	}
}
