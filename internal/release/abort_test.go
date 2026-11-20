package release

import (
	"errors"
	"reflect"
	"testing"
)

// TestAbortClearsCandidate covers the Abort event of docs/specs/release.md: it
// is legal for the candidate in either phase, clears it, leaves Current and
// Previous untouched, and makes a new Deploy legal.
func TestAbortClearsCandidate(t *testing.T) {
	t.Parallel()

	bases := []struct {
		name  string
		start func(t *testing.T) State
	}{
		{"no current", func(*testing.T) State { return State{} }},
		{"with current", func(t *testing.T) State { return withCurrent(t, "r1") }},
		{
			"with current and previous",
			func(t *testing.T) State { return withCurrentAfter(t, withCurrent(t, "r1"), "r2") },
		},
	}
	phases := []struct {
		name         string
		hasPreDeploy bool
	}{
		{"pending candidate", true},
		{"ready candidate", false},
	}

	for _, base := range bases {
		for _, phase := range phases {
			t.Run(base.name+", "+phase.name, func(t *testing.T) {
				t.Parallel()
				start := base.start(t)
				deployed, err := start.Deploy("c1", "img:c1", phase.hasPreDeploy)
				start = must(t, deployed, err)

				got, err := start.Abort("c1")
				if err != nil {
					t.Fatalf("Abort(c1) error = %v, want nil", err)
				}
				if got.Candidate != nil {
					t.Errorf("candidate after Abort = %+v, want none", got.Candidate)
				}
				if got.Phase != PhaseNone {
					t.Errorf("phase after Abort = %v, want %v", got.Phase, PhaseNone)
				}
				if !reflect.DeepEqual(got.Current, start.Current) {
					t.Errorf("current after Abort = %+v, want the input %+v", got.Current, start.Current)
				}
				if !reflect.DeepEqual(got.Previous, start.Previous) {
					t.Errorf("previous after Abort = %+v, want the input %+v", got.Previous, start.Previous)
				}
				if _, err := got.Deploy("next", "img:next", false); err != nil {
					t.Errorf("Deploy after Abort error = %v, want nil", err)
				}
			})
		}
	}

	t.Run("illegal aborts", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name  string
			start func(t *testing.T) State
		}{
			{
				"no candidate on an empty app",
				func(*testing.T) State { return State{} },
			},
			{
				"no candidate after a switch",
				func(t *testing.T) State { return withCurrent(t, "r1") },
			},
			{
				"different id",
				func(t *testing.T) State {
					deployed, err := withCurrent(t, "r1").Deploy("c2", "img:c2", false)
					return must(t, deployed, err)
				},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				start := tc.start(t)
				got, err := start.Abort("c1")
				if !errors.Is(err, ErrIllegalTransition) {
					t.Fatalf("Abort(c1) error = %v, want an error matching ErrIllegalTransition", err)
				}
				if !reflect.DeepEqual(got, start) {
					t.Errorf("state after a refused Abort = %+v, want the input %+v", got, start)
				}
			})
		}
	})
}
