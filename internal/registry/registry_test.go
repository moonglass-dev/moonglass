package registry_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/registry"
	"moonglass.dev/moonglass/internal/store"
	"moonglass.dev/moonglass/internal/store/sqlite"
)

type empty struct{}

var t0 = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// env is a registry over a fresh SQLite store with one org and one user.
type env struct {
	reg    *registry.Registry
	st     store.Store
	db     *sql.DB
	orgID  string
	userID string
	now    time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mg.db")
	st, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("sqlite.Open(%q) error = %v, want nil", path, err)
	}
	t.Cleanup(func() { _ = st.Close() })
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open(sqlite, %q) error = %v, want nil", path, err)
	}
	t.Cleanup(func() { _ = db.Close() })
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
	e := &env{st: st, db: db, orgID: orgID, userID: userID, now: t0}
	e.reg = registry.New(st, func() time.Time { return e.now })
	return e
}

func (e *env) principal(role store.Role, scopes ...registry.Scope) registry.Principal {
	return registry.Principal{OrgID: e.orgID, UserID: e.userID, TokenID: "tok-1", Role: role, Scopes: scopes}
}

var wildcard = registry.Scope{Project: "*", Environment: "*", Operation: "*"}

func TestOperationRequiresDestructiveFlag(t *testing.T) {
	h := func(context.Context, registry.Call, empty) (empty, error) { return empty{}, nil }
	tests := []struct {
		name    string
		spec    registry.Spec
		wantErr bool
	}{
		{"flag omitted", registry.Spec{Name: "x.omitted", Method: "GET", Path: "/v1/x", MinRole: store.RoleViewer}, true},
		{"flag no", registry.Spec{Name: "x.no", Method: "GET", Path: "/v1/x", MinRole: store.RoleViewer, Destructive: registry.No}, false},
		{"flag yes", registry.Spec{Name: "x.yes", Method: "POST", Path: "/v1/x", MinRole: store.RoleAdmin, Destructive: registry.Yes}, false},
		{"empty name", registry.Spec{Name: "", Method: "GET", Path: "/v1/x", MinRole: store.RoleViewer, Destructive: registry.No}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			_, err := registry.Register(e.reg, tt.spec, h)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Register(%+v) error = %v, wantErr %v", tt.spec, err, tt.wantErr)
			}
			if tt.wantErr && tt.spec.Name != "" && !strings.Contains(err.Error(), tt.spec.Name) {
				t.Errorf("Register error = %q, want it to name the operation %q", err, tt.spec.Name)
			}
		})
	}
	t.Run("duplicate name", func(t *testing.T) {
		e := newEnv(t)
		spec := registry.Spec{Name: "x.dup", Method: "GET", Path: "/v1/x", MinRole: store.RoleViewer, Destructive: registry.No}
		if _, err := registry.Register(e.reg, spec, h); err != nil {
			t.Fatalf("first Register error = %v, want nil", err)
		}
		if _, err := registry.Register(e.reg, spec, h); err == nil {
			t.Errorf("second Register of %q error = nil, want a duplicate error", spec.Name)
		}
	})
	t.Run("shipped operations set the flag", func(t *testing.T) {
		ops := registry.Operations()
		if len(ops) == 0 {
			t.Fatalf("registry.Operations() = empty, want at least servers.list")
		}
		for _, s := range ops {
			if s.Destructive == registry.Unset {
				t.Errorf("operation %q has Destructive = Unset, want Yes or No", s.Name)
			}
		}
	})
}

func TestDestructiveBelowAdminIsRejected(t *testing.T) {
	h := func(context.Context, registry.Call, empty) (empty, error) { return empty{}, nil }
	tests := []struct {
		role        store.Role
		destructive registry.Flag
		wantErr     bool
	}{
		{store.RoleViewer, registry.Yes, true},
		{store.RoleMember, registry.Yes, true},
		{store.RoleAdmin, registry.Yes, false},
		{store.RoleOwner, registry.Yes, false},
		{store.RoleMember, registry.No, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.role)+"/"+string(rune('0'+tt.destructive)), func(t *testing.T) {
			e := newEnv(t)
			spec := registry.Spec{Name: "x.op", Method: "POST", Path: "/v1/x", MinRole: tt.role, Destructive: tt.destructive}
			_, err := registry.Register(e.reg, spec, h)
			if (err != nil) != tt.wantErr {
				t.Errorf("Register(role=%s destructive=%d) error = %v, wantErr %v", tt.role, tt.destructive, err, tt.wantErr)
			}
		})
	}
	for _, s := range registry.Operations() {
		if s.Destructive == registry.Yes && s.MinRole != store.RoleAdmin && s.MinRole != store.RoleOwner {
			t.Errorf("shipped destructive operation %q has MinRole = %s, want admin or owner", s.Name, s.MinRole)
		}
	}
}

func TestRoleAndScopeAreEnforced(t *testing.T) {
	proj := registry.Target{Project: "web", Environment: "prod"}
	tests := []struct {
		name    string
		role    store.Role
		scopes  []registry.Scope
		agent   bool
		minRole store.Role
		destr   registry.Flag
		target  registry.Target
		wantErr error
	}{
		{"viewer denied member op", store.RoleViewer, []registry.Scope{wildcard}, false, store.RoleMember, registry.No, registry.Target{}, registry.ErrDenied},
		{"member allowed member op", store.RoleMember, []registry.Scope{wildcard}, false, store.RoleMember, registry.No, registry.Target{}, nil},
		{"owner wildcard allowed", store.RoleOwner, []registry.Scope{wildcard}, false, store.RoleViewer, registry.No, registry.Target{}, nil},
		{"operation mismatch denied", store.RoleOwner, []registry.Scope{{Project: "*", Environment: "*", Operation: "other.op"}}, false, store.RoleMember, registry.No, registry.Target{}, registry.ErrDenied},
		{"project mismatch denied", store.RoleOwner, []registry.Scope{{Project: "p2", Environment: "*", Operation: "*"}}, false, store.RoleMember, registry.No, proj, registry.ErrDenied},
		{"environment mismatch denied", store.RoleOwner, []registry.Scope{{Project: "p1", Environment: "e2", Operation: "*"}}, false, store.RoleMember, registry.No, proj, registry.ErrDenied},
		{"specific row never matches org-level call", store.RoleOwner, []registry.Scope{{Project: "p1", Environment: "*", Operation: "*"}}, false, store.RoleMember, registry.No, registry.Target{}, registry.ErrDenied},
		{"matching specific row allowed", store.RoleOwner, []registry.Scope{{Project: "p1", Environment: "e1", Operation: "t.op"}}, false, store.RoleMember, registry.No, proj, nil},
		{"no scope rows denied", store.RoleOwner, nil, false, store.RoleMember, registry.No, registry.Target{}, registry.ErrDenied},
		{"agent destructive pending approval", store.RoleOwner, []registry.Scope{wildcard}, true, store.RoleAdmin, registry.Yes, registry.Target{}, registry.ErrPendingApproval},
		{"agent non-destructive allowed", store.RoleOwner, []registry.Scope{wildcard}, true, store.RoleMember, registry.No, registry.Target{}, nil},
		{"human destructive allowed", store.RoleAdmin, []registry.Scope{wildcard}, false, store.RoleAdmin, registry.Yes, registry.Target{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			ctx := context.Background()
			project, err := e.st.CreateProject(ctx, e.orgID, "web")
			if err != nil {
				t.Fatalf("CreateProject(%q) error = %v, want nil", "web", err)
			}
			environment, err := e.st.CreateEnvironment(ctx, e.orgID, project.ID, "prod")
			if err != nil {
				t.Fatalf("CreateEnvironment(%q, %q) error = %v, want nil", project.ID, "prod", err)
			}
			effects := 0
			op, err := registry.Register(e.reg, registry.Spec{Name: "t.op", Method: "POST", Path: "/v1/t", MinRole: tt.minRole, Destructive: tt.destr},
				func(context.Context, registry.Call, empty) (empty, error) { effects++; return empty{}, nil })
			if err != nil {
				t.Fatalf("Register error = %v, want nil", err)
			}
			ids := map[string]string{"p1": project.ID, "e1": environment.ID}
			scopes := make([]registry.Scope, len(tt.scopes))
			for i, row := range tt.scopes {
				if id, ok := ids[row.Project]; ok {
					row.Project = id
				}
				if id, ok := ids[row.Environment]; ok {
					row.Environment = id
				}
				scopes[i] = row
			}
			p := e.principal(tt.role, scopes...)
			p.Agent = tt.agent
			_, err = registry.Invoke(context.Background(), e.reg, op, p, tt.target, empty{})
			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
				t.Fatalf("Invoke error = %v, want %v", err, tt.wantErr)
			}
			wantEffects, wantOutcome := 1, "allowed"
			if tt.wantErr != nil {
				wantEffects, wantOutcome = 0, "denied"
			}
			if effects != wantEffects {
				t.Errorf("handler side effects = %d, want %d", effects, wantEffects)
			}
			evs, err := e.st.AuditEvents(context.Background(), e.orgID)
			if err != nil || len(evs) != 1 {
				t.Fatalf("AuditEvents = %d events, err %v, want exactly 1 event", len(evs), err)
			}
			if evs[0].Outcome != wantOutcome || evs[0].Operation != "t.op" {
				t.Errorf("audit event outcome/operation = %q/%q, want %q/%q", evs[0].Outcome, evs[0].Operation, wantOutcome, "t.op")
			}
		})
	}
}
