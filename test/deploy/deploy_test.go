package deploy_test

import (
	"context"
	"slices"
	"testing"
	"time"

	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

// mark is one operation's ordering window: from just before its switch or
// rollback was called until the next operation's call, a new release id must
// arrive and the releases it replaced must not.
type mark struct {
	release  string
	banned   []string
	called   time.Time
	returned time.Time
	within   time.Duration
}

// TestTenDeploysUnderLoadDropNothing proves ten health-gated deploys under
// steady load drop no request, and that each switch reaches the proxy within
// 2 s and never lets an older release answer again
// (docs/specs/deploy-suite.md, "What the suite asserts").
func TestTenDeploysUnderLoadDropNothing(t *testing.T) {
	t.Parallel()
	s := newSuite(t)
	cfg := s.firstRelease()

	l := startLoad(cfg)
	marks := s.tenDeploys()
	time.Sleep(2 * time.Second)
	res := l.stop()
	loadEnd := time.Now()

	if res.failed != 0 {
		t.Fatalf("load under ten deploys: %s", res)
	}
	checkOrder(t, res.responses, marks, loadEnd)
}

// TestRollbackUnderLoadIsUnderFiveSeconds proves a rollback returns SUCCEEDED
// under 5 seconds of the call, measured with the monotonic clock, and that the
// release before the last one answers within 1 s afterwards
// (docs/specs/deploy-suite.md, "What the suite asserts").
func TestRollbackUnderLoadIsUnderFiveSeconds(t *testing.T) {
	t.Parallel()
	s := newSuite(t)
	cfg := s.firstRelease()

	l := startLoad(cfg)
	marks := s.tenDeploys()
	// A beat of steady load, so the r10 mark gets its answer before the rollback.
	time.Sleep(200 * time.Millisecond)

	called := time.Now()
	res := s.x.Rollback(context.Background(), "rollback-"+s.app, &agentv1.Rollback{AppId: s.app})
	elapsed := time.Since(called)
	returned := time.Now()

	if got, want := res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
		t.Fatalf("Rollback outcome = %v with detail %q, want %v", got, res.GetDetail(), want)
	}
	if elapsed >= 5*time.Second {
		t.Errorf("Rollback returned after %s, want under 5 s", elapsed)
	}
	marks = append(marks, mark{
		release: "r09", banned: []string{"r10"},
		called: called, returned: returned, within: time.Second,
	})

	time.Sleep(2 * time.Second)
	loadRes := l.stop()
	loadEnd := time.Now()

	if loadRes.failed != 0 {
		t.Fatalf("load under ten deploys and one rollback: %s", loadRes)
	}
	checkOrder(t, loadRes.responses, marks, loadEnd)
}

// checkOrder asserts, for every mark, that the switched-to release answered
// within its window and that no request sent after the switch returned was
// answered with a release it replaced. An answer to a request already in flight
// when the switch returned may carry the older release: a reload lets requests
// on the old route finish (docs/specs/release.md, "Draining"), so it is excused
// by the write time of its request. Any 2xx body that is not a release of the
// run also fails.
func checkOrder(t *testing.T, responses []response, marks []mark, loadEnd time.Time) {
	t.Helper()
	ordered := slices.Clone(responses)
	slices.SortFunc(ordered, func(a, b response) int { return a.arrived.Compare(b.arrived) })

	known := map[string]bool{"r00": true}
	for _, m := range marks {
		known[m.release] = true
	}
	for _, r := range ordered {
		if !known[r.release] {
			t.Errorf("a response carried release id %q at %s, want one of the run's releases",
				r.release, r.arrived.Format(time.RFC3339Nano))
		}
	}

	for i, m := range marks {
		first := -1
		for j, r := range ordered {
			if !r.arrived.Before(m.returned) && r.release == m.release {
				first = j
				break
			}
		}
		if first < 0 {
			t.Errorf("after the operation at %s returned at %s, no response carried %s, want one within %s",
				m.called.Format(time.RFC3339Nano), m.returned.Format(time.RFC3339Nano), m.release, m.within)
			continue
		}
		if lag := ordered[first].arrived.Sub(m.returned); lag > m.within {
			t.Errorf("the first %s response arrived %s after the operation returned at %s, want within %s",
				m.release, lag, m.returned.Format(time.RFC3339Nano), m.within)
		}

		windowEnd := loadEnd
		if i+1 < len(marks) {
			windowEnd = marks[i+1].called
		}
		for _, r := range ordered[first+1:] {
			if !r.arrived.Before(windowEnd) || !slices.Contains(m.banned, r.release) {
				continue
			}
			if !r.sent.IsZero() && r.sent.Before(m.returned) {
				continue
			}
			t.Errorf("at %s, %s after the switch to %s, a response carried the older release %s, want none before %s",
				r.arrived.Format(time.RFC3339Nano), r.arrived.Sub(m.returned), m.release, r.release,
				windowEnd.Format(time.RFC3339Nano))
		}
	}
}
