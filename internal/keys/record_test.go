package keys

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"filippo.io/age"
)

const recordTestEnvironmentID = "0123456789abcdef0123456789abcdef"

// recordsEqual compares the fields of a KeyRecord, none of which is exported
// in the Identity itself.
func recordsEqual(a, b KeyRecord) bool {
	return a.EnvironmentID == b.EnvironmentID &&
		a.Protected == b.Protected &&
		a.Identity.Secret() == b.Identity.Secret()
}

// noKeyLeak fails when err carries key text.
func noKeyLeak(t *testing.T, name string, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if strings.Contains(err.Error(), "AGE-SECRET-KEY") {
		t.Errorf("%s: error contains key text", name)
	}
}

func TestKeyRecordRoundTrip(t *testing.T) {
	envKey, err := GenerateEnvironmentKey()
	if err != nil {
		t.Fatalf("GenerateEnvironmentKey: unexpected error %v", err)
	}

	tests := []struct {
		name      string
		protected bool
	}{
		{"unprotected", false},
		{"protected", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := KeyRecord{EnvironmentID: recordTestEnvironmentID, Identity: envKey, Protected: tt.protected}
			want := fmt.Sprintf(`{"v":1,"environment_id":%q,"protected":%t,"identity":%q}`,
				rec.EnvironmentID, rec.Protected, envKey.Secret())

			got, err := MarshalKeyRecord(rec)
			if err != nil {
				t.Fatalf("MarshalKeyRecord: unexpected error %v", err)
			}
			if string(got) != want {
				t.Errorf("MarshalKeyRecord: expected %s, got %s", want, got)
			}

			parsed, err := ParseKeyRecord(got)
			if err != nil {
				t.Fatalf("ParseKeyRecord of a marshalled record: unexpected error %v", err)
			}
			if !recordsEqual(parsed, rec) {
				t.Errorf("ParseKeyRecord: expected environment %q protected %v, got environment %q protected %v",
					rec.EnvironmentID, rec.Protected, parsed.EnvironmentID, parsed.Protected)
			}
		})
	}
}

// malformedKeyRecords is the table of refused key records, shared with the fuzz
// target's seed corpus.
func malformedKeyRecords(t testing.TB, envKey Identity, envID string) []struct {
	name  string
	input string
} {
	t.Helper()
	secret := envKey.Secret()
	recipient := envKey.Recipient().String()
	q := func(s string) string { return fmt.Sprintf("%q", s) }

	return []struct {
		name  string
		input string
	}{
		{"version 0", fmt.Sprintf(`{"v":0,"environment_id":%s,"protected":false,"identity":%s}`, q(envID), q(secret))},
		{"version 2", fmt.Sprintf(`{"v":2,"environment_id":%s,"protected":false,"identity":%s}`, q(envID), q(secret))},
		{"version 1.0", fmt.Sprintf(`{"v":1.0,"environment_id":%s,"protected":false,"identity":%s}`, q(envID), q(secret))},
		{"missing v", fmt.Sprintf(`{"environment_id":%s,"protected":false,"identity":%s}`, q(envID), q(secret))},
		{"missing environment_id", fmt.Sprintf(`{"v":1,"protected":false,"identity":%s}`, q(secret))},
		{"missing protected", fmt.Sprintf(`{"v":1,"environment_id":%s,"identity":%s}`, q(envID), q(secret))},
		{"missing identity", fmt.Sprintf(`{"v":1,"environment_id":%s,"protected":false}`, q(envID))},
		{"unknown field", fmt.Sprintf(`{"v":1,"environment_id":%s,"protected":false,"identity":%s,"extra":1}`, q(envID), q(secret))},
		{"duplicated v", fmt.Sprintf(`{"v":1,"v":1,"environment_id":%s,"protected":false,"identity":%s}`, q(envID), q(secret))},
		{"duplicated identity", fmt.Sprintf(`{"v":1,"environment_id":%s,"protected":false,"identity":%s,"identity":%s}`, q(envID), q(secret), q(secret))},
		{"escaped duplicate v", fmt.Sprintf(`{"v":1,"\u0076":1,"environment_id":%s,"protected":false,"identity":%s}`, q(envID), q(secret))},
		{"trailing object", fmt.Sprintf(`{"v":1,"environment_id":%s,"protected":false,"identity":%s}{}`, q(envID), q(secret))},
		{"trailing text", fmt.Sprintf(`{"v":1,"environment_id":%s,"protected":false,"identity":%s}x`, q(envID), q(secret))},
		{"uppercase environment_id", fmt.Sprintf(`{"v":1,"environment_id":%s,"protected":false,"identity":%s}`, q(strings.ToUpper(envID)), q(secret))},
		{"31-character environment_id", fmt.Sprintf(`{"v":1,"environment_id":%s,"protected":false,"identity":%s}`, q(envID[:31]), q(secret))},
		{"33-character environment_id", fmt.Sprintf(`{"v":1,"environment_id":%s,"protected":false,"identity":%s}`, q(envID+"a"), q(secret))},
		{"non-hex environment_id", fmt.Sprintf(`{"v":1,"environment_id":%s,"protected":false,"identity":%s}`, q(strings.Repeat("zz", 16)), q(secret))},
		{"recipient as identity", fmt.Sprintf(`{"v":1,"environment_id":%s,"protected":false,"identity":%s}`, q(envID), q(recipient))},
		{"corrupted identity", fmt.Sprintf(`{"v":1,"environment_id":%s,"protected":false,"identity":%s}`, q(envID), q(corruptLastChar(secret)))},
		{"protected as string", fmt.Sprintf(`{"v":1,"environment_id":%s,"protected":"false","identity":%s}`, q(envID), q(secret))},
		{"protected as number", fmt.Sprintf(`{"v":1,"environment_id":%s,"protected":0,"identity":%s}`, q(envID), q(secret))},
		{"protected as null", fmt.Sprintf(`{"v":1,"environment_id":%s,"protected":null,"identity":%s}`, q(envID), q(secret))},
		{"array", `[]`},
		{"empty input", ``},
	}
}

func TestKeyRecordRejectsMalformed(t *testing.T) {
	envKey, err := GenerateEnvironmentKey()
	if err != nil {
		t.Fatalf("GenerateEnvironmentKey: unexpected error %v", err)
	}

	for _, tt := range malformedKeyRecords(t, envKey, recordTestEnvironmentID) {
		t.Run(tt.name, func(t *testing.T) {
			rec, err := ParseKeyRecord([]byte(tt.input))
			if err == nil {
				t.Fatalf("ParseKeyRecord: expected an error, got a record for environment %q", rec.EnvironmentID)
			}
			noKeyLeak(t, "ParseKeyRecord", err)
		})
	}
}

func TestWrapUnwrapKeyRecord(t *testing.T) {
	envKey, err := GenerateEnvironmentKey()
	if err != nil {
		t.Fatalf("GenerateEnvironmentKey: unexpected error %v", err)
	}
	otherKey, err := GenerateEnvironmentKey()
	if err != nil {
		t.Fatalf("GenerateEnvironmentKey (second call): unexpected error %v", err)
	}

	const envA = "11111111111111111111111111111111"
	const envB = "22222222222222222222222222222222"
	rec := KeyRecord{EnvironmentID: envA, Identity: envKey, Protected: true}

	ciphertext, err := WrapKeyRecord(rec, envKey.Recipient())
	if err != nil {
		t.Fatalf("WrapKeyRecord: unexpected error %v", err)
	}

	t.Run("round trip", func(t *testing.T) {
		got, err := UnwrapKeyRecord(ciphertext, envKey, envA)
		if err != nil {
			t.Fatalf("UnwrapKeyRecord: unexpected error %v", err)
		}
		if !recordsEqual(got, rec) {
			t.Errorf("unwrapped record: expected environment %q protected %v, got environment %q protected %v",
				rec.EnvironmentID, rec.Protected, got.EnvironmentID, got.Protected)
		}
	})

	t.Run("wrong identity", func(t *testing.T) {
		got, err := UnwrapKeyRecord(ciphertext, otherKey, envA)
		if err == nil {
			t.Errorf("UnwrapKeyRecord with another identity: expected an error, got a record for environment %q", got.EnvironmentID)
			return
		}
		noKeyLeak(t, "UnwrapKeyRecord with another identity", err)
	})

	t.Run("tampered ciphertext", func(t *testing.T) {
		tests := []struct {
			name  string
			index int
		}{
			{"header", 0},
			{"payload", len(ciphertext) / 2},
			{"last byte", len(ciphertext) - 1},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				damaged := bytes.Clone(ciphertext)
				damaged[tt.index] ^= 0xff

				got, err := UnwrapKeyRecord(damaged, envKey, envA)
				if err == nil {
					t.Errorf("UnwrapKeyRecord of a tampered ciphertext: expected an error, got a record for environment %q", got.EnvironmentID)
					return
				}
				noKeyLeak(t, "UnwrapKeyRecord of a tampered ciphertext", err)
			})
		}
	})

	t.Run("environment mismatch", func(t *testing.T) {
		got, err := UnwrapKeyRecord(ciphertext, envKey, envB)
		if err == nil {
			t.Fatalf("UnwrapKeyRecord for %q: expected an error, got a record for environment %q", envB, got.EnvironmentID)
		}
		if !strings.Contains(err.Error(), envA) || !strings.Contains(err.Error(), envB) {
			t.Errorf("UnwrapKeyRecord for %q: expected an error naming both %q and %q, got %v", envB, envA, envB, err)
		}
		noKeyLeak(t, "UnwrapKeyRecord for another environment", err)
	})

	t.Run("oversized plaintext", func(t *testing.T) {
		recipient, err := age.ParseX25519Recipient(envKey.Recipient().String())
		if err != nil {
			t.Fatalf("age.ParseX25519Recipient: unexpected error %v", err)
		}
		var buf bytes.Buffer
		w, err := age.Encrypt(&buf, recipient)
		if err != nil {
			t.Fatalf("age.Encrypt: unexpected error %v", err)
		}
		if _, err := w.Write(bytes.Repeat([]byte("x"), 2048)); err != nil {
			t.Fatalf("age.Encrypt write: unexpected error %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("age.Encrypt close: unexpected error %v", err)
		}

		if got, err := UnwrapKeyRecord(buf.Bytes(), envKey, envA); err == nil {
			t.Errorf("UnwrapKeyRecord of a 2048-byte plaintext: expected an error, got a record for environment %q", got.EnvironmentID)
		}
	})
}

func FuzzParseKeyRecord(f *testing.F) {
	envKey, err := GenerateEnvironmentKey()
	if err != nil {
		f.Fatalf("GenerateEnvironmentKey: unexpected error %v", err)
	}
	for _, protected := range []bool{false, true} {
		seed, err := MarshalKeyRecord(KeyRecord{EnvironmentID: recordTestEnvironmentID, Identity: envKey, Protected: protected})
		if err != nil {
			f.Fatalf("MarshalKeyRecord: unexpected error %v", err)
		}
		f.Add(seed)
	}
	for _, tt := range malformedKeyRecords(f, envKey, recordTestEnvironmentID) {
		f.Add([]byte(tt.input))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		rec, err := ParseKeyRecord(data)
		if err != nil {
			if strings.Contains(err.Error(), "AGE-SECRET-KEY") {
				t.Fatalf("ParseKeyRecord error contains key text")
			}
			return
		}

		out, err := MarshalKeyRecord(rec)
		if err != nil {
			t.Fatalf("MarshalKeyRecord of a parsed record: unexpected error %v", err)
		}
		again, err := ParseKeyRecord(out)
		if err != nil {
			t.Fatalf("ParseKeyRecord of a marshalled record: unexpected error %v", err)
		}
		if !recordsEqual(rec, again) {
			t.Fatalf("round trip changed the record: protected %v then %v", rec.Protected, again.Protected)
		}
	})
}
