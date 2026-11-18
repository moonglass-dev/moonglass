package keys

import (
	"bytes"
	"crypto/rand"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// updateVectors rewrites internal/keys/testdata. It exists only for the
// checked-in stock age vectors.
var updateVectors = flag.Bool("update-vectors", false, "rewrite the stock age test vectors in testdata")

const vectorEnvironmentID = "3f2a4c5d6e7f8091a2b3c4d5e6f70819"

// writeKeyVectors writes the stock age vectors: a test-only recovery identity,
// a record for that identity, and that record wrapped to the recovery key and
// to an agent agree key's recipient.
func writeKeyVectors(t *testing.T) {
	t.Helper()
	dir := "testdata"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create %s: %v", dir, err)
	}

	recovery, err := GenerateEnvironmentKey()
	if err != nil {
		t.Fatalf("GenerateEnvironmentKey: unexpected error %v", err)
	}
	record := KeyRecord{EnvironmentID: vectorEnvironmentID, Identity: recovery, Protected: false}
	recordJSON, err := MarshalKeyRecord(record)
	if err != nil {
		t.Fatalf("MarshalKeyRecord: unexpected error %v", err)
	}
	recoveryCiphertext, err := WrapKeyRecord(record, recovery.Recipient())
	if err != nil {
		t.Fatalf("WrapKeyRecord to the recovery key: unexpected error %v", err)
	}

	agentKey := make([]byte, 32)
	if _, err := rand.Read(agentKey); err != nil {
		t.Fatalf("generate agent agree key: %v", err)
	}
	agentIdentity, err := AgreeIdentity(agentKey)
	if err != nil {
		t.Fatalf("AgreeIdentity: unexpected error %v", err)
	}
	agentCiphertext, err := WrapKeyRecord(record, agentIdentity.Recipient())
	if err != nil {
		t.Fatalf("WrapKeyRecord to the agent recipient: unexpected error %v", err)
	}

	write := func(name string, data []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("recovery.txt", []byte(recovery.Secret()+"\n"))
	write("record.json", recordJSON)
	write("record.age", recoveryCiphertext)
	write("agent-agree.key", agentKey)
	write("agent-record.age", agentCiphertext)
}

// stockAge returns the stock age binary, or skips when it is not installed.
func stockAge(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("age")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("stock age is not on PATH and CI is set: install it (ADR-0015 decision 4)")
		}
		t.Skip("stock age is not on PATH: skipping the stock age vector check")
	}
	return path
}

// decryptWithStockAge runs `age -d -i identityPath` over ciphertext.
func decryptWithStockAge(t *testing.T, agePath, identityPath string, ciphertext []byte) []byte {
	t.Helper()
	ciphertextPath := filepath.Join(t.TempDir(), "ciphertext.age")
	if err := os.WriteFile(ciphertextPath, ciphertext, 0o600); err != nil {
		t.Fatalf("write ciphertext: %v", err)
	}
	out, err := exec.Command(agePath, "-d", "-i", identityPath, ciphertextPath).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			t.Fatalf("age -d -i %s: %v (stderr %q)", identityPath, err, exit.Stderr)
		}
		t.Fatalf("age -d -i %s: %v", identityPath, err)
	}
	return out
}

// writeIdentityFile writes an identity line where stock age can read it.
func writeIdentityFile(t *testing.T, identity Identity) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "identity.txt")
	if err := os.WriteFile(path, []byte(identity.Secret()+"\n"), 0o600); err != nil {
		t.Fatalf("write identity file: %v", err)
	}
	return path
}

func TestKeyVectorsDecryptWithStockAge(t *testing.T) {
	if *updateVectors {
		writeKeyVectors(t)
	}
	agePath := stockAge(t)

	recordJSON, err := os.ReadFile(filepath.Join("testdata", "record.json"))
	if err != nil {
		t.Fatalf("read testdata/record.json: %v", err)
	}
	wantRecord, err := ParseKeyRecord(recordJSON)
	if err != nil {
		t.Fatalf("ParseKeyRecord of testdata/record.json: unexpected error %v", err)
	}

	recoveryText, err := os.ReadFile(filepath.Join("testdata", "recovery.txt"))
	if err != nil {
		t.Fatalf("read testdata/recovery.txt: %v", err)
	}
	recovery, err := ParseIdentity(strings.TrimSuffix(string(recoveryText), "\n"))
	if err != nil {
		t.Fatalf("ParseIdentity of testdata/recovery.txt: unexpected error %v", err)
	}
	recoveryCiphertext, err := os.ReadFile(filepath.Join("testdata", "record.age"))
	if err != nil {
		t.Fatalf("read testdata/record.age: %v", err)
	}

	agentKey, err := os.ReadFile(filepath.Join("testdata", "agent-agree.key"))
	if err != nil {
		t.Fatalf("read testdata/agent-agree.key: %v", err)
	}
	agentIdentity, err := AgreeIdentity(agentKey)
	if err != nil {
		t.Fatalf("AgreeIdentity of testdata/agent-agree.key: unexpected error %v", err)
	}
	agentCiphertext, err := os.ReadFile(filepath.Join("testdata", "agent-record.age"))
	if err != nil {
		t.Fatalf("read testdata/agent-record.age: %v", err)
	}

	tests := []struct {
		name         string
		identityPath string
		identity     Identity
		ciphertext   []byte
	}{
		{"recovery key", filepath.Join("testdata", "recovery.txt"), recovery, recoveryCiphertext},
		{"agent agree key", writeIdentityFile(t, agentIdentity), agentIdentity, agentCiphertext},
	}
	for _, tt := range tests {
		t.Run("checked-in "+tt.name, func(t *testing.T) {
			got := decryptWithStockAge(t, agePath, tt.identityPath, tt.ciphertext)
			if !bytes.Equal(got, recordJSON) {
				t.Errorf("stock age plaintext: expected testdata/record.json, got %q", got)
			}

			rec, err := UnwrapKeyRecord(tt.ciphertext, tt.identity, wantRecord.EnvironmentID)
			if err != nil {
				t.Fatalf("UnwrapKeyRecord of %s: unexpected error %v", tt.name, err)
			}
			if !recordsEqual(rec, wantRecord) {
				t.Errorf("unwrapped record: expected environment %q protected %v, got environment %q protected %v",
					wantRecord.EnvironmentID, wantRecord.Protected, rec.EnvironmentID, rec.Protected)
			}
		})
	}

	t.Run("fresh environment key", func(t *testing.T) {
		fresh, err := GenerateEnvironmentKey()
		if err != nil {
			t.Fatalf("GenerateEnvironmentKey: unexpected error %v", err)
		}
		rec := KeyRecord{EnvironmentID: vectorEnvironmentID, Identity: fresh, Protected: true}
		want, err := MarshalKeyRecord(rec)
		if err != nil {
			t.Fatalf("MarshalKeyRecord: unexpected error %v", err)
		}
		ciphertext, err := WrapKeyRecord(rec, fresh.Recipient())
		if err != nil {
			t.Fatalf("WrapKeyRecord: unexpected error %v", err)
		}

		if got := decryptWithStockAge(t, agePath, writeIdentityFile(t, fresh), ciphertext); !bytes.Equal(got, want) {
			t.Errorf("stock age plaintext: expected %s, got %q", want, got)
		}
		unwrapped, err := UnwrapKeyRecord(ciphertext, fresh, vectorEnvironmentID)
		if err != nil {
			t.Fatalf("UnwrapKeyRecord: unexpected error %v", err)
		}
		if !recordsEqual(unwrapped, rec) {
			t.Errorf("unwrapped record: expected environment %q protected %v, got environment %q protected %v",
				rec.EnvironmentID, rec.Protected, unwrapped.EnvironmentID, unwrapped.Protected)
		}
	})

	t.Run("fresh agent key", func(t *testing.T) {
		agent, err := GenerateAgentIdentity()
		if err != nil {
			t.Fatalf("GenerateAgentIdentity: unexpected error %v", err)
		}
		recipient, err := AgreeRecipient(agent.AgreePublic)
		if err != nil {
			t.Fatalf("AgreeRecipient: unexpected error %v", err)
		}
		identity, err := AgreeIdentity(agent.AgreePrivate)
		if err != nil {
			t.Fatalf("AgreeIdentity: unexpected error %v", err)
		}
		rec := KeyRecord{EnvironmentID: vectorEnvironmentID, Identity: identity, Protected: false}
		want, err := MarshalKeyRecord(rec)
		if err != nil {
			t.Fatalf("MarshalKeyRecord: unexpected error %v", err)
		}
		ciphertext, err := WrapKeyRecord(rec, recipient)
		if err != nil {
			t.Fatalf("WrapKeyRecord to the agent recipient: unexpected error %v", err)
		}

		if got := decryptWithStockAge(t, agePath, writeIdentityFile(t, identity), ciphertext); !bytes.Equal(got, want) {
			t.Errorf("stock age plaintext: expected %s, got %q", want, got)
		}
		unwrapped, err := UnwrapKeyRecord(ciphertext, identity, vectorEnvironmentID)
		if err != nil {
			t.Fatalf("UnwrapKeyRecord: unexpected error %v", err)
		}
		if !recordsEqual(unwrapped, rec) {
			t.Errorf("unwrapped record: expected environment %q protected %v, got environment %q protected %v",
				rec.EnvironmentID, rec.Protected, unwrapped.EnvironmentID, unwrapped.Protected)
		}
	})
}
