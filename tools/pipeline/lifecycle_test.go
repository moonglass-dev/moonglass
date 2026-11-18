package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const testPlan = `# Plan

- **T-1** Say hello — write pkg/hello.txt · depends_on: — · risk: low · paths: pkg/
- **T-2** Follow up — after hello · depends_on: T-1 · risk: standard
`

// fakeForge stands in for GitHub. Merging fast-forwards main on the origin.
type fakeForge struct {
	dir                  string
	pr                   *pullRequest
	ci                   string
	firstCI, firstDetail string
	checksN              int
}

func (f *fakeForge) findPR(string) (*pullRequest, error) { return f.pr, nil }

func (f *fakeForge) createPR(_, _, _ string) error {
	f.pr = &pullRequest{Number: 7, URL: "https://example.test/pull/7", State: "OPEN"}
	return nil
}

func (f *fakeForge) checks(int) (string, string, error) {
	f.checksN++
	if f.checksN == 1 && f.firstCI != "" {
		return f.firstCI, f.firstDetail, nil
	}
	return f.ci, "", nil
}

func (f *fakeForge) merge(_ int, sha, _ string) error {
	if _, err := git(f.dir, "push", "--quiet", "origin", sha+":refs/heads/main"); err != nil {
		return err
	}
	f.pr.State = "MERGED"
	return nil
}

type testRepo struct {
	t     *testing.T
	dir   string
	forge *fakeForge
	e     *env
}

// newTestRepo makes a bare origin and a clone holding the test plan.
func newTestRepo(t *testing.T) *testRepo {
	t.Helper()
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "gitconfig")
	if err := os.WriteFile(cfg, []byte("[user]\n\tname = Pipeline Test\n\temail = test@example.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("MOONGLASS_GITHUB_REPO", "example/moonglass")
	origin := filepath.Join(tmp, "origin.git")
	dir := filepath.Join(tmp, "work")
	mustGit(t, tmp, "init", "--quiet", "--bare", "-b", "main", origin)
	mustGit(t, tmp, "clone", "--quiet", origin, dir)
	mustGit(t, dir, "switch", "--quiet", "-c", "main")
	writeT(t, dir, planPath, testPlan)
	writeT(t, dir, ".gitignore", ".pipeline/\n")
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "--quiet", "-m", "plan")
	mustGit(t, dir, "push", "--quiet", "-u", "origin", "main")
	f := &fakeForge{dir: dir, ci: "pass"}
	e := &env{
		repo:  dir,
		forge: f,
		runCI: func(string) (string, error) { return "ok", nil },
		sleep: func(time.Duration) {},
	}
	return &testRepo{t: t, dir: dir, forge: f, e: e}
}

// run calls one pipeline command and returns its last JSON document.
func (r *testRepo) run(args ...string) (map[string]any, error) {
	r.t.Helper()
	var out bytes.Buffer
	r.e.out = &out
	err := dispatch(r.e, args)
	var last map[string]any
	dec := json.NewDecoder(&out)
	for {
		var doc map[string]any
		if derr := dec.Decode(&doc); errors.Is(derr, io.EOF) {
			break
		} else if derr != nil {
			r.t.Fatalf("%v printed invalid JSON: %v\n%s", args, derr, out.String())
		}
		last = doc
	}
	return last, err
}

func (r *testRepo) mustRun(args ...string) map[string]any {
	r.t.Helper()
	doc, err := r.run(args...)
	if err != nil {
		r.t.Fatalf("%v: %v", args, err)
	}
	return doc
}

// expectStep claims the next step and checks it is the given child or command.
func (r *testRepo) expectStep(want string) map[string]any {
	r.t.Helper()
	doc := r.mustRun("ticket-status", "next", "T-1")
	got, _ := doc["step"].(string)
	if got == "run" {
		got = doc["run"].(string)
	}
	if got == "done" {
		got = "done " + doc["reason"].(string)
	}
	if got != want {
		r.t.Fatalf("next step = %q, want %q (full: %v)", got, want, doc)
	}
	return doc
}

func (r *testRepo) status() report {
	r.t.Helper()
	rep, err := buildReport(r.e)
	if err != nil {
		r.t.Fatalf("buildReport: %v", err)
	}
	return rep
}

func TestTicketLifecycleToMerge(t *testing.T) {
	r := newTestRepo(t)

	if rep := r.status(); !slices.Equal(rep.Ready, []string{"T-1"}) || rep.Merged != 0 {
		t.Fatalf("before start: ready %v merged %d, want [T-1] 0", rep.Ready, rep.Merged)
	}
	start := r.mustRun("ticket-status", "start", "T-1")
	if start["branch"] != "p/T-1" || start["worker_run"] != float64(1) {
		t.Fatalf("start = %v", start)
	}
	if rep := r.status(); !slices.Equal(rep.InProgress, []string{"T-1"}) {
		t.Fatalf("after start: in progress %v, want [T-1]", rep.InProgress)
	}

	claim := r.expectStep("ticket")
	if claim["client_request_id"] != "T-1:ticket:1" {
		t.Errorf("client_request_id = %v, want T-1:ticket:1", claim["client_request_id"])
	}
	writeT(t, r.dir, ticketPath("T-1"), completeTicket("T-1", "low", "[pkg/]"))
	r.mustRun("verify", "record", "T-1")

	// A plan child that writes outside its scope is rejected and retried with the reason.
	r.expectStep("plan")
	writeT(t, r.dir, ticketDir("T-1")+planFile, readyPlan)
	writeT(t, r.dir, "pkg/stray.txt", "not mine\n")
	if _, err := r.run("verify", "record", "T-1"); err == nil || !strings.Contains(err.Error(), "outside what the plan step may write") {
		t.Fatalf("record of an out-of-scope plan: err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.dir, "pkg/stray.txt")); !os.IsNotExist(err) {
		t.Fatalf("rejected change was not discarded: %v", err)
	}
	retry := r.expectStep("plan")
	if inputs := retry["inputs"]; inputs == nil || !strings.Contains(inputs.([]any)[0].(string), "rejected-plan-1.txt") {
		t.Fatalf("retry inputs = %v, want the rejection note", inputs)
	}
	writeT(t, r.dir, ticketDir("T-1")+planFile, readyPlan)
	r.mustRun("verify", "record", "T-1")

	// Low risk with no named tests: straight to the lock, then the build.
	r.expectStep("scripts/verify lock T-1")
	r.mustRun("verify", "lock", "T-1")
	r.expectStep("build")
	writeT(t, r.dir, "pkg/hello.txt", "hello\n")
	if rec := r.mustRun("verify", "record", "T-1"); rec["check_ok"] != true {
		t.Fatalf("record build = %v, want check_ok true", rec)
	}

	r.expectStep("scripts/ship pr T-1")
	if pr := r.mustRun("ship", "pr", "T-1"); pr["pr"] != float64(7) {
		t.Fatalf("ship pr = %v", pr)
	}
	r.expectStep("scripts/verify skip-review T-1")
	r.mustRun("verify", "skip-review", "T-1")
	r.expectStep("done mergeable: the dispatcher merges it")

	if rep := r.status(); !slices.Equal(rep.Mergeable, []string{"T-1"}) {
		t.Fatalf("mergeable %v, want [T-1]", rep.Mergeable)
	}
	if m := r.mustRun("ship", "merge", "T-1", "--wait"); m["merge"] != "merged" {
		t.Fatalf("ship merge --wait = %v, want merged", m)
	}
	rep := r.status()
	if rep.Merged != 1 || !slices.Equal(rep.Ready, []string{"T-2"}) || len(rep.Mergeable) != 0 {
		t.Fatalf("after merge: merged %d ready %v mergeable %v; want 1 [T-2] []", rep.Merged, rep.Ready, rep.Mergeable)
	}
	if refExists(r.dir, remoteBranchRef("T-1")) {
		t.Errorf("p/T-1 still exists on origin after the merge")
	}
}

func TestMergeFailureGoesBackToTheWorker(t *testing.T) {
	r := newTestRepo(t)
	driveToMergeable(r)
	r.forge.ci = "fail"
	if _, err := r.run("ship", "merge", "T-1"); err == nil || !strings.Contains(err.Error(), "CI failed") {
		t.Fatalf("merge on red CI: err = %v", err)
	}
	if rep := r.status(); !slices.Equal(rep.InProgress, []string{"T-1"}) {
		t.Fatalf("after red CI: in progress %v, want [T-1] so the worker is restarted", rep.InProgress)
	}
	mustGit(t, r.dir, "pull", "--quiet", "--ff-only", "origin", "p/T-1")
	r.expectStep("repair")
}

func TestMergeWithNoChecksCatchesUpBeforeWaiting(t *testing.T) {
	r := newTestRepo(t)
	driveToMergeable(r)
	other := filepath.Join(t.TempDir(), "other")
	mustGit(t, r.dir, "clone", "--quiet", "--branch", "main", mustOrigin(t, r.dir), other)
	writeT(t, other, "README.md", "another ticket landed\n")
	mustGit(t, other, "add", "-A")
	mustGit(t, other, "commit", "--quiet", "-m", "other ticket")
	mustGit(t, other, "push", "--quiet", "origin", "main")

	r.forge.ci = "pass"
	r.forge.firstCI = "pending"
	r.forge.firstDetail = noChecksYet
	if m := r.mustRun("ship", "merge", "T-1", "--wait"); m["merge"] != "merged" {
		t.Fatalf("ship merge with no checks while behind = %v, want merged", m)
	}
	mustGit(t, r.dir, "fetch", "--quiet", "origin")
	if _, ok, err := (refTree{r.dir, "origin/main"}).read("README.md"); err != nil || !ok {
		t.Errorf("origin/main lacks README.md after the merge (err %v)", err)
	}
}

func TestPendingChecksWaitBeforeCatchUp(t *testing.T) {
	r := newTestRepo(t)
	driveToMergeable(r)
	other := filepath.Join(t.TempDir(), "other")
	mustGit(t, r.dir, "clone", "--quiet", "--branch", "main", mustOrigin(t, r.dir), other)
	writeT(t, other, "README.md", "another ticket landed\n")
	mustGit(t, other, "add", "-A")
	mustGit(t, other, "commit", "--quiet", "-m", "other ticket")
	mustGit(t, other, "push", "--quiet", "origin", "main")

	r.forge.ci = "pending"
	r.forge.firstCI = "pending"
	r.forge.firstDetail = "ci"
	m := r.mustRun("ship", "merge", "T-1")
	if m["merge"] != "waiting-ci" {
		t.Fatalf("ship merge while CI is running = %v, want waiting-ci", m)
	}
	mustGit(t, r.dir, "fetch", "--quiet", "origin")
	if _, ok, err := (refTree{r.dir, "origin/p/T-1"}).read("README.md"); err != nil || ok {
		t.Errorf("ticket branch contains README.md before CI finished (err %v, ok %v)", err, ok)
	}
}

func TestMergeCatchesUpWhenMainMoved(t *testing.T) {
	r := newTestRepo(t)
	driveToMergeable(r)
	other := filepath.Join(t.TempDir(), "other")
	mustGit(t, r.dir, "clone", "--quiet", "--branch", "main", mustOrigin(t, r.dir), other)
	writeT(t, other, "README.md", "another ticket landed\n")
	mustGit(t, other, "add", "-A")
	mustGit(t, other, "commit", "--quiet", "-m", "other ticket")
	mustGit(t, other, "push", "--quiet", "origin", "main")

	if m := r.mustRun("ship", "merge", "T-1", "--wait"); m["merge"] != "merged" {
		t.Fatalf("ship merge --wait after main moved = %v, want merged", m)
	}
	mustGit(t, r.dir, "fetch", "--quiet", "origin")
	for _, f := range []string{"README.md", "pkg/hello.txt"} {
		if _, ok, err := (refTree{r.dir, "origin/main"}).read(f); err != nil || !ok {
			t.Errorf("origin/main lacks %s after the merge (err %v)", f, err)
		}
	}
}

func TestHighRiskTicketsRunOneAtATime(t *testing.T) {
	r := newTestRepo(t)
	writeT(t, r.dir, planPath, `# Plan

- **T-1** First core change — x · depends_on: — · risk: high
- **T-2** Second core change — y · depends_on: — · risk: high
- **T-3** A script — z · depends_on: — · risk: low
`)
	mustGit(t, r.dir, "commit", "--quiet", "-am", "plan")
	mustGit(t, r.dir, "push", "--quiet", "origin", "main")
	if rep := r.status(); !slices.Equal(rep.Ready, []string{"T-1", "T-3"}) {
		t.Fatalf("ready %v, want [T-1 T-3]: one high-risk ticket at a time", rep.Ready)
	}
	r.mustRun("ticket-status", "start", "T-1")
	rep := r.status()
	if !slices.Equal(rep.Ready, []string{"T-3"}) || rep.Tickets[1].Reason == "" {
		t.Fatalf("with T-1 in flight: ready %v, T-2 reason %q", rep.Ready, rep.Tickets[1].Reason)
	}
}

func TestBranchWaitsForUnmergedDependencies(t *testing.T) {
	r := newTestRepo(t)
	writeT(t, r.dir, planPath, `# Plan

- **T-1** First core change — x · depends_on: — · risk: high
- **T-2** Second core change — y · depends_on: T-1 · risk: high
`)
	mustGit(t, r.dir, "commit", "--quiet", "-am", "plan")
	mustGit(t, r.dir, "push", "--quiet", "origin", "main")
	// T-2's branch exists, as after a resume, but T-1 has not merged.
	r.mustRun("ticket-status", "start", "T-2")
	rep := r.status()
	if len(rep.InProgress) != 0 || !slices.Equal(rep.Ready, []string{"T-1"}) {
		t.Fatalf("in progress %v ready %v, want [] [T-1]: T-2 waits and does not hold the high-risk slot", rep.InProgress, rep.Ready)
	}
	if got, want := rep.Tickets[1].Reason, "waits: depends on T-1"; got != want {
		t.Errorf("T-2 reason = %q, want %q", got, want)
	}
}

func TestBlockedChildStopsOnlyItsTicketAndResumes(t *testing.T) {
	r := newTestRepo(t)
	r.mustRun("ticket-status", "start", "T-1")
	r.expectStep("ticket")
	writeT(t, r.dir, ticketDir("T-1")+blockedFile, "The spec for hello is missing\n\nNo docs/specs/hello.md.\n")
	if rec := r.mustRun("verify", "record", "T-1"); rec["blocked"] != "The spec for hello is missing" {
		t.Fatalf("record of BLOCKED.md = %v", rec)
	}
	rep := r.status()
	if !slices.Equal(rep.Attention, []string{"T-1"}) || rep.Tickets[0].Reason != "The spec for hello is missing" {
		t.Fatalf("attention %v reason %q", rep.Attention, rep.Tickets[0].Reason)
	}
	r.expectStep("done blocked: The spec for hello is missing")

	if res := r.mustRun("ticket-status", "set", "T-1", "resume"); res["status"] != "todo" {
		t.Fatalf("resume = %v, want status todo", res)
	}
	if _, err := os.Stat(filepath.Join(r.dir, ticketDir("T-1")+blockedFile)); !os.IsNotExist(err) {
		t.Errorf("BLOCKED.md survived resume: %v", err)
	}
	r.expectStep("ticket")
}

func TestResumeTakesMainAndKeepsCounting(t *testing.T) {
	r := newTestRepo(t)
	r.mustRun("ticket-status", "start", "T-1")
	r.expectStep("ticket")
	writeT(t, r.dir, ticketPath("T-1"), completeTicket("T-1", "low", "[pkg/]"))
	r.mustRun("verify", "record", "T-1")
	r.expectStep("plan")
	writeT(t, r.dir, ticketDir("T-1")+planFile, readyPlan)
	r.mustRun("verify", "record", "T-1")
	r.expectStep("scripts/verify lock T-1")
	r.mustRun("verify", "lock", "T-1")

	// A red build round, then the worker stops the ticket.
	r.e.runCI = func(string) (string, error) { return "FAIL", errors.New("make ci failed") }
	r.expectStep("build")
	writeT(t, r.dir, "pkg/hello.txt", "hello\n")
	if rec := r.mustRun("verify", "record", "T-1"); rec["check_ok"] != false {
		t.Fatalf("record build = %v, want check_ok false", rec)
	}
	r.mustRun("ticket-status", "set", "T-1", "blocked", "--reason", "make ci is red")

	// The owner fixes the cause on main.
	other := filepath.Join(t.TempDir(), "other")
	mustGit(t, r.dir, "clone", "--quiet", "--branch", "main", mustOrigin(t, r.dir), other)
	writeT(t, other, "docs/fix.md", "fixed\n")
	mustGit(t, other, "add", "-A")
	mustGit(t, other, "commit", "--quiet", "-m", "fix on main")
	mustGit(t, other, "push", "--quiet", "origin", "main")

	if res := r.mustRun("ticket-status", "set", "T-1", "resume"); res["status"] != "tests-locked" {
		t.Fatalf("resume = %v, want status tests-locked", res)
	}
	if _, err := git(r.dir, "merge-base", "--is-ancestor", "origin/main", "HEAD"); err != nil {
		t.Fatalf("p/T-1 does not contain origin/main after resume: %v", err)
	}
	// Main moved the code under the old check, so it is checked again, not built again.
	r.expectStep("scripts/verify check T-1")
	if _, err := r.run("verify", "check", "T-1"); err == nil {
		t.Fatalf("verify check with make ci red: err = nil, want a failure")
	}
	r.expectStep("build")
	writeT(t, r.dir, "pkg/hello.txt", "hello, again\n")
	r.e.runCI = func(string) (string, error) { return "ok", nil }
	if rec := r.mustRun("verify", "record", "T-1"); rec["check_ok"] != true {
		t.Fatalf("record build after resume = %v, want check_ok true", rec)
	}
	rd, err := loadRounds(fsTree{r.dir}, "T-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := rd.done("build"); got != 2 {
		t.Errorf("done:build after a build round on each side of the resume = %d, want 2", got)
	}
	r.expectStep("scripts/ship pr T-1")
}

func TestStepLimitBlocks(t *testing.T) {
	r := newTestRepo(t)
	r.mustRun("ticket-status", "start", "T-1")
	for round := 1; round <= limits["ticket"]; round++ {
		r.expectStep("ticket")
		writeT(t, r.dir, "pkg/stray.txt", "x\n")
		if _, err := r.run("verify", "record", "T-1"); err == nil {
			t.Fatalf("round %d: out-of-scope record accepted", round)
		}
	}
	r.expectStep("done blocked: step ticket already ran 2 times, the limit")
}

func TestStopFileHaltsStateChanges(t *testing.T) {
	r := newTestRepo(t)
	writeT(t, r.dir, ".pipeline/STOP", "")
	for _, args := range [][]string{
		{"ticket-status", "start", "T-1"},
		{"ticket-status", "next", "T-1"},
		{"verify", "check", "T-1"},
		{"ship", "merge", "T-1"},
	} {
		if _, err := r.run(args...); !errors.Is(err, errStop) {
			t.Errorf("%v with .pipeline/STOP: err = %v, want errStop", args, err)
		}
	}
	if rep := r.status(); !rep.Stop {
		t.Errorf("status does not report the stop file")
	}
}

func driveToMergeable(r *testRepo) {
	t := r.t
	t.Helper()
	r.mustRun("ticket-status", "start", "T-1")
	r.expectStep("ticket")
	writeT(t, r.dir, ticketPath("T-1"), completeTicket("T-1", "low", "[pkg/]"))
	r.mustRun("verify", "record", "T-1")
	r.expectStep("plan")
	writeT(t, r.dir, ticketDir("T-1")+planFile, readyPlan)
	r.mustRun("verify", "record", "T-1")
	r.expectStep("scripts/verify lock T-1")
	r.mustRun("verify", "lock", "T-1")
	r.expectStep("build")
	writeT(t, r.dir, "pkg/hello.txt", "hello\n")
	r.mustRun("verify", "record", "T-1")
	r.expectStep("scripts/ship pr T-1")
	r.mustRun("ship", "pr", "T-1")
	r.expectStep("scripts/verify skip-review T-1")
	r.mustRun("verify", "skip-review", "T-1")
}

func mustOrigin(t *testing.T, dir string) string {
	t.Helper()
	url, err := git(dir, "remote", "get-url", "origin")
	if err != nil {
		t.Fatal(err)
	}
	return url
}

func TestRemovedTestLinesOwnerAllowance(t *testing.T) {
	dir := t.TempDir()
	mustGit(t, dir, "init", "--quiet", "-b", "main")
	mustGit(t, dir, "config", "user.email", "test@example.test")
	mustGit(t, dir, "config", "user.name", "Pipeline Test")
	writeT(t, dir, "internal/protocol/deploy_test.go", "package protocol\nfunc TestOld(t *testing.T) {}\n")
	writeT(t, dir, "internal/proxy/other_test.go", "package proxy\nfunc TestOld(t *testing.T) {}\n")
	writeT(t, dir, "internal/policy/config_test.go", "package policy\nfunc TestOld(t *testing.T) {}\n")
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "--quiet", "-m", "base")
	base, err := git(dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	writeT(t, dir, "internal/protocol/deploy_test.go", "package protocol\nfunc TestNew(t *testing.T) {}\n")
	writeT(t, dir, "internal/proxy/other_test.go", "package proxy\nfunc TestNew(t *testing.T) {}\n")
	writeT(t, dir, "internal/policy/config_test.go", "package policy\nfunc TestNew(t *testing.T) {}\n")
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "--quiet", "-m", "edit")
	if msg, err := removedTestLines(dir, base, "HEAD", "W2-8"); err != nil || msg == "" || strings.Contains(msg, "deploy_test.go") {
		t.Fatalf("W2-8 allowance = %q, %v; want deploy_test.go allowed and another test still blocked", msg, err)
	}
	if msg, err := removedTestLines(dir, base, "HEAD", "W2-12"); err != nil || msg == "" {
		t.Fatalf("W2-12 allowance = %q, %v; want a test file it does not own still blocked", msg, err)
	}
	if msg, err := removedTestLines(dir, base, "HEAD", "W3-7"); err != nil || msg == "" || strings.Contains(msg, "deploy_test.go") {
		t.Fatalf("W3-7 allowance = %q, %v; want deploy_test.go allowed and another test still blocked", msg, err)
	}
	if msg, err := removedTestLines(dir, base, "HEAD", "W5-3a"); err != nil || msg == "" || strings.Contains(msg, "config_test.go") {
		t.Fatalf("W5-3a allowance = %q, %v; want config_test.go allowed and another test still blocked", msg, err)
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := git(dir, args...); err != nil {
		t.Fatal(err)
	}
}

func writeT(t *testing.T, dir, rel, content string) {
	t.Helper()
	if err := writeFile(dir, rel, []byte(content)); err != nil {
		t.Fatal(err)
	}
}
