package registry_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/registry"
	"moonglass.dev/moonglass/internal/store"
)

// addOrg creates an org and, when role is not empty, makes userID its member.
func addOrg(t *testing.T, e *env, userID string, role store.Role) string {
	t.Helper()
	orgID, err := e.st.CreateOrg(context.Background())
	if err != nil {
		t.Fatalf("CreateOrg error = %v, want nil", err)
	}
	if role != "" {
		if err := e.st.AddMembership(context.Background(), orgID, userID, role); err != nil {
			t.Fatalf("AddMembership(%s, %s, %s) error = %v, want nil", orgID, userID, role, err)
		}
	}
	return orgID
}

// newSession mints a session bearer for userID and stores its hash until
// expires, returning the bearer and the stored session.
func newSession(t *testing.T, e *env, userID string, expires time.Time) (string, store.Session) {
	t.Helper()
	ctx := context.Background()
	text, err := registry.NewToken()
	if err != nil {
		t.Fatalf("NewToken error = %v, want nil", err)
	}
	sess, err := e.st.AddSession(ctx, userID, store.HashToken(text), expires)
	if err != nil {
		t.Fatalf("AddSession error = %v, want nil", err)
	}
	return text, sess
}

func TestAuthenticateSession(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	memberOrg := addOrg(t, e, e.userID, store.RoleMember)
	strangerOrg := addOrg(t, e, e.userID, "")

	liveText, live := newSession(t, e, e.userID, e.now.Add(14*24*time.Hour))
	secondText, second := newSession(t, e, e.userID, e.now.Add(14*24*time.Hour))
	expiredText, _ := newSession(t, e, e.userID, e.now)
	revokedText, revoked := newSession(t, e, e.userID, e.now.Add(time.Hour))
	if err := e.st.RevokeSession(ctx, e.userID, revoked.ID, e.now); err != nil {
		t.Fatalf("RevokeSession(%s) error = %v, want nil", revoked.ID, err)
	}
	unknownText, err := registry.NewToken()
	if err != nil {
		t.Fatalf("NewToken error = %v, want nil", err)
	}
	malformedText := "abc"
	malformedMG := "mg_" + strings.Repeat("a", 51)

	tests := []struct {
		name    string
		session string
		orgID   string
		want    registry.Principal
		wantErr bool
	}{
		{
			name:    "live session in the org the user owns",
			session: liveText,
			orgID:   e.orgID,
			want: registry.Principal{
				OrgID: e.orgID, UserID: e.userID, SessionID: live.ID,
				Role: store.RoleOwner, Scopes: []registry.Scope{wildcard},
			},
		},
		{
			name:    "live session in a second org the user is a member of",
			session: liveText,
			orgID:   memberOrg,
			want: registry.Principal{
				OrgID: memberOrg, UserID: e.userID, SessionID: live.ID,
				Role: store.RoleMember, Scopes: []registry.Scope{wildcard},
			},
		},
		{
			name:    "another live session carries its own id",
			session: secondText,
			orgID:   memberOrg,
			want: registry.Principal{
				OrgID: memberOrg, UserID: e.userID, SessionID: second.ID,
				Role: store.RoleMember, Scopes: []registry.Scope{wildcard},
			},
		},
		{
			name:    "unknown well-formed session",
			session: unknownText,
			orgID:   e.orgID,
			wantErr: true,
		},
		{
			name:    "expired session at its expiry",
			session: expiredText,
			orgID:   e.orgID,
			wantErr: true,
		},
		{
			name:    "revoked session",
			session: revokedText,
			orgID:   e.orgID,
			wantErr: true,
		},
		{
			name:    "malformed value without the mg_ prefix",
			session: malformedText,
			orgID:   e.orgID,
			wantErr: true,
		},
		{
			name:    "malformed mg_ value of the wrong length",
			session: malformedMG,
			orgID:   e.orgID,
			wantErr: true,
		},
		{
			name:    "org the user is not a member of",
			session: liveText,
			orgID:   strangerOrg,
			wantErr: true,
		},
		{
			name:    "empty org",
			session: liveText,
			orgID:   "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := e.reg.AuthenticateSession(ctx, tt.session, tt.orgID)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("AuthenticateSession(session %q, org %q) principal = %+v, err = nil, want ErrUnauthenticated", tt.session, tt.orgID, got)
				}
				if !errors.Is(err, registry.ErrUnauthenticated) {
					t.Fatalf("AuthenticateSession(session %q, org %q) error = %v, want it to match ErrUnauthenticated", tt.session, tt.orgID, err)
				}
				if strings.Contains(err.Error(), tt.session) {
					t.Errorf("AuthenticateSession error = %q, want it not to hold the session value %q", err, tt.session)
				}
				return
			}
			if err != nil {
				t.Fatalf("AuthenticateSession(session %q, org %q) error = %v, want nil", tt.session, tt.orgID, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("AuthenticateSession(session %q, org %q) principal = %+v, want %+v", tt.session, tt.orgID, got, tt.want)
			}
		})
	}
}

func TestSessionCallAuditsSessionID(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	text, sess := newSession(t, e, e.userID, e.now.Add(14*24*time.Hour))
	p, err := e.reg.AuthenticateSession(ctx, text, e.orgID)
	if err != nil {
		t.Fatalf("AuthenticateSession error = %v, want nil", err)
	}
	op, err := registry.Register(e.reg, registry.Spec{Name: "t.op", Method: "GET", Path: "/v1/t", MinRole: store.RoleMember, Destructive: registry.No},
		func(context.Context, registry.Call, empty) (empty, error) { return empty{}, nil })
	if err != nil {
		t.Fatalf("Register error = %v, want nil", err)
	}
	if _, err := registry.Invoke(ctx, e.reg, op, p, registry.Target{}, empty{}); err != nil {
		t.Fatalf("Invoke(session principal) error = %v, want nil", err)
	}
	if _, err := registry.Invoke(ctx, e.reg, op, e.principal(store.RoleOwner, wildcard), registry.Target{}, empty{}); err != nil {
		t.Fatalf("Invoke(bearer principal) error = %v, want nil", err)
	}

	evs, err := e.st.AuditEvents(ctx, e.orgID)
	if err != nil {
		t.Fatalf("AuditEvents error = %v, want nil", err)
	}
	if len(evs) != 2 {
		t.Fatalf("AuditEvents = %d events, want 2", len(evs))
	}
	bySession := evs[0]
	if bySession.SessionID != sess.ID {
		t.Errorf("session call SessionID = %q, want %q", bySession.SessionID, sess.ID)
	}
	if bySession.TokenID != "" {
		t.Errorf("session call TokenID = %q, want empty", bySession.TokenID)
	}
	if bySession.StepUpID != "" {
		t.Errorf("session call StepUpID = %q, want empty", bySession.StepUpID)
	}
	byBearer := evs[1]
	if byBearer.TokenID != "tok-1" {
		t.Errorf("bearer call TokenID = %q, want %q", byBearer.TokenID, "tok-1")
	}
	if byBearer.SessionID != "" {
		t.Errorf("bearer call SessionID = %q, want empty", byBearer.SessionID)
	}
	if byBearer.StepUpID != "" {
		t.Errorf("bearer call StepUpID = %q, want empty", byBearer.StepUpID)
	}
}

func TestAuditHashCoversSessionAndStepUp(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	text, sess := newSession(t, e, e.userID, e.now.Add(14*24*time.Hour))
	p, err := e.reg.AuthenticateSession(ctx, text, e.orgID)
	if err != nil {
		t.Fatalf("AuthenticateSession error = %v, want nil", err)
	}
	op, err := registry.Register(e.reg, registry.Spec{Name: "t.op", Method: "GET", Path: "/v1/t", MinRole: store.RoleMember, Destructive: registry.No},
		func(context.Context, registry.Call, empty) (empty, error) { return empty{}, nil })
	if err != nil {
		t.Fatalf("Register error = %v, want nil", err)
	}
	if _, err := registry.Invoke(ctx, e.reg, op, p, registry.Target{}, empty{}); err != nil {
		t.Fatalf("Invoke(session principal) error = %v, want nil", err)
	}
	if _, err := registry.Invoke(ctx, e.reg, op, e.principal(store.RoleOwner, wildcard), registry.Target{}, empty{}); err != nil {
		t.Fatalf("Invoke(bearer principal) error = %v, want nil", err)
	}
	evs, err := e.st.AuditEvents(ctx, e.orgID)
	if err != nil {
		t.Fatalf("AuditEvents error = %v, want nil", err)
	}
	if len(evs) != 2 {
		t.Fatalf("AuditEvents = %d events, want 2", len(evs))
	}
	if evs[0].SessionID != sess.ID {
		t.Fatalf("event 1 SessionID = %q, want %q", evs[0].SessionID, sess.ID)
	}
	if err := registry.VerifyAudit(evs); err != nil {
		t.Fatalf("VerifyAudit(untouched) error = %v, want nil", err)
	}

	wantHash := func(e store.AuditEvent) string {
		body, err := json.Marshal([]any{e.PrevHash, e.OrgID, e.Seq, e.ID, e.At, e.PrincipalKind, e.UserID, e.TokenID, e.SessionID, e.StepUpID, e.Operation, e.Outcome, e.Reason})
		if err != nil {
			t.Fatalf("marshal the 13 hashed fields of event %d: %v", e.Seq, err)
		}
		sum := sha256.Sum256(body)
		return hex.EncodeToString(sum[:])
	}
	for _, ev := range evs {
		if want := wantHash(ev); ev.Hash != want {
			t.Errorf("event %d Hash = %q, want SHA-256 of the 13-field JSON array %q", ev.Seq, ev.Hash, want)
		}
	}

	tests := []struct {
		name    string
		mutate  func([]store.AuditEvent)
		wantSeq int64
	}{
		{"session id changed on event 1", func(c []store.AuditEvent) { c[0].SessionID = "another-session" }, 1},
		{"step up id changed on event 1", func(c []store.AuditEvent) { c[0].StepUpID = "su-1" }, 1},
		{"session id changed on event 2", func(c []store.AuditEvent) { c[1].SessionID = "another-session" }, 2},
		{"step up id changed on event 2", func(c []store.AuditEvent) { c[1].StepUpID = "su-1" }, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clone := append([]store.AuditEvent(nil), evs...)
			tt.mutate(clone)
			err := registry.VerifyAudit(clone)
			if err == nil {
				t.Fatalf("VerifyAudit(%s) error = nil, want a failure naming sequence %d", tt.name, tt.wantSeq)
			}
			if want := fmt.Sprintf("sequence %d", tt.wantSeq); !strings.Contains(err.Error(), want) {
				t.Errorf("VerifyAudit(%s) error = %q, want it to name %q", tt.name, err, want)
			}
		})
	}
}
