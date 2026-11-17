package keys

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestGenerateAgentIdentity(t *testing.T) {
	a, err := GenerateAgentIdentity()
	if err != nil {
		t.Fatalf("GenerateAgentIdentity: unexpected error %v", err)
	}
	b, err := GenerateAgentIdentity()
	if err != nil {
		t.Fatalf("GenerateAgentIdentity (second call): unexpected error %v", err)
	}

	tests := []struct {
		name string
		got  int
		want int
	}{
		{"ed25519 public length", len(a.SigningPublic), ed25519.PublicKeySize},
		{"ed25519 private length", len(a.SigningPrivate), ed25519.PrivateKeySize},
		{"x25519 public length", len(a.AgreePublic), 32},
		{"x25519 private length", len(a.AgreePrivate), 32},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s: expected %d, got %d", tt.name, tt.want, tt.got)
			}
		})
	}

	if bytes.Equal(a.SigningPrivate, a.SigningPublic) {
		t.Errorf("ed25519 private key equals public key %x", a.SigningPublic)
	}
	if bytes.Equal(a.AgreePrivate, a.AgreePublic) {
		t.Errorf("x25519 private key equals public key %x", a.AgreePublic)
	}
	if bytes.Equal(a.SigningPublic, b.SigningPublic) {
		t.Errorf("two identities share signing public key %x", a.SigningPublic)
	}
	if bytes.Equal(a.AgreePublic, b.AgreePublic) {
		t.Errorf("two identities share agree public key %x", a.AgreePublic)
	}
}

func TestFingerprintIsSHA256OfSigningKey(t *testing.T) {
	fixed := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize)).Public().(ed25519.PublicKey)
	id, err := GenerateAgentIdentity()
	if err != nil {
		t.Fatalf("GenerateAgentIdentity: unexpected error %v", err)
	}

	tests := []struct {
		name string
		pub  ed25519.PublicKey
	}{
		{"fixed key", fixed},
		{"generated key", id.SigningPublic},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sum := sha256.Sum256(tt.pub)
			want := hex.EncodeToString(sum[:])

			got, err := Fingerprint(tt.pub)
			if err != nil {
				t.Fatalf("Fingerprint: unexpected error %v", err)
			}
			if got != want {
				t.Errorf("fingerprint: expected %q, got %q", want, got)
			}
			if len(got) != 64 {
				t.Errorf("fingerprint length: expected 64, got %d", len(got))
			}
			if got != strings.ToLower(got) {
				t.Errorf("fingerprint: expected lowercase hex, got %q", got)
			}
		})
	}

	for _, n := range []int{0, 31, 33} {
		if got, err := Fingerprint(make(ed25519.PublicKey, n)); err == nil {
			t.Errorf("Fingerprint of %d-byte key: expected an error, got %q", n, got)
		}
	}
}
