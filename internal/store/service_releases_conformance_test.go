package store_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"moonglass.dev/moonglass/internal/store"
)

func TestServiceReleaseStateConformanceSQLite(t *testing.T) {
	s, _ := newSQLite(t)
	runServiceReleaseStateConformance(t, s)
}

func TestServiceReleaseStateConformancePostgres(t *testing.T) {
	runServiceReleaseStateConformance(t, newPGEnv(t, true).openApp(t))
}

// runServiceReleaseStateConformance covers the two release-id columns of a
// service and the three store calls that read and write them.
func runServiceReleaseStateConformance(t *testing.T, s store.Store) {
	ctx := context.Background()

	t.Run("new service has no releases", func(t *testing.T) {
		tgt := seedTarget(t, s)
		svc := seedService(t, s, tgt, tgt.server, "fresh")
		if svc.CurrentReleaseID != "" || svc.PreviousReleaseID != "" {
			t.Errorf("CreateService release ids = (%q, %q), want empty and empty", svc.CurrentReleaseID, svc.PreviousReleaseID)
		}

		serviceByID := func(list []store.Service) (store.Service, bool) {
			for _, got := range list {
				if got.ID == svc.ID {
					return got, true
				}
			}
			return store.Service{}, false
		}

		list, err := s.Services(ctx, tgt.orgID, tgt.env.ID)
		if err != nil {
			t.Fatalf("Services error = %v, want nil", err)
		}
		listed, ok := serviceByID(list)
		if !ok {
			t.Fatalf("Services = %+v, want the service %s among them", list, svc.ID)
		}
		if listed.CurrentReleaseID != "" || listed.PreviousReleaseID != "" {
			t.Errorf("Services release ids = (%q, %q), want empty and empty", listed.CurrentReleaseID, listed.PreviousReleaseID)
		}

		byName, err := s.ServiceByName(ctx, tgt.orgID, tgt.env.ID, "fresh")
		if err != nil {
			t.Fatalf("ServiceByName(fresh) error = %v, want nil", err)
		}
		if byName.CurrentReleaseID != "" || byName.PreviousReleaseID != "" {
			t.Errorf("ServiceByName release ids = (%q, %q), want empty and empty", byName.CurrentReleaseID, byName.PreviousReleaseID)
		}
	})

	t.Run("by name", func(t *testing.T) {
		tgt := seedTarget(t, s)
		svc := seedService(t, s, tgt, tgt.server, "web")
		got, err := s.ServiceByName(ctx, tgt.orgID, tgt.env.ID, "web")
		if err != nil {
			t.Fatalf("ServiceByName(web) in org %s error = %v, want nil", tgt.orgID, err)
		}
		if !reflect.DeepEqual(got, svc) {
			t.Errorf("ServiceByName(web) = %+v, want the CreateService result %+v", got, svc)
		}
	})

	t.Run("by name not found", func(t *testing.T) {
		a, b := seedTarget(t, s), seedTarget(t, s)
		second := mustCreateEnvironment(t, s, a.orgID, a.project.ID, "staging")
		for _, tc := range []struct {
			name string
			org  string
			env  string
			svc  string
		}{
			{"unknown name", a.orgID, a.env.ID, "missing"},
			{"name in another environment of the org", a.orgID, second.ID, "api"},
			{"name in another org's environment", a.orgID, b.env.ID, "api"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if _, err := s.ServiceByName(ctx, tc.org, tc.env, tc.svc); !errors.Is(err, store.ErrNotFound) {
					t.Errorf("ServiceByName(%q, environment %s) in org %s error = %v, want ErrNotFound", tc.svc, tc.env, tc.org, err)
				}
			})
		}
	})

	t.Run("set and read back", func(t *testing.T) {
		tgt := seedTarget(t, s)
		svc := seedService(t, s, tgt, tgt.server, "web")

		if err := s.SetServiceReleases(ctx, tgt.orgID, svc.ID, "r2", "r1"); err != nil {
			t.Fatalf("SetServiceReleases(r2, r1) error = %v, want nil", err)
		}
		readReleaseIDs := func() (string, string) {
			t.Helper()
			byName, err := s.ServiceByName(ctx, tgt.orgID, tgt.env.ID, "web")
			if err != nil {
				t.Fatalf("ServiceByName(web) error = %v, want nil", err)
			}
			list, err := s.Services(ctx, tgt.orgID, tgt.env.ID)
			if err != nil {
				t.Fatalf("Services error = %v, want nil", err)
			}
			found := false
			for _, got := range list {
				if got.ID != svc.ID {
					continue
				}
				found = true
				if got.CurrentReleaseID != byName.CurrentReleaseID || got.PreviousReleaseID != byName.PreviousReleaseID {
					t.Errorf("Services release ids = (%q, %q), ServiceByName release ids = (%q, %q), want them equal",
						got.CurrentReleaseID, got.PreviousReleaseID, byName.CurrentReleaseID, byName.PreviousReleaseID)
				}
			}
			if !found {
				t.Errorf("Services = %+v, want the service %s among them", list, svc.ID)
			}
			return byName.CurrentReleaseID, byName.PreviousReleaseID
		}

		if got, prev := readReleaseIDs(); got != "r2" || prev != "r1" {
			t.Errorf("release ids after SetServiceReleases(r2, r1) = (%q, %q), want (%q, %q)", got, prev, "r2", "r1")
		}

		if err := s.SetServiceReleases(ctx, tgt.orgID, svc.ID, "r3", ""); err != nil {
			t.Fatalf("SetServiceReleases(r3, \"\") error = %v, want nil", err)
		}
		if got, prev := readReleaseIDs(); got != "r3" || prev != "" {
			t.Errorf("release ids after SetServiceReleases(r3, \"\") = (%q, %q), want (%q, empty)", got, prev, "r3")
		}
	})

	t.Run("set refused", func(t *testing.T) {
		a, b := seedTarget(t, s), seedTarget(t, s)
		svc := seedService(t, s, a, a.server, "web")
		bSvc := seedService(t, s, b, b.server, "web")
		if err := s.SetServiceReleases(ctx, a.orgID, svc.ID, "r2", "r1"); err != nil {
			t.Fatalf("SetServiceReleases on org %s's service error = %v, want nil", a.orgID, err)
		}

		for _, serviceID := range []string{randHex(t, 16), bSvc.ID} {
			if err := s.SetServiceReleases(ctx, a.orgID, serviceID, "x3", "x2"); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("SetServiceReleases(service %s) in org %s error = %v, want ErrNotFound", serviceID, a.orgID, err)
			}
		}

		kept, err := s.ServiceByName(ctx, a.orgID, a.env.ID, "web")
		if err != nil {
			t.Fatalf("ServiceByName(web) in org %s error = %v, want nil", a.orgID, err)
		}
		if kept.CurrentReleaseID != "r2" || kept.PreviousReleaseID != "r1" {
			t.Errorf("org %s's service release ids after the refused sets = (%q, %q), want (%q, %q)",
				a.orgID, kept.CurrentReleaseID, kept.PreviousReleaseID, "r2", "r1")
		}

		other, err := s.ServiceByName(ctx, b.orgID, b.env.ID, "web")
		if err != nil {
			t.Fatalf("ServiceByName(web) in org %s error = %v, want nil", b.orgID, err)
		}
		if other.CurrentReleaseID != "" || other.PreviousReleaseID != "" {
			t.Errorf("org %s's service release ids after org %s's refused sets = (%q, %q), want empty and empty",
				b.orgID, a.orgID, other.CurrentReleaseID, other.PreviousReleaseID)
		}
	})
}
