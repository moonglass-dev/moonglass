package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/protocol"
	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
	"moonglass.dev/moonglass/internal/store"
)

// ownerBearer mints an owner token for an owner user of f's org and returns its
// text.
func ownerBearer(t *testing.T, f *fixture) string {
	t.Helper()
	ctx := context.Background()
	userID, err := f.st.UserForIdentity(ctx, "owner@example.com")
	if err != nil {
		t.Fatalf("UserForIdentity: unexpected error %v", err)
	}
	if err := f.st.AddMembership(ctx, f.org, userID, store.RoleOwner); err != nil {
		t.Fatalf("AddMembership: unexpected error %v", err)
	}
	token, err := f.srv.MintOwnerToken(ctx, f.org, userID, "cross-origin", testNow.Add(time.Hour))
	if err != nil {
		t.Fatalf("MintOwnerToken: unexpected error %v", err)
	}
	return token
}

// enrollPost builds the POST /v1/enroll request the CLI sends, with no browser
// headers.
func enrollPost(t *testing.T, token string) *http.Request {
	t.Helper()
	env, err := protocol.Wrap(agentv1.MessageType_ENROLL_REQUEST, "cross-origin-test", newRequest(t, token))
	if err != nil {
		t.Fatalf("protocol.Wrap: unexpected error %v", err)
	}
	body, err := protocol.Marshal(env)
	if err != nil {
		t.Fatalf("protocol.Marshal: unexpected error %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/enroll", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func TestCrossOriginWriteRefused(t *testing.T) {
	tests := []struct {
		name string
		// request builds the request and returns it with the enrolment token it
		// sent, when it sent one.
		request    func(t *testing.T, f *fixture) (req *http.Request, enrolToken string)
		wantStatus int
		// checkResult compares the decoded enrolment result to wantResult.
		checkResult bool
		wantResult  agentv1.EnrollResult
		wantServers int
		wantEvents  int
	}{
		{
			name: "cross-site enrol POST is refused before the handler",
			request: func(t *testing.T, f *fixture) (*http.Request, string) {
				tok := f.issue(t)
				req := enrollPost(t, tok)
				req.Header.Set("Sec-Fetch-Site", "cross-site")
				return req, tok
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name: "an Origin that differs from Host is refused without Sec-Fetch-Site",
			request: func(t *testing.T, f *fixture) (*http.Request, string) {
				tok := f.issue(t)
				req := enrollPost(t, tok)
				req.Header.Set("Origin", "https://evil.example")
				return req, tok
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name: "cross-site registry POST is refused before the mux",
			request: func(t *testing.T, f *fixture) (*http.Request, string) {
				req := httptest.NewRequest(http.MethodPost, "/v1/servers", nil)
				req.Header.Set("Authorization", "Bearer "+ownerBearer(t, f))
				req.Header.Set("Sec-Fetch-Site", "cross-site")
				return req, ""
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name: "the CLI's enrol POST with no browser headers reaches the handler",
			request: func(t *testing.T, f *fixture) (*http.Request, string) {
				tok := f.issue(t)
				return enrollPost(t, tok), tok
			},
			wantStatus:  http.StatusOK,
			checkResult: true,
			wantResult:  agentv1.EnrollResult_ACCEPTED,
			wantServers: 1,
		},
		{
			name: "a cross-site GET reaches the handler",
			request: func(t *testing.T, f *fixture) (*http.Request, string) {
				req := httptest.NewRequest(http.MethodGet, "/v1/servers", nil)
				req.Header.Set("Authorization", "Bearer "+ownerBearer(t, f))
				req.Header.Set("Sec-Fetch-Site", "cross-site")
				return req, ""
			},
			wantStatus: http.StatusOK,
			wantEvents: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			req, enrolToken := tt.request(t, f)

			rec := httptest.NewRecorder()
			f.srv.Handler().ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.checkResult {
				renv, err := protocol.Unmarshal(rec.Body.Bytes())
				if err != nil {
					t.Fatalf("protocol.Unmarshal response: unexpected error %v", err)
				}
				var resp agentv1.EnrollResponse
				if err := protocol.Open(renv, &resp); err != nil {
					t.Fatalf("protocol.Open EnrollResponse: unexpected error %v", err)
				}
				if resp.GetResult() != tt.wantResult {
					t.Errorf("result = %v, want %v", resp.GetResult(), tt.wantResult)
				}
			}
			if got := len(f.servers(t)); got != tt.wantServers {
				t.Errorf("registered servers = %d, want %d", got, tt.wantServers)
			}
			evs, err := f.st.AuditEvents(ctx, f.org)
			if err != nil {
				t.Fatalf("AuditEvents: unexpected error %v", err)
			}
			if len(evs) != tt.wantEvents {
				t.Errorf("audit events = %d, want %d (%v)", len(evs), tt.wantEvents, evs)
			}
			if enrolToken != "" {
				infos, err := f.st.EnrolmentTokens(ctx, f.org)
				if err != nil {
					t.Fatalf("EnrolmentTokens: unexpected error %v", err)
				}
				if len(infos) != 1 {
					t.Fatalf("enrolment tokens = %d, want 1", len(infos))
				}
				if tt.wantServers == 0 && infos[0].Used {
					t.Errorf("enrolment token Used = true, want it unused: the handler must not run")
				}
			}
		})
	}
}
