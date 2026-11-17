package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// step is what a ticket worker does next. Child steps name a prompt in
// docs/pipeline/children/; script steps name the exact command to run.
type step struct {
	Step            string   `json:"step"`
	Round           int      `json:"round,omitempty"`
	Child           string   `json:"child,omitempty"`
	Inputs          []string `json:"inputs,omitempty"`
	ClientRequestID string   `json:"client_request_id,omitempty"`
	Run             string   `json:"run,omitempty"`
	Reason          string   `json:"reason,omitempty"`
	// block is set when the next step is to stop the ticket.
	block string
	// status is set when the next step is a plain status change.
	status string
}

var childPrompts = map[string]string{
	"ticket":  "docs/pipeline/children/ticket.md",
	"plan":    "docs/pipeline/children/plan.md",
	"tests":   "docs/pipeline/children/tests.md",
	"approve": "docs/pipeline/children/approve.md",
	"build":   "docs/pipeline/children/build.md",
	"review":  "docs/pipeline/children/review.md",
	"fix":     "docs/pipeline/children/fix.md",
	"repair":  "docs/pipeline/children/fix.md",
	"arch":    "docs/pipeline/children/arch-review.md",
}

func (v view) child(name string, inputs ...string) step {
	if v.rejected != "" && v.r.Claim != nil && v.r.Claim.Step == name {
		inputs = append(inputs, v.rejected)
	}
	return step{Step: name, Child: childPrompts[name], Inputs: inputs}
}

func runStep(cmd, id string) step {
	return step{Step: "run", Run: cmd + " " + id}
}

func done(reason string) step { return step{Step: "done", Reason: reason} }

// decide is the whole per-ticket state machine. It only reads; cmdNext acts.
func decide(v view) step {
	id := v.plan.ID
	dir := ticketDir(id)
	st := v.t.Status
	if v.blocked != "" && !slices.Contains([]string{"blocked", "needs-human", "merged"}, st) {
		return step{block: v.blocked}
	}
	switch st {
	case "merged":
		return done("merged")
	case "blocked", "needs-human":
		return done(st + ": " + v.blocked)
	}
	if v.plan.Kind == "review" {
		return decideReview(v)
	}
	switch st {
	case "todo":
		switch {
		case !v.hasTicket || v.t.incomplete() != "":
			return v.child("ticket")
		case v.planVerdict == "":
			return v.child("plan")
		case v.planVerdict == "ready":
			return step{status: "planned"}
		default:
			return step{status: "needs-human", block: v.planReason}
		}
	case "planned":
		switch {
		case v.t.Risk == "low" && len(v.t.Tests) == 0:
			return runStep("scripts/verify lock", id)
		case v.r.done("tests") == 0:
			return v.child("tests")
		case v.r.done("approve") < v.r.done("tests"):
			return v.child("approve")
		case v.appr != nil && v.appr.Verdict == "approve":
			return runStep("scripts/verify lock", id)
		default:
			return v.child("tests", dir+approvalFile)
		}
	case "tests-locked":
		switch {
		case v.r.done("build") == 0:
			return v.child("build")
		case v.check == nil || !v.checkFresh:
			return runStep("scripts/verify check", id)
		case v.check.OK:
			return runStep("scripts/ship pr", id)
		default:
			return v.child("build", dir+checkFile)
		}
	case "built", "in-review":
		switch {
		case v.merge != nil && !v.merge.Handled:
			return v.child("repair", dir+mergeFile)
		case v.check == nil || !v.checkFresh:
			return runStep("scripts/verify check", id)
		case !v.check.OK:
			return step{block: "verify check failed after the last fix: see " + dir + checkFile}
		case v.rev == nil || !v.reviewFresh:
			if v.check.SkipReviewEligible {
				return runStep("scripts/verify skip-review", id)
			}
			return v.child("review")
		case v.rev.Verdict == "approve":
			return done("mergeable: the dispatcher merges it")
		default:
			return v.child("fix", dir+reviewFile)
		}
	}
	return step{block: "unknown status " + st}
}

func decideReview(v view) step {
	id := v.plan.ID
	switch {
	case !v.reviewDocExists():
		return v.child("arch")
	case v.check == nil || !v.checkFresh:
		return runStep("scripts/verify check", id)
	case !v.check.OK:
		return v.child("arch", ticketDir(id)+checkFile)
	case v.t.Status == "todo":
		return runStep("scripts/ship pr", id)
	case v.rev == nil || !v.reviewFresh:
		return runStep("scripts/verify skip-review", id)
	default:
		return done("mergeable: the dispatcher merges it")
	}
}

func (v view) reviewDocExists() bool { return v.r.done("arch") > 0 }

func (r rounds) done(step string) int {
	if r.Counts == nil {
		return 0
	}
	return r.Counts["done:"+step]
}

func cmdNext(e *env, id string, peek bool) error {
	if err := requireRunning(e.repo); err != nil {
		return err
	}
	dir, v, err := ticketWorktree(e, id)
	if err != nil {
		return err
	}
	s := decide(v)
	if peek {
		writeJSON(e.out, s)
		return nil
	}
	switch {
	case s.block != "":
		if s.status == "" {
			s.status = "blocked"
		}
		if err := setStatus(dir, id, s.status, s.block, v.t.Status); err != nil {
			return err
		}
		s = done(s.status + ": " + s.block)
	case s.status != "":
		if err := setStatus(dir, id, s.status, "", v.t.Status); err != nil {
			return err
		}
		return cmdNext(e, id, false)
	case s.Child != "":
		round := v.r.Counts[s.Step] + 1
		if round > limits[s.Step] {
			reason := fmt.Sprintf("step %s already ran %d times, the limit", s.Step, limits[s.Step])
			if err := setStatus(dir, id, "blocked", reason, v.t.Status); err != nil {
				return err
			}
			s = done("blocked: " + reason)
			break
		}
		v.r.Counts[s.Step] = round
		v.r.Claim = &claim{Step: s.Step, Round: round}
		if err := writeRounds(dir, id, v.r, fmt.Sprintf("%s: claim %s round %d", id, s.Step, round)); err != nil {
			return err
		}
		s.Round = round
		s.ClientRequestID = fmt.Sprintf("%s:%s:%d", id, s.Step, round)
	}
	writeJSON(e.out, s)
	return nil
}

// ticketWorktree checks that we are a clean checkout of p/ID and loads it.
func ticketWorktree(e *env, id string) (string, view, error) {
	dir := e.repo
	branch, err := currentBranch(dir)
	if err != nil {
		return "", view{}, err
	}
	if branch != branchRef(id) {
		return "", view{}, fmt.Errorf("this checkout is on %s, not %s: run scripts/ticket-status start %s here first", branch, branchRef(id), id)
	}
	dirty, err := dirtyFiles(dir)
	if err != nil {
		return "", view{}, err
	}
	if len(dirty) > 0 {
		return "", view{}, fmt.Errorf("uncommitted changes (%s): run scripts/verify record %s first", strings.Join(dirty, ", "), id)
	}
	p, err := findPlanTicket(e, id)
	if err != nil {
		return "", view{}, err
	}
	sha, err := head(dir)
	if err != nil {
		return "", view{}, err
	}
	v, err := loadView(dir, fsTree{dir}, sha, p)
	return dir, v, err
}

func writeRounds(dir, id string, r rounds, msg string) error {
	path := filepath.Join(dir, filepath.FromSlash(ticketDir(id)+roundsFile))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(path, encodeJSON(r), 0o600); err != nil {
		return err
	}
	if err := commitAll(dir, msg); err != nil {
		return err
	}
	return push(dir, id)
}

// setStatus changes the ticket status in dir, commits and pushes. Blocking
// writes BLOCKED.md with the reason and the status to return to.
func setStatus(dir, id, status, reason, previous string) error {
	path := filepath.Join(dir, filepath.FromSlash(ticketPath(id)))
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", ticketPath(id), err)
	}
	text, err := withStatus(string(b), status)
	if err != nil {
		return fmt.Errorf("%s: %w", ticketPath(id), err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return err
	}
	if status == "blocked" || status == "needs-human" {
		bpath := filepath.Join(dir, filepath.FromSlash(ticketDir(id)+blockedFile))
		old, _ := os.ReadFile(bpath)
		if reason == "" {
			reason = firstLine(string(old))
		}
		note := fmt.Sprintf("# %s\n\nBlocked at status: %s\n", reason, previous)
		if len(old) > 0 && firstLine(string(old)) == reason {
			note = strings.TrimRight(string(old), "\n") + "\n\nBlocked at status: " + previous + "\n"
		}
		if err := os.MkdirAll(filepath.Dir(bpath), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(bpath, []byte(note), 0o600); err != nil {
			return err
		}
	}
	if err := commitAll(dir, fmt.Sprintf("%s: status %s", id, status)); err != nil {
		return err
	}
	return push(dir, id)
}

func cmdStart(e *env, id string) error {
	if err := requireRunning(e.repo); err != nil {
		return err
	}
	if err := fetch(e); err != nil {
		return err
	}
	p, err := findPlanTicket(e, id)
	if err != nil {
		return err
	}
	if p.Kind == "human" {
		return fmt.Errorf("%s is a human ticket: the pipeline does not run it", id)
	}
	dirty, err := dirtyFiles(e.repo)
	if err != nil {
		return err
	}
	if len(dirty) > 0 {
		return fmt.Errorf("checkout has uncommitted changes (%s): start needs a clean worktree", strings.Join(dirty, ", "))
	}
	from := "origin/main"
	if refExists(e.repo, remoteBranchRef(id)) {
		from = remoteBranchRef(id)
	}
	// A stopped worker's worktree may still have p/ID checked out; the new
	// worker owns the branch now.
	if _, err = git(e.repo, "switch", "--quiet", "--ignore-other-worktrees", "-C", branchRef(id), from); err != nil {
		return err
	}
	tpath := filepath.Join(e.repo, filepath.FromSlash(ticketPath(id)))
	if _, statErr := os.Stat(tpath); os.IsNotExist(statErr) {
		if err = writeFile(e.repo, ticketPath(id), []byte(skeleton(p))); err != nil {
			return err
		}
	}
	r, err := loadRounds(fsTree{e.repo}, id)
	if err != nil {
		return err
	}
	r.Counts["worker"]++
	run := r.Counts["worker"]
	if err = writeRounds(e.repo, id, r, fmt.Sprintf("%s: start worker run %d", id, run)); err != nil {
		return err
	}
	_, v, err := ticketWorktree(e, id)
	if err != nil {
		return err
	}
	if run > limits["worker"] && v.t.Status != "merged" && v.t.Status != "blocked" && v.t.Status != "needs-human" {
		reason := fmt.Sprintf("the worker was started %d times without finishing", run)
		if err := setStatus(e.repo, id, "blocked", reason, v.t.Status); err != nil {
			return err
		}
		v.t.Status = "blocked"
	}
	writeJSON(e.out, map[string]any{"id": id, "branch": branchRef(id), "worker_run": run, "status": v.t.Status})
	return nil
}

func cmdSet(e *env, id, status, reason string) error {
	if !slices.Contains(statuses, status) && status != "resume" {
		return fmt.Errorf("status must be one of %v, or resume", statuses)
	}
	if status != "blocked" && status != "needs-human" {
		if err := requireRunning(e.repo); err != nil {
			return err
		}
	}
	return inTicketCheckout(e, id, func(dir string) error {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(ticketPath(id))))
		if err != nil {
			return fmt.Errorf("read %s: %w", ticketPath(id), err)
		}
		t, err := parseTicket(ticketPath(id), string(b))
		if err != nil {
			return err
		}
		if status == "resume" {
			return resume(e, dir, id, t)
		}
		if err := setStatus(dir, id, status, reason, t.Status); err != nil {
			return err
		}
		writeJSON(e.out, map[string]any{"id": id, "status": status})
		return nil
	})
}

// resume is the human's way back from blocked or needs-human: it restores the
// status recorded in BLOCKED.md, deletes BLOCKED.md and resets the step budget.
// A needs-human plan is deleted so the plan step runs again on the edited ticket.
func resume(e *env, dir, id string, t ticket) error {
	if t.Status != "blocked" && t.Status != "needs-human" {
		return fmt.Errorf("%s is %s, not blocked or needs-human", id, t.Status)
	}
	bpath := filepath.Join(dir, filepath.FromSlash(ticketDir(id)+blockedFile))
	note, _ := os.ReadFile(bpath)
	back := "todo"
	for _, l := range strings.Split(string(note), "\n") {
		if s, ok := strings.CutPrefix(l, "Blocked at status: "); ok && slices.Contains(statuses, strings.TrimSpace(s)) {
			back = strings.TrimSpace(s)
		}
	}
	if back == "blocked" || back == "needs-human" {
		back = "todo"
	}
	if t.Status == "needs-human" {
		back = "todo"
		_ = os.Remove(filepath.Join(dir, filepath.FromSlash(ticketDir(id)+planFile)))
	}
	_ = os.Remove(bpath)
	r, err := loadRounds(fsTree{dir}, id)
	if err != nil {
		return err
	}
	for k := range r.Counts {
		if !strings.HasPrefix(k, "done:") {
			delete(r.Counts, k)
		}
	}
	r.Claim = nil
	if t.Status == "needs-human" {
		delete(r.Counts, "done:plan")
	}
	if err = os.WriteFile(filepath.Join(dir, filepath.FromSlash(ticketDir(id)+roundsFile)), encodeJSON(r), 0o600); err != nil {
		return err
	}
	tpath := filepath.Join(dir, filepath.FromSlash(ticketPath(id)))
	b, err := os.ReadFile(tpath)
	if err != nil {
		return err
	}
	text, err := withStatus(string(b), back)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tpath, []byte(text), 0o600); err != nil {
		return err
	}
	if err := commitAll(dir, fmt.Sprintf("%s: resume at %s", id, back)); err != nil {
		return err
	}
	if err := push(dir, id); err != nil {
		return err
	}
	writeJSON(e.out, map[string]any{"id": id, "status": back})
	return nil
}
