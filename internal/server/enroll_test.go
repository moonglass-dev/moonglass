package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/keys"
	"moonglass.dev/moonglass/internal/protocol"
	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
	"moonglass.dev/moonglass/internal/store"
	"moonglass.dev/moonglass/internal/store/sqlite"
)

var testNow = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

type fixture struct {
	srv *Server
	st  store.Store
	ts  *httptest.Server
	org string
	dir string
}

func newFixture(t *testing.T) *fixture {
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
	s, err := New(st, dir, func() time.Time { return testNow })
	if err != nil {
		t.Fatalf("New: unexpected error %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return &fixture{srv: s, st: st, ts: ts, org: org, dir: dir}
}

func (f *fixture) issue(t *testing.T) string {
	t.Helper()
	tok, err := f.srv.IssueEnrolToken(context.Background(), f.org)
	if err != nil {
		t.Fatalf("IssueEnrolToken: unexpected error %v", err)
	}
	return tok
}

func (f *fixture) servers(t *testing.T) []store.Server {
	t.Helper()
	got, err := f.st.Servers(context.Background(), f.org)
	if err != nil {
		t.Fatalf("Servers: unexpected error %v", err)
	}
	return got
}

func tokenID(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newRequest(t *testing.T, token string) *agentv1.EnrollRequest {
	t.Helper()
	id, err := keys.GenerateAgentIdentity()
	if err != nil {
		t.Fatalf("GenerateAgentIdentity: unexpected error %v", err)
	}
	return &agentv1.EnrollRequest{
		TokenId:               tokenID(token),
		AgentSigningPublicKey: id.SigningPublic,
		AgentAgeRecipient:     id.AgreePublic,
	}
}

// post sends req with the given Authorization header value (empty means none)
// and returns the HTTP status and, when the status is 200, the typed response.
func post(t *testing.T, url, authorization string, req *agentv1.EnrollRequest) (int, *agentv1.EnrollResponse) {
	t.Helper()
	env, err := protocol.Wrap(agentv1.MessageType_ENROLL_REQUEST, "enroll-test", req)
	if err != nil {
		t.Fatalf("protocol.Wrap: unexpected error %v", err)
	}
	body, err := protocol.Marshal(env)
	if err != nil {
		t.Fatalf("protocol.Marshal: unexpected error %v", err)
	}
	hreq, err := http.NewRequest(http.MethodPost, url+"/v1/enroll", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("http.NewRequest: unexpected error %v", err)
	}
	if authorization != "" {
		hreq.Header.Set("Authorization", authorization)
	}
	res, err := http.DefaultClient.Do(hreq)
	if err != nil {
		t.Fatalf("POST /v1/enroll: unexpected error %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read response: unexpected error %v", err)
	}
	if res.StatusCode != http.StatusOK {
		return res.StatusCode, nil
	}
	renv, err := protocol.Unmarshal(raw)
	if err != nil {
		t.Fatalf("protocol.Unmarshal response: unexpected error %v", err)
	}
	var resp agentv1.EnrollResponse
	if err := protocol.Open(renv, &resp); err != nil {
		t.Fatalf("protocol.Open EnrollResponse: unexpected error %v", err)
	}
	return res.StatusCode, &resp
}

func TestEnrollRejectsInvalidToken(t *testing.T) {
	validShape := "mg_" + strings.Repeat("a", 52)

	tests := []struct {
		name string
		// setup returns the bearer value and the request to send.
		setup func(t *testing.T, f *fixture) (auth string, req *agentv1.EnrollRequest)
	}{
		{"unknown token", func(t *testing.T, f *fixture) (string, *agentv1.EnrollRequest) {
			return "Bearer " + validShape, newRequest(t, validShape)
		}},
		{"missing authorization header", func(t *testing.T, f *fixture) (string, *agentv1.EnrollRequest) {
			return "", newRequest(t, f.issue(t))
		}},
		{"bearer without mg_ prefix", func(t *testing.T, f *fixture) (string, *agentv1.EnrollRequest) {
			bad := "xx_" + strings.Repeat("a", 52)
			return "Bearer " + bad, newRequest(t, bad)
		}},
		{"bearer of wrong length", func(t *testing.T, f *fixture) (string, *agentv1.EnrollRequest) {
			bad := "mg_" + strings.Repeat("a", 10)
			return "Bearer " + bad, newRequest(t, bad)
		}},
		{"expired token", func(t *testing.T, f *fixture) (string, *agentv1.EnrollRequest) {
			tok := "mg_" + strings.Repeat("b", 52)
			if err := f.st.AddEnrolmentToken(context.Background(), f.org, store.HashToken(tok), testNow.Add(-time.Minute)); err != nil {
				t.Fatalf("AddEnrolmentToken: unexpected error %v", err)
			}
			return "Bearer " + tok, newRequest(t, tok)
		}},
		{"token_id does not match bearer", func(t *testing.T, f *fixture) (string, *agentv1.EnrollRequest) {
			tok := f.issue(t)
			req := newRequest(t, tok)
			req.TokenId = tokenID(validShape)
			return "Bearer " + tok, req
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			log.SetOutput(&logs)
			t.Cleanup(func() { log.SetOutput(io.Discard) })

			f := newFixture(t)
			auth, req := tt.setup(t, f)

			status, resp := post(t, f.ts.URL, auth, req)

			if status != http.StatusOK {
				t.Fatalf("status: expected %d, got %d", http.StatusOK, status)
			}
			if resp.GetResult() != agentv1.EnrollResult_TOKEN_INVALID {
				t.Errorf("result: expected %v, got %v", agentv1.EnrollResult_TOKEN_INVALID, resp.GetResult())
			}
			if len(resp.GetControlPlanePublicKey()) != 0 {
				t.Errorf("control_plane_public_key: expected empty, got %d bytes", len(resp.GetControlPlanePublicKey()))
			}
			if got := f.servers(t); len(got) != 0 {
				t.Errorf("registered servers: expected 0, got %d (%v)", len(got), got)
			}
			secret := strings.TrimPrefix(auth, "Bearer ")
			if secret != "" && strings.Contains(logs.String(), secret) {
				t.Errorf("log output contains the token secret: %q", logs.String())
			}
		})
	}
}

func TestEnrollStoresAgeRecipient(t *testing.T) {
	f := newFixture(t)
	tok := f.issue(t)
	req := newRequest(t, tok)

	status, resp := post(t, f.ts.URL, "Bearer "+tok, req)
	if status != http.StatusOK {
		t.Fatalf("status: expected %d, got %d", http.StatusOK, status)
	}
	if resp.GetResult() != agentv1.EnrollResult_ACCEPTED {
		t.Fatalf("result: expected %v, got %v", agentv1.EnrollResult_ACCEPTED, resp.GetResult())
	}

	want, err := keys.AgreeRecipient(req.GetAgentAgeRecipient())
	if err != nil {
		t.Fatalf("keys.AgreeRecipient: unexpected error %v", err)
	}
	if !strings.HasPrefix(want.String(), "age1") {
		t.Errorf("recipient = %q, want an age1 prefix", want.String())
	}

	got := f.servers(t)
	if len(got) != 1 {
		t.Fatalf("registered servers: expected 1, got %d (%v)", len(got), got)
	}
	if got[0].AgeRecipient != want.String() {
		t.Errorf("servers[0].AgeRecipient = %q, want %q", got[0].AgeRecipient, want.String())
	}

	srv, err := f.st.ServerByPublicKey(context.Background(), hex.EncodeToString(req.GetAgentSigningPublicKey()))
	if err != nil {
		t.Fatalf("ServerByPublicKey: unexpected error %v", err)
	}
	if srv.AgeRecipient != want.String() {
		t.Errorf("ServerByPublicKey AgeRecipient = %q, want %q", srv.AgeRecipient, want.String())
	}
}

func TestEnrollDuplicateKeyStoresNoRecipient(t *testing.T) {
	f := newFixture(t)
	tokA, tokB := f.issue(t), f.issue(t)
	reqA := newRequest(t, tokA)
	reqB := newRequest(t, tokB)
	reqB.AgentSigningPublicKey = reqA.AgentSigningPublicKey

	if bytes.Equal(reqA.AgentAgeRecipient, reqB.AgentAgeRecipient) {
		t.Fatalf("test setup: the two requests must carry different age recipients")
	}

	if status, resp := post(t, f.ts.URL, "Bearer "+tokA, reqA); status != http.StatusOK || resp.GetResult() != agentv1.EnrollResult_ACCEPTED {
		t.Fatalf("first enroll: expected 200 ACCEPTED, got %d %v", status, resp.GetResult())
	}
	if status, resp := post(t, f.ts.URL, "Bearer "+tokB, reqB); status != http.StatusOK || resp.GetResult() != agentv1.EnrollResult_TOKEN_INVALID {
		t.Fatalf("second enroll: expected 200 %v, got %d %v", agentv1.EnrollResult_TOKEN_INVALID, status, resp.GetResult())
	}

	first, err := keys.AgreeRecipient(reqA.GetAgentAgeRecipient())
	if err != nil {
		t.Fatalf("keys.AgreeRecipient(reqA): unexpected error %v", err)
	}
	second, err := keys.AgreeRecipient(reqB.GetAgentAgeRecipient())
	if err != nil {
		t.Fatalf("keys.AgreeRecipient(reqB): unexpected error %v", err)
	}

	got := f.servers(t)
	if len(got) != 1 {
		t.Fatalf("registered servers: expected 1, got %d (%v)", len(got), got)
	}
	if got[0].AgeRecipient != first.String() {
		t.Errorf("servers[0].AgeRecipient = %q, want the first recipient %q", got[0].AgeRecipient, first.String())
	}
	if got[0].AgeRecipient == second.String() {
		t.Errorf("servers[0].AgeRecipient = %q, want the first recipient, not the second %q", got[0].AgeRecipient, second.String())
	}
}

func TestEnrollRejectsReplay(t *testing.T) {
	t.Run("same request twice", func(t *testing.T) {
		f := newFixture(t)
		tok := f.issue(t)
		req := newRequest(t, tok)

		status, first := post(t, f.ts.URL, "Bearer "+tok, req)
		if status != http.StatusOK || first.GetResult() != agentv1.EnrollResult_ACCEPTED {
			t.Fatalf("first enroll: expected 200 ACCEPTED, got %d %v", status, first.GetResult())
		}
		if n := len(first.GetControlPlanePublicKey()); n != 32 {
			t.Errorf("control_plane_public_key length: expected 32, got %d", n)
		}

		status, second := post(t, f.ts.URL, "Bearer "+tok, req)
		if status != http.StatusOK {
			t.Fatalf("second enroll status: expected %d, got %d", http.StatusOK, status)
		}
		if second.GetResult() != agentv1.EnrollResult_TOKEN_USED {
			t.Errorf("second result: expected %v, got %v", agentv1.EnrollResult_TOKEN_USED, second.GetResult())
		}
		if len(second.GetControlPlanePublicKey()) != 0 {
			t.Errorf("second control_plane_public_key: expected empty, got %d bytes", len(second.GetControlPlanePublicKey()))
		}
		if got := f.servers(t); len(got) != 1 {
			t.Errorf("registered servers: expected 1, got %d", len(got))
		}
	})

	t.Run("duplicate agent key with a second token", func(t *testing.T) {
		f := newFixture(t)
		tokA, tokB := f.issue(t), f.issue(t)
		reqA := newRequest(t, tokA)
		reqB := newRequest(t, tokB)
		reqB.AgentSigningPublicKey = reqA.AgentSigningPublicKey

		if _, resp := post(t, f.ts.URL, "Bearer "+tokA, reqA); resp.GetResult() != agentv1.EnrollResult_ACCEPTED {
			t.Fatalf("first enroll: expected ACCEPTED, got %v", resp.GetResult())
		}
		_, resp := post(t, f.ts.URL, "Bearer "+tokB, reqB)
		if resp.GetResult() != agentv1.EnrollResult_TOKEN_INVALID {
			t.Errorf("duplicate key result: expected %v, got %v", agentv1.EnrollResult_TOKEN_INVALID, resp.GetResult())
		}
		if got := f.servers(t); len(got) != 1 {
			t.Errorf("registered servers: expected 1, got %d", len(got))
		}
		infos, err := f.st.EnrolmentTokens(context.Background(), f.org)
		if err != nil {
			t.Fatalf("EnrolmentTokens: unexpected error %v", err)
		}
		used := 0
		for _, i := range infos {
			if i.Used {
				used++
			}
		}
		if used != 2 {
			t.Errorf("used tokens: expected 2 (the second stays spent), got %d", used)
		}
	})

	t.Run("control-plane identity survives reopening", func(t *testing.T) {
		f := newFixture(t)
		tok1 := f.issue(t)
		_, r1 := post(t, f.ts.URL, "Bearer "+tok1, newRequest(t, tok1))

		s2, err := New(f.st, f.dir, func() time.Time { return testNow })
		if err != nil {
			t.Fatalf("New (reopen): unexpected error %v", err)
		}
		ts2 := httptest.NewServer(s2.Handler())
		defer ts2.Close()
		tok2, err := s2.IssueEnrolToken(context.Background(), f.org)
		if err != nil {
			t.Fatalf("IssueEnrolToken: unexpected error %v", err)
		}
		_, r2 := post(t, ts2.URL, "Bearer "+tok2, newRequest(t, tok2))

		if r1.GetResult() != agentv1.EnrollResult_ACCEPTED || r2.GetResult() != agentv1.EnrollResult_ACCEPTED {
			t.Fatalf("results: expected ACCEPTED twice, got %v and %v", r1.GetResult(), r2.GetResult())
		}
		if !bytes.Equal(r1.GetControlPlanePublicKey(), r2.GetControlPlanePublicKey()) {
			t.Errorf("control-plane key changed across reopen: %x then %x", r1.GetControlPlanePublicKey(), r2.GetControlPlanePublicKey())
		}
	})
}
