package store_test

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/store"
)

// pgError is the part of a Postgres server error this suite checks: the
// SQLSTATE code. internal/store tests do not import pgx (ADR-0010).
type pgError interface{ SQLState() string }

var releaseIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func TestReleasesConformanceSQLite(t *testing.T) {
	s, _ := newSQLite(t)
	runReleasesConformance(t, s)
}

func TestReleasesConformancePostgres(t *testing.T) {
	runReleasesConformance(t, newPGEnv(t, true).openApp(t))
}

// seedService adds a service named name on serverID to the target's environment.
func seedService(t *testing.T, s store.Store, tgt orgTarget, serverID, name string) store.Service {
	t.Helper()
	return mustCreateService(t, s, tgt.orgID, tgt.env.ID, name, serverID)
}

// sampleRelease returns a release for serviceID with every input field set,
// and junk in every field that enqueue must ignore.
func sampleRelease(serviceID string) store.Release {
	return store.Release{
		ServiceID:           serviceID,
		ImageRef:            "registry.example/web:1.4",
		GitRepository:       "https://git.example/web.git",
		GitCommit:           "9f2c1e7a",
		Port:                8080,
		HealthPath:          "/healthz",
		Domains:             []string{"web.example.com", "www.example.com"},
		PreDeploy:           []string{"migrate", "--up"},
		PreDeployReversible: true,
		Status:              store.ReleaseSucceeded,
		Step:                store.StepSwitch,
		RequestID:           "junk-request",
		ImageID:             "sha256:junk",
		RepoDigest:          "junk-digest",
		Detail:              "junk detail",
		CreatedAt:           "1999-01-01T00:00:00Z",
		FinishedAt:          "1999-01-02T00:00:00Z",
	}
}

// enqueued returns what EnqueueRelease must store for in: the input fields
// kept, the ignored fields cleared, and the queued status and times set.
func enqueued(in store.Release, orgID, id string, now time.Time) store.Release {
	in.OrgID, in.ID = orgID, id
	in.Status, in.Step = store.ReleaseQueued, ""
	in.RequestID, in.ImageID, in.RepoDigest, in.Detail = "", "", "", ""
	in.CreatedAt, in.FinishedAt = now.Format(time.RFC3339), ""
	return in
}

// at returns the fixed base time plus i seconds, in UTC.
func at(i int) time.Time {
	return time.Date(2026, time.November, 18, 12, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Second)
}

func mustEnqueue(t *testing.T, s store.Store, orgID string, r store.Release, i int) store.Release {
	t.Helper()
	got, err := s.EnqueueRelease(context.Background(), orgID, r, at(i))
	if err != nil {
		t.Fatalf("EnqueueRelease(service %s, at %d) in org %s error = %v, want nil", r.ServiceID, i, orgID, err)
	}
	return got
}

func statuses(rs []store.Release) []store.ReleaseStatus {
	out := make([]store.ReleaseStatus, len(rs))
	for i, r := range rs {
		out[i] = r.Status
	}
	return out
}

func releaseIDs(rs []store.Release) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}

// sameRelease compares every field; string slices compare with slices.Equal,
// so nil and empty count as equal.
func sameRelease(a, b store.Release) bool {
	return a.OrgID == b.OrgID && a.ID == b.ID && a.ServiceID == b.ServiceID &&
		a.ImageRef == b.ImageRef && a.GitRepository == b.GitRepository && a.GitCommit == b.GitCommit &&
		a.Port == b.Port && a.HealthPath == b.HealthPath &&
		slices.Equal(a.Domains, b.Domains) && slices.Equal(a.PreDeploy, b.PreDeploy) &&
		a.PreDeployReversible == b.PreDeployReversible &&
		a.Status == b.Status && a.Step == b.Step && a.RequestID == b.RequestID &&
		a.ImageID == b.ImageID && a.RepoDigest == b.RepoDigest && a.Detail == b.Detail &&
		a.CreatedAt == b.CreatedAt && a.FinishedAt == b.FinishedAt
}

// checkNames reports err unless it is non-nil and names every one of want.
func checkNames(t *testing.T, what string, err error, want ...string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s error = nil, want an error naming %v", what, want)
		return
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("%s error = %q, want it to name %q", what, err, w)
		}
	}
}

func runReleasesConformance(t *testing.T, s store.Store) {
	ctx := context.Background()

	t.Run("enqueue and read", func(t *testing.T) {
		tgt := seedTarget(t, s)
		svc := seedService(t, s, tgt, tgt.server, "web")
		got := mustEnqueue(t, s, tgt.orgID, sampleRelease(svc.ID), 0)
		if !releaseIDPattern.MatchString(got.ID) {
			t.Errorf("EnqueueRelease id = %q, want 32 lowercase hex characters", got.ID)
		}
		want := enqueued(sampleRelease(svc.ID), tgt.orgID, got.ID, at(0))
		if !sameRelease(got, want) {
			t.Errorf("EnqueueRelease = %+v, want %+v", got, want)
		}

		read, err := s.Release(ctx, tgt.orgID, got.ID)
		if err != nil {
			t.Fatalf("Release(%s) error = %v, want nil", got.ID, err)
		}
		if !sameRelease(read, want) {
			t.Errorf("Release = %+v, want %+v", read, want)
		}
		list, err := s.Releases(ctx, tgt.orgID, svc.ID)
		if err != nil {
			t.Fatalf("Releases error = %v, want nil", err)
		}
		if len(list) != 1 || !sameRelease(list[0], want) {
			t.Errorf("Releases = %+v, want exactly %+v", list, want)
		}

		bare := seedService(t, s, tgt, tgt.server, "bare")
		in := store.Release{ServiceID: bare.ID, Port: 9000}
		gotBare := mustEnqueue(t, s, tgt.orgID, in, 1)
		wantBare := enqueued(in, tgt.orgID, gotBare.ID, at(1))
		read, err = s.Release(ctx, tgt.orgID, gotBare.ID)
		if err != nil {
			t.Fatalf("Release(%s) error = %v, want nil", gotBare.ID, err)
		}
		if !sameRelease(read, wantBare) {
			t.Errorf("Release with no argv or domains = %+v, want %+v (no PreDeploy, no Domains, PreDeployReversible false)", read, wantBare)
		}
	})

	t.Run("enqueue refused", func(t *testing.T) {
		a, b := seedTarget(t, s), seedTarget(t, s)
		bSvc := seedService(t, s, b, b.server, "web")
		for _, svcID := range []string{randHex(t, 16), bSvc.ID} {
			if _, err := s.EnqueueRelease(ctx, a.orgID, sampleRelease(svcID), at(0)); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("EnqueueRelease(service %s) in org %s error = %v, want ErrNotFound", svcID, a.orgID, err)
			}
		}
		for _, org := range []string{a.orgID, b.orgID} {
			list, err := s.Releases(ctx, org, bSvc.ID)
			if err != nil {
				t.Fatalf("Releases(org %s) error = %v, want nil", org, err)
			}
			if len(list) != 0 {
				t.Errorf("Releases(org %s, service %s) = %+v, want no rows", org, bSvc.ID, list)
			}
		}
	})

	t.Run("queue", func(t *testing.T) {
		tgt := seedTarget(t, s)
		svc := seedService(t, s, tgt, tgt.server, "web")
		org := tgt.orgID
		r1 := mustEnqueue(t, s, org, sampleRelease(svc.ID), 0)
		if err := s.StartRelease(ctx, org, r1.ID); err != nil {
			t.Fatalf("StartRelease(r1) error = %v, want nil", err)
		}
		r2 := mustEnqueue(t, s, org, sampleRelease(svc.ID), 1)
		r3 := mustEnqueue(t, s, org, sampleRelease(svc.ID), 3)

		list, err := s.Releases(ctx, org, svc.ID)
		if err != nil {
			t.Fatalf("Releases error = %v, want nil", err)
		}
		wantStatuses := []store.ReleaseStatus{store.ReleaseQueued, store.ReleaseSuperseded, store.ReleaseRunning}
		if got := statuses(list); !slices.Equal(got, wantStatuses) {
			t.Errorf("statuses after the third enqueue (newest first) = %v, want %v", got, wantStatuses)
		}
		r2Read, err := s.Release(ctx, org, r2.ID)
		if err != nil {
			t.Fatalf("Release(r2) error = %v, want nil", err)
		}
		if r2Read.Status != store.ReleaseSuperseded || r2Read.FinishedAt != at(3).Format(time.RFC3339) {
			t.Errorf("Release(r2) status = %q, finished_at = %q; want %q, %q",
				r2Read.Status, r2Read.FinishedAt, store.ReleaseSuperseded, at(3).Format(time.RFC3339))
		}

		if err := s.StartRelease(ctx, org, r3.ID); !errors.Is(err, store.ErrReleaseState) {
			t.Errorf("StartRelease(r3) while r1 runs error = %v, want ErrReleaseState", err)
		}
		if r3Read, err := s.Release(ctx, org, r3.ID); err != nil || r3Read.Status != store.ReleaseQueued {
			t.Errorf("Release(r3) after the refused start = status %q, %v; want %q, nil", r3Read.Status, err, store.ReleaseQueued)
		}
		if err := s.StartRelease(ctx, org, r2.ID); !errors.Is(err, store.ErrReleaseState) {
			t.Errorf("StartRelease(r2, superseded) error = %v, want ErrReleaseState", err)
		}
	})

	t.Run("steps and image", func(t *testing.T) {
		tgt := seedTarget(t, s)
		svc := seedService(t, s, tgt, tgt.server, "web")
		org := tgt.orgID
		q := mustEnqueue(t, s, org, sampleRelease(svc.ID), 0)

		if err := s.SetReleaseStep(ctx, org, q.ID, store.StepDeploy, "c"+randHex(t, 16)); !errors.Is(err, store.ErrReleaseState) {
			t.Errorf("SetReleaseStep on a queued release error = %v, want ErrReleaseState", err)
		}
		if err := s.SetReleaseImage(ctx, org, q.ID, "sha256:queued", "repo@sha256:queued"); !errors.Is(err, store.ErrReleaseState) {
			t.Errorf("SetReleaseImage on a queued release error = %v, want ErrReleaseState", err)
		}

		if err := s.StartRelease(ctx, org, q.ID); err != nil {
			t.Fatalf("StartRelease error = %v, want nil", err)
		}
		err := s.SetReleaseStep(ctx, org, q.ID, store.ReleaseStep("bogus"), "c"+randHex(t, 16))
		checkNames(t, "SetReleaseStep(bogus)", err, "build", "deploy", "pre_deploy", "switch")
		if r, err := s.Release(ctx, org, q.ID); err != nil || r.Step != "" || r.RequestID != "" || r.Status != store.ReleaseRunning {
			t.Errorf("Release after bogus step = step %q, request %q, status %q, %v; want unchanged: empty step, empty request, running",
				r.Step, r.RequestID, r.Status, err)
		}

		reqID := "c" + randHex(t, 16)
		if err := s.SetReleaseStep(ctx, org, q.ID, store.StepDeploy, reqID); err != nil {
			t.Fatalf("SetReleaseStep(deploy) error = %v, want nil", err)
		}
		if err := s.SetReleaseImage(ctx, org, q.ID, "sha256:built", "repo@sha256:built"); err != nil {
			t.Fatalf("SetReleaseImage error = %v, want nil", err)
		}
		read, err := s.Release(ctx, org, q.ID)
		if err != nil {
			t.Fatalf("Release error = %v, want nil", err)
		}
		if read.Step != store.StepDeploy || read.RequestID != reqID || read.ImageID != "sha256:built" || read.RepoDigest != "repo@sha256:built" {
			t.Errorf("Release after step and image = step %q, request %q, image %q, digest %q; want %q, %q, %q, %q",
				read.Step, read.RequestID, read.ImageID, read.RepoDigest, store.StepDeploy, reqID, "sha256:built", "repo@sha256:built")
		}

		if err := s.FinishRelease(ctx, org, q.ID, store.ReleaseSucceeded, "ok", at(5)); err != nil {
			t.Fatalf("FinishRelease error = %v, want nil", err)
		}
		if err := s.SetReleaseStep(ctx, org, q.ID, store.StepSwitch, "c"+randHex(t, 16)); !errors.Is(err, store.ErrReleaseState) {
			t.Errorf("SetReleaseStep on a finished release error = %v, want ErrReleaseState", err)
		}
		if err := s.SetReleaseImage(ctx, org, q.ID, "sha256:late", "repo@sha256:late"); !errors.Is(err, store.ErrReleaseState) {
			t.Errorf("SetReleaseImage on a finished release error = %v, want ErrReleaseState", err)
		}
	})

	t.Run("finish", func(t *testing.T) {
		tgt := seedTarget(t, s)
		org := tgt.orgID

		for i, st := range []store.ReleaseStatus{store.ReleaseSucceeded, store.ReleaseFailed, store.ReleaseRejected} {
			svc := seedService(t, s, tgt, tgt.server, "fin-"+string(st))
			r := mustEnqueue(t, s, org, sampleRelease(svc.ID), i)
			if err := s.StartRelease(ctx, org, r.ID); err != nil {
				t.Fatalf("StartRelease(%s) error = %v, want nil", st, err)
			}
			detail := "detail " + string(st)
			if err := s.FinishRelease(ctx, org, r.ID, st, detail, at(10+i)); err != nil {
				t.Fatalf("FinishRelease(%s) error = %v, want nil", st, err)
			}
			got, err := s.Release(ctx, org, r.ID)
			if err != nil {
				t.Fatalf("Release(%s) error = %v, want nil", st, err)
			}
			if got.Status != st || got.Detail != detail || got.FinishedAt != at(10+i).Format(time.RFC3339) {
				t.Errorf("Release after FinishRelease(%s) = status %q, detail %q, finished_at %q; want %q, %q, %q",
					st, got.Status, got.Detail, got.FinishedAt, st, detail, at(10+i).Format(time.RFC3339))
			}
		}

		svc := seedService(t, s, tgt, tgt.server, "next")
		first := mustEnqueue(t, s, org, sampleRelease(svc.ID), 20)
		if err := s.StartRelease(ctx, org, first.ID); err != nil {
			t.Fatalf("StartRelease(first) error = %v, want nil", err)
		}
		second := mustEnqueue(t, s, org, sampleRelease(svc.ID), 21)
		if err := s.FinishRelease(ctx, org, first.ID, store.ReleaseSucceeded, "done", at(22)); err != nil {
			t.Fatalf("FinishRelease(first) error = %v, want nil", err)
		}
		if err := s.StartRelease(ctx, org, second.ID); err != nil {
			t.Fatalf("StartRelease(second) after the finish error = %v, want nil", err)
		}
		if r, err := s.Release(ctx, org, second.ID); err != nil || r.Status != store.ReleaseRunning {
			t.Errorf("Release(second) after it started = status %q, %v; want %q, nil", r.Status, err, store.ReleaseRunning)
		}

		if err := s.FinishRelease(ctx, org, first.ID, store.ReleaseFailed, "again", at(23)); !errors.Is(err, store.ErrReleaseState) {
			t.Errorf("FinishRelease on a finished release error = %v, want ErrReleaseState", err)
		}
		if r, err := s.Release(ctx, org, first.ID); err != nil || r.Status != store.ReleaseSucceeded || r.Detail != "done" || r.FinishedAt != at(22).Format(time.RFC3339) {
			t.Errorf("Release(first) after the refused finish = status %q, detail %q, finished_at %q, %v; want %q, %q, %q",
				r.Status, r.Detail, r.FinishedAt, err, store.ReleaseSucceeded, "done", at(22).Format(time.RFC3339))
		}

		held := mustEnqueue(t, s, org, sampleRelease(seedService(t, s, tgt, tgt.server, "held").ID), 30)
		for _, st := range []store.ReleaseStatus{store.ReleaseQueued, store.ReleaseRunning, store.ReleaseSuperseded} {
			err := s.FinishRelease(ctx, org, held.ID, st, "nope", at(31))
			checkNames(t, "FinishRelease with status "+string(st), err, "succeeded", "failed", "rejected")
		}
		if r, err := s.Release(ctx, org, held.ID); err != nil || r.Status != store.ReleaseQueued || r.Detail != "" || r.FinishedAt != "" {
			t.Errorf("Release(held) after refused finishes = status %q, detail %q, finished_at %q, %v; want %q, empty, empty, nil",
				r.Status, r.Detail, r.FinishedAt, err, store.ReleaseQueued)
		}

		if err := s.FinishRelease(ctx, org, held.ID, store.ReleaseRejected, "refused by policy", at(32)); err != nil {
			t.Errorf("FinishRelease(queued release, rejected) error = %v, want nil", err)
		}
		if r, err := s.Release(ctx, org, held.ID); err != nil || r.Status != store.ReleaseRejected || r.Detail != "refused by policy" {
			t.Errorf("Release(held) after rejecting it = status %q, detail %q, %v; want %q, %q, nil",
				r.Status, r.Detail, err, store.ReleaseRejected, "refused by policy")
		}
	})

	t.Run("history", func(t *testing.T) {
		tgt := seedTarget(t, s)
		svc := seedService(t, s, tgt, tgt.server, "web")
		ids := make([]string, 51)
		for i := range ids {
			ids[i] = mustEnqueue(t, s, tgt.orgID, sampleRelease(svc.ID), i).ID
		}
		list, err := s.Releases(ctx, tgt.orgID, svc.ID)
		if err != nil {
			t.Fatalf("Releases error = %v, want nil", err)
		}
		var want []string
		for i := 50; i >= 1; i-- {
			want = append(want, ids[i])
		}
		if got := releaseIDs(list); !slices.Equal(got, want) {
			t.Errorf("Releases ids = %d entries %v, want the 50 newest, enqueues 50 down to 1: %v", len(got), got, want)
		}
	})

	t.Run("in flight", func(t *testing.T) {
		tgt := seedTarget(t, s)
		org := tgt.orgID
		srv2, err := s.AddServer(ctx, org, "pub-"+randHex(t, 16))
		if err != nil {
			t.Fatalf("AddServer error = %v, want nil", err)
		}
		a := seedService(t, s, tgt, tgt.server, "a")
		b := seedService(t, s, tgt, tgt.server, "b")
		c := seedService(t, s, tgt, srv2.ID, "c")

		aRunning := mustEnqueue(t, s, org, sampleRelease(a.ID), 0)
		if err := s.StartRelease(ctx, org, aRunning.ID); err != nil {
			t.Fatalf("StartRelease(a) error = %v, want nil", err)
		}
		aQueued := mustEnqueue(t, s, org, sampleRelease(a.ID), 1)
		bDone := mustEnqueue(t, s, org, sampleRelease(b.ID), 2)
		if err := s.StartRelease(ctx, org, bDone.ID); err != nil {
			t.Fatalf("StartRelease(b) error = %v, want nil", err)
		}
		if err := s.FinishRelease(ctx, org, bDone.ID, store.ReleaseSucceeded, "ok", at(3)); err != nil {
			t.Fatalf("FinishRelease(b) error = %v, want nil", err)
		}
		cQueued := mustEnqueue(t, s, org, sampleRelease(c.ID), 4)

		got, err := s.InFlightReleases(ctx, org, tgt.server)
		want := []string{aRunning.ID, aQueued.ID}
		if err != nil || !slices.Equal(releaseIDs(got), want) {
			t.Errorf("InFlightReleases(server 1) = %v, %v, want %v, nil", releaseIDs(got), err, want)
		}
		if wantStatuses := []store.ReleaseStatus{store.ReleaseRunning, store.ReleaseQueued}; !slices.Equal(statuses(got), wantStatuses) {
			t.Errorf("InFlightReleases(server 1) statuses = %v, want %v, oldest first", statuses(got), wantStatuses)
		}

		gotC, err := s.InFlightReleases(ctx, org, srv2.ID)
		if wantC := []string{cQueued.ID}; err != nil || !slices.Equal(releaseIDs(gotC), wantC) {
			t.Errorf("InFlightReleases(server 2) = %v, %v, want %v, nil", releaseIDs(gotC), err, wantC)
		}

		none, err := s.InFlightReleases(ctx, org, randHex(t, 16))
		if err != nil || len(none) != 0 {
			t.Errorf("InFlightReleases(unknown server) = %+v, %v, want no rows, nil", none, err)
		}
	})

	t.Run("concurrent enqueue", func(t *testing.T) {
		tgt := seedTarget(t, s)
		svc := seedService(t, s, tgt, tgt.server, "busy")
		var wg sync.WaitGroup
		var errs [8]error
		for i := range errs {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, errs[i] = s.EnqueueRelease(ctx, tgt.orgID, sampleRelease(svc.ID), at(i))
			}(i)
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Errorf("concurrent EnqueueRelease %d error = %v, want nil", i, err)
			}
		}

		list, err := s.Releases(ctx, tgt.orgID, svc.ID)
		if err != nil {
			t.Fatalf("Releases error = %v, want nil", err)
		}
		queued, superseded := 0, 0
		for _, r := range list {
			switch r.Status {
			case store.ReleaseQueued:
				queued++
			case store.ReleaseSuperseded:
				superseded++
			case store.ReleaseRunning:
			case store.ReleaseSucceeded:
			case store.ReleaseFailed:
			case store.ReleaseRejected:
			}
		}
		if len(list) != 8 || queued != 1 || superseded != 7 {
			t.Errorf("after 8 concurrent enqueues: %d rows, %d queued, %d superseded; want 8 rows, 1 queued, 7 superseded (statuses %v)",
				len(list), queued, superseded, statuses(list))
		}
	})

	t.Run("second org", func(t *testing.T) {
		a, b := seedTarget(t, s), seedTarget(t, s)
		svc := seedService(t, s, a, a.server, "web")
		running := mustEnqueue(t, s, a.orgID, sampleRelease(svc.ID), 0)
		if err := s.StartRelease(ctx, a.orgID, running.ID); err != nil {
			t.Fatalf("StartRelease error = %v, want nil", err)
		}
		queued := mustEnqueue(t, s, a.orgID, sampleRelease(svc.ID), 1)
		before, err := s.Releases(ctx, a.orgID, svc.ID)
		if err != nil {
			t.Fatalf("Releases(a) error = %v, want nil", err)
		}

		notFound := func(what string, err error) {
			t.Helper()
			if !errors.Is(err, store.ErrNotFound) {
				t.Errorf("%s by org %s on org %s's release error = %v, want ErrNotFound", what, b.orgID, a.orgID, err)
			}
		}
		_, err = s.Release(ctx, b.orgID, running.ID)
		notFound("Release", err)
		_, err = s.Release(ctx, b.orgID, queued.ID)
		notFound("Release (queued)", err)
		notFound("StartRelease", s.StartRelease(ctx, b.orgID, queued.ID))
		notFound("SetReleaseStep", s.SetReleaseStep(ctx, b.orgID, running.ID, store.StepBuild, "c"+randHex(t, 16)))
		notFound("SetReleaseImage", s.SetReleaseImage(ctx, b.orgID, running.ID, "sha256:x", "repo@sha256:x"))
		notFound("FinishRelease", s.FinishRelease(ctx, b.orgID, running.ID, store.ReleaseFailed, "x", at(5)))
		notFound("FinishRelease (queued)", s.FinishRelease(ctx, b.orgID, queued.ID, store.ReleaseRejected, "x", at(5)))

		after, err := s.Releases(ctx, a.orgID, svc.ID)
		if err != nil {
			t.Fatalf("Releases(a) after org b's calls error = %v, want nil", err)
		}
		if !reflect.DeepEqual(before, after) {
			t.Errorf("org A's releases changed by org B's calls:\n before = %+v\n after  = %+v", before, after)
		}

		leaked, err := s.Releases(ctx, b.orgID, svc.ID)
		if err != nil || len(leaked) != 0 {
			t.Errorf("Releases(b, a's service) = %+v, %v, want no rows, nil", leaked, err)
		}
		inFlight, err := s.InFlightReleases(ctx, b.orgID, a.server)
		if err != nil || len(inFlight) != 0 {
			t.Errorf("InFlightReleases(b, a's server) = %+v, %v, want no rows, nil", inFlight, err)
		}
	})
}

func TestPostgresRLSOnReleases(t *testing.T) {
	env := newPGEnv(t, true)
	ctx := context.Background()
	s := env.openApp(t)

	var targets [2]orgTarget
	var releases [2]store.Release
	for i := range targets {
		targets[i] = seedTarget(t, s)
		svc := seedService(t, s, targets[i], targets[i].server, "web")
		releases[i] = mustEnqueue(t, s, targets[i].orgID, sampleRelease(svc.ID), i)
	}
	a, b := targets[0], targets[1]
	ra, rb := releases[0], releases[1]

	app := env.rawDB(t, env.appDSN)
	empty, nonexistent := "", randHex(t, 16)
	for _, tc := range []struct {
		name    string
		setting *string
	}{
		{"unset", nil},
		{"empty", &empty},
		{"nonexistent org", &nonexistent},
	} {
		if got := orgIDsUnder(t, app, tc.setting, "releases"); len(got) != 0 {
			t.Errorf("app.org_id %s: SELECT org_id FROM releases (no WHERE) = %v, want no rows", tc.name, got)
		}
	}

	got := orgIDsUnder(t, app, &a.orgID, "releases")
	if len(got) == 0 {
		t.Errorf("app.org_id=A: SELECT FROM releases returned no rows, want A's rows")
	}
	for _, id := range got {
		if id != a.orgID {
			t.Errorf("app.org_id=A: SELECT org_id FROM releases (no WHERE) returned org %q, want only %q (B is %q)", id, a.orgID, b.orgID)
		}
	}

	const insert = "INSERT INTO releases (id, org_id, service_id, port, domains, pre_deploy_reversible, status, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)"
	now := at(0).Format(time.RFC3339)
	for _, tc := range []struct {
		name    string
		org     string
		service string
		wantErr bool
	}{
		{"another org's row", b.orgID, rb.ServiceID, true},
		{"own row", a.orgID, ra.ServiceID, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := app.BeginTx(ctx, nil)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer func() { _ = tx.Rollback() }()
			if _, err := tx.ExecContext(ctx, "SELECT set_config('app.org_id', $1, true)", a.orgID); err != nil {
				t.Fatalf("set_config: %v", err)
			}
			_, err = tx.ExecContext(ctx, insert, randHex(t, 16), tc.org, tc.service, 8080, "[]", false, "failed", now)

			if !tc.wantErr {
				if err != nil {
					t.Errorf("control INSERT INTO releases for org %s under app.org_id=%s error = %v, want nil", tc.org, a.orgID, err)
				}
				return
			}
			var pgErr pgError
			if !errors.As(err, &pgErr) || pgErr.SQLState() != "42501" {
				t.Errorf("INSERT INTO releases for org %s under app.org_id=%s error = %v, want pgconn.PgError code %q", tc.org, a.orgID, err, "42501")
			}
		})
	}

	super := env.rawDB(t, env.superDSN)
	var rls bool
	if err := super.QueryRowContext(ctx, "SELECT rowsecurity FROM pg_tables WHERE schemaname = 'public' AND tablename = 'releases'").Scan(&rls); err != nil {
		t.Fatalf("query pg_tables: %v", err)
	}
	if !rls {
		t.Errorf("table %q row level security = %v, want true", "releases", rls)
	}
}
