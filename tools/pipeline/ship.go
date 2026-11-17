package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

// pullRequest is the part of a GitHub pull request the pipeline reads.
type pullRequest struct {
	Number     int    `json:"number"`
	URL        string `json:"url"`
	State      string `json:"state"`
	HeadSHA    string `json:"headRefOid"`
	MergeState string `json:"mergeStateStatus"`
}

// forge is GitHub. Tests replace it.
type forge interface {
	findPR(branch string) (*pullRequest, error)
	createPR(branch, title, body string) error
	// checks returns pass, fail or pending for the PR's head commit.
	checks(number int) (string, string, error)
	merge(number int, sha, subject string) error
}

type ghForge struct{ dir string }

func (g ghForge) gh(args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	cmd.Dir = g.dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return stdout.String(), fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func (g ghForge) findPR(branch string) (*pullRequest, error) {
	out, err := g.gh("pr", "list", "--head", branch, "--state", "all", "--limit", "1",
		"--json", "number,url,state,headRefOid,mergeStateStatus")
	if err != nil {
		return nil, err
	}
	var prs []pullRequest
	if err := json.Unmarshal([]byte(out), &prs); err != nil {
		return nil, fmt.Errorf("parse gh pr list: %w", err)
	}
	if len(prs) == 0 || prs[0].State == "CLOSED" {
		return nil, nil
	}
	return &prs[0], nil
}

func (g ghForge) createPR(branch, title, body string) error {
	_, err := g.gh("pr", "create", "--base", "main", "--head", branch, "--title", title, "--body", body)
	return err
}

func (g ghForge) checks(number int) (string, string, error) {
	out, err := g.gh("pr", "checks", strconv.Itoa(number), "--json", "name,bucket,link")
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return "", "", err
	}
	if strings.TrimSpace(out) == "" {
		return "pending", "no checks reported yet", nil
	}
	var cs []struct{ Name, Bucket, Link string }
	if jerr := json.Unmarshal([]byte(out), &cs); jerr != nil {
		return "", "", fmt.Errorf("parse gh pr checks: %w", errors.Join(jerr, err))
	}
	return summarizeChecks(cs)
}

func summarizeChecks(cs []struct{ Name, Bucket, Link string }) (string, string, error) {
	if len(cs) == 0 {
		return "pending", "no checks reported yet", nil
	}
	var failed, pending []string
	for _, c := range cs {
		switch c.Bucket {
		case "fail", "cancel":
			failed = append(failed, c.Name+" "+c.Link)
		case "pending":
			pending = append(pending, c.Name)
		}
	}
	switch {
	case len(failed) > 0:
		return "fail", strings.Join(failed, "; "), nil
	case len(pending) > 0:
		return "pending", strings.Join(pending, ", "), nil
	}
	return "pass", "", nil
}

func (g ghForge) merge(number int, sha, subject string) error {
	_, err := g.gh("pr", "merge", strconv.Itoa(number), "--squash", "--match-head-commit", sha, "--subject", subject, "--body", "")
	return err
}

func prTitle(id, title string) string { return id + ": " + title }

func prBody(id string, t ticket) string {
	return fmt.Sprintf(`Ticket: [%s](%s) · risk: %s

Built by the ticket pipeline (docs/pipeline/README.md). Results: [plan](%s%s), [tests approval](%s%s), [check](%s%s), [review](%s%s).
`, ticketPath(id), ticketPath(id), t.Risk, ticketDir(id), planFile, ticketDir(id), approvalFile, ticketDir(id), checkFile, ticketDir(id), reviewFile)
}

func cmdPR(e *env, id string) error {
	if err := requireRunning(e.repo); err != nil {
		return err
	}
	dir, v, err := ticketWorktree(e, id)
	if err != nil {
		return err
	}
	if v.check == nil || !v.checkFresh || !v.check.OK {
		return failf("check.json is missing, failed or older than the code: run scripts/verify check %s", id)
	}
	if err = push(dir, id); err != nil {
		return err
	}
	g := e.gh()
	pr, err := g.findPR(branchRef(id))
	if err != nil {
		return err
	}
	if pr == nil || pr.State == "MERGED" {
		if err = g.createPR(branchRef(id), prTitle(id, v.t.Title), prBody(id, v.t)); err != nil {
			return err
		}
		if pr, err = g.findPR(branchRef(id)); err != nil {
			return err
		}
		if pr == nil {
			return fmt.Errorf("created a pull request for %s but cannot find it", branchRef(id))
		}
	}
	if v.t.Status == "tests-locked" || v.t.Status == "todo" {
		if err := setStatus(dir, id, "built", "", v.t.Status); err != nil {
			return err
		}
	}
	writeJSON(e.out, map[string]any{"id": id, "pr": pr.Number, "url": pr.URL})
	return nil
}

const (
	mergePollInterval = 30 * time.Second
	mergeWaitPolls    = 80 // 40 minutes: two CI runs with room to spare
)

// cmdMerge advances one ticket's merge and says where it stands: merged,
// waiting-ci, or ci-red / conflict, which are written to merge.json for the
// ticket worker's repair round. With wait it repeats every 30 s while CI runs.
func cmdMerge(e *env, id string, wait bool) error {
	for poll := 1; ; poll++ {
		if err := requireRunning(e.repo); err != nil {
			return err
		}
		state, detail, err := mergeOnce(e, id)
		if err != nil {
			return err
		}
		if state == "waiting-ci" && wait && poll < mergeWaitPolls {
			e.pause(mergePollInterval)
			continue
		}
		writeJSON(e.out, map[string]any{"id": id, "merge": state, "detail": detail})
		switch state {
		case "ci-red":
			return failf("CI failed on %s: recorded in merge.json for a repair round", branchRef(id))
		case "conflict":
			return failf("merging main into %s conflicts: recorded in merge.json for a repair round", branchRef(id))
		}
		return nil
	}
}

// mergeOnce takes one merge step and returns the resulting state and detail.
func mergeOnce(e *env, id string) (string, string, error) {
	if err := fetch(e); err != nil {
		return "", "", err
	}
	p, err := findPlanTicket(e, id)
	if err != nil {
		return "", "", err
	}
	if !refExists(e.repo, remoteBranchRef(id)) {
		onMain, mainErr := loadView(e.repo, refTree{e.repo, "origin/main"}, "", p)
		if mainErr == nil && onMain.hasTicket && onMain.t.Status == "merged" {
			return "merged", "", nil
		}
		return "", "", fmt.Errorf("%s has no branch on origin and is not merged on main", id)
	}
	g := e.gh()
	pr, err := g.findPR(branchRef(id))
	if err != nil {
		return "", "", err
	}
	if pr == nil {
		return "", "", failf("%s has no pull request: the worker runs scripts/ship pr %s", id, id)
	}
	if pr.State == "MERGED" {
		return "merged", pr.URL, nil
	}
	sha, err := git(e.repo, "rev-parse", remoteBranchRef(id))
	if err != nil {
		return "", "", err
	}
	v, err := loadView(e.repo, refTree{e.repo, remoteBranchRef(id)}, sha, p)
	if err != nil {
		return "", "", err
	}
	if v.t.Status != "merged" && !v.mergeable() {
		return "", "", failf("%s is not mergeable (status %s): its worker has not finished", id, v.t.Status)
	}
	checks, detail, err := g.checks(pr.Number)
	if err != nil {
		return "", "", err
	}
	if checks == "fail" {
		if err := recordMergeFailure(e, id, "ci-red", detail, sha); err != nil {
			return "", "", err
		}
		return "ci-red", detail, nil
	}
	behind := pr.MergeState == "BEHIND" || !isAncestor(e.repo, "origin/main", sha)
	if v.t.Status != "merged" || behind {
		if checks == "pending" && v.t.Status != "merged" {
			return "waiting-ci", "CI is still running on the branch before it catches up with main: " + detail, nil
		}
		conflict, err := catchUpAndMark(e, id, sha)
		if err != nil {
			return "", "", err
		}
		if conflict != "" {
			if err := recordMergeFailure(e, id, "conflict", conflict, sha); err != nil {
				return "", "", err
			}
			return "conflict", conflict, nil
		}
		return "waiting-ci", "merged main in and marked merged; CI runs again", nil
	}
	if checks == "pending" {
		return "waiting-ci", detail, nil
	}
	if err := g.merge(pr.Number, sha, fmt.Sprintf("%s (#%d)", prTitle(id, v.t.Title), pr.Number)); err != nil {
		return "", "", err
	}
	if _, err := git(e.repo, "push", "--quiet", "origin", "--delete", "refs/heads/"+branchRef(id)); err != nil {
		return "merged", pr.URL + " (the branch was not deleted: " + err.Error() + ")", nil
	}
	return "merged", pr.URL, nil
}

func isAncestor(dir, a, b string) bool {
	_, err := git(dir, "merge-base", "--is-ancestor", a, b)
	return err == nil
}

// regenerated maps files whose rebase conflicts are resolved by regenerating them.
var regenerated = map[string][]string{
	"testdata/tests.txt":        {"make", "test-list"},
	"internal/api/openapi.json": {"make", "generate"},
	"internal/cli/generated.go": {"make", "generate"},
}

// catchUpAndMark merges origin/main into p/ID in a temporary worktree, sets
// the ticket status to merged, and pushes with a lease on the inspected SHA.
// Merging, not rebasing: the squash merge flattens the branch anyway, and a
// merge never rewrites commits a worker or a repair round already pushed.
// It returns a description of the conflict instead when the merge stops.
func catchUpAndMark(e *env, id, sha string) (string, error) {
	conflict := ""
	err := withBranchWorktree(e, id, func(dir string) error {
		if _, err := git(dir, "merge", "--quiet", "--no-edit", "origin/main"); err != nil {
			files, ferr := git(dir, "diff", "--name-only", "--diff-filter=U")
			if ferr != nil || files == "" {
				return fmt.Errorf("merge origin/main into %s: %w", branchRef(id), err)
			}
			var cmds [][]string
			for _, f := range lines(files) {
				cmd, ok := regenerated[f]
				if !ok {
					conflict = "conflicting files: " + strings.ReplaceAll(files, "\n", ", ")
					_, _ = git(dir, "merge", "--abort")
					return nil
				}
				if !slices.ContainsFunc(cmds, func(c []string) bool { return slices.Equal(c, cmd) }) {
					cmds = append(cmds, cmd)
				}
			}
			for _, c := range cmds {
				if _, err := runCmd(dir, c[0], c[1:]...); err != nil {
					conflict = "regenerating " + strings.Join(c, " ") + " failed: " + err.Error()
					_, _ = git(dir, "merge", "--abort")
					return nil
				}
			}
			if err := commitAll(dir, "Merge origin/main into "+branchRef(id)); err != nil {
				return err
			}
		}
		t, err := loadTicketAt(dir, id)
		if err != nil {
			return err
		}
		if t.Status != "merged" {
			text, serr := withStatus(readOrEmpty(dir, ticketPath(id)), "merged")
			if serr != nil {
				return serr
			}
			if err = writeFile(dir, ticketPath(id), []byte(text)); err != nil {
				return err
			}
			if err = commitAll(dir, id+": mark merged"); err != nil {
				return err
			}
		}
		_, err = git(dir, "push", "--quiet", "--force-with-lease=refs/heads/"+branchRef(id)+":"+sha, "origin", "HEAD:refs/heads/"+branchRef(id))
		if err != nil {
			return fmt.Errorf("push the rebased %s (someone pushed to it since %s): %w", branchRef(id), sha[:12], err)
		}
		return nil
	})
	return conflict, err
}

func readOrEmpty(dir, rel string) string {
	b, _, _ := fsTree{dir}.read(rel)
	return string(b)
}

// recordMergeFailure writes merge.json on the ticket branch and puts the
// ticket back to in-review, so the dispatcher restarts its worker for repair.
func recordMergeFailure(e *env, id, result, detail, sha string) error {
	return withBranchWorktree(e, id, func(dir string) error {
		m := mergeResult{Result: result, Commit: sha, Detail: detail}
		if err := writeFile(dir, ticketDir(id)+mergeFile, encodeJSON(m)); err != nil {
			return err
		}
		t, err := loadTicketAt(dir, id)
		if err != nil {
			return err
		}
		if t.Status == "merged" {
			text, err := withStatus(readOrEmpty(dir, ticketPath(id)), "in-review")
			if err != nil {
				return err
			}
			if err := writeFile(dir, ticketPath(id), []byte(text)); err != nil {
				return err
			}
		}
		if err := commitAll(dir, fmt.Sprintf("%s: merge failed (%s)", id, result)); err != nil {
			return err
		}
		return push(dir, id)
	})
}
