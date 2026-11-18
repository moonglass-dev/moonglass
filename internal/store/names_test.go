package store_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"moonglass.dev/moonglass/internal/store"
)

func TestProjectNamesValidated(t *testing.T) {
	ctx := context.Background()
	s, raw := newSQLite(t)
	org, srv := seedOrg(t, s)
	project := mustCreateProject(t, s, org, "p")
	env := mustCreateEnvironment(t, s, org, project.ID, "e")

	counts := func(t *testing.T) (projects, environments, services int) {
		t.Helper()
		for _, c := range []struct {
			table string
			dest  *int
		}{
			{"projects", &projects},
			{"environments", &environments},
			{"services", &services},
		} {
			if err := raw.QueryRowContext(ctx, "SELECT count(*) FROM "+c.table).Scan(c.dest); err != nil {
				t.Fatalf("count %s: %v", c.table, err)
			}
		}
		return projects, environments, services
	}

	cases := []struct {
		label string
		name  string
		valid bool
	}{
		{"one letter", "a", true},
		{"letter dash digit", "a-1", true},
		{"63 bytes", strings.Repeat("a", 63), true},
		{"empty", "", false},
		{"64 bytes", strings.Repeat("a", 64), false},
		{"upper case", "Web", false},
		{"underscore", "a_b", false},
		{"dot", "a.b", false},
		{"space", "a b", false},
		{"leading dash", "-a", false},
		{"trailing dash", "a-", false},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			if tc.valid {
				p, err := s.CreateProject(ctx, org, tc.name)
				if err != nil {
					t.Fatalf("CreateProject(%q) error = %v, want nil", tc.name, err)
				}
				e, err := s.CreateEnvironment(ctx, org, p.ID, tc.name)
				if err != nil {
					t.Fatalf("CreateEnvironment(%q) error = %v, want nil", tc.name, err)
				}
				if _, err := s.CreateService(ctx, org, e.ID, tc.name, srv); err != nil {
					t.Errorf("CreateService(%q) error = %v, want nil", tc.name, err)
				}
				return
			}

			beforeProjects, beforeEnvironments, beforeServices := counts(t)
			checkInvalid := func(what string, err error) {
				t.Helper()
				if !errors.Is(err, store.ErrInvalidName) {
					t.Errorf("%s(%q) error = %v, want ErrInvalidName", what, tc.name, err)
					return
				}
				if want := strconv.Quote(tc.name); !strings.Contains(err.Error(), want) {
					t.Errorf("%s(%q) error = %q, want it to quote %s", what, tc.name, err, want)
				}
			}

			_, err := s.CreateProject(ctx, org, tc.name)
			checkInvalid("CreateProject", err)
			_, err = s.CreateEnvironment(ctx, org, project.ID, tc.name)
			checkInvalid("CreateEnvironment", err)
			_, err = s.CreateService(ctx, org, env.ID, tc.name, srv)
			checkInvalid("CreateService", err)

			afterProjects, afterEnvironments, afterServices := counts(t)
			if beforeProjects != afterProjects || beforeEnvironments != afterEnvironments || beforeServices != afterServices {
				t.Errorf("row counts after rejecting %q = projects %d, environments %d, services %d, want %d, %d, %d",
					tc.name, afterProjects, afterEnvironments, afterServices,
					beforeProjects, beforeEnvironments, beforeServices)
			}
		})
	}
}
