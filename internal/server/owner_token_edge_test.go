package server

import (
	"context"
	"testing"

	"moonglass.dev/moonglass/internal/store"
)

func TestIssueOwnerTokenAddsOwnerBesideExistingAdmins(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	admin, err := f.st.UserForIdentity(ctx, "admin@example.com")
	if err != nil {
		t.Fatalf("UserForIdentity(admin@example.com): unexpected error %v", err)
	}
	if err := f.st.AddMembership(ctx, f.org, admin, store.RoleAdmin); err != nil {
		t.Fatalf("AddMembership: unexpected error %v", err)
	}

	text, err := f.srv.IssueOwnerToken(ctx, f.org, "ci")
	if err != nil {
		t.Fatalf("IssueOwnerToken: unexpected error %v", err)
	}

	localOwner, err := f.st.UserForIdentity(ctx, "local:owner")
	if err != nil {
		t.Fatalf("UserForIdentity(local:owner): unexpected error %v", err)
	}
	members, err := f.st.Memberships(ctx, f.org)
	if err != nil {
		t.Fatalf("Memberships: unexpected error %v", err)
	}
	want := map[string]store.Role{admin: store.RoleAdmin, localOwner: store.RoleOwner}
	if len(members) != len(want) {
		t.Fatalf("memberships: expected exactly %d (%v), got %d (%v)", len(want), want, len(members), members)
	}
	for _, m := range members {
		if role, ok := want[m.UserID]; !ok || m.Role != role {
			t.Errorf("membership %s: expected role %q, got %q (and no other change)", m.UserID, want[m.UserID], m.Role)
		}
	}

	p, err := f.srv.reg.Authenticate(ctx, text)
	if err != nil {
		t.Fatalf("Authenticate: unexpected error %v", err)
	}
	if p.UserID != localOwner || p.Role != store.RoleOwner {
		t.Errorf("principal: expected user %s role %q, got user %s role %q", localOwner, store.RoleOwner, p.UserID, p.Role)
	}
	wantNoAuditEvents(t, f.st, f.org)
}
