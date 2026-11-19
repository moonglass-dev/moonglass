package keys

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Agent data-dir files (docs/specs/enrolment.md, "Agent identity"). This package
// is the only reader and writer of them; they are mode 0600.
const (
	fileSigning     = "agent-signing.key"
	fileAgree       = "agent-agree.key"
	filePin         = "control-plane.pub"
	fileFingerprint = "fingerprint"
)

// writeAtomic writes data to dir/name with mode 0600 through a temp file in the
// same directory and a rename over the target.
func writeAtomic(dir, name string, data []byte) error {
	path := filepath.Join(dir, name)
	tmp, err := os.CreateTemp(dir, name+".tmp-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// LoadAgentSigningKey reads and length-checks agent-signing.key.
func LoadAgentSigningKey(dataDir string) (ed25519.PrivateKey, error) {
	path := filepath.Join(dataDir, fileSigning)
	key, err := os.ReadFile(path) //nolint:gosec // G304: dataDir is the operator's own flag, joined with a fixed file name
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%s is %d bytes, expected %d", path, len(key), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(key), nil
}

// LoadAgentIdentity reads the identity saved in dataDir. saved is false when
// agent-signing.key does not exist. It reads only those two files.
func LoadAgentIdentity(dataDir string) (AgentIdentity, bool, error) {
	sk, err := LoadAgentSigningKey(dataDir)
	if errors.Is(err, fs.ErrNotExist) {
		return AgentIdentity{}, false, nil
	}
	if err != nil {
		return AgentIdentity{}, false, err
	}
	agreePath := filepath.Join(dataDir, fileAgree)
	agree, err := os.ReadFile(agreePath) //nolint:gosec // G304: dataDir is the operator's own flag, joined with a fixed file name
	if err != nil {
		return AgentIdentity{}, false, fmt.Errorf("read %s: %w", agreePath, err)
	}
	priv, err := ecdh.X25519().NewPrivateKey(agree)
	if err != nil {
		return AgentIdentity{}, false, fmt.Errorf("%s is not an X25519 private key: %w", agreePath, err)
	}
	return AgentIdentity{
		SigningPublic:  sk.Public().(ed25519.PublicKey),
		SigningPrivate: sk,
		AgreePublic:    priv.PublicKey().Bytes(),
		AgreePrivate:   agree,
	}, true, nil
}

// WriteAgentIdentity writes the raw private keys of id as agent-signing.key and
// agent-agree.key. It writes nothing when a key has the wrong length.
func WriteAgentIdentity(dataDir string, id AgentIdentity) error {
	signingPath := filepath.Join(dataDir, fileSigning)
	if len(id.SigningPrivate) != ed25519.PrivateKeySize {
		return fmt.Errorf("write %s: signing key is %d bytes, expected %d", signingPath, len(id.SigningPrivate), ed25519.PrivateKeySize)
	}
	agreePath := filepath.Join(dataDir, fileAgree)
	if len(id.AgreePrivate) != 32 {
		return fmt.Errorf("write %s: agree key is %d bytes, expected 32", agreePath, len(id.AgreePrivate))
	}
	if err := writeAtomic(dataDir, fileSigning, id.SigningPrivate); err != nil {
		return err
	}
	return writeAtomic(dataDir, fileAgree, id.AgreePrivate)
}

// LoadControlPlanePin reads and length-checks control-plane.pub.
func LoadControlPlanePin(dataDir string) (ed25519.PublicKey, error) {
	path := filepath.Join(dataDir, filePin)
	pin, err := os.ReadFile(path) //nolint:gosec // G304: dataDir is the operator's own flag, joined with a fixed file name
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(pin) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%s is %d bytes, expected %d", path, len(pin), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(pin), nil
}

// WriteControlPlanePin writes the raw control-plane public key as
// control-plane.pub. A wrong-length key is refused before any file changes.
func WriteControlPlanePin(dataDir string, pin ed25519.PublicKey) error {
	path := filepath.Join(dataDir, filePin)
	if len(pin) != ed25519.PublicKeySize {
		return fmt.Errorf("write %s: control-plane key is %d bytes, expected %d", path, len(pin), ed25519.PublicKeySize)
	}
	return writeAtomic(dataDir, filePin, pin)
}

// WriteFingerprint writes the fingerprint and a newline as fingerprint.
func WriteFingerprint(dataDir, fingerprint string) error {
	return writeAtomic(dataDir, fileFingerprint, []byte(fingerprint+"\n"))
}
