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
// org-level calls, which only a "*" scope row matches.
type Target struct {
	ProjectID     string
	EnvironmentID string
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

// Principal is the authenticated caller. Agent marks an agent identity.
type Principal struct {
	OrgID   string
	UserID  string
	TokenID string
	Role    store.Role
	Scopes  []Scope
	Agent   bool
}

// Call is what a handler may know about its caller. It has no methods: a
// handler cannot write an audit event.
type Call struct {
	OrgID  string
	UserID string
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
// writes one audit event: allowed, denied or error.
func Invoke[In, Out any](ctx context.Context, r *Registry, op *Operation[In, Out], p Principal, t Target, in In) (Out, error) {
	var zero Out
	if err := authorise(op.Spec, p, t); err != nil {
		if aerr := r.audit(ctx, p, op.Spec.Name, "denied", err.Error()); aerr != nil {
			return zero, aerr
		}
		return zero, err
	}
	out, err := op.handler(ctx, Call{OrgID: p.OrgID, UserID: p.UserID}, in)
	outcome, reason := "allowed", ""
	if err != nil {
		outcome, reason = "error", "the handler returned an error"
	}
	if aerr := r.audit(ctx, p, op.Spec.Name, outcome, reason); aerr != nil {
		return zero, aerr
	}
	if err != nil {
		return zero, err
	}
	return out, nil
}

func authorise(s Spec, p Principal, t Target) error {
	if roleRank[p.Role] < roleRank[s.MinRole] {
		return fmt.Errorf("%w: %s needs role %s, the caller has %q", ErrDenied, s.Name, s.MinRole, p.Role)
	}
	if !scopeAllows(p.Scopes, s.Name, t) {
		return fmt.Errorf("%w: no scope row of the token covers %s on this target", ErrDenied, s.Name)
	}
	if s.Destructive == Yes && p.Agent {
		return fmt.Errorf("%w: %s", ErrPendingApproval, s.Name)
	}
	return nil
}

func scopeAllows(rows []Scope, operation string, t Target) bool {
	for _, row := range rows {
		if matches(row.Operation, operation) && matches(row.Project, t.ProjectID) && matches(row.Environment, t.EnvironmentID) {
			return true
		}
	}
	return false
}

func matches(pattern, value string) bool { return pattern == "*" || pattern == value }

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
			Operation: operation, Outcome: outcome, Reason: reason, PrevHash: prev,
		})
	})
	if err != nil {
		return fmt.Errorf("write audit event for %s: %w", operation, err)
	}
	return nil
}
