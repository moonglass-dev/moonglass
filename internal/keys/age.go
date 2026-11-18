package keys

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"filippo.io/age"
)

// Recipient is an age X25519 public key; it is public and may be printed.
type Recipient struct {
	recipient *age.X25519Recipient
}

func (r Recipient) String() string { return r.recipient.String() }

// Identity is an age X25519 private key. It has no String method, so only
// Secret prints it.
type Identity struct {
	identity *age.X25519Identity
}

func (i Identity) Recipient() Recipient { return Recipient{recipient: i.identity.Recipient()} }

func (i Identity) Secret() string { return i.identity.String() }

func GenerateEnvironmentKey() (Identity, error) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		return Identity{}, fmt.Errorf("generate environment key: %w", err)
	}
	return Identity{identity: identity}, nil
}

// AgreeRecipient returns the age recipient of a raw 32-byte X25519 public key.
func AgreeRecipient(agreePublic []byte) (Recipient, error) {
	if len(agreePublic) != 32 {
		return Recipient{}, fmt.Errorf("agent agree public key is %d bytes, expected 32", len(agreePublic))
	}
	return ParseRecipient(bech32Encode("age", agreePublic))
}

// AgreeIdentity returns the age identity of a raw 32-byte X25519 private key.
func AgreeIdentity(agreePrivate []byte) (Identity, error) {
	if len(agreePrivate) != 32 {
		return Identity{}, fmt.Errorf("agent agree private key is %d bytes, expected 32", len(agreePrivate))
	}
	return ParseIdentity(strings.ToUpper(bech32Encode("AGE-SECRET-KEY-", agreePrivate)))
}

// ParseRecipient accepts only an age X25519 recipient; age refuses the rest.
func ParseRecipient(s string) (Recipient, error) {
	recipient, err := age.ParseX25519Recipient(s)
	if err != nil {
		return Recipient{}, fmt.Errorf("not an age X25519 recipient (age1…): %w", err)
	}
	return Recipient{recipient: recipient}, nil
}

// ParseIdentity accepts only an age X25519 identity. Its error is fixed because
// age's parser can quote part of the secret input.
func ParseIdentity(s string) (Identity, error) {
	identity, err := age.ParseX25519Identity(s)
	if err != nil {
		return Identity{}, errors.New("not an age X25519 identity")
	}
	return Identity{identity: identity}, nil
}

// CreateRecoveryKeyFile writes a new recovery identity file with mode 0600.
func CreateRecoveryKeyFile(path string) (Recipient, error) {
	identity, err := GenerateEnvironmentKey()
	if err != nil {
		return Recipient{}, err
	}
	//nolint:gosec // G304: the owner names the file with --out
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return Recipient{}, fmt.Errorf("create recovery key file %s: %w: keys init never overwrites a key: choose a new path", path, err)
		}
		return Recipient{}, fmt.Errorf("create recovery key file %s: %w", path, err)
	}
	fail := func(cause error) (Recipient, error) {
		_ = f.Close()
		_ = os.Remove(path)
		return Recipient{}, fmt.Errorf("create recovery key file %s: %w", path, cause)
	}
	if _, err := io.WriteString(f, identity.Secret()+"\n"); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return Recipient{}, fmt.Errorf("create recovery key file %s: close: %w", path, err)
	}
	return identity.Recipient(), nil
}
