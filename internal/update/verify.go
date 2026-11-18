// Package update verifies release artifacts before the agent installs them.
//
// Verify accepts OpenSSH SSHSIG signatures from pinned sk-ssh-ed25519 keys in
// the moonglass-release namespace, as ADR-0007 decision 1 requires.
package update

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	principal               = "moonglass-release"
	namespace               = "moonglass-release"
	namespacesOption        = `namespaces="moonglass-release"`
	keyType                 = "sk-ssh-ed25519@openssh.com"
	hashAlg                 = "sha512"
	magic                   = "SSHSIG"
	armorBegin              = "-----BEGIN SSH SIGNATURE-----"
	armorEnd                = "-----END SSH SIGNATURE-----"
	version          uint32 = 1
	requiredFlags    byte   = 0x05
)

// ErrBadSignature is wrapped by every rejection from Verify and ParseSigner.
var ErrBadSignature = errors.New("release signature rejected")

// bad returns a rejection that names the failed check and wraps ErrBadSignature.
func bad(format string, args ...any) error {
	return fmt.Errorf("update: %s: %w", fmt.Sprintf(format, args...), ErrBadSignature)
}

// Signer is one pinned release key, parsed from an allowed_signers line.
type Signer struct {
	blob        []byte
	key         ed25519.PublicKey
	application []byte
}

// ParseSigner parses one pinned key in exactly the allowed_signers form of
// docs/specs/install.md, "Pinned keys": four space-separated fields, nothing else.
func ParseSigner(line string) (Signer, error) {
	parts := strings.Split(line, " ")
	if len(parts) != 4 {
		return Signer{}, bad("allowed_signers line has %d space-separated fields, want 4", len(parts))
	}
	for _, c := range []struct{ name, got, want string }{
		{"principal", parts[0], principal},
		{"option", parts[1], namespacesOption},
		{"key type", parts[2], keyType},
	} {
		if c.got != c.want {
			return Signer{}, bad("%s is %q, want %q", c.name, c.got, c.want)
		}
	}
	blob, err := base64.StdEncoding.Strict().DecodeString(parts[3])
	if err != nil {
		return Signer{}, fmt.Errorf("update: decode pinned key: %w", errors.Join(ErrBadSignature, err))
	}
	parsed, err := parseKeyBlob(blob)
	if err != nil {
		return Signer{}, err
	}
	return Signer{blob: blob, key: parsed.key, application: parsed.application}, nil
}

// wire reads the OpenSSH framing of a uint32 length followed by that many bytes.
// Every length is checked against the bytes remaining before anything is sliced.
type wire []byte

func (w *wire) take(n int, field string) ([]byte, error) {
	if n < 0 || len(*w) < n {
		return nil, bad("%s is truncated", field)
	}
	b := (*w)[:n]
	*w = (*w)[n:]
	return b, nil
}

func (w *wire) u32(field string) (uint32, error) {
	b, err := w.take(4, field)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b), nil
}

func (w *wire) str(field string) ([]byte, error) {
	n, err := w.u32(field + " length")
	if err != nil {
		return nil, err
	}
	if int64(n) > int64(len(*w)) {
		return nil, bad("%s length %d exceeds the %d bytes remaining", field, n, len(*w))
	}
	b := (*w)[:n]
	*w = (*w)[n:]
	return b, nil
}

// strWant reads a string and requires it to equal want; an empty want requires
// an empty string.
func (w *wire) strWant(field, want string) error {
	got, err := w.str(field)
	if err == nil && string(got) != want {
		err = bad("%s is %q, want %q", field, got, want)
	}
	return err
}

func (w wire) done(field string) error {
	if len(w) != 0 {
		return bad("%d trailing bytes after %s", len(w), field)
	}
	return nil
}

// parsedKeyBlob is a decoded public key blob of PROTOCOL.u2f.
type parsedKeyBlob struct {
	key         ed25519.PublicKey
	application []byte
}

func parseKeyBlob(b []byte) (parsedKeyBlob, error) {
	w := wire(b)
	if err := w.strWant("key type", keyType); err != nil {
		return parsedKeyBlob{}, err
	}
	key, err := w.str("Ed25519 public key")
	if err != nil {
		return parsedKeyBlob{}, err
	}
	if len(key) != ed25519.PublicKeySize {
		return parsedKeyBlob{}, bad("Ed25519 public key length is %d, want %d", len(key), ed25519.PublicKeySize)
	}
	app, err := w.str("application")
	if err != nil {
		return parsedKeyBlob{}, err
	}
	if err := w.done("key blob"); err != nil {
		return parsedKeyBlob{}, err
	}
	return parsedKeyBlob{key: ed25519.PublicKey(key), application: app}, nil
}

// sshsig is a parsed signature blob and the pinned signer it names.
type sshsig struct {
	signer     Signer
	ed25519Sig []byte
	counter    uint32
	flags      byte
}

// Verify reports nil only when armored is a valid signature over message by one
// of the pinned signers. The message is hashed while streaming. Every rejection
// wraps ErrBadSignature; a read error from message does not.
func Verify(message io.Reader, armored []byte, pinned []Signer) error {
	sig, err := parseSignature(armored, pinned)
	if err != nil {
		return err
	}
	h := sha512.New()
	if _, err := io.Copy(h, message); err != nil {
		return fmt.Errorf("update: reading the signed file: %w", err)
	}
	data := []byte(magic)
	data = appendWireString(data, []byte(namespace))
	data = appendWireString(data, nil)
	data = appendWireString(data, []byte(hashAlg))
	data = appendWireString(data, h.Sum(nil))

	appHash := sha256.Sum256(sig.signer.application)
	dataHash := sha256.Sum256(data)
	signed := make([]byte, 0, sha256.Size+1+4+sha256.Size)
	signed = append(signed, appHash[:]...)
	signed = append(signed, sig.flags)
	signed = binary.BigEndian.AppendUint32(signed, sig.counter)
	signed = append(signed, dataHash[:]...)

	if !ed25519.Verify(sig.signer.key, signed, sig.ed25519Sig) {
		return bad("Ed25519 signature does not verify")
	}
	return nil
}

// parseSignature checks every field of the signature blob in the ticket order.
func parseSignature(armored []byte, pinned []Signer) (sshsig, error) {
	blob, err := unarmor(armored)
	if err != nil {
		return sshsig{}, err
	}
	w := wire(blob)
	preamble, err := w.take(6, "preamble")
	if err != nil {
		return sshsig{}, err
	}
	if string(preamble) != magic {
		return sshsig{}, bad("preamble is %q, want %q", preamble, magic)
	}
	ver, err := w.u32("version")
	if err != nil {
		return sshsig{}, err
	}
	if ver != version {
		return sshsig{}, bad("version is %d, want %d", ver, version)
	}
	pubBlob, err := w.str("public key blob")
	if err != nil {
		return sshsig{}, err
	}
	if err = w.strWant("namespace", namespace); err != nil {
		return sshsig{}, err
	}
	if err = w.strWant("reserved", ""); err != nil {
		return sshsig{}, err
	}
	if err = w.strWant("hash algorithm", hashAlg); err != nil {
		return sshsig{}, err
	}
	sigBlob, err := w.str("signature")
	if err != nil {
		return sshsig{}, err
	}
	if err = w.done("signature blob"); err != nil {
		return sshsig{}, err
	}
	if _, err = parseKeyBlob(pubBlob); err != nil {
		return sshsig{}, err
	}
	var signer Signer
	pinnedFound := false
	for _, s := range pinned {
		if bytes.Equal(pubBlob, s.blob) {
			signer, pinnedFound = s, true
			break
		}
	}
	if !pinnedFound {
		return sshsig{}, bad("signing key is not pinned")
	}
	inner := wire(sigBlob)
	if err = inner.strWant("signature type", keyType); err != nil {
		return sshsig{}, err
	}
	edSig, err := inner.str("Ed25519 signature")
	if err != nil {
		return sshsig{}, err
	}
	if len(edSig) != ed25519.SignatureSize {
		return sshsig{}, bad("Ed25519 signature length is %d, want %d", len(edSig), ed25519.SignatureSize)
	}
	flags, err := inner.take(1, "flags")
	if err != nil {
		return sshsig{}, err
	}
	counter, err := inner.u32("counter")
	if err != nil {
		return sshsig{}, err
	}
	if err = inner.done("inner signature"); err != nil {
		return sshsig{}, err
	}
	if flags[0] != requiredFlags {
		return sshsig{}, bad("signature flags are 0x%02x, want 0x%02x (user presence and user verification)", flags[0], requiredFlags)
	}
	return sshsig{signer: signer, ed25519Sig: edSig, counter: counter, flags: flags[0]}, nil
}

// unarmor strips the BEGIN and END lines and decodes the standard base64 body.
func unarmor(armored []byte) ([]byte, error) {
	body := armored
	if len(body) > 0 && body[len(body)-1] == '\n' {
		body = body[:len(body)-1]
	}
	lines := bytes.Split(body, []byte("\n"))
	if len(lines) < 3 {
		return nil, bad("armored signature has %d lines, want at least 3", len(lines))
	}
	if string(lines[0]) != armorBegin {
		return nil, bad("armored signature preamble is not %q", armorBegin)
	}
	if string(lines[len(lines)-1]) != armorEnd {
		return nil, bad("armored signature trailer is not %q", armorEnd)
	}
	encoded := make([]byte, 0, len(body))
	for _, line := range lines[1 : len(lines)-1] {
		if !isBase64Line(line) {
			return nil, bad("armored signature body is not standard base64")
		}
		encoded = append(encoded, line...)
	}
	blob, err := base64.StdEncoding.Strict().DecodeString(string(encoded))
	if err != nil {
		return nil, fmt.Errorf("update: decode armored signature: %w", errors.Join(ErrBadSignature, err))
	}
	return blob, nil
}

// isBase64Line reports whether line is non-empty standard base64.
func isBase64Line(line []byte) bool {
	if len(line) == 0 {
		return false
	}
	for _, c := range line {
		if !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/=", rune(c)) {
			return false
		}
	}
	return true
}

// appendWireString appends s framed as an OpenSSH string.
func appendWireString(b, s []byte) []byte {
	//nolint:gosec // G115: s is at most 64 bytes (the namespace, an empty string, the hash name or a SHA-512 sum).
	b = binary.BigEndian.AppendUint32(b, uint32(len(s)))
	return append(b, s...)
}
