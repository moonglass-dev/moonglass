package keys

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// certLifetime is how long a carrier certificate claims to be valid. Nothing
// trusts it: peers compare the raw public key, never the subject or expiry.
const certLifetime = 24 * time.Hour

// SelfSignedCert returns a short-lived self-signed x509 certificate for priv.
// Go has no raw-public-key TLS, so the certificate is only a carrier for the
// Ed25519 identity; TLS 1.3 CertificateVerify proves possession of priv.
func SelfSignedCert(priv ed25519.PrivateKey) (tls.Certificate, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return tls.Certificate{}, fmt.Errorf("tls certificate: private key is %d bytes, expected %d", len(priv), ed25519.PrivateKeySize)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("tls certificate: generate serial: %w", err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "moonglass"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(certLifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, priv.Public(), priv)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("tls certificate: sign: %w", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}, nil
}

// PeerPublicKey returns the Ed25519 public key of the leaf certificate in
// rawCerts. It rejects an empty chain, an unparsable leaf and any other key type.
func PeerPublicKey(rawCerts [][]byte) (ed25519.PublicKey, error) {
	if len(rawCerts) == 0 {
		return nil, errors.New("tls peer sent no certificate")
	}
	leaf, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return nil, fmt.Errorf("tls peer certificate is not valid x509: %w", err)
	}
	pub, ok := leaf.PublicKey.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("tls peer key is %T, expected an Ed25519 key", leaf.PublicKey)
	}
	return pub, nil
}

// ClientTLSConfig returns a TLS 1.3 client config that presents priv as its
// identity and accepts only a server whose Ed25519 key equals pin.
func ClientTLSConfig(priv ed25519.PrivateKey, pin ed25519.PublicKey) (*tls.Config, error) {
	cert, err := SelfSignedCert(priv)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
		// The server certificate is a key carrier, not a CA-issued identity:
		// the chain, name and expiry are ignored and VerifyPeerCertificate
		// compares the leaf key to the pin instead.
		InsecureSkipVerify: true, //nolint:gosec // G402: replaced by the pin comparison in VerifyPeerCertificate
		// No ClientSessionCache is set, so this client never resumes a session
		// and VerifyPeerCertificate runs on every handshake.
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error { //nolint:gosec // G123: no session cache, so there is no resumption to bypass the pin
			got, err := PeerPublicKey(rawCerts)
			if err != nil {
				return err
			}
			if !got.Equal(pin) {
				return errors.New("control plane key does not match the pinned key: refusing to connect")
			}
			return nil
		},
	}, nil
}

// ServerTLSConfig returns a TLS 1.3 server config that presents priv and
// requires a client certificate. authorize decides whether the client's
// Ed25519 key is allowed; a non-nil error fails the handshake.
func ServerTLSConfig(priv ed25519.PrivateKey, authorize func(ed25519.PublicKey) error) (*tls.Config, error) {
	cert, err := SelfSignedCert(priv)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
		ClientAuth:   tls.RequireAnyClientCert,
		// Resumed sessions would skip VerifyPeerCertificate, so none are issued.
		SessionTicketsDisabled: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			got, err := PeerPublicKey(rawCerts)
			if err != nil {
				return err
			}
			return authorize(got)
		},
	}, nil
}
