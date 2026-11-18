package keys

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"filippo.io/age"
)

const maxRecordSize = 1024 // the largest key record plaintext accepted
// KeyRecord is the plaintext of every wrapped environment key (docs/specs/secrets.md).
type KeyRecord struct {
	Identity      Identity
	EnvironmentID string
	Protected     bool
}

func recordError(msg string) (KeyRecord, error) {
	return KeyRecord{}, errors.New("key record: " + msg)
}

// validEnvironmentID reports whether s is exactly 32 lowercase hex characters.
func validEnvironmentID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// MarshalKeyRecord returns the exact key-record bytes for r.
func MarshalKeyRecord(r KeyRecord) ([]byte, error) {
	if !validEnvironmentID(r.EnvironmentID) || r.Identity.identity == nil {
		return nil, errors.New("key record: invalid environment_id or missing identity")
	}
	// Both strings are validated ASCII, so %q matches JSON string encoding.
	return fmt.Appendf(nil, "{\"v\":1,\"environment_id\":%q,\"protected\":%t,\"identity\":%q}",
		r.EnvironmentID, r.Protected, r.Identity.Secret()), nil
}

// ParseKeyRecord parses strictly: exact version, once per field, no trailing data.
func ParseKeyRecord(b []byte) (KeyRecord, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return KeyRecord{}, fmt.Errorf("key record: %w", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return recordError("not a JSON object")
	}
	var rec KeyRecord
	seen := make(map[string]bool, 4)
	for dec.More() {
		keyTok, keyErr := dec.Token()
		if keyErr != nil {
			return KeyRecord{}, fmt.Errorf("key record: %w", keyErr)
		}
		key := keyTok.(string)
		if seen[key] {
			return recordError("duplicated field")
		}
		seen[key] = true
		var raw json.RawMessage
		if decodeErr := dec.Decode(&raw); decodeErr != nil {
			return KeyRecord{}, fmt.Errorf("key record: %w", decodeErr)
		}
		switch key {
		case "v":
			if string(raw) != "1" {
				return recordError("v must be 1")
			}
		case "environment_id":
			var id string
			if idErr := json.Unmarshal(raw, &id); idErr != nil || !validEnvironmentID(id) {
				return recordError("environment_id must be 32 lowercase hex characters")
			}
			rec.EnvironmentID = id
		case "protected":
			if p := string(raw); p != "true" && p != "false" {
				return recordError("protected must be true or false")
			}
			rec.Protected = string(raw) == "true"
		case "identity":
			var text string
			if textErr := json.Unmarshal(raw, &text); textErr != nil {
				return recordError("identity must be an age X25519 identity")
			}
			identity, parseErr := ParseIdentity(text)
			if parseErr != nil {
				return recordError("identity must be an age X25519 identity")
			}
			rec.Identity = identity
		default:
			return recordError("unknown field (allowed: v, environment_id, protected, identity)")
		}
	}
	tok, err = dec.Token()
	if err != nil {
		return KeyRecord{}, fmt.Errorf("key record: %w", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '}' {
		return recordError("not a JSON object")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return recordError("trailing data after the key record")
	}
	if len(seen) != 4 {
		return recordError("missing field")
	}
	return rec, nil
}

// WrapKeyRecord encrypts the marshalled record to exactly one recipient.
func WrapKeyRecord(r KeyRecord, to Recipient) ([]byte, error) {
	plaintext, err := MarshalKeyRecord(r)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, to.recipient)
	if err != nil {
		return nil, fmt.Errorf("wrap key record: %w", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		return nil, fmt.Errorf("wrap key record: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("wrap key record: %w", err)
	}
	return buf.Bytes(), nil
}

// UnwrapKeyRecord decrypts with one identity and refuses another environment.
// A partial or oversized plaintext is never parsed: age authenticates it.
func UnwrapKeyRecord(ciphertext []byte, with Identity, environmentID string) (KeyRecord, error) {
	r, err := age.Decrypt(bytes.NewReader(ciphertext), with.identity)
	if err != nil {
		return KeyRecord{}, fmt.Errorf("unwrap key record: %w", err)
	}
	plaintext, err := io.ReadAll(io.LimitReader(r, maxRecordSize+1))
	if err != nil {
		return KeyRecord{}, fmt.Errorf("unwrap key record: %w", err)
	}
	if len(plaintext) > maxRecordSize {
		return KeyRecord{}, fmt.Errorf("unwrap key record: plaintext is larger than %d bytes", maxRecordSize)
	}
	rec, err := ParseKeyRecord(plaintext)
	if err != nil {
		return KeyRecord{}, fmt.Errorf("unwrap key record: %w", err)
	}
	if rec.EnvironmentID != environmentID {
		return KeyRecord{}, fmt.Errorf("unwrap key record: key record is for environment %s, not %s", rec.EnvironmentID, environmentID)
	}
	return rec, nil
}
