package keys

import (
	"fmt"
	"strings"
	"testing"
)

// corruptLastChar changes the final Bech32 character, keeping the case class so
// the only fault is the checksum.
func corruptLastChar(s string) string {
	last := s[len(s)-1]
	var repl byte
	switch {
	case last >= 'a' && last <= 'z':
		repl = 'q'
		if last == 'q' {
			repl = 'p'
		}
	case last >= 'A' && last <= 'Z':
		repl = 'Q'
		if last == 'Q' {
			repl = 'P'
		}
	default:
		repl = '2'
		if last == '2' {
			repl = '3'
		}
	}
	return s[:len(s)-1] + string(repl)
}

func TestGenerateEnvironmentKey(t *testing.T) {
	first, err := GenerateEnvironmentKey()
	if err != nil {
		t.Fatalf("GenerateEnvironmentKey: unexpected error %v", err)
	}
	second, err := GenerateEnvironmentKey()
	if err != nil {
		t.Fatalf("GenerateEnvironmentKey (second call): unexpected error %v", err)
	}

	if first.Secret() == second.Secret() {
		t.Errorf("two generated environment keys share an identity, expected different identities")
	}
	if first.Recipient().String() == second.Recipient().String() {
		t.Errorf("two generated environment keys share the recipient %q, expected different recipients", first.Recipient().String())
	}

	for i, key := range []Identity{first, second} {
		t.Run(fmt.Sprintf("key %d", i), func(t *testing.T) {
			recipient := key.Recipient().String()
			if !strings.HasPrefix(recipient, "age1") {
				t.Errorf("recipient: expected an age1 prefix, got %q", recipient)
			}
			parsed, err := ParseIdentity(key.Secret())
			if err != nil {
				t.Fatalf("ParseIdentity of a generated identity: unexpected error %v", err)
			}
			if derived := parsed.Recipient().String(); derived != recipient {
				t.Errorf("recipient from the parsed identity: expected %q, got %q", recipient, derived)
			}
		})
	}
}

func TestAgentAgreeKeyAsAgeRecipient(t *testing.T) {
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

	if got, want := recipient.String(), identity.Recipient().String(); got != want {
		t.Errorf("agent recipient: expected %q from AgreeIdentity's identity, got %q from AgreeRecipient", want, got)
	}
	if got := recipient.String(); !strings.HasPrefix(got, "age1") {
		t.Errorf("agent recipient: expected an age1 prefix, got %q", got)
	}

	envKey, err := GenerateEnvironmentKey()
	if err != nil {
		t.Fatalf("GenerateEnvironmentKey: unexpected error %v", err)
	}
	rec := KeyRecord{EnvironmentID: strings.Repeat("ab", 16), Identity: envKey, Protected: true}
	ciphertext, err := WrapKeyRecord(rec, recipient)
	if err != nil {
		t.Fatalf("WrapKeyRecord to the agent recipient: unexpected error %v", err)
	}
	got, err := UnwrapKeyRecord(ciphertext, identity, rec.EnvironmentID)
	if err != nil {
		t.Fatalf("UnwrapKeyRecord with the agent identity: unexpected error %v", err)
	}
	if !recordsEqual(got, rec) {
		t.Errorf("unwrapped record: expected environment %q protected %v, got environment %q protected %v",
			rec.EnvironmentID, rec.Protected, got.EnvironmentID, got.Protected)
	}

	for _, n := range []int{0, 31, 33} {
		t.Run(fmt.Sprintf("%d-byte agent key", n), func(t *testing.T) {
			if got, err := AgreeRecipient(make([]byte, n)); err == nil {
				t.Errorf("AgreeRecipient with a %d-byte public key: expected an error, got %q", n, got.String())
			} else if !strings.Contains(err.Error(), "expected 32") {
				t.Errorf("AgreeRecipient with a %d-byte public key: expected an error naming the expected length, got %v", n, err)
			}

			if got, err := AgreeIdentity(make([]byte, n)); err == nil {
				t.Errorf("AgreeIdentity with a %d-byte private key: expected an error, got the recipient %q", n, got.Recipient().String())
			} else if !strings.Contains(err.Error(), "expected 32") {
				t.Errorf("AgreeIdentity with a %d-byte private key: expected an error naming the expected length, got %v", n, err)
			}
		})
	}
}

func TestOnlyX25519RecipientsAndIdentitiesParse(t *testing.T) {
	envKey, err := GenerateEnvironmentKey()
	if err != nil {
		t.Fatalf("GenerateEnvironmentKey: unexpected error %v", err)
	}
	recipient := envKey.Recipient().String()
	identity := envKey.Secret()

	tests := []struct {
		name          string
		value         string
		wantRecipient bool
		wantIdentity  bool
	}{
		{"x25519 recipient", recipient, true, false},
		{"x25519 identity", identity, false, true},
		{"ssh-ed25519 public key", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIL1v8kP0hXq2yW2f5P5Y0m9wzq3cHh8yqkq7Yh2mZb3c", false, false},
		{"ssh-rsa public key", "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQC7Z0w3n0mV1jqYcJk1zY8h4Gg0YpXk6P0mV4mY0w2s7Qk8", false, false},
		{"plugin recipient", bech32Encode("age1yubikey", make([]byte, 33)), false, false},
		{"plugin identity", strings.ToUpper(bech32Encode("AGE-PLUGIN-YUBIKEY-", make([]byte, 32))), false, false},
		{"post-quantum hybrid recipient", bech32Encode("age1pq", make([]byte, 1216)), false, false},
		{"empty string", "", false, false},
		{"corrupted recipient checksum", corruptLastChar(recipient), false, false},
		{"corrupted identity checksum", corruptLastChar(identity), false, false},
		{"lowercase identity", strings.ToLower(identity), false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotRecipient, recipientErr := ParseRecipient(tt.value)
			if (recipientErr == nil) != tt.wantRecipient {
				t.Errorf("ParseRecipient: expected accepted=%v, got error %v", tt.wantRecipient, recipientErr)
			}
			if tt.wantRecipient && recipientErr == nil {
				if got, want := gotRecipient.String(), tt.value; got != want {
					t.Errorf("ParseRecipient: expected %q back, got %q", want, got)
				}
			}

			gotIdentity, identityErr := ParseIdentity(tt.value)
			if (identityErr == nil) != tt.wantIdentity {
				t.Errorf("ParseIdentity: expected accepted=%v, got error %v", tt.wantIdentity, identityErr)
			}
			if tt.wantIdentity && identityErr == nil {
				if got := gotIdentity.Secret(); got != tt.value {
					t.Errorf("ParseIdentity: the parsed identity does not match the input")
				}
			}
			if !tt.wantIdentity && identityErr != nil && tt.value != "" && strings.Contains(identityErr.Error(), tt.value) {
				t.Errorf("ParseIdentity error contains the input key text")
			}
		})
	}
}
