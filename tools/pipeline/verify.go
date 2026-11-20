package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// The protected paths a ticket may touch only when allowed_paths names them
// at or below their root: never through a wider directory.
var (
	protectedDirs  = []string{".github/", ".claude/", "docs/adr/", "docs/pipeline/", "docs/plan/", "tools/pipeline/", "scripts/", "proto/"}
	protectedFiles = []string{"Makefile", ".golangci.yml", ".gitignore", "AGENTS.md", "CLAUDE.md", "docs/threat-model.md", "go.mod", "go.sum"}
	trustedCore    = []string{"internal/protocol/", "internal/policy/", "internal/keys/", "internal/auth/", "internal/store/", "internal/registry/", "internal/update/", "scripts/install", "deploy/systemd/"}
	bannedWordsRe  = regexp.MustCompile(`(?i)\b(TODO|FIXME|XXX|HACK|fallback|legacy)\b`)
	generatedFiles = []string{"internal/api/openapi.json", "internal/cli/generated.go", "testdata/tests.txt"}
)

const skipReviewMaxLines = 200

func isTestFile(f string) bool {
	return strings.HasSuffix(f, "_test.go") ||
		(slices.Contains(strings.Split(f, "/"), "testdata") && f != "testdata/tests.txt")
}

func isTicketDoc(id, f string) bool {
	return f == ticketPath(id) || strings.HasPrefix(f, ticketDir(id))
}

func matchEntry(entry, f string) bool {
	if strings.HasSuffix(entry, "/") {
		return strings.HasPrefix(f, entry)
	}
	return f == entry || strings.HasPrefix(f, entry+"/")
}

func protectedRoot(f string) (string, bool) {
	if strings.HasSuffix(f, ".pb.go") || strings.HasSuffix(f, ".proto") || slices.Contains(protectedFiles, f) {
		return f, true
	}
	for _, d := range protectedDirs {
		if strings.HasPrefix(f, d) {
			return d, true
		}
	}
	return "", false
}

// pathAllowed: the ticket's own docs and the test list are always allowed;
// anything else must match allowed_paths, and a protected path only through
// an entry at or below its protected root.
func pathAllowed(t ticket, f string) bool {
	if isTicketDoc(t.ID, f) || f == "testdata/tests.txt" {
		return true
	}
	root, protected := protectedRoot(f)
	for _, e := range t.AllowedPaths {
		switch {
		case !matchEntry(e, f):
		case !protected, e == f:
			return true
		case strings.HasSuffix(root, "/") && strings.HasPrefix(strings.TrimSuffix(e, "/")+"/", root):
			return true
		}
	}
	return false
}

// stepAllows is the write scope of each child step, checked by record.
func stepAllows(step string, t ticket, f string) bool {
	id := t.ID
	if f == ticketDir(id)+blockedFile {
		return true
	}
	switch step {
	case "ticket":
		return f == ticketPath(id)
	case "plan":
		return f == ticketDir(id)+planFile
	case "tests":
		return isTestFile(f) && (pathAllowed(t, f) || strings.HasPrefix(f, "testdata/"))
	case "approve":
		return f == ticketDir(id)+approvalFile
	case "review":
		return f == ticketDir(id)+reviewFile
	case "arch":
		return f == "docs/reviews/"+id+".md" || f == planPath
	case "build", "fix", "repair":
		return !isTicketDoc(id, f)
	}
	return false
}

func cmdRecord(e *env, id string) error {
	if err := requireRunning(e.repo); err != nil {
		return err
	}
	dir := e.repo
	branch, err := currentBranch(dir)
	if err != nil {
		return err
	}
	if branch != branchRef(id) {
		return fmt.Errorf("this checkout is on %s, not %s", branch, branchRef(id))
	}
	r, err := loadRounds(fsTree{dir}, id)
	if err != nil {
		return err
	}
	if r.Claim == nil {
		return fmt.Errorf("%s has no open claim: run scripts/ticket-status next %s", id, id)
	}
	c := *r.Claim
	claimCommit, err := git(dir, "log", "-1", "--format=%H", "--", ticketDir(id)+roundsFile)
	if err != nil || claimCommit == "" {
		return fmt.Errorf("find the claim commit for %s: %w", id, err)
	}
	committed, err := changedSince(dir, claimCommit, "HEAD")
	if err != nil {
		return err
	}
	dirty, err := dirtyFiles(dir)
	if err != nil {
		return err
	}
	changed := uniq(append(committed, dirty...))
	p, err := findPlanTicket(e, id)
	if err != nil {
		return err
	}
	tb, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(ticketPath(id))))
	if err != nil {
		return err
	}

	if note, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(ticketDir(id)+blockedFile))); err == nil {
		prev, _ := parseTicket(ticketPath(id), string(tb))
		if _, err := git(dir, "reset", "--quiet", "--hard", claimCommit); err != nil {
			return err
		}
		if _, err := git(dir, "clean", "-fdq"); err != nil {
			return err
		}
		if err := writeFile(dir, ticketDir(id)+blockedFile, note); err != nil {
			return err
		}
		if err := setStatus(dir, id, "blocked", firstLine(string(note)), prev.Status); err != nil {
			return err
		}
		writeJSON(e.out, map[string]any{"id": id, "step": c.Step, "round": c.Round, "blocked": firstLine(string(note))})
		return nil
	}

	t, terr := parseTicket(ticketPath(id), string(tb))
	var problems []string
	if terr != nil {
		problems = append(problems, terr.Error())
	}
	for _, f := range changed {
		if f == ticketDir(id)+roundsFile || strings.HasPrefix(f, ticketDir(id)+"rejected-") {
			problems = append(problems, f+" is written by the pipeline scripts only")
			continue
		}
		if terr == nil && !stepAllows(c.Step, t, f) {
			problems = append(problems, f+" is outside what the "+c.Step+" step may write")
		}
	}
	if len(problems) == 0 {
		problems = validateResult(dir, id, c, p, t, changed)
	}
	if len(problems) > 0 {
		return reject(dir, id, c, claimCommit, changed, problems)
	}

	if c.Step == "review" {
		var rv review
		b, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(ticketDir(id)+reviewFile)))
		if err := decodeStrict(reviewFile, b, &rv); err != nil {
			return err
		}
		rv.Commit = claimCommit
		if err := writeFile(dir, ticketDir(id)+reviewFile, encodeJSON(rv)); err != nil {
			return err
		}
	}
	if c.Step == "repair" {
		var m mergeResult
		b, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(ticketDir(id)+mergeFile)))
		if err := decodeStrict(mergeFile, b, &m); err != nil {
			return err
		}
		m.Handled = true
		if err := writeFile(dir, ticketDir(id)+mergeFile, encodeJSON(m)); err != nil {
			return err
		}
	}
	// done counts every accepted round over the ticket's life. The round
	// number restarts at 1 after a resume, so it cannot order steps.
	r.Counts["done:"+c.Step]++
	r.Claim = nil
	if err := writeFile(dir, ticketDir(id)+roundsFile, encodeJSON(r)); err != nil {
		return err
	}
	if err := commitAll(dir, fmt.Sprintf("%s: %s round %d", id, c.Step, c.Round)); err != nil {
		return err
	}
	if err := push(dir, id); err != nil {
		return err
	}
	switch c.Step {
	case "review":
		if err := setStatus(dir, id, "in-review", "", t.Status); err != nil {
			return err
		}
	case "build", "fix", "repair", "arch":
		res, err := checkAndRecord(e, dir, id)
		if err != nil {
			return err
		}
		writeJSON(e.out, map[string]any{"id": id, "step": c.Step, "round": c.Round, "recorded": true, "check_ok": res.OK})
		return nil
	}
	writeJSON(e.out, map[string]any{"id": id, "step": c.Step, "round": c.Round, "recorded": true})
	return nil
}

func validateResult(dir, id string, c claim, p planTicket, t ticket, changed []string) []string {
	read := func(path string) ([]byte, bool) {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(path)))
		return b, err == nil
	}
	switch c.Step {
	case "ticket":
		switch {
		case t.incomplete() != "":
			return []string{"the ticket is incomplete: " + t.incomplete()}
		case t.Status != "todo":
			return []string{"the ticket step must leave status: todo"}
		case !slices.Equal(t.DependsOn, p.DependsOn):
			return []string{fmt.Sprintf("depends_on must match the build plan: %v", p.DependsOn)}
		}
	case "plan":
		b, ok := read(ticketDir(id) + planFile)
		if !ok {
			return []string{"plan.md was not written"}
		}
		if _, _, err := planVerdict(string(b)); err != nil {
			return []string{err.Error()}
		}
	case "tests":
		if !slices.ContainsFunc(changed, func(f string) bool { return strings.HasSuffix(f, "_test.go") }) {
			return []string{"no _test.go file was added or changed"}
		}
	case "approve":
		var a approval
		b, ok := read(ticketDir(id) + approvalFile)
		if !ok {
			return []string{"approval.json was not written"}
		}
		if err := decodeStrict(approvalFile, b, &a); err != nil {
			return []string{err.Error()}
		}
		if err := a.validate(c.Round); err != nil {
			return []string{err.Error()}
		}
	case "review":
		var rv review
		b, ok := read(ticketDir(id) + reviewFile)
		if !ok {
			return []string{"review.json was not written"}
		}
		if err := decodeStrict(reviewFile, b, &rv); err != nil {
			return []string{err.Error()}
		}
		if err := rv.validate(c.Round); err != nil {
			return []string{err.Error()}
		}
	case "arch":
		if _, ok := read("docs/reviews/" + id + ".md"); !ok {
			return []string{"docs/reviews/" + id + ".md was not written"}
		}
		b, _ := read(planPath)
		if _, err := parsePlan(string(b)); err != nil {
			return []string{err.Error()}
		}
	case "build", "fix", "repair":
		if len(changed) == 0 {
			return []string{"nothing was changed"}
		}
	}
	return nil
}

// reject throws away the child's changes and records why, so the next claim
// of the same step gets the reason as an input.
func reject(dir, id string, c claim, claimCommit string, changed, problems []string) error {
	if _, err := git(dir, "reset", "--quiet", "--hard", claimCommit); err != nil {
		return err
	}
	if _, err := git(dir, "clean", "-fdq"); err != nil {
		return err
	}
	note := fmt.Sprintf("Rejected %s round %d.\n\nProblems:\n- %s\n\nChanged files (discarded):\n- %s\n",
		c.Step, c.Round, strings.Join(problems, "\n- "), strings.Join(changed, "\n- "))
	if err := writeFile(dir, rejectionPath(id, c.Step, c.Round), []byte(note)); err != nil {
		return err
	}
	if err := commitAll(dir, fmt.Sprintf("%s: reject %s round %d", id, c.Step, c.Round)); err != nil {
		return err
	}
	if err := push(dir, id); err != nil {
		return err
	}
	return failf("rejected %s round %d: %s (run scripts/ticket-status next %s)", c.Step, c.Round, strings.Join(problems, "; "), id)
}

func writeFile(dir, rel string, b []byte) error {
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func uniq(xs []string) []string {
	slices.Sort(xs)
	return slices.Compact(xs)
}

func loadTicketAt(dir, id string) (ticket, error) {
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(ticketPath(id))))
	if err != nil {
		return ticket{}, fmt.Errorf("read %s: %w", ticketPath(id), err)
	}
	return parseTicket(ticketPath(id), string(b))
}

func cmdLock(e *env, id string) error {
	if err := requireRunning(e.repo); err != nil {
		return err
	}
	dir, v, err := ticketWorktree(e, id)
	if err != nil {
		return err
	}
	t := v.t
	if t.Status != "planned" {
		return fmt.Errorf("%s is %s: lock runs on a planned ticket", id, t.Status)
	}
	noTests := t.Risk == "low" && len(t.Tests) == 0
	if !noTests && (v.appr == nil || v.appr.Verdict != "approve" || v.r.done("approve") < v.r.done("tests")) {
		return failf("the latest tests are not approved: run the approve step")
	}
	base, err := mergeBase(dir, "HEAD", "origin/main")
	if err != nil {
		return err
	}
	changed, err := changedSince(dir, base, "HEAD")
	if err != nil {
		return err
	}
	var tests, goTests []string
	for _, f := range changed {
		if isTestFile(f) {
			if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
				tests = append(tests, f)
			}
			if strings.HasSuffix(f, ".go") {
				goTests = append(goTests, f)
			}
		}
	}
	if !noTests && len(goTests) == 0 {
		return failf("no _test.go file changed since %s", base[:12])
	}
	if msg, err := removedTestLines(dir, base, "HEAD", id); err != nil {
		return err
	} else if msg != "" {
		return failf("%s", msg)
	}
	if len(goTests) > 0 {
		args := append([]string{"fmt", "--diff"}, goTests...)
		out, err := runCmd(dir, "golangci-lint", args...)
		if err != nil || strings.TrimSpace(out) != "" {
			return failf("test files are not formatted (run make fmt):\n%s", tail(out, 30))
		}
	}
	if t.Risk != "low" {
		for pkg, names := range groupTests(t.Tests) {
			out, err := runCmd(dir, "go", "test", "-count=1", "-run", "^("+strings.Join(names, "|")+")$", pkg)
			if err == nil {
				return failf("the acceptance tests in %s already pass before any implementation:\n%s", pkg, tail(out, 15))
			}
		}
	}
	var lock strings.Builder
	fmt.Fprintf(&lock, "# base %s\n", base)
	sort.Strings(tests)
	for _, f := range tests {
		sum, err := fileHash(filepath.Join(dir, f))
		if err != nil {
			return err
		}
		fmt.Fprintf(&lock, "%s  %s\n", sum, f)
	}
	if err := writeFile(dir, ticketDir(id)+lockFile, []byte(lock.String())); err != nil {
		return err
	}
	if err := setStatus(dir, id, "tests-locked", "", t.Status); err != nil {
		return err
	}
	writeJSON(e.out, map[string]any{"id": id, "locked": tests, "status": "tests-locked"})
	return nil
}

func groupTests(tests []string) map[string][]string {
	out := map[string][]string{}
	for _, t := range tests {
		pkg, name, _ := strings.Cut(t, " ")
		out[pkg] = append(out[pkg], name)
	}
	return out
}

func fileHash(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// ownerTestEdits are existing test files the owner allowed a ticket to change
// (W2-8 and W2-12 on 2026-11-17, W3-7, W5-3a, FR1-10, W2-3, W2-11b, W2-6, W4-1d, W5-5a, W2-11c and FR1-2 on 2026-11-18, FR1-1, FR4-4, W2-4b, W2-11g and FR4-1 on 2026-11-19, W2-11d and W2-10a on 2026-11-20). The lines on main no longer match the behaviour those tickets
// have to build. Any other test file that existed on main is still untouchable.
var ownerTestEdits = map[string]map[string]bool{
	"W2-8": {
		"internal/protocol/deploy_test.go": true,
	},
	"W2-12": {
		"internal/proxy/proxy_test.go":  true,
		"internal/proxy/memory_test.go": true,
	},
	"W3-7": {
		"internal/protocol/deploy_test.go": true,
	},
	"W5-3a": {
		"internal/policy/config_test.go": true,
	},
	"FR1-10": {
		"internal/store/projects_conformance_test.go": true,
	},
	"W2-3": {
		"internal/deploy/registry_test.go": true,
	},
	"W2-11b": {
		"internal/store/releases_conformance_test.go": true,
	},
	"W2-6": {
		"internal/deploy/predeploy_test.go": true,
	},
	"W4-1d": {
		"internal/store/linktokens_conformance_test.go": true,
	},
	"W5-5a": {
		"internal/protocol/deploy_test.go": true,
	},
	"W2-11c": {
		"internal/registry/registry_test.go": true,
		"internal/registry/target_test.go":   true,
	},
	"FR1-1": {
		"internal/agent/enroll_test.go":       true,
		"internal/agent/connect_test.go":      true,
		"internal/server/enroll_test.go":      true,
		"internal/server/owner_token_test.go": true,
		"cmd/moonglass/enroll_test.go":        true,
		"internal/proxy/memory_test.go":       true,
		"cmd/moonglass/connect_test.go":       true,
	},
	"FR1-2": {
		"internal/store/conformance_test.go":           true,
		"internal/store/projects_conformance_test.go":  true,
		"internal/store/releases_conformance_test.go":  true,
		"internal/store/rollbacks_conformance_test.go": true,
		"internal/store/pg_rls_test.go":                true,
		"internal/server/agent_test.go":                true,
	},
	"FR4-4": {
		"tools/pipeline/parse_test.go": true,
	},
	"W2-4b": {
		"internal/deploy/helpers_test.go": true,
	},
	"W2-11g": {
		"internal/cli/generated_test.go": true,
	},
	"FR4-1": {
		"internal/proxy/memory_test.go": true,
	},
	"W2-11d": {
		"internal/api/routes_test.go": true,
	},
	"W2-10a": {
		"internal/agent/state/state_test.go":            true,
		"internal/agent/state/predeploy_test.go":        true,
		"internal/agent/state/switch_test.go":           true,
		"internal/agent/state/rollback_test.go":         true,
		"internal/agent/state/stops_test.go":            true,
		"internal/agent/state/current_releases_test.go": true,
		"internal/deploy/helpers_test.go":               true,
		"internal/deploy/deploy_test.go":                true,
		"internal/deploy/predeploy_test.go":             true,
		"internal/deploy/switch_test.go":                true,
		"internal/deploy/rollback_test.go":              true,
		"internal/deploy/domains_test.go":               true,
		"internal/deploy/domain_owner_test.go":          true,
		"internal/deploy/registry_test.go":              true,
		"internal/deploy/stop_test.go":                  true,
		"internal/deploy/retain_test.go":                true,
		"internal/deploy/run_test.go":                   true,
		// A later owner decision the same day also lets state_test.go change two
		// assertions: TestPackageImportsReleaseAndStoreOnly permits exactly
		// internal/protocol/agentv1, and TestOpenMigratesSpecSchema wants the
		// commands table.
	},
}

func ownerAllowsTestEdit(id, path string) bool {
	return ownerTestEdits[id][path]
}

// removedTestLines fails when a test file that existed at base lost lines:
// tests are never deleted or weakened to get green, except ownerTestEdits.
func removedTestLines(dir, base, to, id string) (string, error) {
	out, err := git(dir, "diff", "--numstat", "--no-renames", base, to)
	if err != nil {
		return "", err
	}
	for _, l := range lines(out) {
		f := strings.Fields(l)
		if len(f) != 3 || !strings.HasSuffix(f[2], "_test.go") || f[1] == "0" || f[1] == "-" {
			continue
		}
		if ownerAllowsTestEdit(id, f[2]) {
			continue
		}
		if _, err := git(dir, "cat-file", "-e", base+":"+f[2]); err != nil {
			continue
		}
		return fmt.Sprintf("%s lost %s lines that existed on main: existing tests are never removed or weakened", f[2], f[1]), nil
	}
	return "", nil
}

func cmdCheck(e *env, id string) error {
	if err := requireRunning(e.repo); err != nil {
		return err
	}
	if _, _, err := ticketWorktree(e, id); err != nil {
		return err
	}
	res, err := checkAndRecord(e, e.repo, id)
	if err != nil {
		return err
	}
	if !res.OK {
		return failf("verify check failed: see %s%s", ticketDir(id), checkFile)
	}
	return nil
}

// checkAndRecord runs every gate, writes check.json, commits and pushes it.
func checkAndRecord(e *env, dir, id string) (checkResult, error) {
	res, err := runGates(e, dir, id)
	if err != nil {
		return res, err
	}
	if err := writeFile(dir, ticketDir(id)+checkFile, encodeJSON(res)); err != nil {
		return res, err
	}
	if err := commitAll(dir, fmt.Sprintf("%s: check ok=%v", id, res.OK)); err != nil {
		return res, err
	}
	if err := push(dir, id); err != nil {
		return res, err
	}
	writeJSON(e.out, res)
	return res, nil
}

func runGates(e *env, dir, id string) (checkResult, error) {
	res := checkResult{}
	sha, err := head(dir)
	if err != nil {
		return res, err
	}
	res.Commit = sha
	p, err := findPlanTicket(e, id)
	if err != nil {
		return res, err
	}
	t, err := loadTicketAt(dir, id)
	if err != nil {
		return res, err
	}
	base, err := mergeBase(dir, "HEAD", "origin/main")
	if err != nil {
		return res, err
	}
	changed, err := changedSince(dir, base, "HEAD")
	if err != nil {
		return res, err
	}
	add := func(name string, ok bool, detail string) {
		res.Gates = append(res.Gates, gateResult{Name: name, OK: ok, Detail: detail})
	}

	dirty, err := dirtyFiles(dir)
	if err != nil {
		return res, err
	}
	add("clean", len(dirty) == 0, strings.Join(dirty, ", "))
	add("ticket", t.incomplete() == "", t.incomplete())

	lockMsg, err := lockedTestsChanged(dir, id)
	if err != nil {
		return res, err
	}
	add("tests-locked", lockMsg == "", lockMsg)
	keptMsg, err := removedTestLines(dir, base, "HEAD", id)
	if err != nil {
		return res, err
	}
	add("tests-kept", keptMsg == "", keptMsg)

	var outside []string
	for _, f := range changed {
		if !pathAllowed(t, f) {
			outside = append(outside, f)
		}
	}
	add("paths", len(outside) == 0, strings.Join(outside, ", "))

	n, err := diffSize(dir, base)
	if err != nil {
		return res, err
	}
	res.DiffLines = n
	add("size", t.MaxDiffLines > 0 && n <= t.MaxDiffLines, fmt.Sprintf("%d counted lines, cap %d", n, t.MaxDiffLines))

	words, err := bannedWords(dir, base)
	if err != nil {
		return res, err
	}
	add("words", len(words) == 0, strings.Join(words, "; "))

	deps, err := unapprovedDeps(dir, base)
	if err != nil {
		return res, err
	}
	add("dependencies", len(deps) == 0, strings.Join(deps, ", "))

	cheapOK := allOK(res.Gates)
	if cheapOK {
		if err := ensureTestPostgres(); err != nil {
			add("named-tests", false, err.Error())
		} else {
			msg := namedTests(dir, t.Tests)
			add("named-tests", msg == "", msg)
		}
	}
	if allOK(res.Gates) {
		out, err := e.ci(dir)
		detail := ""
		if err != nil {
			detail = tail(out, 60)
		}
		add("make-ci", err == nil, detail)
		if err == nil {
			res.ChangedCoverage, err = changedCoverage(dir, base)
			if err != nil {
				return res, err
			}
		}
	}
	res.OK = allOK(res.Gates)
	res.SkipReviewEligible, res.SkipReviewReason = skipEligible(p, t, res, changed)
	return res, nil
}

func allOK(gs []gateResult) bool {
	for _, g := range gs {
		if !g.OK {
			return false
		}
	}
	return true
}

func lockedTestsChanged(dir, id string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(ticketDir(id)+lockFile)))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var bad []string
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	for sc.Scan() {
		sum, f, ok := strings.Cut(sc.Text(), "  ")
		if !ok || strings.HasPrefix(sc.Text(), "#") {
			continue
		}
		got, err := fileHash(filepath.Join(dir, f))
		if err != nil {
			bad = append(bad, f+" (deleted)")
			continue
		}
		if got != sum {
			if ownerAllowsTestEdit(id, f) {
				continue
			}
			bad = append(bad, f)
		}
	}
	if len(bad) == 0 {
		return "", nil
	}
	return "locked test files changed: " + strings.Join(bad, ", "), nil
}

func countsTowardSize(f string) bool {
	return !isTestFile(f) && !strings.HasSuffix(f, ".pb.go") && !strings.HasSuffix(f, ".md") &&
		!strings.HasPrefix(f, "docs/") && f != "go.mod" && f != "go.sum" && !slices.Contains(generatedFiles, f)
}

func diffSize(dir, base string) (int, error) {
	out, err := git(dir, "diff", "--numstat", "--no-renames", base, "HEAD")
	if err != nil {
		return 0, err
	}
	total := 0
	for _, l := range lines(out) {
		f := strings.Fields(l)
		if len(f) != 3 || f[0] == "-" || !countsTowardSize(f[2]) {
			continue
		}
		a, _ := strconv.Atoi(f[0])
		d, _ := strconv.Atoi(f[1])
		total += a + d
	}
	return total, nil
}

// addedLines maps each changed file to its added lines, by new line number.
func addedLines(dir, base string, paths ...string) (map[string]map[int]string, error) {
	args := append([]string{"diff", "-U0", "--no-renames", base, "HEAD", "--"}, paths...)
	out, err := git(dir, args...)
	if err != nil {
		return nil, err
	}
	res := map[string]map[int]string{}
	file, n := "", 0
	hunk := regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)
	for _, l := range lines(out) {
		switch {
		case strings.HasPrefix(l, "+++ "):
			file = strings.TrimPrefix(strings.TrimPrefix(l, "+++ "), "b/")
		case strings.HasPrefix(l, "@@"):
			if m := hunk.FindStringSubmatch(l); m != nil {
				n, _ = strconv.Atoi(m[1])
			}
		case strings.HasPrefix(l, "+") && file != "/dev/null":
			if res[file] == nil {
				res[file] = map[int]string{}
			}
			res[file][n] = l[1:]
			n++
		}
	}
	return res, nil
}

// bannedWordsSkip names the files whose added lines the banned-words gate
// ignores: prose and lockfiles that legitimately contain the words, and
// ui/vite.config.ts and ui/scaffold_test.go, where adapter-static's
// client-side routing option is spelled "fallback" and no other spelling
// exists (ADR-0016, ticket W2-Sb).
func bannedWordsSkip(f string) bool {
	return strings.HasSuffix(f, ".md") || strings.HasPrefix(f, "docs/") || f == "go.mod" || f == "go.sum" ||
		f == "ui/vite.config.ts" ||
		f == "ui/scaffold_test.go"
}

func bannedWords(dir, base string) ([]string, error) {
	added, err := addedLines(dir, base)
	if err != nil {
		return nil, err
	}
	var hits []string
	for f, ls := range added {
		if bannedWordsSkip(f) {
			continue
		}
		for n, l := range ls {
			if w := bannedWordsRe.FindString(l); w != "" {
				hits = append(hits, fmt.Sprintf("%s:%d %q", f, n, w))
			}
		}
	}
	sort.Strings(hits)
	return hits, nil
}

var (
	requireRe = regexp.MustCompile(`^\s*(?:require\s+)?([a-zA-Z0-9.\-_]+\.[a-zA-Z]{2,}[^\s]*)\s+v\d`)
	acceptRe  = regexp.MustCompile(`(?mi)^-\s+\*{0,2}status:?\*{0,2}:?\s*accepted`)
)

// unapprovedDeps lists direct requirements added to go.mod that no accepted
// ADR on main names in full.
func unapprovedDeps(dir, base string) ([]string, error) {
	added, err := addedLines(dir, base, "go.mod")
	if err != nil {
		return nil, err
	}
	var mods []string
	for _, l := range added["go.mod"] {
		if strings.Contains(l, "// indirect") {
			continue
		}
		if m := requireRe.FindStringSubmatch(l); m != nil {
			mods = append(mods, m[1])
		}
	}
	if len(mods) == 0 {
		return nil, nil
	}
	list, err := git(dir, "ls-tree", "--name-only", base, "docs/adr/")
	if err != nil {
		return nil, err
	}
	var accepted strings.Builder
	for _, f := range lines(list) {
		b, ok, err := refTree{dir, base}.read(f)
		if err != nil {
			return nil, err
		}
		if ok && acceptRe.Match(b) {
			accepted.Write(b)
		}
	}
	var bad []string
	for _, m := range mods {
		if !strings.Contains(accepted.String(), m) {
			bad = append(bad, m+" (no accepted ADR names it)")
		}
	}
	return bad, nil
}

// namedTests runs each acceptance test and requires it to pass, not skip.
func namedTests(dir string, tests []string) string {
	var bad []string
	for pkg, names := range groupTests(tests) {
		out, err := runCmd(dir, "go", "test", "-count=1", "-v", "-run", "^("+strings.Join(names, "|")+")$", pkg)
		for _, n := range names {
			switch {
			case strings.Contains(out, "--- SKIP: "+n+" "):
				bad = append(bad, pkg+" "+n+" skipped")
			case !strings.Contains(out, "--- PASS: "+n+" "):
				bad = append(bad, pkg+" "+n+" did not pass")
			}
		}
		if err != nil && len(bad) == 0 {
			bad = append(bad, pkg+": "+tail(out, 10))
		}
	}
	sort.Strings(bad)
	return strings.Join(bad, "; ")
}

// changedCoverage is the share of changed, executable, non-test Go lines that
// coverage.out (written by make ci) marks as run. nil when no such lines exist.
func changedCoverage(dir, base string) (*float64, error) {
	prof, err := os.ReadFile(filepath.Join(dir, "coverage.out"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	module := modulePath(dir)
	type block struct{ start, end, count int }
	blocks := map[string][]block{}
	re := regexp.MustCompile(`^(.+\.go):(\d+)\.\d+,(\d+)\.\d+ \d+ (\d+)$`)
	for _, l := range strings.Split(string(prof), "\n") {
		m := re.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		f := strings.TrimPrefix(m[1], module+"/")
		s, _ := strconv.Atoi(m[2])
		en, _ := strconv.Atoi(m[3])
		c, _ := strconv.Atoi(m[4])
		blocks[f] = append(blocks[f], block{s, en, c})
	}
	added, err := addedLines(dir, base, "*.go")
	if err != nil {
		return nil, err
	}
	total, covered := 0, 0
	for f, ls := range added {
		if strings.HasSuffix(f, "_test.go") || !countsTowardSize(f) {
			continue
		}
		for n := range ls {
			inBlock, hit := false, false
			for _, b := range blocks[f] {
				if n >= b.start && n <= b.end {
					inBlock = true
					hit = hit || b.count > 0
				}
			}
			if inBlock {
				total++
				if hit {
					covered++
				}
			}
		}
	}
	if total == 0 {
		return nil, nil
	}
	ratio := float64(covered) / float64(total)
	return &ratio, nil
}

func modulePath(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		if m, ok := strings.CutPrefix(l, "module "); ok {
			return strings.TrimSpace(m)
		}
	}
	return ""
}

// skipEligible is the exact rule for skipping model review: review tickets
// (docs only), or low-risk tickets that pass every gate, stay small, avoid
// the trusted core and contracts, and have their changed Go lines covered.
func skipEligible(p planTicket, t ticket, res checkResult, changed []string) (bool, string) {
	if !res.OK {
		return false, "verify check failed"
	}
	if p.Kind == "review" {
		for _, f := range changed {
			if !strings.HasPrefix(f, "docs/") {
				return false, "a review ticket changed " + f
			}
		}
		return true, "review ticket: docs only"
	}
	if t.Risk != "low" {
		return false, "risk is " + t.Risk
	}
	if res.DiffLines > skipReviewMaxLines {
		return false, fmt.Sprintf("%d counted lines is over %d", res.DiffLines, skipReviewMaxLines)
	}
	for _, f := range changed {
		for _, c := range trustedCore {
			if matchEntry(c, f) {
				return false, "touches trusted core " + f
			}
		}
		if _, protected := protectedRoot(f); protected {
			return false, "touches protected path " + f
		}
	}
	goChanged := slices.ContainsFunc(changed, func(f string) bool {
		return strings.HasSuffix(f, ".go") && countsTowardSize(f)
	})
	if goChanged && (res.ChangedCoverage == nil || *res.ChangedCoverage < 0.8) {
		return false, "changed Go lines are under 80% covered"
	}
	return true, "low risk, small, covered, no trusted core or protected paths"
}

func cmdSkipReview(e *env, id string) error {
	if err := requireRunning(e.repo); err != nil {
		return err
	}
	dir, v, err := ticketWorktree(e, id)
	if err != nil {
		return err
	}
	if v.t.Status != "built" && v.t.Status != "in-review" {
		return fmt.Errorf("%s is %s: a review skip needs built or in-review", id, v.t.Status)
	}
	if v.check == nil || !v.checkFresh || !v.check.OK || !v.check.SkipReviewEligible {
		return failf("check.json does not allow skipping review for this commit: run scripts/verify check %s, then the review step", id)
	}
	sha, err := head(dir)
	if err != nil {
		return err
	}
	rv := review{
		Round: v.r.done("review"), Verdict: "approve", Skipped: true, Reason: v.check.SkipReviewReason,
		Commit: sha, Blockers: []finding{}, Majors: []finding{}, Minors: []finding{},
	}
	if err := writeFile(dir, ticketDir(id)+reviewFile, encodeJSON(rv)); err != nil {
		return err
	}
	if err := setStatus(dir, id, "in-review", "", v.t.Status); err != nil {
		return err
	}
	writeJSON(e.out, rv)
	return nil
}
