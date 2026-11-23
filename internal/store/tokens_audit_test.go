package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/store"
)

func TestTokensAndAuditSQLite(t *testing.T) {
	ctx := context.Background()
	s, _ := newSQLite(t)
	orgID, err := s.CreateOrg(ctx)
	if err != nil {
		t.Fatalf("CreateOrg error = %v, want nil", err)
	}
	userID, err := s.UserForIdentity(ctx, "o@example.com")
	if err != nil {
		t.Fatalf("UserForIdentity error = %v, want nil", err)
	}
	if err := s.AddMembership(ctx, orgID, userID, store.RoleAdmin); err != nil {
		t.Fatalf("AddMembership error = %v, want nil", err)
	}
	tok := store.Token{
		ID: "t1", OrgID: orgID, UserID: userID, Name: "ci", Last4: "abcd",
		ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		Scopes:    []store.Scope{{Project: "*", Environment: "*", Operation: "*"}},
	}
	if err := s.AddToken(ctx, orgID, tok, store.HashToken("mg_x")); err != nil {
		t.Fatalf("AddToken error = %v, want nil", err)
	}
	got, role, err := s.TokenByHash(ctx, store.HashToken("mg_x"))
	if err != nil {
		t.Fatalf("TokenByHash error = %v, want nil", err)
	}
	if got.ID != "t1" || got.OrgID != orgID || got.Name != "ci" || got.Last4 != "abcd" || len(got.Scopes) != 1 || role != store.RoleAdmin {
		t.Errorf("TokenByHash = %+v role %s, want token t1 in org %s with one scope and role admin", got, role, orgID)
	}
	if _, _, err := s.TokenByHash(ctx, store.HashToken("mg_other")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("TokenByHash(unknown) error = %v, want ErrNotFound", err)
	}

	for i := int64(1); i <= 2; i++ {
		err := s.AppendAudit(ctx, orgID, func(prev string, seq int64) store.AuditEvent {
			return store.AuditEvent{OrgID: orgID, Seq: seq, ID: "e", PrevHash: prev, Hash: "h" + string(rune('0'+seq)), Operation: "x", Outcome: "allowed", PrincipalKind: "human"}
		})
		if err != nil {
			t.Fatalf("AppendAudit #%d error = %v, want nil", i, err)
		}
	}
	evs, err := s.AuditEvents(ctx, orgID)
	if err != nil || len(evs) != 2 {
		t.Fatalf("AuditEvents = %d events, err %v, want 2", len(evs), err)
	}
	if evs[0].Seq != 1 || evs[0].PrevHash != "" || evs[1].Seq != 2 || evs[1].PrevHash != "h1" {
		t.Errorf("events = %+v, want seq 1 with empty prev, then seq 2 with prev h1", evs)
	}
}
