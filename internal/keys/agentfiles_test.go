package keys

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

const (
	testSigningFile = "agent-signing.key"
	testAgreeFile   = "agent-agree.key"
	testPinFile     = "control-plane.pub"
	testFingerFile  = "fingerprint"
)

// writeTestKeyFile puts data in name under dir as a plain 0600 file.
func writeTestKeyFile(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		t.Fatalf("write %s: unexpected error %v", name, err)
	}
}

// assertFileMode fails unless dir/name has exactly perm.
func assertFileMode(t *testing.T, dir, name string, perm os.FileMode) {
	t.Helper()
	info, err := os.Stat(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("stat %s: unexpected error %v", name, err)
	}
	if got := info.Mode().Perm(); got != perm {
		t.Errorf("%s mode: expected %o, got %o", name, perm, got)
	}
}

// assertNoTempFiles fails if dir holds a leftover atomic-write temp file.
func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	left, err := filepath.Glob(filepath.Join(dir, "*.tmp-*"))
	if err != nil {
		t.Fatalf("glob temp files: unexpected error %v", err)
	}
	if len(left) != 0 {
		t.Errorf("leftover temp files: expected none, got %v", left)
	}
}

func TestAgentIdentityFilesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	first, err := GenerateAgentIdentity()
	if err != nil {
		t.Fatalf("GenerateAgentIdentity: unexpected error %v", err)
	}

	if err := WriteAgentIdentity(dir, first); err != nil {
		t.Fatalf("WriteAgentIdentity: unexpected error %v", err)
	}
	got, saved, err := LoadAgentIdentity(dir)
	if err != nil {
		t.Fatalf("LoadAgentIdentity: unexpected error %v", err)
	}
	if !saved {
		t.Fatalf("LoadAgentIdentity saved: expected true, got false")
	}

	fields := []struct {
		name string
		got  []byte
		want []byte
	}{
		{"SigningPublic", got.SigningPublic, first.SigningPublic},
		{"SigningPrivate", got.SigningPrivate, first.SigningPrivate},
		{"AgreePublic", got.AgreePublic, first.AgreePublic},
		{"AgreePrivate", got.AgreePrivate, first.AgreePrivate},
	}
	for _, f := range fields {
		if !bytes.Equal(f.got, f.want) {
			t.Errorf("%s: expected %d bytes equal to the generated identity, got %d bytes", f.name, len(f.want), len(f.got))
		}
	}

	signing := readKeysFile(t, dir, testSigningFile)
	if !bytes.Equal(signing, first.SigningPrivate) {
		t.Errorf("%s: expected the 64-byte private key, got %d bytes", testSigningFile, len(signing))
	}
	if len(signing) != ed25519.PrivateKeySize {
		t.Errorf("%s length: expected %d, got %d", testSigningFile, ed25519.PrivateKeySize, len(signing))
	}
	agree := readKeysFile(t, dir, testAgreeFile)
	if !bytes.Equal(agree, first.AgreePrivate) {
		t.Errorf("%s: expected the 32-byte private key, got %d bytes", testAgreeFile, len(agree))
	}
	if len(agree) != 32 {
		t.Errorf("%s length: expected 32, got %d", testAgreeFile, len(agree))
	}
	assertFileMode(t, dir, testSigningFile, 0o600)
	assertFileMode(t, dir, testAgreeFile, 0o600)

	second, err := GenerateAgentIdentity()
	if err != nil {
		t.Fatalf("GenerateAgentIdentity (second): unexpected error %v", err)
	}
	if err := WriteAgentIdentity(dir, second); err != nil {
		t.Fatalf("WriteAgentIdentity (second): unexpected error %v", err)
	}
	replaced, saved, err := LoadAgentIdentity(dir)
	if err != nil {
		t.Fatalf("LoadAgentIdentity (second): unexpected error %v", err)
	}
	if !saved {
		t.Fatalf("LoadAgentIdentity saved after replacing: expected true, got false")
	}
	replacedFields := []struct {
		name string
		got  []byte
		want []byte
	}{
		{"SigningPublic", replaced.SigningPublic, second.SigningPublic},
		{"SigningPrivate", replaced.SigningPrivate, second.SigningPrivate},
		{"AgreePublic", replaced.AgreePublic, second.AgreePublic},
		{"AgreePrivate", replaced.AgreePrivate, second.AgreePrivate},
	}
	for _, f := range replacedFields {
		if !bytes.Equal(f.got, f.want) {
			t.Errorf("%s after a second write: expected %d bytes of the second identity, got %d bytes", f.name, len(f.want), len(f.got))
		}
	}
	if got := readKeysFile(t, dir, testSigningFile); !bytes.Equal(got, second.SigningPrivate) {
		t.Errorf("%s after a second write: expected the second 64-byte private key, got %d bytes", testSigningFile, len(got))
	}
	if got := readKeysFile(t, dir, testAgreeFile); !bytes.Equal(got, second.AgreePrivate) {
		t.Errorf("%s after a second write: expected the second 32-byte private key, got %d bytes", testAgreeFile, len(got))
	}
	assertNoTempFiles(t, dir)
}

func TestLoadAgentIdentityAbsentDir(t *testing.T) {
	dir := t.TempDir()

	got, saved, err := LoadAgentIdentity(dir)
	if err != nil {
		t.Fatalf("LoadAgentIdentity on an empty dir: unexpected error %v", err)
	}
	if saved {
		t.Errorf("saved: expected false, got true")
	}
	for _, f := range []struct {
		name string
		n    int
	}{
		{"SigningPublic", len(got.SigningPublic)},
		{"SigningPrivate", len(got.SigningPrivate)},
		{"AgreePublic", len(got.AgreePublic)},
		{"AgreePrivate", len(got.AgreePrivate)},
	} {
		if f.n != 0 {
			t.Errorf("%s: expected the zero identity, got %d bytes", f.name, f.n)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: unexpected error %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("entries after LoadAgentIdentity: expected 0, got %d", len(entries))
	}
}

func TestLoadAgentIdentityRejectsMalformed(t *testing.T) {
	good, err := GenerateAgentIdentity()
	if err != nil {
		t.Fatalf("GenerateAgentIdentity: unexpected error %v", err)
	}

	tests := []struct {
		name     string
		signing  []byte // nil means write no signing file
		agree    []byte // nil means write no agree file
		wantFile string
	}{
		{
			name:     "signing key with no agree key",
			signing:  good.SigningPrivate,
			wantFile: testAgreeFile,
		},
		{
			name:     "63-byte signing key",
			signing:  good.SigningPrivate[:ed25519.PrivateKeySize-1],
			agree:    good.AgreePrivate,
			wantFile: testSigningFile,
		},
		{
			name:     "31-byte agree key",
			signing:  good.SigningPrivate,
			agree:    good.AgreePrivate[:31],
			wantFile: testAgreeFile,
		},
		{
			name:     "33-byte agree key",
			signing:  good.SigningPrivate,
			agree:    append(append([]byte{}, good.AgreePrivate...), 0),
			wantFile: testAgreeFile,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.signing != nil {
				writeTestKeyFile(t, dir, testSigningFile, tt.signing)
			}
			if tt.agree != nil {
				writeTestKeyFile(t, dir, testAgreeFile, tt.agree)
			}

			_, saved, err := LoadAgentIdentity(dir)

			if err == nil {
				t.Fatalf("LoadAgentIdentity: expected an error naming %s, got none", tt.wantFile)
			}
			if saved {
				t.Errorf("saved: expected false, got true")
			}
			if got := err.Error(); !bytes.Contains([]byte(got), []byte(tt.wantFile)) {
				t.Errorf("error: expected it to name %s, got %q", tt.wantFile, got)
			}
		})
	}

	t.Run("LoadAgentSigningKey rejects a 65-byte key", func(t *testing.T) {
		dir := t.TempDir()
		writeTestKeyFile(t, dir, testSigningFile, append(append([]byte{}, good.SigningPrivate...), 0))

		_, err := LoadAgentSigningKey(dir)

		if err == nil {
			t.Fatalf("LoadAgentSigningKey: expected an error naming %s, got none", testSigningFile)
		}
		if got := err.Error(); !bytes.Contains([]byte(got), []byte(testSigningFile)) {
			t.Errorf("error: expected it to name %s, got %q", testSigningFile, got)
		}
	})

	t.Run("LoadAgentSigningKey on a missing file", func(t *testing.T) {
		dir := t.TempDir()

		_, err := LoadAgentSigningKey(dir)

		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("error: expected errors.Is fs.ErrNotExist, got %v", err)
		}
		if err == nil {
			t.Fatalf("LoadAgentSigningKey: expected an error naming %s, got none", testSigningFile)
		}
		if got := err.Error(); !bytes.Contains([]byte(got), []byte(testSigningFile)) {
			t.Errorf("error: expected it to name %s, got %q", testSigningFile, got)
		}
	})
}

func TestControlPlanePinFile(t *testing.T) {
	dir := t.TempDir()
	first := make(ed25519.PublicKey, ed25519.PublicKeySize)
	if _, err := rand.Read(first); err != nil {
		t.Fatalf("rand.Read: unexpected error %v", err)
	}

	if err := WriteControlPlanePin(dir, first); err != nil {
		t.Fatalf("WriteControlPlanePin: unexpected error %v", err)
	}
	got, err := LoadControlPlanePin(dir)
	if err != nil {
		t.Fatalf("LoadControlPlanePin: unexpected error %v", err)
	}
	if !bytes.Equal(got, first) {
		t.Errorf("pin: expected the 32-byte written key, got %d bytes", len(got))
	}
	if file := readKeysFile(t, dir, testPinFile); !bytes.Equal(file, first) {
		t.Errorf("%s: expected the raw 32-byte key, got %d bytes", testPinFile, len(file))
	}
	assertFileMode(t, dir, testPinFile, 0o600)

	second := make(ed25519.PublicKey, ed25519.PublicKeySize)
	if _, err := rand.Read(second); err != nil {
		t.Fatalf("rand.Read (second): unexpected error %v", err)
	}
	if err := WriteControlPlanePin(dir, second); err != nil {
		t.Fatalf("WriteControlPlanePin (second): unexpected error %v", err)
	}
	got, err = LoadControlPlanePin(dir)
	if err != nil {
		t.Fatalf("LoadControlPlanePin (second): unexpected error %v", err)
	}
	if !bytes.Equal(got, second) {
		t.Errorf("pin after a second write: expected the second key, got %d bytes", len(got))
	}

	badWrites := []struct {
		name string
		pin  ed25519.PublicKey
	}{
		{"31-byte pin", second[:31]},
		{"33-byte pin", append(append(ed25519.PublicKey{}, second...), 0)},
	}
	for _, tt := range badWrites {
		t.Run(tt.name, func(t *testing.T) {
			err := WriteControlPlanePin(dir, tt.pin)

			if err == nil {
				t.Fatalf("WriteControlPlanePin of a %d-byte key: expected an error naming %s, got none", len(tt.pin), testPinFile)
			}
			if got := err.Error(); !bytes.Contains([]byte(got), []byte(testPinFile)) {
				t.Errorf("error: expected it to name %s, got %q", testPinFile, got)
			}
			if file := readKeysFile(t, dir, testPinFile); !bytes.Equal(file, second) {
				t.Errorf("%s after a rejected write: expected it byte-for-byte unchanged (32 bytes), got %d bytes", testPinFile, len(file))
			}
			assertNoTempFiles(t, dir)
		})
	}

	t.Run("wrong-length file on disk", func(t *testing.T) {
		badDir := t.TempDir()
		writeTestKeyFile(t, badDir, testPinFile, second[:31])

		_, err := LoadControlPlanePin(badDir)

		if err == nil {
			t.Fatalf("LoadControlPlanePin of a 31-byte file: expected an error naming %s, got none", testPinFile)
		}
		if got := err.Error(); !bytes.Contains([]byte(got), []byte(testPinFile)) {
			t.Errorf("error: expected it to name %s, got %q", testPinFile, got)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := LoadControlPlanePin(t.TempDir())

		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("error: expected errors.Is fs.ErrNotExist, got %v", err)
		}
		if err == nil {
			t.Fatalf("LoadControlPlanePin: expected an error naming %s, got none", testPinFile)
		}
		if got := err.Error(); !bytes.Contains([]byte(got), []byte(testPinFile)) {
			t.Errorf("error: expected it to name %s, got %q", testPinFile, got)
		}
	})
}

func TestFingerprintFile(t *testing.T) {
	id, err := GenerateAgentIdentity()
	if err != nil {
		t.Fatalf("GenerateAgentIdentity: unexpected error %v", err)
	}
	fp, err := Fingerprint(id.SigningPublic)
	if err != nil {
		t.Fatalf("Fingerprint: unexpected error %v", err)
	}
	dir := t.TempDir()

	if err := WriteFingerprint(dir, fp); err != nil {
		t.Fatalf("WriteFingerprint: unexpected error %v", err)
	}

	want := fp + "\n"
	if got := string(readKeysFile(t, dir, testFingerFile)); got != want {
		t.Errorf("%s: expected %q, got %q", testFingerFile, want, got)
	}
	assertFileMode(t, dir, testFingerFile, 0o600)

	t.Run("missing directory", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "missing")

		err := WriteFingerprint(missing, fp)

		if err == nil {
			t.Fatalf("WriteFingerprint into a missing directory: expected an error naming %s, got none", testFingerFile)
		}
		if got := err.Error(); !bytes.Contains([]byte(got), []byte(testFingerFile)) {
			t.Errorf("error: expected it to name %s, got %q", testFingerFile, got)
		}
	})
}

// readKeysFile reads dir/name or fails the test.
func readKeysFile(t *testing.T, dir, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: unexpected error %v", name, err)
	}
	return b
}
