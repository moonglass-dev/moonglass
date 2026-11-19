package registry_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/registry"
	"moonglass.dev/moonglass/internal/store"
)

// countingStore counts ResolveTarget calls and injects one failure when err is
// set; every other method goes to the embedded store.
type countingStore struct {
	store.Store
	calls int
	err   error
}

func (c *countingStore) ResolveTarget(ctx context.Context, orgID, project, environment string) (string, string, error) {
	c.calls++
	if c.err != nil {
		return "", "", c.err
	}
	return c.Store.ResolveTarget(ctx, orgID, project, environment)
}

// onlyEvent returns the org's single audit event, failing on any other count.
func onlyEvent(t *testing.T, e *env) store.AuditEvent {
	t.Helper()
	evs, err := e.st.AuditEvents(context.Background(), e.orgID)
	if err != nil {
		t.Fatalf("AuditEvents error = %v, want nil", err)
	}
	if len(evs) != 1 {
		t.Fatalf("AuditEvents = %d events, want exactly 1", len(evs))
	}
	return evs[0]
}

// createProject creates one project of the test org.
func createProject(t *testing.T, e *env, name string) store.Project {
	t.Helper()
	project, err := e.st.CreateProject(context.Background(), e.orgID, name)
	if err != nil {
		t.Fatalf("CreateProject(%q) error = %v, want nil", name, err)
	}
	return project
}

// createEnvironment creates one environment of the test org's project.
func createEnvironment(t *testing.T, e *env, projectID, name string) store.Environment {
	t.Helper()
	environment, err := e.st.CreateEnvironment(context.Background(), e.orgID, projectID, name)
	if err != nil {
		t.Fatalf("CreateEnvironment(%q, %q) error = %v, want nil", projectID, name, err)
	}
	return environment
}

func TestInvokeResolvesTargetAfterRoleCheck(t *testing.T) {
	tests := []struct {
		name        string
		role        store.Role
		wantErr     error
		wantCalls   int
		wantEffects int
		wantOutcome string
	}{
		{"viewer denied before resolution", store.RoleViewer, registry.ErrDenied, 0, 0, "denied"},
		{"member allowed after one resolution", store.RoleMember, nil, 1, 1, "allowed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			project := createProject(t, e, "web")
			_ = createEnvironment(t, e, project.ID, "prod")
			cs := &countingStore{Store: e.st}
			reg := registry.New(cs, func() time.Time { return t0 })
			effects := 0
			op, err := registry.Register(reg, registry.Spec{Name: "t.op", Method: "POST", Path: "/v1/t", MinRole: store.RoleMember, Destructive: registry.No},
				func(context.Context, registry.Call, empty) (empty, error) { effects++; return empty{}, nil })
			if err != nil {
				t.Fatalf("Register error = %v, want nil", err)
			}
			_, err = registry.Invoke(context.Background(), reg, op, e.principal(tt.role, wildcard), registry.Target{Project: "web", Environment: "prod"}, empty{})
			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
				t.Fatalf("Invoke error = %v, want %v", err, tt.wantErr)
			}
			if cs.calls != tt.wantCalls {
				t.Errorf("ResolveTarget calls = %d, want %d", cs.calls, tt.wantCalls)
			}
			if effects != tt.wantEffects {
				t.Errorf("handler runs = %d, want %d", effects, tt.wantEffects)
			}
			ev := onlyEvent(t, e)
			if ev.Outcome != tt.wantOutcome {
				t.Errorf("audit outcome = %q, want %q", ev.Outcome, tt.wantOutcome)
			}
		})
	}
}

func TestInvokeUnknownTargetInScopeIsNotFound(t *testing.T) {
	tests := []struct {
		name        string
		target      registry.Target
		scopes      func(webID string) []registry.Scope
		wantMessage string
	}{
		{
			"unknown project",
			registry.Target{Project: "ghost"},
			func(string) []registry.Scope { return []registry.Scope{wildcard} },
			`project "ghost" not found`,
		},
		{
			"unknown environment in a known project",
			registry.Target{Project: "web", Environment: "ghost"},
			func(string) []registry.Scope { return []registry.Scope{wildcard} },
			`environment "ghost" not found in project "web"`,
		},
		{
			"unknown environment under the project and any environment",
			registry.Target{Project: "web", Environment: "ghost"},
			func(webID string) []registry.Scope {
				return []registry.Scope{{Project: webID, Environment: "*", Operation: "*"}}
			},
			`environment "ghost" not found in project "web"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			project := createProject(t, e, "web")
			effects := 0
			op, err := registry.Register(e.reg, registry.Spec{Name: "t.op", Method: "POST", Path: "/v1/t", MinRole: store.RoleMember, Destructive: registry.No},
				func(context.Context, registry.Call, empty) (empty, error) { effects++; return empty{}, nil })
			if err != nil {
				t.Fatalf("Register error = %v, want nil", err)
			}
			_, err = registry.Invoke(context.Background(), e.reg, op, e.principal(store.RoleMember, tt.scopes(project.ID)...), tt.target, empty{})
			if !errors.Is(err, registry.ErrNotFound) {
				t.Fatalf("Invoke error = %v, want it to match registry.ErrNotFound", err)
			}
			if errors.Is(err, store.ErrNotFound) {
				t.Errorf("Invoke error = %v, want it not to match store.ErrNotFound", err)
			}
			if err == nil || err.Error() != tt.wantMessage {
				t.Errorf("Invoke error = %q, want %q", err, tt.wantMessage)
			}
			if effects != 0 {
				t.Errorf("handler runs = %d, want 0", effects)
			}
			ev := onlyEvent(t, e)
			if ev.Outcome != "error" || ev.Reason != "not found" {
				t.Errorf("audit outcome/reason = %q/%q, want %q/%q", ev.Outcome, ev.Reason, "error", "not found")
			}
		})
	}
}

type targetIDs struct {
	web   string
	prod  string
	other string
}

func TestInvokeUnknownTargetOutOfScopeIsDenied(t *testing.T) {
	tests := []struct {
		name     string
		scopes   func(targetIDs) []registry.Scope
		baseline registry.Target
		target   registry.Target
	}{
		{
			"unknown project under another project's row",
			func(x targetIDs) []registry.Scope {
				return []registry.Scope{{Project: x.other, Environment: "*", Operation: "*"}}
			},
			registry.Target{Project: "web", Environment: "prod"},
			registry.Target{Project: "ghost"},
		},
		{
			"unknown environment under another project's row",
			func(x targetIDs) []registry.Scope {
				return []registry.Scope{{Project: x.other, Environment: "*", Operation: "*"}}
			},
			registry.Target{Project: "web", Environment: "prod"},
			registry.Target{Project: "web", Environment: "ghost"},
		},
		{
			"unknown environment under a specific environment row",
			func(x targetIDs) []registry.Scope {
				return []registry.Scope{{Project: x.web, Environment: x.prod, Operation: "*"}}
			},
			registry.Target{Project: "web", Environment: "stage"},
			registry.Target{Project: "web", Environment: "ghost"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			web := createProject(t, e, "web")
			prod := createEnvironment(t, e, web.ID, "prod")
			_ = createEnvironment(t, e, web.ID, "stage")
			other := createProject(t, e, "other")
			effects := 0
			op, err := registry.Register(e.reg, registry.Spec{Name: "t.op", Method: "POST", Path: "/v1/t", MinRole: store.RoleMember, Destructive: registry.No},
				func(context.Context, registry.Call, empty) (empty, error) { effects++; return empty{}, nil })
			if err != nil {
				t.Fatalf("Register error = %v, want nil", err)
			}
			p := e.principal(store.RoleOwner, tt.scopes(targetIDs{web: web.ID, prod: prod.ID, other: other.ID})...)
			ctx := context.Background()
			_, baseErr := registry.Invoke(ctx, e.reg, op, p, tt.baseline, empty{})
			if !errors.Is(baseErr, registry.ErrDenied) {
				t.Fatalf("baseline Invoke(%+v) error = %v, want ErrDenied", tt.baseline, baseErr)
			}
			_, err = registry.Invoke(ctx, e.reg, op, p, tt.target, empty{})
			if !errors.Is(err, registry.ErrDenied) {
				t.Fatalf("Invoke(%+v) error = %v, want ErrDenied", tt.target, err)
			}
			if errors.Is(err, registry.ErrNotFound) {
				t.Errorf("Invoke(%+v) error = %v, want it not to match registry.ErrNotFound", tt.target, err)
			}
			if err == nil || err.Error() != baseErr.Error() {
				t.Errorf("Invoke(%+v) error = %q, want the baseline error %q", tt.target, err, baseErr)
			}
			if effects != 0 {
				t.Errorf("handler runs = %d, want 0", effects)
			}
			evs, err := e.st.AuditEvents(ctx, e.orgID)
			if err != nil {
				t.Fatalf("AuditEvents error = %v, want nil", err)
			}
			if len(evs) != 2 {
				t.Fatalf("AuditEvents = %d events, want 2", len(evs))
			}
			base, got := evs[0], evs[1]
			if base.Outcome != "denied" {
				t.Fatalf("baseline audit outcome = %q, want %q", base.Outcome, "denied")
			}
			if got.Outcome != base.Outcome || got.Reason != base.Reason {
				t.Errorf("audit outcome/reason = %q/%q, want the baseline %q/%q", got.Outcome, got.Reason, base.Outcome, base.Reason)
			}
		})
	}
}

func TestInvokePassesResolvedIDsToHandler(t *testing.T) {
	tests := []struct {
		name            string
		level           string
		target          registry.Target
		wantProject     bool
		wantEnvironment bool
	}{
		{"project level", "project", registry.Target{Project: "web"}, true, false},
		{"environment level", "environment", registry.Target{Project: "web", Environment: "prod"}, true, true},
		{"org level", "org", registry.Target{}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			web := createProject(t, e, "web")
			prod := createEnvironment(t, e, web.ID, "prod")
			var scopes []registry.Scope
			switch tt.level {
			case "project":
				scopes = []registry.Scope{{Project: web.ID, Environment: "*", Operation: "*"}}
			case "environment":
				scopes = []registry.Scope{{Project: web.ID, Environment: prod.ID, Operation: "*"}}
			case "org":
				scopes = []registry.Scope{wildcard}
			}
			want := registry.Call{OrgID: e.orgID, UserID: e.userID}
			if tt.wantProject {
				want.ProjectID = web.ID
			}
			if tt.wantEnvironment {
				want.EnvironmentID = prod.ID
			}
			var got registry.Call
			ran := 0
			op, err := registry.Register(e.reg, registry.Spec{Name: "t.op", Method: "POST", Path: "/v1/t", MinRole: store.RoleMember, Destructive: registry.No},
				func(_ context.Context, c registry.Call, _ empty) (empty, error) { ran++; got = c; return empty{}, nil })
			if err != nil {
				t.Fatalf("Register error = %v, want nil", err)
			}
			if _, err := registry.Invoke(context.Background(), e.reg, op, e.principal(store.RoleMember, scopes...), tt.target, empty{}); err != nil {
				t.Fatalf("Invoke(%+v) error = %v, want nil", tt.target, err)
			}
			if ran != 1 {
				t.Errorf("handler runs = %d, want 1", ran)
			}
			if got != want {
				t.Errorf("handler Call = %+v, want %+v", got, want)
			}
		})
	}
}

func TestRefusalKindsAndAuditReasons(t *testing.T) {
	allKinds := []error{registry.ErrInvalidInput, registry.ErrNotFound, registry.ErrConflict}
	tests := []struct {
		name   string
		kind   error
		reason string
	}{
		{"invalid input", registry.ErrInvalidInput, "invalid input"},
		{"not found", registry.ErrNotFound, "not found"},
		{"conflict", registry.ErrConflict, "conflict"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			msg := "refused: " + tt.name
			op, err := registry.Register(e.reg, registry.Spec{Name: "t.op", Method: "POST", Path: "/v1/t", MinRole: store.RoleMember, Destructive: registry.No},
				func(context.Context, registry.Call, empty) (empty, error) {
					return empty{}, registry.Refuse(tt.kind, msg)
				})
			if err != nil {
				t.Fatalf("Register error = %v, want nil", err)
			}
			_, err = registry.Invoke(context.Background(), e.reg, op, e.principal(store.RoleMember, wildcard), registry.Target{}, empty{})
			if !errors.Is(err, tt.kind) {
				t.Fatalf("Invoke error = %v, want it to match %v", err, tt.kind)
			}
			for _, other := range allKinds {
				if errors.Is(other, tt.kind) {
					continue
				}
				if errors.Is(err, other) {
					t.Errorf("Invoke error = %v, want it not to match %v", err, other)
				}
			}
			if err == nil || err.Error() != msg {
				t.Errorf("Invoke error = %q, want exactly %q", err, msg)
			}
			if unwrapped := errors.Unwrap(err); unwrapped != nil {
				t.Errorf("errors.Unwrap(error) = %v, want nil", unwrapped)
			}
			ev := onlyEvent(t, e)
			if ev.Outcome != "error" || ev.Reason != tt.reason {
				t.Errorf("audit outcome/reason = %q/%q, want %q/%q", ev.Outcome, ev.Reason, "error", tt.reason)
			}
			if strings.Contains(ev.Reason, msg) {
				t.Errorf("audit reason = %q, want it not to hold the refusal message %q", ev.Reason, msg)
			}
		})
	}
}

func TestUnmappedStoreErrorIsNotARefusal(t *testing.T) {
	allKinds := []error{registry.ErrInvalidInput, registry.ErrNotFound, registry.ErrConflict}
	tests := []struct {
		name     string
		sentinel error
	}{
		{"store not found", store.ErrNotFound},
		{"store duplicate", store.ErrDuplicate},
		{"store invalid name", store.ErrInvalidName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			effects := 0
			wrapped := fmt.Errorf("create project %q: %w", "web", tt.sentinel)
			op, err := registry.Register(e.reg, registry.Spec{Name: "t.op", Method: "POST", Path: "/v1/t", MinRole: store.RoleMember, Destructive: registry.No},
				func(context.Context, registry.Call, empty) (empty, error) { effects++; return empty{}, wrapped })
			if err != nil {
				t.Fatalf("Register error = %v, want nil", err)
			}
			_, err = registry.Invoke(context.Background(), e.reg, op, e.principal(store.RoleMember, wildcard), registry.Target{}, empty{})
			if !errors.Is(err, tt.sentinel) {
				t.Fatalf("Invoke error = %v, want it to match %v", err, tt.sentinel)
			}
			for _, kind := range allKinds {
				if errors.Is(err, kind) {
					t.Errorf("Invoke error = %v, want it not to match the refusal kind %v", err, kind)
				}
			}
			if err == nil || err.Error() != wrapped.Error() {
				t.Errorf("Invoke error = %q, want the handler error %q unchanged", err, wrapped)
			}
			if effects != 1 {
				t.Errorf("handler runs = %d, want 1", effects)
			}
			ev := onlyEvent(t, e)
			if ev.Outcome != "error" || ev.Reason != "the handler returned an error" {
				t.Errorf("audit outcome/reason = %q/%q, want %q/%q", ev.Outcome, ev.Reason, "error", "the handler returned an error")
			}
		})
	}
}

func TestInvokeResolveStoreFailureIsNotARefusal(t *testing.T) {
	e := newEnv(t)
	injected := errors.New("disk on fire")
	cs := &countingStore{Store: e.st, err: injected}
	reg := registry.New(cs, func() time.Time { return t0 })
	effects := 0
	op, err := registry.Register(reg, registry.Spec{Name: "t.op", Method: "POST", Path: "/v1/t", MinRole: store.RoleMember, Destructive: registry.No},
		func(context.Context, registry.Call, empty) (empty, error) { effects++; return empty{}, nil })
	if err != nil {
		t.Fatalf("Register error = %v, want nil", err)
	}
	_, err = registry.Invoke(context.Background(), reg, op, e.principal(store.RoleMember, wildcard), registry.Target{Project: "web"}, empty{})
	if !errors.Is(err, injected) {
		t.Fatalf("Invoke error = %v, want it to wrap %v", err, injected)
	}
	for _, kind := range []error{registry.ErrInvalidInput, registry.ErrNotFound, registry.ErrConflict, registry.ErrDenied} {
		if errors.Is(err, kind) {
			t.Errorf("Invoke error = %v, want it not to match %v", err, kind)
		}
	}
	if cs.calls != 1 {
		t.Errorf("ResolveTarget calls = %d, want 1", cs.calls)
	}
	if effects != 0 {
		t.Errorf("handler runs = %d, want 0", effects)
	}
	ev := onlyEvent(t, e)
	if ev.Outcome != "error" || ev.Reason != "the target could not be resolved" {
		t.Errorf("audit outcome/reason = %q/%q, want %q/%q", ev.Outcome, ev.Reason, "error", "the target could not be resolved")
	}
	if strings.Contains(ev.Reason, injected.Error()) {
		t.Errorf("audit reason = %q, want it not to hold the store error %q", ev.Reason, injected)
	}
}
