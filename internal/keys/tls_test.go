package keys

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"testing"
)

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: unexpected error %v", err)
	}
	return pub, priv
}

func TestAgentTLSCertCarriesKey(t *testing.T) {
	pub, priv := newKey(t)
	cert, err := SelfSignedCert(priv)
	if err != nil {
		t.Fatalf("SelfSignedCert: unexpected error %v", err)
	}
	if len(cert.Certificate) != 1 {
		t.Fatalf("chain length: expected 1, got %d", len(cert.Certificate))
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("ParseCertificate: unexpected error %v", err)
	}
	got, ok := leaf.PublicKey.(ed25519.PublicKey)
	if !ok || !got.Equal(pub) {
		t.Errorf("leaf public key: expected %x, got %x (ok=%v)", []byte(pub), leaf.PublicKey, ok)
	}

	t.Run("PeerPublicKey returns the key", func(t *testing.T) {
		peer, err := PeerPublicKey(cert.Certificate)
		if err != nil {
			t.Fatalf("PeerPublicKey: unexpected error %v", err)
		}
		if !peer.Equal(pub) {
			t.Errorf("PeerPublicKey: expected %x, got %x", []byte(pub), []byte(peer))
		}
	})

	t.Run("PeerPublicKey rejects bad input", func(t *testing.T) {
		tests := []struct {
			name string
			raw  [][]byte
		}{
			{"empty chain", nil},
			{"garbage certificate", [][]byte{[]byte("not a certificate")}},
		}
		for _, tt := range tests {
			if k, err := PeerPublicKey(tt.raw); err == nil {
				t.Errorf("%s: expected an error, got key %x", tt.name, []byte(k))
			}
		}
	})
}

func TestTLSConfigsPinTLS13(t *testing.T) {
	pub, priv := newKey(t)
	otherPub, _ := newKey(t)

	server, err := ServerTLSConfig(priv, func(ed25519.PublicKey) error { return nil })
	if err != nil {
		t.Fatalf("ServerTLSConfig: unexpected error %v", err)
	}
	if server.MinVersion != tls.VersionTLS13 {
		t.Errorf("server MinVersion: expected %#x, got %#x", tls.VersionTLS13, server.MinVersion)
	}
	if server.ClientAuth != tls.RequireAnyClientCert {
		t.Errorf("server ClientAuth: expected %v, got %v", tls.RequireAnyClientCert, server.ClientAuth)
	}

	tests := []struct {
		name    string
		pin     ed25519.PublicKey
		wantErr bool
	}{
		{"matching pin", pub, false},
		{"mismatching pin", otherPub, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, clientPriv := newKey(t)
			client, err := ClientTLSConfig(clientPriv, tt.pin)
			if err != nil {
				t.Fatalf("ClientTLSConfig: unexpected error %v", err)
			}
			if client.MinVersion != tls.VersionTLS13 {
				t.Errorf("client MinVersion: expected %#x, got %#x", tls.VersionTLS13, client.MinVersion)
			}
			cert, err := SelfSignedCert(priv)
			if err != nil {
				t.Fatalf("SelfSignedCert: unexpected error %v", err)
			}
			err = client.VerifyPeerCertificate(cert.Certificate, nil)
			if (err != nil) != tt.wantErr {
				t.Errorf("VerifyPeerCertificate: expected error=%v, got %v", tt.wantErr, err)
			}
		})
	}
}
