// Package release is the pure state machine for one app's releases. It has no Docker and no I/O.
package release

import (
	"errors"
	"fmt"
)

// ErrIllegalTransition is returned, wrapped with context, for any transition the machine does not allow.
var ErrIllegalTransition = errors.New("release: illegal transition")

// Release is one deployable version of an app.
type Release struct {
	ID    string
	Image string
}

// Phase is the state of the candidate release.
type Phase int

const (
	// PhaseNone means there is no candidate.
	PhaseNone Phase = iota
	// PhasePending means the candidate waits for its pre-deploy outcome.
	PhasePending
	// PhaseReady means the candidate can be switched to current.
	PhaseReady
)

func (p Phase) String() string {
	switch p {
	case PhaseNone:
		return "none"
	case PhasePending:
		return "pending"
	case PhaseReady:
		return "ready"
	default:
		return fmt.Sprintf("phase(%d)", int(p))
	}
}

// State is one app's releases. The zero value is an app with no releases.
// Previous is set only if Current is set. Phase is PhaseNone exactly when Candidate is nil.
// Transitions take and return State by value, so a failed transition leaves it unchanged.
type State struct {
	Current   *Release
	Previous  *Release
	Candidate *Release
	Phase     Phase
}

func illegal(format string, args ...any) error {
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), ErrIllegalTransition)
}

// Deploy creates a candidate release. It is ready at once unless hasPreDeploy is true.
// It is illegal while a candidate exists, with an empty id, or with an id already current or previous.
func (s State) Deploy(id, image string, hasPreDeploy bool) (State, error) {
	if id == "" {
		return s, illegal("deploy with empty release id; give the release an id")
	}
	if s.Candidate != nil {
		return s, illegal("deploy %q while candidate %q exists; switch or abort it first", id, s.Candidate.ID)
	}
	if s.Current != nil && s.Current.ID == id {
		return s, illegal("deploy %q, which is already current; use a new release id", id)
	}
	if s.Previous != nil && s.Previous.ID == id {
		return s, illegal("deploy %q, which is already previous; use a new release id", id)
	}
	next := s
	next.Candidate = &Release{ID: id, Image: image}
	next.Phase = PhaseReady
	if hasPreDeploy {
		next.Phase = PhasePending
	}
	return next, nil
}

// PreDeployDone records the pre-deploy outcome for the pending candidate id.
// Success makes it ready. Failure aborts it: the candidate is cleared and the current release is unchanged.
func (s State) PreDeployDone(id string, ok bool) (State, error) {
	if s.Candidate == nil {
		return s, illegal("pre-deploy done for %q with no candidate; deploy first", id)
	}
	if s.Candidate.ID != id {
		return s, illegal("pre-deploy done for %q, candidate is %q", id, s.Candidate.ID)
	}
	if s.Phase != PhasePending {
		return s, illegal("pre-deploy done for %q, candidate phase is %v, expected %v", id, s.Phase, PhasePending)
	}
	next := s
	if ok {
		next.Phase = PhaseReady
		return next, nil
	}
	next.Candidate = nil
	next.Phase = PhaseNone
	return next, nil
}

// Switch promotes the ready candidate id to current and demotes the old current to previous.
func (s State) Switch(id string) (State, error) {
	if s.Candidate == nil {
		return s, illegal("switch to %q with no candidate; deploy first", id)
	}
	if s.Candidate.ID != id {
		return s, illegal("switch to %q, candidate is %q", id, s.Candidate.ID)
	}
	if s.Phase != PhaseReady {
		return s, illegal("switch to %q, candidate phase is %v, expected %v; wait for pre-deploy", id, s.Phase, PhaseReady)
	}
	return State{Current: s.Candidate, Previous: s.Current}, nil
}

// Abort clears the candidate id in either phase. Current and Previous are
// untouched, so the old release keeps serving and a new Deploy is legal.
func (s State) Abort(id string) (State, error) {
	if s.Candidate == nil {
		return s, illegal("abort %q with no candidate; deploy first", id)
	}
	if s.Candidate.ID != id {
		return s, illegal("abort %q, candidate is %q", id, s.Candidate.ID)
	}
	next := s
	next.Candidate = nil
	next.Phase = PhaseNone
	return next, nil
}

// Rollback promotes previous to current and clears previous. A candidate is left untouched.
func (s State) Rollback() (State, error) {
	if s.Previous == nil {
		return s, illegal("rollback with no previous release; deploy a new release instead")
	}
	next := s
	next.Current = s.Previous
	next.Previous = nil
	return next, nil
}
