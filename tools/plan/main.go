// Command plan is the mechanical half of the Moonglass workflow
// (.agents/skills/dispatch/SKILL.md): ticket status from docs/plan/build-plan.md
// plus git and GitHub, and the pull request steps. Run it through scripts/plan.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	repo     = "moonglass-dev/moonglass"
	planPath = "docs/plan/build-plan.md"
	usage    = `usage:
  scripts/plan [status]                          what is merged, in flight, ready, and what to start next
  scripts/plan show <ID>                         one ticket's plan line and the state of its dependencies
  scripts/plan pr <ID> --body-file F [--title T] [--risk R] [--needs-owner]
                                                 push HEAD to p/<ID> and open or update its pull request;
                                                 --needs-owner parks it as a draft for the owner's decision
  scripts/plan merge <ID> --sha SHA              squash auto-merge, only while the PR head is SHA
  scripts/plan tick                              tick the plan box of every ticket merged on main (drift PR only)`
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "plan:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	if os.Getenv("GH_CONFIG_DIR") == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("find the home directory for GH_CONFIG_DIR: %w", err)
		}
		if err := os.Setenv("GH_CONFIG_DIR", filepath.Join(home, ".config", "gh-moonglass-bot")); err != nil {
			return fmt.Errorf("set GH_CONFIG_DIR: %w", err)
		}
	}
	cmd := "status"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "status":
		return cmdStatus(out)
	case "show":
		return cmdShow(args, out)
	case "pr":
		return cmdPR(args, out)
	case "merge":
		return cmdMerge(args, out)
	case "tick":
		return cmdTick(out)
	default:
		return errors.New(usage)
	}
}

func command(name string, args ...string) (string, error) {
	c := exec.Command(name, args...) //nolint:gosec // G204: only git and gh, with argument lists and never a shell
	var stderr strings.Builder
	c.Stderr = &stderr
	b, err := c.Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(b)), nil
}

// printer writes formatted lines and keeps the first write error.
type printer struct {
	w   io.Writer
	err error
}

func (p *printer) f(format string, a ...any) {
	if p.err == nil {
		_, p.err = fmt.Fprintf(p.w, format, a...)
	}
}

func gh(args ...string) (string, error) { return command("gh", args...) }

func planFile() (string, error) {
	top, err := command("git", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return filepath.Join(top, planPath), nil
}

func loadPlan() ([]Ticket, error) {
	path, err := planFile()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path) //nolint:gosec // G304: the plan file inside this checkout
	if err != nil {
		return nil, fmt.Errorf("read the plan: %w", err)
	}
	return parsePlan(string(b))
}

// mainSubjects returns the subject of every commit on origin/main, newest first.
func mainSubjects() ([]string, []string, error) {
	log, err := mainLog()
	if err != nil {
		return nil, nil, err
	}
	subjects := make([]string, 0, len(log))
	for _, l := range log {
		_, s, _ := strings.Cut(l, "\t")
		subjects = append(subjects, s)
	}
	return log, subjects, nil
}

// mainCI describes the newest ci run on main.
func mainCI() (string, error) {
	return gh("run", "list", "--repo", repo, "--branch", "main", "--workflow", "ci", "--limit", "1",
		"--json", "status,conclusion,headSha,url",
		"--jq", `.[0] | if . == null then "no run" else "\(.status) \(.conclusion) \(.headSha[0:8]) \(.url)" end`)
}

func find(plan []Ticket, id string) (Ticket, bool) {
	for _, t := range plan {
		if t.ID == id {
			return t, true
		}
	}
	return Ticket{}, false
}

// mainLog returns "unix-time<TAB>subject" for every commit on origin/main, newest first.
func mainLog() ([]string, error) {
	if _, err := command("git", "fetch", "--quiet", "origin", "main"); err != nil {
		return nil, err
	}
	s, err := command("git", "log", "--first-parent", "--format=%ct%x09%s", "origin/main")
	if err != nil {
		return nil, err
	}
	return strings.Split(s, "\n"), nil
}

type ghPR struct {
	HeadRefName      string    `json:"headRefName"`
	Mergeable        string    `json:"mergeable"`
	AutoMergeRequest *struct{} `json:"autoMergeRequest"`
	Labels           []struct {
		Name string `json:"name"`
	} `json:"labels"`
	StatusCheckRollup []struct {
		Name       string `json:"name"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	} `json:"statusCheckRollup"`
	Number  int  `json:"number"`
	IsDraft bool `json:"isDraft"`
}

func openPRs() ([]PR, map[string]string, error) {
	s, err := gh("pr", "list", "--repo", repo, "--state", "open", "--limit", "100",
		"--json", "number,headRefName,isDraft,mergeable,autoMergeRequest,labels,statusCheckRollup")
	if err != nil {
		return nil, nil, err
	}
	var raw []ghPR
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return nil, nil, fmt.Errorf("decode gh pr list: %w", err)
	}
	var prs []PR
	conflicts := map[string]string{}
	for _, r := range raw {
		id, ok := strings.CutPrefix(r.HeadRefName, "p/")
		if !ok {
			continue
		}
		p := PR{ID: id, Number: r.Number, Auto: r.AutoMergeRequest != nil, Draft: r.IsDraft, Checks: "none"}
		for _, l := range r.Labels {
			switch l.Name {
			case "high-risk":
				p.High = true
			case "needs-owner":
				p.Owner = true
			}
		}
		for _, c := range r.StatusCheckRollup {
			if c.Name != "ci" {
				continue
			}
			switch {
			case c.Status != "COMPLETED":
				p.Checks = "pending"
			case c.Conclusion == "SUCCESS":
				p.Checks = "pass"
			default:
				p.Checks = "fail"
			}
		}
		if r.Mergeable == "CONFLICTING" {
			conflicts[id] = "conflicts with main"
		}
		prs = append(prs, p)
	}
	return prs, conflicts, nil
}

func cmdStatus(out io.Writer) error {
	p := &printer{w: out}
	plan, err := loadPlan()
	if err != nil {
		return err
	}
	log, subjects, err := mainSubjects()
	if err != nil {
		return err
	}
	ci, err := mainCI()
	if err != nil {
		return err
	}
	prs, conflicts, err := openPRs()
	if err != nil {
		return err
	}
	st := status(plan, mergedIDs(subjects), prs)

	p.f("done %d/%d · open PRs %d of %d slots (count your running workers too) · high-risk open %t\n",
		len(st.Done), st.Total, len(st.PRs)-len(st.Owner), MaxRunning, st.HighRunning)
	p.f("main ci: %s\n", ci)
	p.f("%s\n", drift(log, time.Now()))
	if len(st.PRs) > 0 {
		p.f("\nOPEN PRS\n")
		for _, pr := range st.PRs {
			if pr.Owner {
				continue
			}
			note := conflicts[pr.ID]
			if pr.Draft {
				note = strings.TrimPrefix(note+", draft", ", ")
			}
			p.f("  %-8s #%d  ci=%s  auto-merge=%t  %s\n", pr.ID, pr.Number, pr.Checks, pr.Auto, note)
		}
	}
	if len(st.Owner) > 0 {
		p.f("\nNEEDS OWNER (draft PRs labelled needs-owner; the owner answers on the PR and removes the label)\n")
		for _, pr := range st.PRs {
			if pr.Owner {
				p.f("  %-8s https://github.com/%s/pull/%d\n", pr.ID, repo, pr.Number)
			}
		}
	}
	p.f("\nREADY %d\n", len(st.Ready))
	for _, t := range st.Ready {
		p.f("  %-8s %-8s %s\n", t.ID, t.Risk, t.Title)
	}
	var start []string
	for _, t := range st.Start {
		start = append(start, t.ID)
	}
	p.f("\nSTART NEXT  %s\n", orNone(strings.Join(start, ", ")))
	if len(st.Human) > 0 {
		var ids []string
		for _, t := range st.Human {
			ids = append(ids, t.ID)
		}
		p.f("OWNER TICKETS READY  %s\n", strings.Join(ids, ", "))
	}
	return p.err
}

// drift reports the ticket merges and whole days since the last "drift:"
// commit on main, and whether the weekly drift check is due.
func drift(log []string, now time.Time) string {
	merges := 0
	for _, l := range log {
		ts, s, _ := strings.Cut(l, "\t")
		if strings.HasPrefix(s, "drift: ") {
			sec, err := strconv.ParseInt(ts, 10, 64)
			if err != nil {
				return fmt.Sprintf("drift check: unreadable commit time %q · due true", ts)
			}
			days := int(now.Sub(time.Unix(sec, 0)).Hours() / 24)
			return fmt.Sprintf("drift check: %d merges and %d days since the last one · due %t", merges, days, merges >= DriftMerges || days >= DriftDays)
		}
		if subjectRe.MatchString(s) {
			merges++
		}
	}
	return fmt.Sprintf("drift check: none yet, %d merges · due true", merges)
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func cmdShow(args []string, out io.Writer) error {
	p := &printer{w: out}
	if len(args) != 1 {
		return errors.New(usage)
	}
	plan, err := loadPlan()
	if err != nil {
		return err
	}
	t, ok := find(plan, args[0])
	if !ok {
		return fmt.Errorf("%s is not in %s", args[0], planPath)
	}
	_, subjects, err := mainSubjects()
	if err != nil {
		return err
	}
	merged := mergedIDs(subjects)
	p.f("%s\n", t.Line)
	for _, d := range t.Deps {
		dt, _ := find(plan, d)
		state := "open"
		if dt.Ticked || merged[d] {
			state = "done"
		}
		p.f("  depends on %s (%s): %s\n", d, state, dt.Title)
	}
	return p.err
}

func cmdPR(args []string, out io.Writer) error {
	if len(args) < 1 {
		return errors.New(usage)
	}
	id := args[0]
	fs := flag.NewFlagSet("pr", flag.ContinueOnError)
	body := fs.String("body-file", "", "")
	title := fs.String("title", "", "")
	risk := fs.String("risk", "", "")
	owner := fs.Bool("needs-owner", false, "")
	if err := fs.Parse(args[1:]); err != nil || *body == "" {
		return errors.New(usage)
	}
	if *title == "" || *risk == "" {
		plan, err := loadPlan()
		if err != nil {
			return err
		}
		t, ok := find(plan, id)
		if !ok {
			return fmt.Errorf("%s is not in %s: pass --title and --risk for a request", id, planPath)
		}
		if *title == "" {
			*title = t.Title
		}
		if *risk == "" {
			*risk = t.Risk
		}
	}
	if dirty, err := command("git", "status", "--porcelain"); err != nil {
		return err
	} else if dirty != "" {
		return fmt.Errorf("the worktree has uncommitted changes; commit them first:\n%s", dirty)
	}
	if _, err := command("git", "push", "--quiet", "origin", "HEAD:refs/heads/p/"+id); err != nil {
		return err
	}
	num, err := gh("pr", "list", "--repo", repo, "--state", "open", "--head", "p/"+id, "--json", "number", "--jq", ".[0].number // empty")
	if err != nil {
		return err
	}
	if *owner {
		if _, err = gh("label", "create", "needs-owner", "--repo", repo, "--force", "--color", "FBCA04",
			"--description", "Waiting for a decision only the owner can make"); err != nil {
			return err
		}
	}
	if num != "" {
		edit := []string{"pr", "edit", num, "--repo", repo, "--body-file", *body}
		if *owner {
			edit = append(edit, "--add-label", "needs-owner")
			if _, err = gh("pr", "ready", num, "--repo", repo, "--undo"); err != nil {
				return err
			}
		}
		if _, err = gh(edit...); err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "updated https://github.com/%s/pull/%s\n", repo, num)
		return err
	}
	create := []string{
		"pr", "create", "--repo", repo, "--base", "main", "--head", "p/" + id,
		"--title", id + ": " + *title, "--body-file", *body,
	}
	if *risk == "high" {
		if _, err = gh("label", "create", "high-risk", "--repo", repo, "--force", "--color", "B60205",
			"--description", "Trusted core: one at a time, owner reads the diff"); err != nil {
			return err
		}
		create = append(create, "--label", "high-risk")
	}
	if *owner {
		create = append(create, "--draft", "--label", "needs-owner")
	}
	url, err := gh(create...)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, url)
	return err
}

func cmdMerge(args []string, out io.Writer) error {
	if len(args) < 1 {
		return errors.New(usage)
	}
	id := args[0]
	fs := flag.NewFlagSet("merge", flag.ContinueOnError)
	sha := fs.String("sha", "", "")
	if err := fs.Parse(args[1:]); err != nil || *sha == "" {
		return errors.New(usage)
	}
	s, err := gh("pr", "list", "--repo", repo, "--state", "open", "--head", "p/"+id, "--json", "number,title",
		"--jq", `.[0] | "\(.number)\t\(.title)"`)
	if err != nil {
		return err
	}
	num, title, ok := strings.Cut(s, "\t")
	if !ok || num == "" {
		return fmt.Errorf("no open pull request from p/%s", id)
	}
	if _, err = gh("pr", "merge", num, "--repo", repo, "--auto", "--squash", "--match-head-commit", *sha,
		"--subject", fmt.Sprintf("%s (#%s)", title, num), "--body", ""); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "auto-merge on https://github.com/%s/pull/%s at %s\n", repo, num, *sha)
	return err
}

func cmdTick(out io.Writer) error {
	path, err := planFile()
	if err != nil {
		return err
	}
	b, err := os.ReadFile(path) //nolint:gosec // G304: the plan file inside this checkout
	if err != nil {
		return fmt.Errorf("read the plan: %w", err)
	}
	_, subjects, err := mainSubjects()
	if err != nil {
		return err
	}
	text, ticked, err := tick(string(b), mergedIDs(subjects))
	if err != nil {
		return err
	}
	if err = os.WriteFile(path, []byte(text), 0o644); err != nil { //nolint:gosec // G306: a tracked docs file
		return fmt.Errorf("write the plan: %w", err)
	}
	_, err = fmt.Fprintf(out, "ticked %d: %s\n", len(ticked), orNone(strings.Join(ticked, ", ")))
	return err
}
