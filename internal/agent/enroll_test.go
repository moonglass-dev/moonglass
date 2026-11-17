package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/keys"
	"moonglass.dev/moonglass/internal/protocol"
	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
	"moonglass.dev/moonglass/internal/server"
	"moonglass.dev/moonglass/internal/store"
	"moonglass.dev/moonglass/internal/store/sqlite"
)

// Data-dir layout (docs/specs/enrolment.md): raw key bytes, mode 0600.
const (
	signingFile = "agent-signing.key" // raw 64-byte Ed25519 private key
	agreeFile   = "agent-agree.key"   // raw 32-byte X25519 private key
	pinFile     = "control-plane.pub" // raw 32-byte control-plane public key
	fingerFile  = "fingerprint"       // lowercase hex, optional trailing newline
)

type env struct {
	srv *server.Server
	st  store.Store
	ts  *httptest.Server
	org string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	st, err := sqlite.Open(ctx, filepath.Join(dir, "server.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: unexpected error %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	org, err := st.CreateOrg(ctx)
	if err != nil {
		t.Fatalf("CreateOrg: unexpected error %v", err)
	}
	s, err := server.New(st, dir, time.Now)
	if err != nil {
		t.Fatalf("server.New: unexpected error %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return &env{srv: s, st: st, ts: ts, org: org}
}

func (e *env) issue(t *testing.T) string {
	t.Helper()
	tok, err := e.srv.IssueEnrolToken(context.Background(), e.org)
	if err != nil {
		t.Fatalf("IssueEnrolToken: unexpected error %v", err)
	}
	return tok
}

func (e *env) servers(t *testing.T) []store.Server {
	t.Helper()
	got, err := e.st.Servers(context.Background(), e.org)
	if err != nil {
		t.Fatalf("Servers: unexpected error %v", err)
	}
	return got
}

func readFile(t *testing.T, dir, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: unexpected error %v", name, err)
	}
	return b
}

// snapshot returns every file under dir keyed by relative name.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: unexpected error %v", err)
	}
	for _, e := range entries {
		out[e.Name()] = string(readFile(t, dir, e.Name()))
	}
	return out
}

// rawEnroll posts an arbitrary request and returns the status and, for 200s, the response.
func rawEnroll(t *testing.T, url, bearer string, req *agentv1.EnrollRequest) (int, *agentv1.EnrollResponse) {
	t.Helper()
	wrapped, err := protocol.Wrap(agentv1.MessageType_ENROLL_REQUEST, "raw", req)
	if err != nil {
		t.Fatalf("protocol.Wrap: unexpected error %v", err)
	}
	body, err := protocol.Marshal(wrapped)
	if err != nil {
		t.Fatalf("protocol.Marshal: unexpected error %v", err)
	}
	hreq, err := http.NewRequest(http.MethodPost, url+"/v1/enroll", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("http.NewRequest: unexpected error %v", err)
	}
	hreq.Header.Set("Authorization", "Bearer "+bearer)
	res, err := http.DefaultClient.Do(hreq)
	if err != nil {
		t.Fatalf("POST: unexpected error %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: unexpected error %v", err)
	}
	if res.StatusCode != http.StatusOK {
		return res.StatusCode, nil
	}
	renv, err := protocol.Unmarshal(raw)
	if err != nil {
		t.Fatalf("protocol.Unmarshal: unexpected error %v", err)
	}
	var resp agentv1.EnrollResponse
	if err := protocol.Open(renv, &resp); err != nil {
		t.Fatalf("protocol.Open: unexpected error %v", err)
	}
	return res.StatusCode, &resp
}

func TestEnrollAcceptsAndPins(t *testing.T) {
	e := newEnv(t)
	tok := e.issue(t)
	dir := t.TempDir()

	fp, err := Enroll(context.Background(), e.ts.Client(), dir, e.ts.URL, tok)
	if err != nil {
		t.Fatalf("Enroll: unexpected error %v", err)
	}

	priv := ed25519.PrivateKey(readFile(t, dir, signingFile))
	if len(priv) != ed25519.PrivateKeySize {
		t.Fatalf("%s length: expected %d, got %d", signingFile, ed25519.PrivateKeySize, len(priv))
	}
	pub := priv.Public().(ed25519.PublicKey)
	wantFP, err := keys.Fingerprint(pub)
	if err != nil {
		t.Fatalf("Fingerprint: unexpected error %v", err)
	}
	if fp != wantFP {
		t.Errorf("returned fingerprint: expected %q, got %q", wantFP, fp)
	}
	if got := strings.TrimSpace(string(readFile(t, dir, fingerFile))); got != wantFP {
		t.Errorf("%s: expected %q, got %q", fingerFile, wantFP, got)
	}
	if n := len(readFile(t, dir, agreeFile)); n != 32 {
		t.Errorf("%s length: expected 32, got %d", agreeFile, n)
	}

	got := e.servers(t)
	if len(got) != 1 {
		t.Fatalf("registered servers: expected 1, got %d", len(got))
	}
	if want := hex.EncodeToString(pub); got[0].PublicKey != want {
		t.Errorf("registered public key: expected %q, got %q", want, got[0].PublicKey)
	}

	// The pin equals the control-plane key the server presents.
	tok2 := e.issue(t)
	id, err := keys.GenerateAgentIdentity()
	if err != nil {
		t.Fatalf("GenerateAgentIdentity: unexpected error %v", err)
	}
	sum := sha256.Sum256([]byte(tok2))
	status, resp := rawEnroll(t, e.ts.URL, tok2, &agentv1.EnrollRequest{
		TokenId:               hex.EncodeToString(sum[:]),
		AgentSigningPublicKey: id.SigningPublic,
		AgentAgeRecipient:     id.AgreePublic,
	})
	if status != http.StatusOK {
		t.Fatalf("raw enroll status: expected 200, got %d", status)
	}
	if pin := readFile(t, dir, pinFile); !bytes.Equal(pin, resp.GetControlPlanePublicKey()) {
		t.Errorf("pin: expected %x, got %x", resp.GetControlPlanePublicKey(), pin)
	}

	for _, name := range []string{signingFile, agreeFile, pinFile, fingerFile} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("stat %s: unexpected error %v", name, err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s mode: expected %o, got %o", name, 0o600, perm)
		}
	}
}

func TestEnrollReplayFails(t *testing.T) {
	e := newEnv(t)
	tok := e.issue(t)
	ctx := context.Background()
	first := t.TempDir()

	if _, err := Enroll(ctx, e.ts.Client(), first, e.ts.URL, tok); err != nil {
		t.Fatalf("first Enroll: unexpected error %v", err)
	}
	before := snapshot(t, first)

	tests := []struct {
		name string
		dir  string
	}{
		{"fresh data dir", t.TempDir()},
		{"already enrolled data dir", first},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fp, err := Enroll(ctx, e.ts.Client(), tt.dir, e.ts.URL, tok)

			if !errors.Is(err, ErrTokenUsed) {
				t.Errorf("error: expected errors.Is ErrTokenUsed, got %v", err)
			}
			if fp != "" {
				t.Errorf("fingerprint: expected empty, got %q", fp)
			}
			if got := e.servers(t); len(got) != 1 {
				t.Errorf("registered servers: expected 1, got %d", len(got))
			}
		})
	}

	if after := snapshot(t, first); len(after) != len(before) {
		t.Errorf("enrolled data dir files: expected %d, got %d", len(before), len(after))
	} else {
		for name, want := range before {
			if after[name] != want {
				t.Errorf("%s changed after replay", name)
			}
		}
	}
	if left := snapshot(t, tests[0].dir); len(left) != 0 {
		t.Errorf("failed enroll left %d files in a fresh data dir", len(left))
	}
}

func TestEnrollUnwritableDataDirDoesNotConsumeToken(t *testing.T) {
	e := newEnv(t)
	tok := e.issue(t)
	ctx := context.Background()
	// A regular file as the parent makes MkdirAll fail even for root.
	parent := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(parent, nil, 0o600); err != nil {
		t.Fatalf("WriteFile: unexpected error %v", err)
	}

	fp, err := Enroll(ctx, e.ts.Client(), filepath.Join(parent, "agent"), e.ts.URL, tok)

	if err == nil {
		t.Errorf("Enroll into an unwritable data dir: expected an error, got fingerprint %q", fp)
	}
	if got := e.servers(t); len(got) != 0 {
		t.Errorf("registered servers: expected 0, got %d", len(got))
	}
	dir := t.TempDir()
	if _, err := Enroll(ctx, e.ts.Client(), dir, e.ts.URL, tok); err != nil {
		t.Errorf("Enroll with the same token into a writable dir: unexpected error %v", err)
	}
}

func TestEnrollDoesNotConsumeTokenOnMalformedKeys(t *testing.T) {
	e := newEnv(t)
	tok := e.issue(t)
	good, err := keys.GenerateAgentIdentity()
	if err != nil {
		t.Fatalf("GenerateAgentIdentity: unexpected error %v", err)
	}
	sum := sha256.Sum256([]byte(tok))
	tokenID := hex.EncodeToString(sum[:])

	tests := []struct {
		name    string
		signing []byte
		agree   []byte
	}{
		{"empty signing key", nil, good.AgreePublic},
		{"31-byte signing key", good.SigningPublic[:31], good.AgreePublic},
		{"33-byte signing key", append(append([]byte{}, good.SigningPublic...), 0), good.AgreePublic},
		{"empty agree key", good.SigningPublic, nil},
		{"31-byte agree key", good.SigningPublic, good.AgreePublic[:31]},
		{"33-byte agree key", good.SigningPublic, append(append([]byte{}, good.AgreePublic...), 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, _ := rawEnroll(t, e.ts.URL, tok, &agentv1.EnrollRequest{
				TokenId:               tokenID,
				AgentSigningPublicKey: tt.signing,
				AgentAgeRecipient:     tt.agree,
			})

			if status != http.StatusBadRequest {
				t.Errorf("status: expected %d, got %d", http.StatusBadRequest, status)
			}
			if got := e.servers(t); len(got) != 0 {
				t.Errorf("registered servers: expected 0, got %d", len(got))
			}
			infos, err := e.st.EnrolmentTokens(context.Background(), e.org)
			if err != nil {
				t.Fatalf("EnrolmentTokens: unexpected error %v", err)
			}
			for _, i := range infos {
				if i.Used {
					t.Errorf("token Used: expected false, got true")
				}
			}
		})
	}

	if _, err := Enroll(context.Background(), e.ts.Client(), t.TempDir(), e.ts.URL, tok); err != nil {
		t.Errorf("Enroll after malformed attempts: expected success, got %v", err)
	}
}
