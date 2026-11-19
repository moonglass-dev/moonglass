package store_test

import (
	"context"
	"errors"
	"testing"

	"moonglass.dev/moonglass/internal/store"
)

func TestResolveTargetReportsKnownProjectSQLite(t *testing.T) {
	s, _ := newSQLite(t)
	runResolveTargetConformance(t, s)
}

func TestResolveTargetReportsKnownProjectPostgres(t *testing.T) {
	runResolveTargetConformance(t, newPGEnv(t, true).openApp(t))
}

// runResolveTargetConformance checks every row of the ResolveTarget table in
// docs/specs/storage.md, as org A, with a project that belongs only to org B.
func runResolveTargetConformance(t *testing.T, s store.Store) {
	ctx := context.Background()

	a := seedTarget(t, s)
	b, _ := seedOrg(t, s)
	bOnly := mustCreateProject(t, s, b, "b-only")

	cases := []struct {
		name         string
		project      string
		environment  string
		wantProject  string
		wantEnv      string
		wantNotFound bool
	}{
		{"known project and environment", "web", "prod", a.project.ID, a.env.ID, false},
		{"known project without environment", "web", "", a.project.ID, "", false},
		{"known project with unknown environment", "web", "nope", a.project.ID, "", true},
		{"unknown project with environment", "nope", "prod", "", "", true},
		{"unknown project without environment", "nope", "", "", "", true},
		{"another org's project without environment", bOnly.Name, "", "", "", true},
		{"another org's project with environment", bOnly.Name, "prod", "", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			projectID, environmentID, err := s.ResolveTarget(ctx, a.orgID, tc.project, tc.environment)

			wantErr := "nil"
			if tc.wantNotFound {
				wantErr = "ErrNotFound"
			}
			errOK := err == nil
			if tc.wantNotFound {
				errOK = errors.Is(err, store.ErrNotFound)
			}
			if projectID != tc.wantProject || environmentID != tc.wantEnv || !errOK {
				t.Errorf("ResolveTarget(%q, %q) = (%q, %q, %v), want (%q, %q, %s)",
					tc.project, tc.environment, projectID, environmentID, err,
					tc.wantProject, tc.wantEnv, wantErr)
			}
		})
	}
}
