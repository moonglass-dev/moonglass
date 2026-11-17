package registry_test

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"moonglass.dev/moonglass/internal/registry"
	"moonglass.dev/moonglass/internal/store"
)

func twoEvents(t *testing.T, e *env) []store.AuditEvent {
	t.Helper()
	ctx := context.Background()
	op, err := registry.Register(e.reg, registry.Spec{Name: "t.op", Method: "GET", Path: "/v1/t", MinRole: store.RoleMember, Destructive: registry.No},
		func(context.Context, registry.Call, empty) (empty, error) { return empty{}, nil })
	if err != nil {
		t.Fatalf("Register error = %v, want nil", err)
	}
	for _, role := range []store.Role{store.RoleOwner, store.RoleOwner} {
		if _, err := registry.Invoke(ctx, e.reg, op, e.principal(role, wildcard), registry.Target{}, empty{}); err != nil {
			t.Fatalf("Invoke error = %v, want nil", err)
		}
	}
	evs, err := e.st.AuditEvents(ctx, e.orgID)
	if err != nil {
		t.Fatalf("AuditEvents error = %v, want nil", err)
	}
	return evs
}

func TestAuditEventsAreHashChained(t *testing.T) {
	e := newEnv(t)
	evs := twoEvents(t, e)
	if len(evs) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(evs))
	}
	if evs[0].Seq != 1 || evs[1].Seq != 2 {
		t.Errorf("seq = %d, %d, want 1, 2", evs[0].Seq, evs[1].Seq)
	}
	if evs[0].PrevHash != "" {
		t.Errorf("event 1 PrevHash = %q, want empty genesis value", evs[0].PrevHash)
	}
	if evs[1].PrevHash != evs[0].Hash {
		t.Errorf("event 2 PrevHash = %q, want event 1 Hash %q", evs[1].PrevHash, evs[0].Hash)
	}
	hex := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for _, ev := range evs {
		if !hex.MatchString(ev.Hash) {
			t.Errorf("event %d Hash = %q, want 64 hex characters", ev.Seq, ev.Hash)
		}
	}
	if evs[0].Hash == evs[1].Hash {
		t.Errorf("both events have Hash %q, want distinct hashes", evs[0].Hash)
	}
	if err := registry.VerifyAudit(evs); err != nil {
		t.Fatalf("VerifyAudit(untouched) error = %v, want nil", err)
	}
	if err := e.reg.VerifyAudit(context.Background(), e.orgID); err != nil {
		t.Fatalf("Registry.VerifyAudit error = %v, want nil", err)
	}

	clone := func() []store.AuditEvent { return append([]store.AuditEvent(nil), evs...) }
	tests := []struct {
		name    string
		mutate  func([]store.AuditEvent) []store.AuditEvent
		wantSeq int64
	}{
		{"operation changed", func(c []store.AuditEvent) []store.AuditEvent { c[0].Operation = "other.op"; return c }, 1},
		{"outcome changed", func(c []store.AuditEvent) []store.AuditEvent { c[1].Outcome = "denied"; return c }, 2},
		{"principal changed", func(c []store.AuditEvent) []store.AuditEvent { c[1].UserID = "someone-else"; return c }, 2},
		{"time changed", func(c []store.AuditEvent) []store.AuditEvent { c[0].At = "2020-01-01T00:00:00Z"; return c }, 1},
		{"order swapped", func(c []store.AuditEvent) []store.AuditEvent { c[0], c[1] = c[1], c[0]; return c }, 0},
		{"first deleted", func(c []store.AuditEvent) []store.AuditEvent { return c[1:] }, 0},
		{"prev hash changed", func(c []store.AuditEvent) []store.AuditEvent { c[1].PrevHash = "00"; return c }, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := registry.VerifyAudit(tt.mutate(clone()))
			if err == nil {
				t.Fatalf("VerifyAudit(%s) error = nil, want failure", tt.name)
			}
			if tt.wantSeq == 0 {
				if !regexp.MustCompile(`[12]`).MatchString(err.Error()) {
					t.Errorf("VerifyAudit(%s) error = %q, want it to name a sequence number", tt.name, err)
				}
			} else if !strings.Contains(err.Error(), strconv.FormatInt(tt.wantSeq, 10)) {
				t.Errorf("VerifyAudit(%s) error = %q, want it to name sequence %d", tt.name, err, tt.wantSeq)
			}
		})
	}
}

func TestAuditEventsWrittenByRegistryNotHandler(t *testing.T) {
	ctx := context.Background()
	if n := reflect.TypeFor[registry.Call]().NumMethod(); n != 0 {
		t.Errorf("registry.Call has %d exported methods, want 0 (a handler cannot write audit events)", n)
	}
	e := newEnv(t)
	handlerErr := errors.New("boom")
	mk := func(name string, min store.Role, h func(context.Context, registry.Call, empty) (empty, error)) *registry.Operation[empty, empty] {
		op, err := registry.Register(e.reg, registry.Spec{Name: name, Method: "POST", Path: "/v1/" + name, MinRole: min, Destructive: registry.No}, h)
		if err != nil {
			t.Fatalf("Register(%s) error = %v, want nil", name, err)
		}
		return op
	}
	ok := mk("t.ok", store.RoleMember, func(context.Context, registry.Call, empty) (empty, error) { return empty{}, nil })
	fail := mk("t.fail", store.RoleMember, func(context.Context, registry.Call, empty) (empty, error) { return empty{}, handlerErr })

	steps := []struct {
		op      *registry.Operation[empty, empty]
		role    store.Role
		agent   bool
		wantErr error
		outcome string
		kind    string
		opName  string
	}{
		{ok, store.RoleOwner, false, nil, "allowed", "human", "t.ok"},
		{ok, store.RoleViewer, false, registry.ErrDenied, "denied", "human", "t.ok"},
		{fail, store.RoleOwner, true, handlerErr, "error", "agent", "t.fail"},
	}
	for i, s := range steps {
		p := e.principal(s.role, wildcard)
		p.Agent = s.agent
		_, err := registry.Invoke(ctx, e.reg, s.op, p, registry.Target{}, empty{})
		if !errors.Is(err, s.wantErr) {
			t.Fatalf("step %d Invoke error = %v, want %v", i, err, s.wantErr)
		}
		evs, err := e.st.AuditEvents(ctx, e.orgID)
		if err != nil || len(evs) != i+1 {
			t.Fatalf("step %d: %d events (err %v), want %d", i, len(evs), err, i+1)
		}
		got := evs[i]
		if got.Outcome != s.outcome || got.PrincipalKind != s.kind || got.Operation != s.opName || got.UserID != e.userID || got.TokenID != "tok-1" {
			t.Errorf("step %d event = outcome %q kind %q op %q user %q token %q, want %q %q %q %q %q",
				i, got.Outcome, got.PrincipalKind, got.Operation, got.UserID, got.TokenID, s.outcome, s.kind, s.opName, e.userID, "tok-1")
		}
	}
}
