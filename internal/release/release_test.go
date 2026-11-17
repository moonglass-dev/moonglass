package release

import (
	"errors"
	"reflect"
	"testing"
)

// must applies a transition and fails the test on error.
func must(t *testing.T, s State, err error) State {
	t.Helper()
	if err != nil {
		t.Fatalf("transition error = %v", err)
	}
	return s
}

func ids(s State) (cur, prev, cand string) {
	if s.Current != nil {
		cur = s.Current.ID
	}
	if s.Previous != nil {
		prev = s.Previous.ID
	}
	if s.Candidate != nil {
		cand = s.Candidate.ID
	}
	return cur, prev, cand
}

// withCurrent returns an app serving id.
func withCurrent(t *testing.T, id string) State {
	t.Helper()
	s, err := State{}.Deploy(id, "img:"+id, false)
	s = must(t, s, err)
	s, err = s.Switch(id)
	return must(t, s, err)
}

func TestDeployThenSwitchMakesNewCurrent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		start        func(t *testing.T) State
		hasPreDeploy bool
		wantPrev     string
	}{
		{"empty app, no pre-deploy", func(*testing.T) State { return State{} }, false, ""},
		{"empty app, pre-deploy", func(*testing.T) State { return State{} }, true, ""},
		{"with current, no pre-deploy", func(t *testing.T) State { return withCurrent(t, "r1") }, false, "r1"},
		{"with current, pre-deploy", func(t *testing.T) State { return withCurrent(t, "r1") }, true, "r1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			start := tt.start(t)
			wantCur, _, _ := ids(start)

			s, err := start.Deploy("r2", "img:r2", tt.hasPreDeploy)
			s = must(t, s, err)
			if cur, _, cand := ids(s); cur != wantCur || cand != "r2" {
				t.Fatalf("after Deploy: expected current %q candidate %q, got current %q candidate %q", wantCur, "r2", cur, cand)
			}
			if tt.hasPreDeploy {
				if s.Phase != PhasePending {
					t.Fatalf("phase after Deploy with pre-deploy: expected %v, got %v", PhasePending, s.Phase)
				}
				s, err = s.PreDeployDone("r2", true)
				s = must(t, s, err)
			}
			if s.Phase != PhaseReady {
				t.Fatalf("phase before Switch: expected %v, got %v", PhaseReady, s.Phase)
			}
			if cur, _, _ := ids(s); cur != wantCur {
				t.Fatalf("current before Switch: expected %q, got %q", wantCur, cur)
			}

			s, err = s.Switch("r2")
			s = must(t, s, err)
			cur, prev, cand := ids(s)
			if cur != "r2" || prev != tt.wantPrev || cand != "" {
				t.Fatalf("after Switch: expected current r2 previous %q candidate \"\", got current %q previous %q candidate %q",
					tt.wantPrev, cur, prev, cand)
			}
			if s.Current.Image != "img:r2" {
				t.Fatalf("current image: expected %q, got %q", "img:r2", s.Current.Image)
			}
			if s.Phase != PhaseNone {
				t.Fatalf("phase after Switch: expected %v, got %v", PhaseNone, s.Phase)
			}
		})
	}
}

func TestPreDeployFailureLeavesOldServing(t *testing.T) {
	t.Parallel()
	s := withCurrent(t, "r0")
	s = withCurrentAfter(t, s, "r1") // current r1, previous r0
	_, wantPrev, _ := ids(s)

	s, err := s.Deploy("r2", "img:r2", true)
	s = must(t, s, err)
	s, err = s.PreDeployDone("r2", false)
	s = must(t, s, err)

	cur, prev, cand := ids(s)
	if cur != "r1" || prev != wantPrev || cand != "" {
		t.Fatalf("after failed pre-deploy: expected current r1 previous %q candidate \"\", got current %q previous %q candidate %q",
			wantPrev, cur, prev, cand)
	}
	if s.Phase != PhaseNone {
		t.Fatalf("phase after abort: expected %v, got %v", PhaseNone, s.Phase)
	}
	before := s
	if got, err := s.Switch("r2"); err == nil || !reflect.DeepEqual(got, before) {
		t.Fatalf("Switch after abort: expected error and unchanged state, got err %v state %+v", err, got)
	}
	if _, err := s.Deploy("r3", "img:r3", false); err != nil {
		t.Fatalf("Deploy after abort: expected nil error, got %v", err)
	}
}

// withCurrentAfter deploys and switches id on top of s.
func withCurrentAfter(t *testing.T, s State, id string) State {
	t.Helper()
	s, err := s.Deploy(id, "img:"+id, false)
	s = must(t, s, err)
	s, err = s.Switch(id)
	return must(t, s, err)
}

func TestRollbackRestoresPrevious(t *testing.T) {
	t.Parallel()
	s := withCurrent(t, "r1")
	s = withCurrentAfter(t, s, "r2")

	s, err := s.Rollback()
	s = must(t, s, err)
	cur, prev, cand := ids(s)
	if cur != "r1" || prev != "" || cand != "" {
		t.Fatalf("after Rollback: expected current r1 previous \"\" candidate \"\", got current %q previous %q candidate %q", cur, prev, cand)
	}
	if got, err := s.Rollback(); err == nil || !reflect.DeepEqual(got, s) {
		t.Fatalf("second Rollback: expected error and unchanged state, got err %v state %+v", err, got)
	}

	t.Run("candidate untouched", func(t *testing.T) {
		t.Parallel()
		s := withCurrent(t, "r1")
		s = withCurrentAfter(t, s, "r2")
		s, err := s.Deploy("r3", "img:r3", true)
		s = must(t, s, err)
		s, err = s.Rollback()
		s = must(t, s, err)
		cur, prev, cand := ids(s)
		if cur != "r1" || prev != "" || cand != "r3" || s.Phase != PhasePending {
			t.Fatalf("Rollback with candidate: expected current r1 previous \"\" candidate r3 phase %v, got current %q previous %q candidate %q phase %v",
				PhasePending, cur, prev, cand, s.Phase)
		}
	})
}

func TestIllegalTransitionDoesNotChangeState(t *testing.T) {
	t.Parallel()
	pending := func(t *testing.T) State {
		s, err := withCurrent(t, "r1").Deploy("r2", "img:r2", true)
		return must(t, s, err)
	}
	ready := func(t *testing.T) State {
		s, err := withCurrent(t, "r1").Deploy("r2", "img:r2", false)
		return must(t, s, err)
	}
	tests := []struct {
		name  string
		start func(t *testing.T) State
		apply func(State) (State, error)
	}{
		{
			"switch with no candidate", func(t *testing.T) State { return withCurrent(t, "r1") },
			func(s State) (State, error) { return s.Switch("r2") },
		},
		{
			"switch on empty app", func(*testing.T) State { return State{} },
			func(s State) (State, error) { return s.Switch("r2") },
		},
		{
			"switch while pending", pending,
			func(s State) (State, error) { return s.Switch("r2") },
		},
		{
			"switch with wrong id", ready,
			func(s State) (State, error) { return s.Switch("r9") },
		},
		{
			"rollback with no previous", func(t *testing.T) State { return withCurrent(t, "r1") },
			func(s State) (State, error) { return s.Rollback() },
		},
		{
			"rollback on empty app", func(*testing.T) State { return State{} },
			func(s State) (State, error) { return s.Rollback() },
		},
		{
			"pre-deploy done with no candidate", func(t *testing.T) State { return withCurrent(t, "r1") },
			func(s State) (State, error) { return s.PreDeployDone("r2", true) },
		},
		{
			"pre-deploy done with wrong id", pending,
			func(s State) (State, error) { return s.PreDeployDone("r9", true) },
		},
		{
			"pre-deploy done on ready candidate", ready,
			func(s State) (State, error) { return s.PreDeployDone("r2", true) },
		},
		{
			"deploy while candidate pending", pending,
			func(s State) (State, error) { return s.Deploy("r3", "img:r3", false) },
		},
		{
			"deploy while candidate ready", ready,
			func(s State) (State, error) { return s.Deploy("r3", "img:r3", false) },
		},
		{
			"deploy with current id", func(t *testing.T) State { return withCurrent(t, "r1") },
			func(s State) (State, error) { return s.Deploy("r1", "img:r1", false) },
		},
		{
			"deploy with previous id", func(t *testing.T) State { return withCurrentAfter(t, withCurrent(t, "r1"), "r2") },
			func(s State) (State, error) { return s.Deploy("r1", "img:r1", false) },
		},
		{
			"deploy with empty id", func(*testing.T) State { return State{} },
			func(s State) (State, error) { return s.Deploy("", "img", false) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			start := tt.start(t)
			want := start
			got, err := tt.apply(start)
			if !errors.Is(err, ErrIllegalTransition) {
				t.Fatalf("error: expected ErrIllegalTransition, got %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("returned state: expected %+v, got %+v", want, got)
			}
			if !reflect.DeepEqual(start, want) {
				t.Fatalf("input state changed: expected %+v, got %+v", want, start)
			}
		})
	}
}
