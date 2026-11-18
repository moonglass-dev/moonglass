package update

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const (
	testPrincipal = "moonglass-release"
	testNamespace = "moonglass-release"
	testKeyType   = "sk-ssh-ed25519@openssh.com"
	testHash      = "sha512"
)

// testKey is a throwaway sk-ssh-ed25519 key. It is generated at test time so
// no key material is committed.
type testKey struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
	app  string
}

func newTestKey(t testing.TB, app string) testKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: unexpected error %v", err)
	}
	return testKey{pub: pub, priv: priv, app: app}
}

func appendU32(b []byte, v uint32) []byte {
	return binary.BigEndian.AppendUint32(b, v)
}

func appendString(b, s []byte) []byte {
	b = appendU32(b, uint32(len(s)))
	return append(b, s...)
}

// keyBlob builds the public key blob of PROTOCOL.u2f: a type string, the
// Ed25519 public key, the application string, then extra bytes.
func keyBlob(keyType string, pub []byte, app string, extra []byte) []byte {
	var b []byte
	b = appendString(b, []byte(keyType))
	b = appendString(b, pub)
	b = appendString(b, []byte(app))
	return append(b, extra...)
}

// line returns the pinned allowed_signers line for the key.
func (k testKey) line() string {
	blob := keyBlob(testKeyType, k.pub, k.app, nil)
	return testPrincipal + ` namespaces="` + testNamespace + `" ` + testKeyType + " " +
		base64.StdEncoding.EncodeToString(blob)
}

func (k testKey) signer(t testing.TB) Signer {
	t.Helper()
	s, err := ParseSigner(k.line())
	if err != nil {
		t.Fatalf("ParseSigner(%q): unexpected error %v", k.line(), err)
	}
	return s
}

// sigFields holds every field of a signature blob so a test can change one of
// them and sign over its own fields.
type sigFields struct {
	preamble   string
	version    uint32
	keyBlob    []byte
	namespace  string
	reserved   string
	hash       string
	innerType  string
	flags      byte
	counter    uint32
	innerExtra []byte
}

func validFields(k testKey) sigFields {
	return sigFields{
		preamble:  "SSHSIG",
		version:   1,
		keyBlob:   keyBlob(testKeyType, k.pub, k.app, nil),
		namespace: testNamespace,
		reserved:  "",
		hash:      testHash,
		innerType: testKeyType,
		flags:     0x05,
		counter:   0,
	}
}

// sign returns the Ed25519 signature over SHA-256(app) || flags || counter ||
// SHA-256(data), where data is built from this case's namespace, reserved and
// hash fields.
func (f sigFields) sign(k testKey, app string, msg []byte) []byte {
	msgHash := sha512.Sum512(msg)
	var data []byte
	data = append(data, []byte("SSHSIG")...)
	data = appendString(data, []byte(f.namespace))
	data = appendString(data, []byte(f.reserved))
	data = appendString(data, []byte(f.hash))
	data = appendString(data, msgHash[:])

	appHash := sha256.Sum256([]byte(app))
	dataHash := sha256.Sum256(data)

	signed := make([]byte, 0, sha256.Size+1+4+sha256.Size)
	signed = append(signed, appHash[:]...)
	signed = append(signed, f.flags)
	signed = appendU32(signed, f.counter)
	signed = append(signed, dataHash[:]...)
	return ed25519.Sign(k.priv, signed)
}

// blob writes the outer signature blob, field by field.
func (f sigFields) blob(sig []byte) []byte {
	var inner []byte
	inner = appendString(inner, []byte(f.innerType))
	inner = appendString(inner, sig)
	inner = append(inner, f.flags)
	inner = appendU32(inner, f.counter)
	inner = append(inner, f.innerExtra...)

	var blob []byte
	blob = append(blob, []byte(f.preamble)...)
	blob = appendU32(blob, f.version)
	blob = appendString(blob, f.keyBlob)
	blob = appendString(blob, []byte(f.namespace))
	blob = appendString(blob, []byte(f.reserved))
	blob = appendString(blob, []byte(f.hash))
	blob = appendString(blob, inner)
	return blob
}

// armor wraps blob in the SSH SIGNATURE armor, base64 lines of width bytes (0
// for one line), with an optional single trailing newline.
func armor(blob []byte, width int, trailingNewline bool) []byte {
	enc := base64.StdEncoding.EncodeToString(blob)
	var b strings.Builder
	b.WriteString("-----BEGIN SSH SIGNATURE-----\n")
	if width > 0 {
		for len(enc) > width {
			b.WriteString(enc[:width])
			b.WriteString("\n")
			enc = enc[width:]
		}
	}
	b.WriteString(enc)
	b.WriteString("\n")
	b.WriteString("-----END SSH SIGNATURE-----")
	if trailingNewline {
		b.WriteString("\n")
	}
	return []byte(b.String())
}

// armoredSignature builds a valid armored signature from k over msg.
func armoredSignature(k testKey, msg []byte, width int, trailingNewline bool) []byte {
	f := validFields(k)
	return armor(f.blob(f.sign(k, k.app, msg)), width, trailingNewline)
}

func wantBad(t testing.TB, err error, substr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a rejection containing %q, got nil", substr)
	}
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("error does not wrap ErrBadSignature: want ErrBadSignature, got %v", err)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Fatalf("error message: want it to contain %q, got %q", substr, err.Error())
	}
}

func TestVerifyAcceptsEitherPinnedKey(t *testing.T) {
	t.Parallel()
	keys := []testKey{newTestKey(t, "ssh:"), newTestKey(t, "ssh:")}
	messages := [][]byte{
		[]byte("release artifact signed by the primary key"),
		[]byte("release artifact signed by the backup key"),
	}
	for keyIdx := range keys {
		for _, reverse := range []bool{false, true} {
			for _, width := range []int{70, 0} {
				for _, trailingNewline := range []bool{true, false} {
					name := fmt.Sprintf("key %d reverse %v width %d trailing newline %v", keyIdx, reverse, width, trailingNewline)
					t.Run(name, func(t *testing.T) {
						t.Parallel()
						k := keys[keyIdx]
						msg := messages[keyIdx]
						armored := armoredSignature(k, msg, width, trailingNewline)

						pinned := []Signer{keys[0].signer(t), keys[1].signer(t)}
						if reverse {
							pinned[0], pinned[1] = pinned[1], pinned[0]
						}

						if err := Verify(bytes.NewReader(msg), armored, pinned); err != nil {
							t.Errorf("Verify: expected nil, got %v", err)
						}
					})
				}
			}
		}
	}
}

func TestVerifyRejectsUnpinnedKey(t *testing.T) {
	t.Parallel()
	a := newTestKey(t, "ssh:")
	b := newTestKey(t, "ssh:")
	c := newTestKey(t, "ssh:")
	msg := []byte("release artifact")

	tests := []struct {
		name   string
		signed testKey
		pinned []Signer
	}{
		{
			name:   "signing key is not in the pinned list",
			signed: c,
			pinned: []Signer{a.signer(t), b.signer(t)},
		},
		{
			name:   "pinned list is empty",
			signed: a,
			pinned: []Signer{},
		},
		{
			name:   "pinned list is nil",
			signed: a,
			pinned: nil,
		},
		{
			name:   "same Ed25519 key with a different application",
			signed: testKey{pub: a.pub, priv: a.priv, app: "ssh:other"},
			pinned: []Signer{a.signer(t)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := validFields(tt.signed)
			armored := armor(f.blob(f.sign(tt.signed, tt.signed.app, msg)), 70, true)
			wantBad(t, Verify(bytes.NewReader(msg), armored, tt.pinned), "not pinned")
		})
	}
}

func TestVerifyRejectsWrongEnvelope(t *testing.T) {
	t.Parallel()
	k := newTestKey(t, "ssh:")
	msg := []byte("release artifact")

	tests := []struct {
		name   string
		mutate func(f *sigFields)
		want   string
	}{
		{
			name:   "preamble is not SSHSIG",
			mutate: func(f *sigFields) { f.preamble = "SSHSIH" },
			want:   "preamble",
		},
		{
			name:   "version 0",
			mutate: func(f *sigFields) { f.version = 0 },
			want:   "version",
		},
		{
			name:   "version 2",
			mutate: func(f *sigFields) { f.version = 2 },
			want:   "version",
		},
		{
			name:   "namespace file",
			mutate: func(f *sigFields) { f.namespace = "file" },
			want:   "namespace",
		},
		{
			name:   "namespace empty",
			mutate: func(f *sigFields) { f.namespace = "" },
			want:   "namespace",
		},
		{
			name:   "namespace moonglass-release2",
			mutate: func(f *sigFields) { f.namespace = "moonglass-release2" },
			want:   "namespace",
		},
		{
			name:   "reserved is not empty",
			mutate: func(f *sigFields) { f.reserved = "x" },
			want:   "reserved",
		},
		{
			name:   "hash sha256",
			mutate: func(f *sigFields) { f.hash = "sha256" },
			want:   "hash",
		},
		{
			name:   "hash SHA512",
			mutate: func(f *sigFields) { f.hash = "SHA512" },
			want:   "hash",
		},
		{
			name:   "outer key type ssh-ed25519",
			mutate: func(f *sigFields) { f.keyBlob = keyBlob("ssh-ed25519", k.pub, k.app, nil) },
			want:   "key type",
		},
		{
			name:   "inner signature type ssh-ed25519",
			mutate: func(f *sigFields) { f.innerType = "ssh-ed25519" },
			want:   "signature type",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := validFields(k)
			tt.mutate(&f)
			armored := armor(f.blob(f.sign(k, k.app, msg)), 70, true)
			wantBad(t, Verify(bytes.NewReader(msg), armored, []Signer{k.signer(t)}), tt.want)
		})
	}
}

func TestVerifyRequiresExactFlags(t *testing.T) {
	t.Parallel()
	k := newTestKey(t, "ssh:")
	msg := []byte("release artifact")

	tests := []struct {
		flags byte
		ok    bool
	}{
		{flags: 0x05, ok: true},
		{flags: 0x00},
		{flags: 0x01},
		{flags: 0x04},
		{flags: 0x07},
		{flags: 0x25},
		{flags: 0x45},
		{flags: 0x85},
		{flags: 0xFF},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("flags 0x%02x", tt.flags), func(t *testing.T) {
			t.Parallel()
			f := validFields(k)
			f.flags = tt.flags
			armored := armor(f.blob(f.sign(k, k.app, msg)), 70, true)
			err := Verify(bytes.NewReader(msg), armored, []Signer{k.signer(t)})
			if tt.ok {
				if err != nil {
					t.Fatalf("Verify: expected nil for flags 0x%02x, got %v", tt.flags, err)
				}
				return
			}
			wantBad(t, err, "flags")
		})
	}
}

func TestVerifyRejectsTamperedSignature(t *testing.T) {
	t.Parallel()
	k := newTestKey(t, "ssh:")
	msg := []byte("release artifact bytes")
	pinned := []Signer{k.signer(t)}
	f := validFields(k)
	validSig := f.sign(k, k.app, msg)

	if err := Verify(bytes.NewReader(msg), armor(f.blob(validSig), 70, true), pinned); err != nil {
		t.Fatalf("Verify of the untampered signature: expected nil, got %v", err)
	}

	changedMsg := append([]byte(nil), msg...)
	changedMsg[len(changedMsg)-1] ^= 0x01
	appendedMsg := append(append([]byte(nil), msg...), 0x00)

	flagsSigned := f
	flagsSigned.flags = 0x07
	flagsMeanwhileEncoded := f
	flagsMeanwhileEncoded.flags = 0x05

	counterSigned := f
	counterSigned.counter = 7
	counterMeanwhileEncoded := f
	counterMeanwhileEncoded.counter = 8

	flippedSig := append([]byte(nil), validSig...)
	flippedSig[0] ^= 0x01

	tests := []struct {
		name    string
		message []byte
		armored []byte
	}{
		{
			name:    "message has one byte changed",
			message: changedMsg,
			armored: armor(f.blob(validSig), 70, true),
		},
		{
			name:    "message has one byte appended",
			message: appendedMsg,
			armored: armor(f.blob(validSig), 70, true),
		},
		{
			name:    "message is empty",
			message: []byte{},
			armored: armor(f.blob(validSig), 70, true),
		},
		{
			name:    "flags changed after signing",
			message: msg,
			armored: armor(flagsMeanwhileEncoded.blob(flagsSigned.sign(k, k.app, msg)), 70, true),
		},
		{
			name:    "counter changed after signing",
			message: msg,
			armored: armor(counterMeanwhileEncoded.blob(counterSigned.sign(k, k.app, msg)), 70, true),
		},
		{
			name:    "one bit of the Ed25519 signature is flipped",
			message: msg,
			armored: armor(f.blob(flippedSig), 70, true),
		},
		{
			name:    "signed over a different application",
			message: msg,
			armored: armor(f.blob(f.sign(k, "ssh:other", msg)), 70, true),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			wantBad(t, Verify(bytes.NewReader(tt.message), tt.armored, pinned), "does not verify")
		})
	}
}

// malformedInput is a rejected armored signature and a name for it.
type malformedInput struct {
	name    string
	armored []byte
}

// malformedSignatures returns the malformed armored signatures shared by the
// encoding test and the fuzz seeds.
func malformedSignatures(t testing.TB, k testKey) []malformedInput {
	t.Helper()
	f := validFields(k)
	sig := f.sign(k, k.app, []byte("malformed signature message"))
	blob := f.blob(sig)
	good := armor(blob, 70, true)
	body := base64.StdEncoding.EncodeToString(blob)

	wrap := func(body string) []byte {
		return []byte("-----BEGIN SSH SIGNATURE-----\n" + body + "\n-----END SSH SIGNATURE-----\n")
	}

	badPadding := body
	if strings.HasSuffix(badPadding, "=") {
		badPadding = strings.TrimRight(badPadding, "=")
	} else {
		badPadding += "="
	}

	var out []malformedInput
	add := func(name string, armored []byte) {
		out = append(out, malformedInput{name: name, armored: armored})
	}

	add("empty input", nil)
	add("BEGIN line missing", []byte(strings.SplitN(string(good), "\n", 2)[1]))
	add("BEGIN line misspelled", []byte(strings.Replace(string(good), "BEGIN SSH SIGNATURE", "BEGIN SSH SIGNATUR", 1)))
	add("END line missing", []byte("-----BEGIN SSH SIGNATURE-----\n"+body))
	add("END line misspelled", []byte(strings.Replace(string(good), "END SSH SIGNATURE", "END SSH SIGNATUR", 1)))
	add("text before BEGIN", append([]byte("x\n"), good...))
	add("text after END", append(append([]byte(nil), good...), 'x'))
	add("two trailing newlines", append(append([]byte(nil), good...), '\n'))
	add("CRLF line ends", []byte(strings.ReplaceAll(string(good), "\n", "\r\n")))
	add("invalid base64 character", wrap("!"+body))
	add("invalid base64 padding", wrap(badPadding))
	add("space inside a base64 line", wrap(body[:10]+" "+body[10:]))
	add("empty line between base64 lines", []byte(strings.Replace(string(good), "-----\n", "-----\n\n", 1)))

	for i := range len(blob) {
		add(fmt.Sprintf("blob truncated at offset %d", i), armor(blob[:i], 0, true))
	}
	add("one byte appended to the blob", armor(append(append([]byte(nil), blob...), 0x00), 0, true))

	// The outer blob is magic (6 bytes), version (4), then the key blob string.
	withKeyBlobLength := func(n uint32) []byte {
		raw := append([]byte(nil), blob...)
		binary.BigEndian.PutUint32(raw[10:14], n)
		return armor(raw, 0, true)
	}
	add("key blob length one past the bytes remaining", withKeyBlobLength(uint32(len(blob)-14+1)))
	add("key blob length 0xFFFFFFFF", withKeyBlobLength(0xFFFFFFFF))

	withKeyBlob := func(kb []byte) []byte {
		m := f
		m.keyBlob = kb
		return armor(m.blob(sig), 0, true)
	}
	add("31-byte public key", withKeyBlob(keyBlob(testKeyType, k.pub[:31], k.app, nil)))
	add("33-byte public key", withKeyBlob(keyBlob(testKeyType, append(append([]byte(nil), k.pub...), 0x00), k.app, nil)))
	add("trailing byte in the key blob", withKeyBlob(keyBlob(testKeyType, k.pub, k.app, []byte{0x00})))

	add("63-byte Ed25519 signature", armor(f.blob(sig[:63]), 0, true))
	add("65-byte Ed25519 signature", armor(f.blob(append(append([]byte(nil), sig...), 0x00)), 0, true))

	innerTrailing := f
	innerTrailing.innerExtra = []byte{0x00}
	add("trailing byte in the inner signature", armor(innerTrailing.blob(sig), 0, true))

	return out
}

func TestVerifyRejectsMalformedEncoding(t *testing.T) {
	t.Parallel()
	k := newTestKey(t, "ssh:")
	pinned := []Signer{k.signer(t)}
	msg := []byte("malformed signature message")

	for _, tt := range malformedSignatures(t, k) {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			wantBad(t, Verify(bytes.NewReader(msg), tt.armored, pinned), "")
		})
	}
}

func TestParseSignerAcceptsOnlyPinnedForm(t *testing.T) {
	t.Parallel()
	k := newTestKey(t, "ssh:")
	blob := keyBlob(testKeyType, k.pub, k.app, nil)
	encoded := base64.StdEncoding.EncodeToString(blob)
	validLine := testPrincipal + ` namespaces="` + testNamespace + `" ` + testKeyType + " " + encoded

	t.Run("pinned form parses and verifies", func(t *testing.T) {
		t.Parallel()
		signer, err := ParseSigner(validLine)
		if err != nil {
			t.Fatalf("ParseSigner(%q): expected nil, got %v", validLine, err)
		}
		msg := []byte("release artifact")
		if err := Verify(bytes.NewReader(msg), armoredSignature(k, msg, 70, true), []Signer{signer}); err != nil {
			t.Fatalf("Verify with the parsed signer: expected nil, got %v", err)
		}
	})

	tests := []struct {
		name string
		line string
	}{
		{
			name: "another principal",
			line: "release" + validLine[len(testPrincipal):],
		},
		{
			name: "namespaces option missing",
			line: testPrincipal + " " + testKeyType + " " + encoded,
		},
		{
			name: "namespaces option is different",
			line: testPrincipal + ` namespaces="file" ` + testKeyType + " " + encoded,
		},
		{
			name: "extra cert-authority option",
			line: testPrincipal + ` cert-authority namespaces="` + testNamespace + `" ` + testKeyType + " " + encoded,
		},
		{
			name: "trailing comment",
			line: validLine + " comment",
		},
		{
			name: "key type ssh-ed25519",
			line: testPrincipal + ` namespaces="` + testNamespace + `" ssh-ed25519 ` +
				base64.StdEncoding.EncodeToString(keyBlob("ssh-ed25519", k.pub, k.app, nil)),
		},
		{
			name: "key type sk-ecdsa-sha2-nistp256@openssh.com",
			line: testPrincipal + ` namespaces="` + testNamespace + `" sk-ecdsa-sha2-nistp256@openssh.com ` +
				base64.StdEncoding.EncodeToString(keyBlob("sk-ecdsa-sha2-nistp256@openssh.com", k.pub, k.app, nil)),
		},
		{
			name: "key blob type differs from the line",
			line: testPrincipal + ` namespaces="` + testNamespace + `" ` + testKeyType + " " +
				base64.StdEncoding.EncodeToString(keyBlob("ssh-ed25519", k.pub, k.app, nil)),
		},
		{
			name: "invalid base64",
			line: testPrincipal + ` namespaces="` + testNamespace + `" ` + testKeyType + " AAAA!AAA",
		},
		{
			name: "key blob with a trailing byte",
			line: testPrincipal + ` namespaces="` + testNamespace + `" ` + testKeyType + " " +
				base64.StdEncoding.EncodeToString(keyBlob(testKeyType, k.pub, k.app, []byte{0x00})),
		},
		{
			name: "31-byte public key",
			line: testPrincipal + ` namespaces="` + testNamespace + `" ` + testKeyType + " " +
				base64.StdEncoding.EncodeToString(keyBlob(testKeyType, k.pub[:31], k.app, nil)),
		},
		{
			name: "OWNER-PASTE line",
			line: "OWNER-PASTE primary release public key here",
		},
		{
			name: "empty input",
			line: "",
		},
		{
			name: "double space between fields",
			line: strings.Replace(validLine, `" `+testKeyType, `"  `+testKeyType, 1),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseSigner(tt.line)
			wantBad(t, err, "")
		})
	}
}

func FuzzParseSignature(f *testing.F) {
	k := newTestKey(f, "ssh:")
	msg := []byte("fuzzed release artifact")
	fields := validFields(k)
	f.Add(armor(fields.blob(fields.sign(k, k.app, msg)), 70, true))
	for _, tt := range malformedSignatures(f, k) {
		f.Add(tt.armored)
	}
	pinned := []Signer{k.signer(f)}

	f.Fuzz(func(t *testing.T, data []byte) {
		if _, err := parseSignature(data, pinned); err != nil && !errors.Is(err, ErrBadSignature) {
			t.Fatalf("parseSignature error does not wrap ErrBadSignature: %v", err)
		}
		if err := Verify(bytes.NewReader(msg), data, pinned); err != nil && !errors.Is(err, ErrBadSignature) {
			t.Fatalf("Verify error does not wrap ErrBadSignature: %v", err)
		}
	})
}
