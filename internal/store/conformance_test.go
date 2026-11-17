package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/store"
)

func TestConformanceSQLite(t *testing.T) {
	s, _ := newSQLite(t)
	runConformance(t, s)
}

func TestConformancePostgres(t *testing.T) {
	env := newPGEnv(t, true)
	runConformance(t, env.openApp(t))
}

func runConformance(t *testing.T, s store.Store) {
	ctx := context.Background()
	newOrg := func(t *testing.T) string {
		t.Helper()
		id, err := s.CreateOrg(ctx)
		if err != nil {
			t.Fatalf("CreateOrg error = %v, want nil", err)
		}
		return id
	}
	newUser := func(t *testing.T) string {
		t.Helper()
		id, err := s.UserForIdentity(ctx, "test:"+randHex(t, 8))
		if err != nil {
			t.Fatalf("UserForIdentity error = %v, want nil", err)
		}
		return id
	}

	t.Run("create org", func(t *testing.T) {
		a, b := newOrg(t), newOrg(t)
		if len(a) != 32 || a == b {
			t.Errorf("CreateOrg ids = %q, %q, want two distinct 32-char ids", a, b)
		}
	})

	t.Run("mint user via identity", func(t *testing.T) {
		ident := "github:" + randHex(t, 8)
		u1, err := s.UserForIdentity(ctx, ident)
		if err != nil || len(u1) != 32 {
			t.Fatalf("UserForIdentity(%q) = %q, %v, want a 32-char id, nil", ident, u1, err)
		}
		u2, err := s.UserForIdentity(ctx, ident)
		if err != nil || u2 != u1 {
			t.Errorf("UserForIdentity(%q) again = %q, %v, want %q, nil", ident, u2, err, u1)
		}
		other := newUser(t)
		if other == u1 {
			t.Errorf("different identity got same user id %q", u1)
		}
	})

	t.Run("memberships and roles", func(t *testing.T) {
		org := newOrg(t)
		roles := []store.Role{store.RoleOwner, store.RoleAdmin, store.RoleMember, store.RoleViewer}
		wantRoles := map[store.Role]string{store.RoleOwner: "owner", store.RoleAdmin: "admin", store.RoleMember: "member", store.RoleViewer: "viewer"}
		users := map[string]store.Role{}
		for _, r := range roles {
			if string(r) != wantRoles[r] {
				t.Errorf("role constant = %q, want %q", r, wantRoles[r])
			}
			u := newUser(t)
			if err := s.AddMembership(ctx, org, u, r); err != nil {
				t.Errorf("AddMembership(role=%q) error = %v, want nil", r, err)
			}
			users[u] = r
		}
		ms, err := s.Memberships(ctx, org)
		if err != nil {
			t.Fatalf("Memberships error = %v, want nil", err)
		}
		if len(ms) != len(users) {
			t.Errorf("Memberships len = %d, want %d: %+v", len(ms), len(users), ms)
		}
		for _, m := range ms {
			if m.OrgID != org || users[m.UserID] != m.Role {
				t.Errorf("membership = %+v, want org %q role %q", m, org, users[m.UserID])
			}
		}

		var first string
		for u := range users {
			first = u
			break
		}
		if err := s.AddMembership(ctx, org, first, store.RoleViewer); !errors.Is(err, store.ErrDuplicate) {
			t.Errorf("duplicate AddMembership error = %v, want %v", err, store.ErrDuplicate)
		}
		if err := s.AddMembership(ctx, org, newUser(t), store.Role("root")); err == nil {
			t.Errorf("AddMembership(role=%q) error = nil, want an error", "root")
		}
	})

	t.Run("list user orgs", func(t *testing.T) {
		a, b, c := newOrg(t), newOrg(t), newOrg(t)
		u := newUser(t)
		for _, o := range []string{a, b} {
			if err := s.AddMembership(ctx, o, u, store.RoleMember); err != nil {
				t.Fatalf("AddMembership error = %v, want nil", err)
			}
		}
		got, err := s.UserOrgs(ctx, u)
		if err != nil || !sameStrings(got, []string{a, b}) {
			t.Errorf("UserOrgs = %v, %v, want %v, nil (not %s)", got, err, []string{a, b}, c)
		}
		none, err := s.UserOrgs(ctx, newUser(t))
		if err != nil || len(none) != 0 {
			t.Errorf("UserOrgs(no memberships) = %v, %v, want empty, nil", none, err)
		}
	})

	t.Run("servers", func(t *testing.T) {
		org := newOrg(t)
		pub := "pub-" + randHex(t, 16)
		srv, err := s.AddServer(ctx, org, pub)
		if err != nil {
			t.Fatalf("AddServer error = %v, want nil", err)
		}
		if srv.OrgID != org || srv.PublicKey != pub || len(srv.ID) != 32 || srv.CreatedAt == "" {
			t.Errorf("AddServer = %+v, want org %q key %q 32-char id, created_at set", srv, org, pub)
		}
		got, err := s.ServerByPublicKey(ctx, pub)
		if err != nil || got.ID != srv.ID || got.OrgID != org || got.PublicKey != pub {
			t.Errorf("ServerByPublicKey = %+v, %v, want id %q org %q key %q", got, err, srv.ID, org, pub)
		}
		if _, err := s.ServerByPublicKey(ctx, "pub-missing-"+randHex(t, 8)); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("ServerByPublicKey(unknown) error = %v, want %v", err, store.ErrNotFound)
		}
		list, err := s.Servers(ctx, org)
		if err != nil || len(list) != 1 || list[0].ID != srv.ID {
			t.Errorf("Servers = %+v, %v, want exactly server %q", list, err, srv.ID)
		}
	})

	t.Run("enrolment tokens", func(t *testing.T) {
		org := newOrg(t)
		now := time.Now().UTC()
		h := store.HashToken("mg_" + randHex(t, 16))
		if err := s.AddEnrolmentToken(ctx, org, h, now.Add(time.Hour)); err != nil {
			t.Fatalf("AddEnrolmentToken error = %v, want nil", err)
		}
		infos, err := s.EnrolmentTokens(ctx, org)
		if err != nil || len(infos) != 1 {
			t.Errorf("EnrolmentTokens len = %d, %v, want 1, nil", len(infos), err)
		}
		got, err := s.UseEnrolmentToken(ctx, h, now)
		if err != nil || got != org {
			t.Errorf("UseEnrolmentToken = %q, %v, want %q, nil", got, err, org)
		}
		if _, err := s.UseEnrolmentToken(ctx, h, now); !errors.Is(err, store.ErrTokenUsed) {
			t.Errorf("UseEnrolmentToken replay error = %v, want %v", err, store.ErrTokenUsed)
		}
		if _, err := s.UseEnrolmentToken(ctx, store.HashToken("mg_unknown"), now); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("UseEnrolmentToken(unknown) error = %v, want %v", err, store.ErrNotFound)
		}
		exp := store.HashToken("mg_" + randHex(t, 16))
		if err := s.AddEnrolmentToken(ctx, org, exp, now.Add(-time.Minute)); err != nil {
			t.Fatalf("AddEnrolmentToken(expired) error = %v, want nil", err)
		}
		if _, err := s.UseEnrolmentToken(ctx, exp, now); !errors.Is(err, store.ErrTokenExpired) {
			t.Errorf("UseEnrolmentToken(expired) error = %v, want %v", err, store.ErrTokenExpired)
		}
	})

	t.Run("org isolation", func(t *testing.T) {
		a, b := newOrg(t), newOrg(t)
		ua, ub := newUser(t), newUser(t)
		must := func(err error) {
			t.Helper()
			if err != nil {
				t.Fatalf("setup error = %v, want nil", err)
			}
		}
		must(s.AddMembership(ctx, a, ua, store.RoleOwner))
		must(s.AddMembership(ctx, b, ub, store.RoleOwner))
		sa, err := s.AddServer(ctx, a, "pub-"+randHex(t, 16))
		must(err)
		_, err = s.AddServer(ctx, b, "pub-"+randHex(t, 16))
		must(err)
		must(s.AddEnrolmentToken(ctx, a, store.HashToken("mg_"+randHex(t, 16)), time.Now().UTC().Add(time.Hour)))

		ms, err := s.Memberships(ctx, b)
		must(err)
		if len(ms) != 1 || ms[0].OrgID != b || ms[0].UserID != ub {
			t.Errorf("Memberships(B) = %+v, want only user %q in org %q", ms, ub, b)
		}
		ss, err := s.Servers(ctx, b)
		must(err)
		if len(ss) != 1 || ss[0].OrgID != b || ss[0].ID == sa.ID {
			t.Errorf("Servers(B) = %+v, want only B's server (not %q)", ss, sa.ID)
		}
		ts, err := s.EnrolmentTokens(ctx, b)
		must(err)
		if len(ts) != 0 {
			t.Errorf("EnrolmentTokens(B) len = %d, want 0", len(ts))
		}
		uo, err := s.UserOrgs(ctx, ua)
		must(err)
		if !sameStrings(uo, []string{a}) {
			t.Errorf("UserOrgs(A's user) = %v, want [%s]", uo, a)
		}
	})
}
