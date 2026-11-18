package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestParsePlanLine(t *testing.T) {
	cases := []struct {
		line    string
		wantErr string
		want    planTicket
	}{
		{
			line: "- **W2-3** Deploy executor — run a release · depends_on: W1-1, W2-2 · risk: standard",
			want: planTicket{ID: "W2-3", Title: "Deploy executor", Intent: "run a release", DependsOn: []string{"W1-1", "W2-2"}, Risk: "standard", Kind: "build"},
		},
		{
			line: "- **R-1** Drift review — whole repo · depends_on: — · risk: low · kind: review · after: 22",
			want: planTicket{ID: "R-1", Title: "Drift review", Intent: "whole repo", DependsOn: []string{}, Risk: "low", Kind: "review", After: 22},
		},
		{line: "- **W2-3** Deploy executor — run · risk: standard", wantErr: "depends_on is required"},
		{line: "- **W2-3** Deploy executor — run · depends_on: — · risk: huge", wantErr: "risk must be one of"},
		{line: "- **W2-3** Deploy executor — run · depends_on: — · risk: low · after: 3", wantErr: "after is only for kind: review"},
		{line: "- **w2-3** Deploy executor — run · depends_on: — · risk: low", wantErr: "bad ticket id"},
		{line: "- **W2-3** Deploy executor — run · depends_on: — · risk: low · colour: red", wantErr: "unknown field"},
		{line: "- **W2-3** Deploy executor, no intent", wantErr: "ticket line must read"},
	}
	for _, c := range cases {
		got, err := parsePlanLine(c.line)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("parsePlanLine(%q) error = %v, want it to contain %q", c.line, err, c.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("parsePlanLine(%q) error = %v, want none", c.line, err)
			continue
		}
		if got.ID != c.want.ID || got.Title != c.want.Title || got.Intent != c.want.Intent || got.Risk != c.want.Risk ||
			got.Kind != c.want.Kind || got.After != c.want.After || !slices.Equal(got.DependsOn, c.want.DependsOn) {
			t.Errorf("parsePlanLine(%q)\n got  %+v\n want %+v", c.line, got, c.want)
		}
	}
}

func TestParsePlanRejectsUnknownDependencyAndDuplicates(t *testing.T) {
	cases := map[string]string{
		"- **A-1** One — x · depends_on: B-1 · risk: low\n":                                                "not in the plan",
		"- **A-1** One — x · depends_on: — · risk: low\n- **A-1** Again — y · depends_on: — · risk: low\n": "listed twice",
		"no tickets here\n": "no ticket lines found",
	}
	for text, want := range cases {
		if _, err := parsePlan(text); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("parsePlan(%q) error = %v, want it to contain %q", text, err, want)
		}
	}
}

func TestParseTicketAndIncomplete(t *testing.T) {
	text := completeTicket("T-1", "low", "[pkg/]")
	tk, err := parseTicket(ticketPath("T-1"), text)
	if err != nil {
		t.Fatalf("parseTicket: %v", err)
	}
	if msg := tk.incomplete(); msg != "" {
		t.Errorf("complete ticket reported incomplete: %s", msg)
	}
	if !slices.Equal(tk.AllowedPaths, []string{"pkg/"}) || tk.MaxDiffLines != 100 {
		t.Errorf("parsed allowed_paths %v, max_diff_lines %d; want [pkg/], 100", tk.AllowedPaths, tk.MaxDiffLines)
	}

	bad := map[string]string{
		strings.Replace(text, "status: todo", "status: done", 1):             "status must be one of",
		strings.Replace(text, "risk: low", "risk: core", 1):                  "risk must be one of",
		strings.Replace(text, "id: T-1", "id: T-2", 1):                       "does not match the file name",
		strings.Replace(text, "reads:", "level: core\nreads:", 1):            "unknown front matter key",
		strings.Replace(text, "tests: []", "tests: [TestHello]", 1):          "must read `./package/path TestName`",
		strings.Replace(text, "max_diff_lines: 100", "max_diff_lines: x", 1): "max_diff_lines must be a whole number",
	}
	for in, want := range bad {
		if _, err := parseTicket(ticketPath("T-1"), in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("parseTicket error = %v, want it to contain %q", err, want)
		}
	}

	incomplete := map[string]string{
		strings.Replace(text, "allowed_paths: [pkg/]", "allowed_paths: []", 1): "allowed_paths is empty",
		strings.Replace(text, "max_diff_lines: 100", "max_diff_lines: 0", 1):   "max_diff_lines is not set",
		strings.Replace(text, "risk: low", "risk: high", 1):                    "must name its acceptance tests",
		strings.Replace(text, "## Security notes", "## Notes", 1):              "missing section ## Security notes",
	}
	for in, want := range incomplete {
		tk, err := parseTicket(ticketPath("T-1"), in)
		if err != nil {
			t.Fatalf("parseTicket: %v", err)
		}
		if got := tk.incomplete(); !strings.Contains(got, want) {
			t.Errorf("incomplete() = %q, want it to contain %q", got, want)
		}
	}
}

func TestWithStatusChangesOnlyTheStatusLine(t *testing.T) {
	text := completeTicket("T-1", "low", "[pkg/]")
	got, err := withStatus(text, "merged")
	if err != nil {
		t.Fatalf("withStatus: %v", err)
	}
	if want := strings.Replace(text, "status: todo", "status: merged", 1); got != want {
		t.Errorf("withStatus changed more than the status line:\n%s", got)
	}
}

func TestPathAllowed(t *testing.T) {
	tk := ticket{ID: "T-1", AllowedPaths: []string{"internal/deploy/", "docs/specs/deploy.md", "scripts/fleet/", "Makefile"}}
	cases := map[string]bool{
		"internal/deploy/run.go":            true,
		"internal/deployer/run.go":          false,
		"docs/specs/deploy.md":              true,
		"docs/tickets/T-1.md":               true,
		"docs/tickets/T-1/plan.md":          true,
		"docs/tickets/T-2.md":               false,
		"testdata/tests.txt":                true,
		"scripts/fleet/wipe-rebuild":        true,
		"scripts/install":                   false,
		"Makefile":                          true,
		".github/workflows/ci.yml":          false,
		"internal/protocol/agentv1/x.pb.go": false,
	}
	for f, want := range cases {
		if got := pathAllowed(tk, f); got != want {
			t.Errorf("pathAllowed(%q) = %v, want %v", f, got, want)
		}
	}
	wide := ticket{ID: "T-1", AllowedPaths: []string{"docs/"}}
	if pathAllowed(wide, "docs/adr/0001-two-process-proxy.md") {
		t.Errorf("allowed_paths docs/ must not open the protected docs/adr/")
	}
}

func TestStepAllows(t *testing.T) {
	tk := ticket{ID: "T-1", AllowedPaths: []string{"internal/deploy/"}}
	cases := []struct {
		step, file string
		want       bool
	}{
		{"ticket", "docs/tickets/T-1.md", true},
		{"ticket", "docs/tickets/T-1/plan.md", false},
		{"plan", "docs/tickets/T-1/plan.md", true},
		{"tests", "internal/deploy/run_test.go", true},
		{"tests", "internal/deploy/run.go", false},
		{"tests", "testdata/escape/fixture.yml", true},
		{"approve", "docs/tickets/T-1/approval.json", true},
		{"approve", "internal/deploy/run_test.go", false},
		{"review", "docs/tickets/T-1/review.json", true},
		{"build", "internal/deploy/run.go", true},
		{"build", "docs/tickets/T-1.md", false},
		{"build", "docs/tickets/T-1/BLOCKED.md", true},
		{"arch", "docs/reviews/T-1.md", true},
		{"arch", "docs/plan/build-plan.md", true},
		{"arch", "internal/deploy/run.go", false},
	}
	for _, c := range cases {
		if got := stepAllows(c.step, tk, c.file); got != c.want {
			t.Errorf("stepAllows(%q, %q) = %v, want %v", c.step, c.file, got, c.want)
		}
	}
}

func TestResultValidation(t *testing.T) {
	if err := (approval{Round: 1, Verdict: "approve"}).validate(1); err != nil {
		t.Errorf("valid approval rejected: %v", err)
	}
	if err := (approval{Round: 2, Verdict: "approve"}).validate(1); err == nil {
		t.Errorf("approval for the wrong round accepted")
	}
	if err := (approval{Round: 1, Verdict: "revise"}).validate(1); err == nil {
		t.Errorf("revise without blockers accepted")
	}
	f := finding{File: "a.go", Line: 3, Issue: "nil map write"}
	if err := (review{Round: 1, Verdict: "changes", Majors: []finding{f}}).validate(1); err != nil {
		t.Errorf("valid review rejected: %v", err)
	}
	if err := (review{Round: 1, Verdict: "approve", Blockers: []finding{f}}).validate(1); err == nil {
		t.Errorf("approving review with a blocker accepted")
	}
	if err := (review{Round: 1, Verdict: "changes", Minors: []finding{f}}).validate(1); err == nil {
		t.Errorf("changes with only minors accepted")
	}
	if err := (review{Round: 1, Verdict: "approve", Skipped: true}).validate(1); err == nil {
		t.Errorf("reviewer-written skipped review accepted")
	}
}

func TestPlanVerdict(t *testing.T) {
	if v, _, err := planVerdict(readyPlan); err != nil || v != "ready" {
		t.Errorf("planVerdict(ready plan) = %q, %v", v, err)
	}
	v, reason, err := planVerdict("---\nverdict: needs-human\nreason: changes the proto contract\n---\n")
	if err != nil || v != "needs-human" || reason != "changes the proto contract" {
		t.Errorf("planVerdict(needs-human) = %q, %q, %v", v, reason, err)
	}
	if _, _, err := planVerdict("---\nverdict: ready\n---\n## Files to change\n"); err == nil {
		t.Errorf("ready plan without its sections accepted")
	}
}

func TestDecide(t *testing.T) {
	plan := planTicket{ID: "T-1", Kind: "build", Risk: "standard"}
	full := ticket{
		ID: "T-1", Title: "x", Status: "todo", Risk: "standard", AllowedPaths: []string{"pkg/"}, MaxDiffLines: 10,
		Tests: []string{"./pkg TestX"}, Body: "## Scope\n\n## Acceptance criteria\n\n## Out of scope\n\n## Security notes\n",
	}
	withStatusT := func(s string) ticket { c := full; c.Status = s; return c }
	okCheck := &checkResult{OK: true}
	cases := []struct {
		name string
		v    view
		want string
	}{
		{"skeleton gets the ticket step", view{plan: plan, t: ticket{ID: "T-1", Status: "todo", Risk: "standard"}, hasTicket: true}, "child ticket"},
		{"full ticket gets a plan", view{plan: plan, t: full, hasTicket: true}, "child plan"},
		{"ready plan moves to planned", view{plan: plan, t: full, hasTicket: true, planVerdict: "ready"}, "status planned"},
		{"needs-human plan stops", view{plan: plan, t: full, hasTicket: true, planVerdict: "needs-human", planReason: "adr"}, "status needs-human"},
		{"planned writes tests", view{plan: plan, t: withStatusT("planned"), r: rounds{Counts: map[string]int{}}}, "child tests"},
		{"tests need approval", view{plan: plan, t: withStatusT("planned"), r: rounds{Counts: map[string]int{"done:tests": 1}}}, "child approve"},
		{"approved tests lock", view{plan: plan, t: withStatusT("planned"), r: rounds{Counts: map[string]int{"done:tests": 1, "done:approve": 1}}, appr: &approval{Verdict: "approve"}}, "run scripts/verify lock T-1"},
		{"revise rewrites tests", view{plan: plan, t: withStatusT("planned"), r: rounds{Counts: map[string]int{"done:tests": 1, "done:approve": 1}}, appr: &approval{Verdict: "revise"}}, "child tests"},
		{"locked builds", view{plan: plan, t: withStatusT("tests-locked"), r: rounds{Counts: map[string]int{}}}, "child build"},
		{"built is checked", view{plan: plan, t: withStatusT("tests-locked"), r: rounds{Counts: map[string]int{"done:build": 1}}}, "run scripts/verify check T-1"},
		{"green check opens the PR", view{plan: plan, t: withStatusT("tests-locked"), r: rounds{Counts: map[string]int{"done:build": 1}}, check: okCheck, checkFresh: true}, "run scripts/ship pr T-1"},
		{"red check retries the build", view{plan: plan, t: withStatusT("tests-locked"), r: rounds{Counts: map[string]int{"done:build": 1}}, check: &checkResult{}, checkFresh: true}, "child build"},
		{"PR gets a review", view{plan: plan, t: withStatusT("built"), check: okCheck, checkFresh: true}, "child review"},
		{"eligible PR skips review", view{plan: plan, t: withStatusT("built"), check: &checkResult{OK: true, SkipReviewEligible: true}, checkFresh: true}, "run scripts/verify skip-review T-1"},
		{"changes get a fix", view{plan: plan, t: withStatusT("in-review"), check: okCheck, checkFresh: true, rev: &review{Verdict: "changes"}, reviewFresh: true}, "child fix"},
		{"approved is mergeable", view{plan: plan, t: withStatusT("in-review"), check: okCheck, checkFresh: true, rev: &review{Verdict: "approve"}, reviewFresh: true}, "done mergeable: the dispatcher merges it"},
		{"merge failure gets a repair", view{plan: plan, t: withStatusT("in-review"), merge: &mergeResult{Result: "ci-red"}}, "child repair"},
		{"red check after a fix blocks", view{plan: plan, t: withStatusT("in-review"), check: &checkResult{}, checkFresh: true}, "block"},
		{"BLOCKED.md blocks", view{plan: plan, t: withStatusT("built"), blocked: "no spec"}, "block"},
		{"merged is done", view{plan: plan, t: withStatusT("merged")}, "done merged"},
	}
	for _, c := range cases {
		s := decide(c.v)
		var got string
		switch {
		case s.block != "" && s.status == "":
			got = "block"
		case s.status != "":
			got = "status " + s.status
		case s.Child != "":
			got = "child " + s.Step
		case s.Run != "":
			got = "run " + s.Run
		default:
			got = "done " + s.Reason
		}
		if got != c.want {
			t.Errorf("%s: decide = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSkipEligible(t *testing.T) {
	cov := func(f float64) *float64 { return &f }
	low := ticket{ID: "T-1", Risk: "low"}
	plan := planTicket{ID: "T-1", Kind: "build"}
	cases := []struct {
		name    string
		t       ticket
		p       planTicket
		res     checkResult
		changed []string
		want    bool
	}{
		{"low, small, covered", low, plan, checkResult{OK: true, DiffLines: 40, ChangedCoverage: cov(0.9)}, []string{"internal/cli/x.go"}, true},
		{"failed check", low, plan, checkResult{OK: false}, nil, false},
		{"standard risk", ticket{Risk: "standard"}, plan, checkResult{OK: true}, nil, false},
		{"too large", low, plan, checkResult{OK: true, DiffLines: 201}, nil, false},
		{"trusted core", low, plan, checkResult{OK: true, ChangedCoverage: cov(1)}, []string{"internal/policy/x.go"}, false},
		{"protected path", low, plan, checkResult{OK: true}, []string{"Makefile"}, false},
		{"uncovered Go", low, plan, checkResult{OK: true, ChangedCoverage: cov(0.5)}, []string{"internal/cli/x.go"}, false},
		{"no coverage data", low, plan, checkResult{OK: true}, []string{"internal/cli/x.go"}, false},
		{"docs only", low, plan, checkResult{OK: true}, []string{"docs/specs/ui.md"}, true},
		{"review ticket, docs only", low, planTicket{Kind: "review"}, checkResult{OK: true}, []string{"docs/reviews/R-1.md", "docs/plan/build-plan.md"}, true},
		{"review ticket touching code", low, planTicket{Kind: "review"}, checkResult{OK: true}, []string{"internal/cli/x.go"}, false},
	}
	for _, c := range cases {
		if got, reason := skipEligible(c.p, c.t, c.res, c.changed); got != c.want {
			t.Errorf("%s: skipEligible = %v (%s), want %v", c.name, got, reason, c.want)
		}
	}
}

func TestGitHubRepo(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://github.com/moonglass-dev/moonglass.git", "moonglass-dev/moonglass"},
		{"git@github.com:moonglass-dev/moonglass.git", "moonglass-dev/moonglass"},
		{"https://github.com/moonglass-dev/moonglass", "moonglass-dev/moonglass"},
	}
	for _, c := range cases {
		got, err := githubRepo(c.in)
		if err != nil || got != c.want {
			t.Errorf("githubRepo(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	if _, err := githubRepo("https://gitlab.com/a/b.git"); err == nil {
		t.Fatal("githubRepo(gitlab) error = nil")
	}
}

func TestPRBodyLinksUseBlobURLs(t *testing.T) {
	body := prBody("moonglass-dev/moonglass", "p/W3-1", "W3-1", ticket{Risk: "high"})
	want := "https://github.com/moonglass-dev/moonglass/blob/p/W3-1/docs/tickets/W3-1.md"
	if !strings.Contains(body, want) {
		t.Fatalf("body missing %s:\n%s", want, body)
	}
	for _, path := range []string{"docs/tickets/W3-1/plan.md", "docs/tickets/W3-1/approval.json", "docs/tickets/W3-1/check.json", "docs/tickets/W3-1/review.json", "docs/pipeline/README.md"} {
		url := "https://github.com/moonglass-dev/moonglass/blob/p/W3-1/" + path
		if !strings.Contains(body, url) {
			t.Errorf("body missing %s", url)
		}
	}
	if strings.Contains(body, "](docs/") {
		t.Fatalf("body still has a relative link:\n%s", body)
	}
}

func TestSummarizeChecks(t *testing.T) {
	type c = struct{ Name, Bucket, Link string }
	cases := []struct {
		in   []c
		want string
	}{
		{nil, "pending"},
		{[]c{{"ci", "pass", ""}}, "pass"},
		{[]c{{"ci", "pass", ""}, {"lint", "pending", ""}}, "pending"},
		{[]c{{"ci", "fail", "u"}, {"lint", "pending", ""}}, "fail"},
		{[]c{{"ci", "cancel", "u"}}, "fail"},
	}
	for _, tc := range cases {
		if got, _, _ := summarizeChecks(tc.in); got != tc.want {
			t.Errorf("summarizeChecks(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The real build plan and every real ticket file must stay readable by the
// pipeline, or the dispatcher stops on its first status call.
func TestRepositoryPlanAndTicketsParse(t *testing.T) {
	root := filepath.Join("..", "..")
	b, err := os.ReadFile(filepath.Join(root, planPath))
	if err != nil {
		t.Fatalf("read build plan: %v", err)
	}
	plan, err := parsePlan(string(b))
	if err != nil {
		t.Fatalf("parse build plan: %v", err)
	}
	files, err := filepath.Glob(filepath.Join(root, "docs", "tickets", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if filepath.Base(f) == "TEMPLATE.md" {
			continue
		}
		id := strings.TrimSuffix(filepath.Base(f), ".md")
		if planByID(plan, id).ID == "" {
			t.Errorf("%s has no line in %s", f, planPath)
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := parseTicket(ticketPath(id), string(b)); err != nil {
			t.Errorf("parse %s: %v", f, err)
		}
	}
}

// Every child prompt the state machine names exists, and the dispatcher and
// worker prompts carry the same provider table.
func TestPipelinePromptsAreConsistent(t *testing.T) {
	root := filepath.Join("..", "..")
	for step, p := range childPrompts {
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Errorf("child prompt for step %s: %v", step, err)
		}
	}
	table := regexp.MustCompile(`(?s)<!-- providers -->.*<!-- /providers -->`)
	var tables []string
	for _, f := range []string{"docs/pipeline/dispatcher.md", "docs/pipeline/ticket-worker.md"} {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatal(err)
		}
		m := table.Find(b)
		if m == nil {
			t.Fatalf("%s has no <!-- providers --> table", f)
		}
		tables = append(tables, string(m))
	}
	if tables[0] != tables[1] {
		t.Errorf("the provider tables in dispatcher.md and ticket-worker.md differ:\n%s\n---\n%s", tables[0], tables[1])
	}
}

func completeTicket(id, risk, paths string) string {
	return `---
id: ` + id + `
title: Say hello
status: todo
risk: ` + risk + `
depends_on: []
allowed_paths: ` + paths + `
max_diff_lines: 100
tests: []
reads: [docs/plan/build-plan.md]
---

# Say hello

## Scope

Write pkg/hello.txt.

## Acceptance criteria

- pkg/hello.txt says hello.

## Out of scope

Everything else.

## Security notes

None.
`
}

const readyPlan = `---
verdict: ready
---

## Files to change

- pkg/hello.txt

## Tests to write

None: low risk.

## Edge cases

None.
`
