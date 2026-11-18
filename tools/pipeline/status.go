package main

import (
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"
)

// view is everything known about one ticket on one tree (a branch or a checkout).
type view struct {
	plan        planTicket
	t           ticket
	hasTicket   bool
	r           rounds
	planVerdict string
	planReason  string
	appr        *approval
	check       *checkResult
	checkFresh  bool
	rev         *review
	reviewFresh bool
	merge       *mergeResult
	blocked     string
	rejected    string
}

// onlyTicketDocs reports whether from..to changes nothing but the ticket's own
// docs, so a check or review of `from` still describes the code at `to`.
func onlyTicketDocs(dir, from, to, id string) (bool, error) {
	if from == to {
		return true, nil
	}
	if from == "" || to == "" || !refExists(dir, from) {
		return false, nil
	}
	files, err := changedSince(dir, from, to)
	if err != nil {
		return false, err
	}
	for _, f := range files {
		if f != ticketPath(id) && !strings.HasPrefix(f, ticketDir(id)) {
			return false, nil
		}
	}
	return true, nil
}

func loadView(dir string, tr tree, headSHA string, p planTicket) (view, error) {
	v := view{plan: p}
	id := p.ID
	b, ok, err := tr.read(ticketPath(id))
	if err != nil {
		return v, err
	}
	if ok {
		v.t, err = parseTicket(ticketPath(id), string(b))
		if err != nil {
			return v, err
		}
		v.hasTicket = true
	} else {
		v.t = ticket{ID: id, Title: p.Title, Status: "todo", Risk: p.Risk, DependsOn: p.DependsOn}
	}
	if v.r, err = loadRounds(tr, id); err != nil {
		return v, err
	}
	if b, ok, err = tr.read(ticketDir(id) + planFile); err != nil {
		return v, err
	} else if ok {
		if v.planVerdict, v.planReason, err = planVerdict(string(b)); err != nil {
			return v, err
		}
	}
	var a approval
	if ok, err = readJSON(tr, ticketDir(id)+approvalFile, &a); err != nil {
		return v, err
	} else if ok {
		v.appr = &a
	}
	var c checkResult
	if ok, err = readJSON(tr, ticketDir(id)+checkFile, &c); err != nil {
		return v, err
	} else if ok {
		v.check = &c
		if v.checkFresh, err = onlyTicketDocs(dir, c.Commit, headSHA, id); err != nil {
			return v, err
		}
	}
	var rv review
	if ok, err = readJSON(tr, ticketDir(id)+reviewFile, &rv); err != nil {
		return v, err
	} else if ok {
		v.rev = &rv
		if v.reviewFresh, err = onlyTicketDocs(dir, rv.Commit, headSHA, id); err != nil {
			return v, err
		}
	}
	var m mergeResult
	if ok, err = readJSON(tr, ticketDir(id)+mergeFile, &m); err != nil {
		return v, err
	} else if ok {
		v.merge = &m
	}
	if b, ok, err = tr.read(ticketDir(id) + blockedFile); err != nil {
		return v, err
	} else if ok {
		v.blocked = firstLine(string(b))
		if v.blocked == "" {
			v.blocked = "BLOCKED.md is empty"
		}
	}
	if c := v.r.Claim; c != nil {
		if b, ok, err = tr.read(rejectionPath(id, c.Step, c.Round)); err != nil {
			return v, err
		} else if ok {
			v.rejected = rejectionPath(id, c.Step, c.Round)
			_ = b
		}
	}
	return v, nil
}

func rejectionPath(id, step string, round int) string {
	return fmt.Sprintf("%srejected-%s-%d.txt", ticketDir(id), step, round)
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(strings.TrimLeft(l, "# ")); l != "" {
			return l
		}
	}
	return ""
}

// mergeable: approved by review (or a recorded skip), checked at this commit,
// and no merge failure still waiting for a repair.
func (v view) mergeable() bool {
	return v.t.Status == "in-review" &&
		v.check != nil && v.check.OK && v.checkFresh &&
		v.rev != nil && v.rev.Verdict == "approve" && v.reviewFresh &&
		(v.merge == nil || v.merge.Handled)
}

type ticketState struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Kind       string   `json:"kind"`
	Risk       string   `json:"risk"`
	Status     string   `json:"status"`
	DependsOn  []string `json:"depends_on"`
	Branch     bool     `json:"branch"`
	Ready      bool     `json:"ready"`
	Mergeable  bool     `json:"mergeable"`
	Merging    bool     `json:"merging"`
	WorkerRuns int      `json:"worker_runs"`
	Reason     string   `json:"reason,omitempty"`
}

type report struct {
	Stop       bool          `json:"stop"`
	Merged     int           `json:"merged"`
	Ready      []string      `json:"ready"`
	InProgress []string      `json:"in_progress"`
	Mergeable  []string      `json:"mergeable"`
	Attention  []string      `json:"attention"`
	Human      []string      `json:"human"`
	Tickets    []ticketState `json:"tickets"`
}

func cmdStatus(e *env, args []string) error {
	asJSON := slices.Contains(args, "--json")
	e.noFetch = e.noFetch || slices.Contains(args, "--no-fetch")
	if err := fetch(e); err != nil {
		return err
	}
	rep, err := buildReport(e)
	if err != nil {
		return err
	}
	if asJSON {
		writeJSON(e.out, rep)
		return nil
	}
	w := tabwriter.NewWriter(e.out, 0, 2, 2, ' ', 0)
	say(w, "ID\tSTATUS\tRISK\tKIND\tFLAGS\tTITLE\n")
	for _, t := range rep.Tickets {
		var flags []string
		for name, on := range map[string]bool{"ready": t.Ready, "mergeable": t.Mergeable, "merging": t.Merging, "branch": t.Branch} {
			if on {
				flags = append(flags, name)
			}
		}
		slices.Sort(flags)
		say(w, "%s\t%s\t%s\t%s\t%s\t%s\n", t.ID, t.Status, t.Risk, t.Kind, strings.Join(flags, ","), t.Title)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	say(e.out, "\nmerged %d · ready %v · in progress %v · mergeable %v · attention %v · stop %v\n",
		rep.Merged, rep.Ready, rep.InProgress, rep.Mergeable, rep.Attention, rep.Stop)
	return nil
}

func buildReport(e *env) (report, error) {
	rep := report{Ready: []string{}, InProgress: []string{}, Mergeable: []string{}, Attention: []string{}, Human: []string{}}
	var err error
	if rep.Stop, err = stopped(e.repo); err != nil {
		return rep, err
	}
	mainRef := "origin/main"
	if !refExists(e.repo, mainRef) {
		return rep, fmt.Errorf("origin/main not found: fetch origin first")
	}
	b, ok, err := refTree{e.repo, mainRef}.read(planPath)
	if err != nil || !ok {
		return rep, fmt.Errorf("read %s on origin/main: %w", planPath, err)
	}
	plan, err := parsePlan(string(b))
	if err != nil {
		return rep, err
	}
	status := map[string]string{}
	var states []ticketState
	for _, p := range plan {
		st := ticketState{ID: p.ID, Title: p.Title, Kind: p.Kind, Risk: p.Risk, DependsOn: p.DependsOn}
		ref := mainRef
		onMain, err := loadView(e.repo, refTree{e.repo, mainRef}, "", p)
		if err != nil {
			return rep, err
		}
		if onMain.t.Status != "merged" && refExists(e.repo, remoteBranchRef(p.ID)) {
			st.Branch = true
			ref = remoteBranchRef(p.ID)
		}
		sha, err := git(e.repo, "rev-parse", ref)
		if err != nil {
			return rep, err
		}
		v, err := loadView(e.repo, refTree{e.repo, ref}, sha, p)
		if err != nil {
			return rep, err
		}
		st.Status = v.t.Status
		st.Risk = v.t.Risk
		st.WorkerRuns = v.r.Counts["worker"]
		st.Mergeable = st.Branch && v.mergeable()
		st.Merging = st.Branch && v.t.Status == "merged"
		if v.t.Status == "blocked" || v.t.Status == "needs-human" {
			st.Reason = v.blocked
			if st.Reason == "" {
				st.Reason = v.planReason
			}
		}
		status[p.ID] = st.Status
		if st.Merging {
			status[p.ID] = "merging"
		}
		states = append(states, st)
	}
	for i := range states {
		p := plan[i]
		if p.Kind != "human" && status[p.ID] == "merged" {
			rep.Merged++
		}
		for _, d := range p.DependsOn {
			if dep := planByID(plan, d); dep.Kind == "human" && p.Kind != "human" && status[p.ID] != "merged" {
				return rep, fmt.Errorf("%s depends on %s, a human ticket the pipeline cannot finish: drop the dependency or make %s an agent ticket", p.ID, d, d)
			}
		}
	}
	// Only one high-risk ticket is in flight at a time (build plan §1).
	// A ticket whose branch outlived a plan change, such as one resumed after
	// a split gave it new dependencies, waits for them like any other.
	waiting := func(i int) []string {
		if !states[i].Branch || states[i].Status == "blocked" || states[i].Status == "needs-human" {
			return nil
		}
		return unmergedDeps(plan[i], status)
	}
	highInFlight := false
	for i, st := range states {
		if st.Risk == "high" && st.Branch && st.Status != "blocked" && st.Status != "needs-human" && len(waiting(i)) == 0 {
			highInFlight = true
		}
	}
	for i := range states {
		st := &states[i]
		p := plan[i]
		switch {
		case p.Kind == "human":
			if st.Status != "merged" {
				rep.Human = append(rep.Human, st.ID)
			}
		case st.Status == "merged" && !st.Merging:
		case st.Status == "blocked" || st.Status == "needs-human":
			rep.Attention = append(rep.Attention, st.ID)
		case st.Mergeable || st.Merging:
			rep.Mergeable = append(rep.Mergeable, st.ID)
		case len(waiting(i)) > 0:
			st.Reason = "waits: depends on " + strings.Join(waiting(i), ", ")
		case st.Branch:
			rep.InProgress = append(rep.InProgress, st.ID)
		case st.Status == "todo" && depsMerged(p, status) && rep.Merged >= p.After:
			if st.Risk == "high" && highInFlight {
				st.Reason = "waits: another high-risk ticket is in flight"
				break
			}
			highInFlight = highInFlight || st.Risk == "high"
			st.Ready = true
			rep.Ready = append(rep.Ready, st.ID)
		}
	}
	rep.Tickets = states
	return rep, nil
}

func planByID(plan []planTicket, id string) planTicket {
	for _, p := range plan {
		if p.ID == id {
			return p
		}
	}
	return planTicket{}
}

func depsMerged(p planTicket, status map[string]string) bool {
	return len(unmergedDeps(p, status)) == 0
}

func unmergedDeps(p planTicket, status map[string]string) []string {
	var out []string
	for _, d := range p.DependsOn {
		if status[d] != "merged" {
			out = append(out, d)
		}
	}
	return out
}

// findPlanTicket reads the plan from origin/main, or from the checkout when
// origin/main has no such ticket yet (a review ticket's own fix lines).
func findPlanTicket(e *env, id string) (planTicket, error) {
	if !idRe.MatchString(id) {
		return planTicket{}, fmt.Errorf("%q is not a ticket id", id)
	}
	for _, tr := range []tree{refTree{e.repo, "origin/main"}, fsTree{e.repo}} {
		b, ok, err := tr.read(planPath)
		if err != nil || !ok {
			continue
		}
		plan, err := parsePlan(string(b))
		if err != nil {
			return planTicket{}, err
		}
		if p := planByID(plan, id); p.ID != "" {
			return p, nil
		}
	}
	return planTicket{}, fmt.Errorf("ticket %s is not in %s", id, planPath)
}
