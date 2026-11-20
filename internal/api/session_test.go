package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/registry"
	"moonglass.dev/moonglass/internal/server"
	"moonglass.dev/moonglass/internal/store"
	"moonglass.dev/moonglass/internal/store/sqlite"
)

const (
	sessionCookieName = "__Host-mg_session"
	sessionOrgHeader  = "Moonglass-Org"

	// bothCredentialsDetail is the fixed 401 detail when a request sends both
	// credentials.
	bothCredentialsDetail = "send a bearer token or a session cookie, not both"
)

// sessionCountingStore counts the credential lookups a request makes, so a
// refusal before either check is observable.
type sessionCountingStore struct {
	store.Store
	tokenCalls   int
	sessionCalls int
}

func (s *sessionCountingStore) TokenByHash(ctx context.Context, tokenHash string) (store.Token, store.Role, error) {
	s.tokenCalls++
	return s.Store.TokenByHash(ctx, tokenHash)
}

func (s *sessionCountingStore) SessionByHash(ctx context.Context, sessionHash string, now time.Time) (store.Session, error) {
	s.sessionCalls++
	return s.Store.SessionByHash(ctx, sessionHash, now)
}

// sessionEnv is the control-plane HTTP listener over a store that counts
// credential lookups, with one org, one owner user, an owner token and a live
// session of that user.
type sessionEnv struct {
	srv     *server.Server
	st      store.Store
	counted *sessionCountingStore
	orgID   string
	userID  string
	token   string
	session store.Session
	text    string
}

func newSessionEnv(t *testing.T) *sessionEnv {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	st, err := sqlite.Open(ctx, filepath.Join(dir, "server.db"))
	if err != nil {
		t.Fatalf("sqlite.Open error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	counted := &sessionCountingStore{Store: st}
	now := time.Now()
	srv, err := server.New(counted, dir, func() time.Time { return now })
	if err != nil {
		t.Fatalf("server.New error = %v, want nil", err)
	}
	orgID, err := st.CreateOrg(ctx)
	if err != nil {
		t.Fatalf("CreateOrg error = %v, want nil", err)
	}
	userID, err := st.UserForIdentity(ctx, "owner@example.com")
	if err != nil {
		t.Fatalf("UserForIdentity error = %v, want nil", err)
	}
	if err := st.AddMembership(ctx, orgID, userID, store.RoleOwner); err != nil {
		t.Fatalf("AddMembership error = %v, want nil", err)
	}
	token, err := srv.MintOwnerToken(ctx, orgID, userID, "test", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("MintOwnerToken error = %v, want nil", err)
	}
	text, err := registry.NewToken()
	if err != nil {
		t.Fatalf("NewToken error = %v, want nil", err)
	}
	session, err := st.AddSession(ctx, userID, store.HashToken(text), now.Add(14*24*time.Hour))
	if err != nil {
		t.Fatalf("AddSession error = %v, want nil", err)
	}
	return &sessionEnv{srv: srv, st: st, counted: counted, orgID: orgID, userID: userID, token: token, session: session, text: text}
}

// newOrg creates another org of the store, without adding the user to it.
func (e *sessionEnv) newOrg(t *testing.T) string {
	t.Helper()
	orgID, err := e.st.CreateOrg(context.Background())
	if err != nil {
		t.Fatalf("CreateOrg error = %v, want nil", err)
	}
	return orgID
}

// get sends GET /v1/servers with the credentials the case asks for.
func (e *sessionEnv) get(t *testing.T, authorization, org string, cookie bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/servers", nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	if org != "" {
		req.Header.Set(sessionOrgHeader, org)
	}
	if cookie {
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: e.text})
	}
	rec := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func (e *sessionEnv) events(t *testing.T, orgID string) []store.AuditEvent {
	t.Helper()
	evs, err := e.st.AuditEvents(context.Background(), orgID)
	if err != nil {
		t.Fatalf("AuditEvents(%s) error = %v, want nil", orgID, err)
	}
	return evs
}

// sessionProblem is the problem document's detail.
type sessionProblem struct {
	Detail string `json:"detail"`
}

func sessionDetail(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var p sessionProblem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode response body %q as problem+json: %v", rec.Body.String(), err)
	}
	return p.Detail
}

func TestBearerAndSessionTogetherIs401(t *testing.T) {
	tests := []struct {
		name      string
		malformed bool
		org       bool
	}{
		{name: "bearer and cookie"},
		{name: "bearer and cookie with Moonglass-Org", org: true},
		{name: "malformed bearer and cookie", malformed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newSessionEnv(t)
			authorization := "Bearer " + e.token
			if tt.malformed {
				authorization = "Bearer not-a-token"
			}
			org := ""
			if tt.org {
				org = e.orgID
			}

			rec := e.get(t, authorization, org, true)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("GET /v1/servers with both credentials: status = %d, want %d (body %q)", rec.Code, http.StatusUnauthorized, rec.Body.String())
			}
			if detail := sessionDetail(t, rec); detail != bothCredentialsDetail {
				t.Errorf("detail = %q, want %q", detail, bothCredentialsDetail)
			}
			if e.counted.tokenCalls != 0 {
				t.Errorf("TokenByHash calls = %d, want 0 (both credentials are refused before either is checked)", e.counted.tokenCalls)
			}
			if e.counted.sessionCalls != 0 {
				t.Errorf("SessionByHash calls = %d, want 0 (both credentials are refused before either is checked)", e.counted.sessionCalls)
			}
			if evs := e.events(t, e.orgID); len(evs) != 0 {
				t.Errorf("audit events = %d, want 0 (%v)", len(evs), evs)
			}
			for _, secret := range []string{e.token, e.text} {
				if strings.Contains(rec.Body.String(), secret) {
					t.Errorf("response body = %q, want it not to hold a credential", rec.Body.String())
				}
			}
		})
	}
}

func TestSessionNeedsMemberOrg(t *testing.T) {
	tests := []struct {
		name    string
		orgKind string
		want    int
	}{
		{name: "cookie without Moonglass-Org", orgKind: "", want: http.StatusUnauthorized},
		{name: "cookie with an org the user is not a member of", orgKind: "other", want: http.StatusUnauthorized},
		{name: "cookie with the member org", orgKind: "member", want: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newSessionEnv(t)
			var org string
			switch tt.orgKind {
			case "other":
				org = e.newOrg(t)
			case "member":
				org = e.orgID
			}

			rec := e.get(t, "", org, true)

			if rec.Code != tt.want {
				t.Fatalf("GET /v1/servers with a session cookie and Moonglass-Org %q: status = %d, want %d (body %q)", org, rec.Code, tt.want, rec.Body.String())
			}
			if tt.want == http.StatusOK {
				evs := e.events(t, org)
				if len(evs) != 1 {
					t.Fatalf("audit events = %d, want 1 (%v)", len(evs), evs)
				}
				if evs[0].SessionID != e.session.ID {
					t.Errorf("audit event SessionID = %q, want the session id %q", evs[0].SessionID, e.session.ID)
				}
				if evs[0].TokenID != "" {
					t.Errorf("audit event TokenID = %q, want empty", evs[0].TokenID)
				}
				if evs[0].StepUpID != "" {
					t.Errorf("audit event StepUpID = %q, want empty", evs[0].StepUpID)
				}
				return
			}
			if detail := sessionDetail(t, rec); detail == "" {
				t.Errorf("detail = empty, want a 401 explanation")
			}
			for _, secret := range []string{e.text, org} {
				if secret != "" && strings.Contains(rec.Body.String(), secret) {
					t.Errorf("401 body = %q, want it not to hold the session value or the Moonglass-Org value %q", rec.Body.String(), secret)
				}
			}
			if evs := e.events(t, e.orgID); len(evs) != 0 {
				t.Errorf("audit events = %d, want 0 (%v)", len(evs), evs)
			}
		})
	}
}

func TestBearerWithOtherOrgHeaderIs401(t *testing.T) {
	tests := []struct {
		name    string
		orgKind string
		want    int
	}{
		{name: "Moonglass-Org names another org", orgKind: "other", want: http.StatusUnauthorized},
		{name: "Moonglass-Org names the token's org", orgKind: "own", want: http.StatusOK},
		{name: "no Moonglass-Org header", orgKind: "", want: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newSessionEnv(t)
			var org string
			switch tt.orgKind {
			case "other":
				org = e.newOrg(t)
			case "own":
				org = e.orgID
			}

			rec := e.get(t, "Bearer "+e.token, org, false)

			if rec.Code != tt.want {
				t.Fatalf("GET /v1/servers with a bearer and Moonglass-Org %q: status = %d, want %d (body %q)", org, rec.Code, tt.want, rec.Body.String())
			}
			if tt.want == http.StatusOK {
				if evs := e.events(t, e.orgID); len(evs) != 1 {
					t.Errorf("audit events = %d, want 1 (%v)", len(evs), evs)
				}
				return
			}
			if detail := sessionDetail(t, rec); detail == "" {
				t.Errorf("detail = empty, want a 401 explanation")
			}
			for _, secret := range []string{e.token, org} {
				if strings.Contains(rec.Body.String(), secret) {
					t.Errorf("401 body = %q, want it not to hold the token or the Moonglass-Org value %q", rec.Body.String(), secret)
				}
			}
			if evs := e.events(t, e.orgID); len(evs) != 0 {
				t.Errorf("audit events = %d, want 0 (%v)", len(evs), evs)
			}
		})
	}
}
