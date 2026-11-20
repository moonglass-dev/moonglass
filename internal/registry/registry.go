// Package registry is the one operation registry (ADR-0006). Every operation
// is declared here once; REST and the CLI are generated from the declarations.
// The registry checks role, scope and gates, and writes the audit event, so a
// handler cannot skip either.
package registry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"moonglass.dev/moonglass/internal/store"
)

// Flag is a tri-state so an omitted value is detectable: the zero value is Unset.
type Flag uint8

// The values of a Spec's Destructive flag. Unset is rejected by Register.
const (
	Unset Flag = iota
	No
	Yes
)

// Errors returned by Invoke and Authenticate. Test them with errors.Is.
var (
	ErrUnauthenticated = errors.New("registry: missing, unknown, expired or revoked token")
	ErrDenied          = errors.New("registry: operation denied")
	ErrPendingApproval = errors.New("registry: destructive call by an agent identity is pending approval")
)

// Scope is one token scope row; "*" matches anything.
type Scope = store.Scope

// Target names the project and environment a call acts on. Both are empty for
// org-level calls, which only a "*" scope row matches; Invoke resolves the
// names in the caller's org.
type Target struct {
	Project     string
	Environment string
}

// Spec declares an operation. Destructive has no default.
type Spec struct {
	Name        string
	Summary     string
	Method      string
	Path        string
	MinRole     store.Role
	Destructive Flag
}

// Principal is the authenticated caller. TokenID names the bearer and SessionID
// the sign-in session; exactly one is set. Agent marks an agent identity.
type Principal struct {
	OrgID     string
	UserID    string
	TokenID   string
	SessionID string
	Role      store.Role
	Scopes    []Scope
	Agent     bool
}

// Call is what a handler may know about its caller. Invoke resolves the
// target's names into ProjectID and EnvironmentID before the handler runs. It
// has no methods: a handler cannot write an audit event.
type Call struct {
	OrgID         string
	UserID        string
	ProjectID     string
	EnvironmentID string
}

// Operation is a registered operation.
type Operation[In, Out any] struct {
	handler func(context.Context, Call, In) (Out, error)
	Spec    Spec
}

// Registry holds the registered operation names and the store the audit chain lives in.
type Registry struct {
	st    store.Store
	now   func() time.Time
	names map[string]bool
	// ServersList is the shipped servers.list operation.
	ServersList *Operation[ServersListIn, ServersListOut]
}

var roleRank = map[store.Role]int{store.RoleViewer: 1, store.RoleMember: 2, store.RoleAdmin: 3, store.RoleOwner: 4}

// New returns a registry over st with the shipped operations registered.
func New(st store.Store, now func() time.Time) *Registry {
	r := &Registry{st: st, now: now, names: map[string]bool{}}
	r.ServersList = registerShipped(r, serversListSpec, func(ctx context.Context, c Call, _ ServersListIn) (ServersListOut, error) {
		servers, err := st.Servers(ctx, c.OrgID)
		if err != nil {
			return ServersListOut{}, fmt.Errorf("list servers: %w", err)
		}
		out := ServersListOut{Servers: make([]ServerInfo, 0, len(servers))}
		for _, s := range servers {
			out.Servers = append(out.Servers, ServerInfo{ID: s.ID, CreatedAt: s.CreatedAt})
		}
		return out, nil
	})
	return r
}

// Register adds an operation. It rejects an empty or duplicate name, a missing
// Destructive flag, an invalid role, and a destructive operation below admin.
func Register[In, Out any](r *Registry, s Spec, h func(context.Context, Call, In) (Out, error)) (*Operation[In, Out], error) {
	switch {
	case s.Name == "":
		return nil, errors.New("register operation: name is empty: give every operation a name such as servers.list")
	case r.names[s.Name]:
		return nil, fmt.Errorf("register operation %q: name is already registered: pick a unique name", s.Name)
	case s.Method == "" || s.Path == "":
		return nil, fmt.Errorf("register operation %q: method and path are required", s.Name)
	case s.Destructive != No && s.Destructive != Yes:
		return nil, fmt.Errorf("register operation %q: Destructive is unset: set it to registry.Yes or registry.No", s.Name)
	case !s.MinRole.Valid():
		return nil, fmt.Errorf("register operation %q: MinRole %q is not owner, admin, member or viewer", s.Name, s.MinRole)
	case s.Destructive == Yes && roleRank[s.MinRole] < roleRank[store.RoleAdmin]:
		return nil, fmt.Errorf("register operation %q: a destructive operation needs MinRole admin or owner, got %s", s.Name, s.MinRole)
	}
	r.names[s.Name] = true
	return &Operation[In, Out]{Spec: s, handler: h}, nil
}

func registerShipped[In, Out any](r *Registry, s Spec, h func(context.Context, Call, In) (Out, error)) *Operation[In, Out] {
	op, err := Register(r, s, h)
	if err != nil {
		panic("registry: shipped operation is invalid: " + err.Error())
	}
	return op
}

// Invoke authorises p for op on t, runs the handler only if allowed, and always
// writes one audit event: allowed, denied or error. It checks the role, then
// resolves the target's names with one store call, then matches the scope rows,
// then answers an unknown target, then applies the destructive gate.
func Invoke[In, Out any](ctx context.Context, r *Registry, op *Operation[In, Out], p Principal, t Target, in In) (Out, error) {
	var zero Out
	if roleRank[p.Role] < roleRank[op.Spec.MinRole] {
		err := fmt.Errorf("%w: %s needs role %s, the caller has %q", ErrDenied, op.Spec.Name, op.Spec.MinRole, p.Role)
		return zero, r.deny(ctx, p, op.Spec.Name, err)
	}
	res, notFound, err := r.resolve(ctx, op.Spec.Name, p.OrgID, t)
	if err != nil {
		if aerr := r.audit(ctx, p, op.Spec.Name, "error", resolveFailureReason); aerr != nil {
			return zero, aerr
		}
		return zero, err
	}
	if !scopeAllows(p.Scopes, op.Spec.Name, res) {
		denial := fmt.Errorf("%w: no scope row of the token covers %s on this target", ErrDenied, op.Spec.Name)
		return zero, r.deny(ctx, p, op.Spec.Name, denial)
	}
	if notFound != nil {
		if aerr := r.audit(ctx, p, op.Spec.Name, "error", "not found"); aerr != nil {
			return zero, aerr
		}
		return zero, notFound
	}
	if op.Spec.Destructive == Yes && p.Agent {
		return zero, r.deny(ctx, p, op.Spec.Name, fmt.Errorf("%w: %s", ErrPendingApproval, op.Spec.Name))
	}
	out, err := op.handler(ctx, Call{OrgID: p.OrgID, UserID: p.UserID, ProjectID: res.project, EnvironmentID: res.environment}, in)
	outcome, reason := "allowed", ""
	if err != nil {
		outcome, reason = "error", handlerReason(err)
	}
	if aerr := r.audit(ctx, p, op.Spec.Name, outcome, reason); aerr != nil {
		return zero, aerr
	}
	if err != nil {
		return zero, err
	}
	return out, nil
}

// resolveFailureReason is the fixed audit reason for a failed target lookup. It
// never holds a name or the store's error text.
const resolveFailureReason = "the target could not be resolved"

// resolved holds a target's ids and which of them the store could name. An
// unresolved column is matched only by "*".
type resolved struct {
	project          string
	environment      string
	projectKnown     bool
	environmentKnown bool
}

// resolve turns the target's names into ids in orgID with one store call. An
// org-level target reads nothing. A name that does not resolve comes back as
// notFound with its column unknown, so no matching row reveals which names
// exist; any other store failure is err.
func (r *Registry) resolve(ctx context.Context, operation, orgID string, t Target) (res resolved, notFound, err error) {
	if t == (Target{}) {
		return resolved{projectKnown: true, environmentKnown: true}, nil, nil
	}
	projectID, environmentID, err := r.st.ResolveTarget(ctx, orgID, t.Project, t.Environment)
	switch {
	case err == nil:
		return resolved{project: projectID, environment: environmentID, projectKnown: true, environmentKnown: true}, nil, nil
	case errors.Is(err, store.ErrNotFound) && projectID == "":
		return resolved{}, Refuse(ErrNotFound, fmt.Sprintf("project %q not found", t.Project)), nil
	case errors.Is(err, store.ErrNotFound):
		return resolved{project: projectID, projectKnown: true},
			Refuse(ErrNotFound, fmt.Sprintf("environment %q not found in project %q", t.Environment, t.Project)), nil
	default:
		return resolved{}, nil, fmt.Errorf("resolve the target of %s: %w", operation, err)
	}
}

// deny audits a denial with err's text and returns err, or the audit failure.
func (r *Registry) deny(ctx context.Context, p Principal, operation string, err error) error {
	if aerr := r.audit(ctx, p, operation, "denied", err.Error()); aerr != nil {
		return aerr
	}
	return err
}

func scopeAllows(rows []Scope, operation string, res resolved) bool {
	for _, row := range rows {
		if matches(row.Operation, operation) && column(row.Project, res.project, res.projectKnown) && column(row.Environment, res.environment, res.environmentKnown) {
			return true
		}
	}
	return false
}

func matches(pattern, value string) bool { return pattern == "*" || pattern == value }

// column reports whether a scope pattern covers an id the store named. An
// unresolved column matches only "*".
func column(pattern, id string, known bool) bool { return pattern == "*" || (known && pattern == id) }

func (r *Registry) audit(ctx context.Context, p Principal, operation, outcome, reason string) error {
	id, err := store.NewID()
	if err != nil {
		return fmt.Errorf("write audit event for %s: %w", operation, err)
	}
	kind := "human"
	if p.Agent {
		kind = "agent"
	}
	at := r.now().UTC().Format(time.RFC3339)
	err = r.st.AppendAudit(ctx, p.OrgID, func(prev string, seq int64) store.AuditEvent {
		return sealEvent(store.AuditEvent{
			OrgID: p.OrgID, Seq: seq, ID: id, At: at, PrincipalKind: kind, UserID: p.UserID, TokenID: p.TokenID,
			SessionID: p.SessionID, StepUpID: "",
			Operation: operation, Outcome: outcome, Reason: reason, PrevHash: prev,
		})
	})
	if err != nil {
		return fmt.Errorf("write audit event for %s: %w", operation, err)
	}
	return nil
}
